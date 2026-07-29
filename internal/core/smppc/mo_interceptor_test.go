package smppc

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// stubMOInterceptor returns a fixed result/error and records what it saw.
type stubMOInterceptor struct {
	result MOInterceptResult
	err    error
	calls  int
	last   MOInterceptData
}

func (s *stubMOInterceptor) InterceptMO(_ context.Context, in MOInterceptData) (MOInterceptResult, error) {
	s.calls++
	s.last = in
	return s.result, s.err
}

// passthroughMO returns the fields it received unchanged — the contract the
// real adapter honours for scripts that do not mutate.
type passthroughMO struct{ last MOInterceptData }

func (p *passthroughMO) InterceptMO(_ context.Context, in MOInterceptData) (MOInterceptResult, error) {
	p.last = in
	return MOInterceptResult{SourceAddr: in.SourceAddr, DestinationAddr: in.DestinationAddr, ShortMessage: in.ShortMessage}, nil
}

func newMOInterceptSession(t *testing.T) (*Session, *capturePublisher, *fakeDeliverEncoder, *strings.Builder) {
	t.Helper()
	session := NewSessionWithDecoder(nil, Config{CID: "cid-1"}, nil, nil, nil, nil)
	publisher := &capturePublisher{}
	encoder := &fakeDeliverEncoder{pickled: []byte("pickled")}
	session.SetDeliverUpstream(publisher, encoder)
	var logged strings.Builder
	session.SetSubmitAuditLogger(slog.New(slog.NewTextHandler(&logged, nil)), false)
	return session, publisher, encoder, &logged
}

func TestMOInterceptorRejectDropsWithoutPublish(t *testing.T) {
	session, publisher, _, logged := newMOInterceptSession(t)
	moi := &stubMOInterceptor{result: MOInterceptResult{Reject: true}}
	session.SetMOInterceptor(moi)

	if status := session.processDeliverMO(deliverPDU([]byte("blocked"), nil)); status != 0 {
		t.Fatalf("status=%#x want ROK (intercept drop acks the SMSC)", status)
	}
	if moi.calls != 1 {
		t.Fatalf("interceptor called %d times, want 1", moi.calls)
	}
	if len(publisher.published) != 0 {
		t.Fatalf("rejected MO must not publish, got %d", len(publisher.published))
	}
	if !strings.Contains(logged.String(), "rejected by interceptor") {
		t.Fatalf("drop line missing: %q", logged.String())
	}
}

func TestMOInterceptorMutationRewritesPublishedPDU(t *testing.T) {
	session, publisher, encoder, _ := newMOInterceptSession(t)
	session.SetMOInterceptor(&stubMOInterceptor{result: MOInterceptResult{
		SourceAddr: []byte("9999"), DestinationAddr: []byte("8888"), ShortMessage: []byte("mutated"),
	}})

	if status := session.processDeliverMO(deliverPDU([]byte("original"), nil)); status != 0 {
		t.Fatalf("status=%#x want ROK", status)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("published=%d want 1", len(publisher.published))
	}
	if string(encoder.pdu.SM.SourceAddress) != "9999" ||
		string(encoder.pdu.SM.DestinationAddress) != "8888" ||
		string(encoder.pdu.SM.ShortMessage) != "mutated" {
		t.Fatalf("mutation not applied: src=%q dst=%q msg=%q",
			encoder.pdu.SM.SourceAddress, encoder.pdu.SM.DestinationAddress, encoder.pdu.SM.ShortMessage)
	}
}

func TestMOInterceptorErrorRespondsUnknownError(t *testing.T) {
	session, publisher, _, _ := newMOInterceptSession(t)
	session.SetMOInterceptor(&stubMOInterceptor{err: errors.New("script boom")})

	if status := session.processDeliverMO(deliverPDU([]byte("hi"), nil)); status != smppStatusUnknownError {
		t.Fatalf("status=%#x want ESME_RUNKNOWNERR", status)
	}
	if len(publisher.published) != 0 {
		t.Fatal("interceptor error must not publish")
	}
}

func TestMOInterceptorPassthroughPreservesFieldsAndContent(t *testing.T) {
	session, _, encoder, _ := newMOInterceptSession(t)
	moi := &passthroughMO{}
	session.SetMOInterceptor(moi)

	if status := session.processDeliverMO(deliverPDU([]byte("keep me"), nil)); status != 0 {
		t.Fatalf("status=%#x want ROK", status)
	}
	if moi.last.ConnectorID != "cid-1" || string(moi.last.SourceAddr) != "1111" ||
		string(moi.last.DestinationAddr) != "2222" || string(moi.last.ShortMessage) != "keep me" {
		t.Fatalf("intercept data wrong: %+v", moi.last)
	}
	if string(encoder.pdu.SM.ShortMessage) != "keep me" {
		t.Fatalf("passthrough altered content: %q", encoder.pdu.SM.ShortMessage)
	}
}

func TestMOInterceptorRunsOnReassembledWhole(t *testing.T) {
	session, publisher, _ := newReassemblySession(t, newMemMultipartStore())
	moi := &stubMOInterceptor{result: MOInterceptResult{Reject: true}}
	session.SetMOInterceptor(moi)

	// Two SAR parts -> one whole "onetwo". Interception runs on every arriving
	// segment AND on the reassembled whole: 3 calls for a 2-part message.
	//
	// This is a deliberate, documented divergence in both directions. The oracle
	// intercepts each arriving PDU (deliver_sm_event_interceptor) and publishes
	// the whole through deliver_sm_event_post_interception
	// (jasmin/managers/listeners.py:535), so it never intercepts the whole. Go
	// intercepts the whole on purpose (plan 009) so a content filter sees the
	// full text instead of a fragment. Segments must be intercepted too, because
	// they are published as they arrive: without it, a reject suppresses only the
	// whole while every segment has already reached the SMPPs destinations.
	for i, part := range []smppwire.PDU{sarPart(5, 2, 1, "one"), sarPart(5, 2, 2, "two")} {
		if status := session.processDeliverMO(part); status != 0 {
			t.Fatalf("part %d status=%#x", i, status)
		}
	}
	if moi.calls != 3 {
		t.Fatalf("interceptor called %d times, want 3 (each segment plus the whole)", moi.calls)
	}
	if string(moi.last.ShortMessage) != "onetwo" {
		t.Fatalf("interceptor saw %q, want reassembled onetwo", moi.last.ShortMessage)
	}
	// Reject must now suppress the segments as well as the whole.
	if len(publisher.published) != 0 {
		t.Fatalf("published=%d; a rejected multipart MO must publish nothing", len(publisher.published))
	}
	for _, publication := range publisher.published {
		headers := publication.envelope.Properties().Headers()
		concatenated, _ := headers["concatenated"].Bool()
		willBeConcatenated, _ := headers["will_be_concatenated"].Bool()
		if concatenated || !willBeConcatenated {
			t.Fatalf("rejected whole was published: %+v", headers)
		}
	}
}
