package smppc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// memMultipartStore is an in-memory MultipartStore for the reassembly tests.
type memMultipartStore struct {
	mu    sync.Mutex
	parts map[string]map[uint32][]byte
}

func newMemMultipartStore() *memMultipartStore {
	return &memMultipartStore{parts: map[string]map[uint32][]byte{}}
}

func key(cid string, ref uint32, dst string) string { return fmt.Sprintf("%s:%d:%s", cid, ref, dst) }

func (m *memMultipartStore) StorePart(_ context.Context, cid string, ref uint32, dst string, seq uint32, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(cid, ref, dst)
	if m.parts[k] == nil {
		m.parts[k] = map[uint32][]byte{}
	}
	m.parts[k][seq] = append([]byte(nil), content...)
	return nil
}

func (m *memMultipartStore) ReadParts(_ context.Context, cid string, ref uint32, dst string) (map[uint32][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[uint32][]byte{}
	for seq, content := range m.parts[key(cid, ref, dst)] {
		out[seq] = content
	}
	return out, nil
}

func (m *memMultipartStore) DeleteParts(_ context.Context, cid string, ref uint32, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.parts, key(cid, ref, dst))
	return nil
}

// sarPart builds a SAR long-message deliver_sm part.
func sarPart(ref uint16, total, seq byte, content string) smppwire.PDU {
	body := &smppwire.SMBody{
		SourceAddress: []byte("111"), DestinationAddress: []byte("222"), ShortMessage: []byte(content),
	}
	body.Optional.SARMessageReference = &ref
	body.Optional.SARTotalSegments = &total
	body.Optional.SARSegmentSequence = &seq
	return smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM}, SM: body}
}

func sarPayloadPart(ref uint16, total, seq byte, content string) smppwire.PDU {
	pdu := sarPart(ref, total, seq, "")
	pdu.SM.ShortMessage = []byte{}
	pdu.SM.Optional.MessagePayload = []byte(content)
	return pdu
}

// udhPart builds a UDH-concatenated long-message deliver_sm part.
func udhPart(ref, total, seq byte, content string) smppwire.PDU {
	sm := append([]byte{0x05, 0x00, 0x03, ref, total, seq}, []byte(content)...)
	body := &smppwire.SMBody{
		SourceAddress: []byte("111"), DestinationAddress: []byte("222"), ShortMessage: sm, ESMClass: 0x40,
	}
	return smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM}, SM: body}
}

func newReassemblySession(t *testing.T, store MultipartStore) (*Session, *capturePublisher, *fakeDeliverEncoder) {
	t.Helper()
	session := NewSessionWithDecoder(nil, Config{CID: "cid-1"}, nil, nil, nil, nil)
	publisher := &capturePublisher{}
	encoder := &fakeDeliverEncoder{pickled: []byte("pickled")}
	session.SetDeliverUpstream(publisher, encoder)
	session.SetMultipartStore(store)
	return session, publisher, encoder
}

func TestReassemblyPublishesOnlyWhenComplete(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []smppwire.PDU
	}{
		{"sar_in_order", []smppwire.PDU{sarPart(7, 3, 1, "aaa"), sarPart(7, 3, 2, "bbb"), sarPart(7, 3, 3, "ccc")}},
		{"sar_out_of_order", []smppwire.PDU{sarPart(7, 3, 3, "ccc"), sarPart(7, 3, 1, "aaa"), sarPart(7, 3, 2, "bbb")}},
		{"udh_in_order", []smppwire.PDU{udhPart(9, 2, 1, "hello "), udhPart(9, 2, 2, "world")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, publisher, _ := newReassemblySession(t, newMemMultipartStore())
			for i, part := range tc.parts {
				status := session.processDeliverMO(part)
				if status != 0 {
					t.Fatalf("part %d status=%#x", i, status)
				}
				if i < len(tc.parts)-1 {
					if len(publisher.published) != i+1 {
						t.Fatalf("published=%d after part %d want segments only", len(publisher.published), i)
					}
					for _, publication := range publisher.published {
						headers := publication.envelope.Properties().Headers()
						concatenated, _ := headers["concatenated"].Bool()
						if concatenated {
							t.Fatalf("whole message published before all parts arrived (after part %d)", i)
						}
					}
				}
			}
			if len(publisher.published) != len(tc.parts)+1 {
				t.Fatalf("published=%d want %d segments plus one whole", len(publisher.published), len(tc.parts))
			}
			var wholes int
			for _, publication := range publisher.published {
				headers := publication.envelope.Properties().Headers()
				concatenated, _ := headers["concatenated"].Bool()
				if concatenated {
					wholes++
				}
			}
			if wholes != 1 {
				t.Fatalf("whole publications=%d want 1", wholes)
			}
		})
	}
}

func TestReassemblyConcatenatesInSequenceOrder(t *testing.T) {
	store := newMemMultipartStore()
	session, _, encoder := newReassemblySession(t, store)
	// Deliver parts out of order; the decoded PDU must carry "onetwothree".
	for _, part := range []smppwire.PDU{sarPart(5, 3, 2, "two"), sarPart(5, 3, 3, "three"), sarPart(5, 3, 1, "one")} {
		if status := session.processDeliverMO(part); status != 0 {
			t.Fatalf("status=%#x", status)
		}
	}
	if got := string(encoder.pdu.SM.ShortMessage); got != "onetwothree" {
		t.Fatalf("reassembled short_message = %q want onetwothree", got)
	}
	if encoder.pdu.SM.ESMClass&0x40 != 0 {
		t.Fatal("reassembled message still has the UDHI indicator set")
	}
	if encoder.pdu.SM.Optional.SARMessageReference != nil {
		t.Fatal("reassembled message still carries SAR TLVs")
	}
}

