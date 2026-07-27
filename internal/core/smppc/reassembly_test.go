package smppc

import (
	"bytes"
	"context"
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
				if i < len(tc.parts)-1 && len(publisher.published) != 0 {
					t.Fatalf("published before all parts arrived (after part %d)", i)
				}
			}
			if len(publisher.published) != 1 {
				t.Fatalf("published=%d want 1 (whole message)", len(publisher.published))
			}
		})
	}
}

func TestReassemblyConcatenatesInSequenceOrder(t *testing.T) {
	store := newMemMultipartStore()
	session, _, encoder := newReassemblySession(t, store)
	// Deliver parts out of order; the re-encoded wire must carry "onetwothree".
	for _, part := range []smppwire.PDU{sarPart(5, 3, 2, "two"), sarPart(5, 3, 3, "three"), sarPart(5, 3, 1, "one")} {
		if status := session.processDeliverMO(part); status != 0 {
			t.Fatalf("status=%#x", status)
		}
	}
	reDecoded, err := smppwire.Read(bytes.NewReader(encoder.wire), smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(reDecoded.SM.ShortMessage); got != "onetwothree" {
		t.Fatalf("reassembled short_message = %q want onetwothree", got)
	}
	if reDecoded.SM.ESMClass&0x40 != 0 {
		t.Fatal("reassembled message still has the UDHI indicator set")
	}
	if reDecoded.SM.Optional.SARMessageReference != nil {
		t.Fatal("reassembled message still carries SAR TLVs")
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
