package smppc

import (
	"errors"
	"sync"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

func TestSubmitResponsePublicationDisabledIgnoresInvalidInputs(t *testing.T) {
	publication, err := NewSubmitResponsePublication(false, "", "", "", "", make([]byte, amqpcompat.MaxBodySize+1))
	if err != nil || publication != nil {
		t.Fatalf("disabled publication = (%v, %v), want (nil, nil)", publication, err)
	}
}

func TestSubmitResponsePublicationRejectsNonResponseRoute(t *testing.T) {
	_, err := NewSubmitResponsePublication(
		true, SubmitResponseAck, "submit.sm.connector-a", "message-1", "now", []byte("body"),
	)
	if !errors.Is(err, ErrInvalidSubmitResponsePublication) {
		t.Fatalf("error = %v, want ErrInvalidSubmitResponsePublication", err)
	}
}

func TestSubmitResponsePublicationRejectsInvalidBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		action     SubmitResponseAction
		routingKey string
		messageID  string
		createdAt  string
		body       []byte
	}{
		{"invalid action", "invalid", "submit.sm.resp.user-1", "message-1", "now", nil},
		{"empty routing key", SubmitResponseAck, "", "message-1", "now", nil},
		{"empty target", SubmitResponseAck, "submit.sm.resp.", "message-1", "now", nil},
		{"nested target", SubmitResponseAck, "submit.sm.resp.user.extra", "message-1", "now", nil},
		{"empty message ID", SubmitResponseAck, "submit.sm.resp.user-1", "", "now", nil},
		{"empty created at", SubmitResponseAck, "submit.sm.resp.user-1", "message-1", "", nil},
		{"oversized body", SubmitResponseRequeue, "submit.sm.resp.user-1", "message-1", "now", make([]byte, amqpcompat.MaxBodySize+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSubmitResponsePublication(
				true, tc.action, tc.routingKey, tc.messageID, tc.createdAt, tc.body,
			)
			if !errors.Is(err, ErrInvalidSubmitResponsePublication) {
				t.Fatalf("error = %v, want ErrInvalidSubmitResponsePublication", err)
			}
		})
	}
}

func TestSubmitResponsePublicationConstructsExactProperties(t *testing.T) {
	publication, err := NewSubmitResponsePublication(
		true, SubmitResponseRequeue, "submit.sm.resp.user-1", "message-1", "2026-01-02 03:04:05.678901", []byte{0x80, 2, 'x'},
	)
	if err != nil {
		t.Fatal(err)
	}
	properties := publication.Properties()
	if properties.MessageID() != "message-1" {
		t.Fatalf("message ID = %q", properties.MessageID())
	}
	headers := properties.Headers()
	if len(headers) != 1 {
		t.Fatalf("header count = %d, want 1", len(headers))
	}
	createdAt, ok := headers["created_at"].String()
	if !ok || createdAt != "2026-01-02 03:04:05.678901" {
		t.Fatalf("created_at = (%q, %v)", createdAt, ok)
	}
	if _, ok := properties.ReplyTo(); ok {
		t.Fatal("unexpected reply-to")
	}
	if _, ok := properties.Priority(); ok {
		t.Fatal("unexpected priority")
	}
}

func TestSubmitResponsePublicationIsConcurrentAndImmutable(t *testing.T) {
	body := []byte{0x80, 2, 'x'}
	publication, err := NewSubmitResponsePublication(
		true, SubmitResponseAck, "submit.sm.resp.user-1", "message-1", "2026-01-02 03:04:05.678901", body,
	)
	if err != nil {
		t.Fatal(err)
	}
	body[2] = 'y'

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			got := publication.Body()
			if len(got) != 3 || got[2] != 'x' {
				t.Errorf("body = %x, want immutable pickle marker", got)
				return
			}
			got[2] = 'z'
			if publication.Body()[2] != 'x' {
				t.Error("body accessor exposed publication storage")
			}
			if publication.Properties().MessageID() != "message-1" {
				t.Error("message ID changed")
			}
		}()
	}
	wg.Wait()
}

func TestLateBillingIntentUsesPartSpecificPublicationIdentity(t *testing.T) {
	first, err := newLateBillingIntent(DurableResponseInput{
		PartKey: "aggregate/000001", UserID: "user-1", BillID: "bill-1", LateBillAmount: "0.75",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := newLateBillingIntent(DurableResponseInput{
		PartKey: "aggregate/000002", UserID: "user-1", BillID: "bill-1", LateBillAmount: "0.75",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.Properties().MessageID()
	secondID := second.Properties().MessageID()
	if firstID != "aggregate/000001:20-late-billing" || secondID != "aggregate/000002:20-late-billing" {
		t.Fatalf("late-billing IDs=(%q,%q)", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatal("distinct part events reused one AMQP message-id")
	}
	if string(first.Body()) != "bill-1" || string(second.Body()) != "bill-1" {
		t.Fatalf("legacy bill bodies=(%q,%q)", first.Body(), second.Body())
	}
}