func TestReassemblyLongContentPublishes(t *testing.T) {
	store := newMemMultipartStore()
	session, publisher, encoder := newReassemblySession(t, store)
	content := strings.Repeat("a", 100)
	var status uint32
	for sequence := byte(1); sequence <= 3; sequence++ {
		status = session.processDeliverMO(sarPart(11, 3, sequence, content))
	}
	if status != 0 {
		t.Fatalf("status=%#x want ROK", status)
	}
	if len(publisher.published) != 4 {
		t.Fatalf("published=%d want 3 segments plus one whole", len(publisher.published))
	}
	if len(encoder.pdu.SM.ShortMessage) != 300 {
		t.Fatalf("reassembled short_message length=%d want 300", len(encoder.pdu.SM.ShortMessage))
	}
}

func TestReassemblyPublishesSegmentsAndWholeWithRoutingMarkers(t *testing.T) {
	session, publisher, encoder := newReassemblySession(t, newMemMultipartStore())
	for _, part := range []smppwire.PDU{sarPart(17, 2, 1, "one"), sarPart(17, 2, 2, "two")} {
		if status := session.processDeliverMO(part); status != 0 {
			t.Fatalf("status=%#x", status)
		}
	}
	if len(publisher.published) != 3 {
		t.Fatalf("published=%d want 3 (two segments and one whole)", len(publisher.published))
	}
	var segments, wholes int
	for _, publication := range publisher.published {
		headers := publication.envelope.Properties().Headers()
		concatenated, concatenatedOK := headers["concatenated"].Bool()
		willBeConcatenated, willBeConcatenatedOK := headers["will_be_concatenated"].Bool()
		if !concatenatedOK || !willBeConcatenatedOK {
			t.Fatalf("routing markers are not bools: %+v", headers)
		}
		switch {
		case !concatenated && willBeConcatenated:
			segments++
		case concatenated && !willBeConcatenated:
			wholes++
		default:
			t.Fatalf("unexpected routing markers concatenated=%v will_be_concatenated=%v", concatenated, willBeConcatenated)
		}
	}
	if segments != 2 || wholes != 1 {
		t.Fatalf("segments=%d wholes=%d want 2/1", segments, wholes)
	}
	messageIDs := make(map[string]struct{}, len(publisher.published))
	for _, publication := range publisher.published {
		messageIDs[publication.envelope.Properties().MessageID()] = struct{}{}
	}
	if len(messageIDs) != len(publisher.published) {
		t.Fatalf("message IDs are not unique: %+v", messageIDs)
	}
	var encodedSegments, encodedWholes int
	for _, pdu := range encoder.pdus {
		if pdu.SM.Optional.SARMessageReference != nil {
			encodedSegments++
			continue
		}
		if string(pdu.SM.ShortMessage) == "onetwo" {
			encodedWholes++
		}
	}
	if encodedSegments != 2 || encodedWholes != 1 {
		t.Fatalf("encoded segments=%d wholes=%d want 2/1", encodedSegments, encodedWholes)
	}
}

func TestReassemblyMessagePayloadStaysInMessagePayload(t *testing.T) {
	store := newMemMultipartStore()
	session, _, encoder := newReassemblySession(t, store)
	content := strings.Repeat("b", 140)
	for sequence := byte(1); sequence <= 2; sequence++ {
		if status := session.processDeliverMO(sarPayloadPart(13, 2, sequence, content)); status != 0 {
			t.Fatalf("status=%#x", status)
		}
	}
	if len(encoder.pdu.SM.ShortMessage) != 0 {
		t.Fatalf("short_message length=%d want 0", len(encoder.pdu.SM.ShortMessage))
	}
	if len(encoder.pdu.SM.Optional.MessagePayload) != 280 {
		t.Fatalf("message_payload length=%d want 280", len(encoder.pdu.SM.Optional.MessagePayload))
	}
}

func TestReassemblyPublishFailureKeepsPartsForRetry(t *testing.T) {
	store := newMemMultipartStore()
	session, publisher, _ := newReassemblySession(t, store)
	publisher.err = errors.New("broker down")
	for sequence := byte(1); sequence <= 2; sequence++ {
		status := session.processDeliverMO(sarPart(12, 2, sequence, "part"))
		if status != smppStatusUnknownError {
			t.Fatalf("status=%#x want ESME_RUNKNOWNERR", status)
		}
	}
	if len(store.parts[key("cid-1", 12, "222")]) != 2 {
		t.Fatalf("stored parts=%d want 2 for retry", len(store.parts[key("cid-1", 12, "222")]))
	}
	publisher.err = nil
	if status := session.processDeliverMO(sarPart(12, 2, 2, "part")); status != 0 {
		t.Fatalf("retry status=%#x want ROK", status)
	}
	if len(publisher.published) != 4 {
		t.Fatalf("publish attempts=%d want 4 (two failed segments, retried segment, whole)", len(publisher.published))
	}
	if len(store.parts[key("cid-1", 12, "222")]) != 0 {
		t.Fatalf("stored parts=%d want 0 after successful retry", len(store.parts[key("cid-1", 12, "222")]))
	}
}

func TestReassemblyNilStoreDropsWithLog(t *testing.T) {
	session, publisher, _ := newReassemblySession(t, nil)
	var logged strings.Builder
	session.SetSubmitAuditLogger(slog.New(slog.NewTextHandler(&logged, nil)), false)
	if status := session.processDeliverMO(sarPart(1, 2, 1, "aa")); status != 0 {
		t.Fatalf("status=%#x", status)
	}
	if len(publisher.published) != 0 {
		t.Fatal("part published without a store")
	}
	if !strings.Contains(logged.String(), "MSG IS LOST") {
		t.Fatalf("legacy drop line missing: %q", logged.String())
	}
}
