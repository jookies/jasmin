package dlr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

type throwerSettleRecorder struct{ settlements chan settlementRecord }

type settlementRecord struct {
	kind    string
	requeue bool
}

func (r *throwerSettleRecorder) Ack(uint64, bool) error {
	r.settlements <- settlementRecord{kind: "ack"}
	return nil
}

func (r *throwerSettleRecorder) Nack(_ uint64, _ bool, requeue bool) error {
	r.settlements <- settlementRecord{kind: "reject", requeue: requeue}
	return nil
}

func (r *throwerSettleRecorder) Reject(_ uint64, requeue bool) error {
	r.settlements <- settlementRecord{kind: "reject", requeue: requeue}
	return nil
}

func throwerDelivery(t *testing.T, envelope amqpcompat.Envelope) (*amqpcompat.Delivery, chan settlementRecord) {
	t.Helper()
	settlements := make(chan settlementRecord, 2)
	raw := amqp.Delivery{
		Acknowledger: &throwerSettleRecorder{settlements: settlements},
		MessageId:    envelope.Properties().MessageID(),
		RoutingKey:   envelope.RoutingKey(),
		Body:         envelope.Body(),
		Headers:      make(amqp.Table),
	}
	for name, field := range envelope.Properties().Headers() {
		if value, ok := field.String(); ok {
			raw.Headers[name] = value
			continue
		}
		if value, ok := field.Integer(); ok {
			raw.Headers[name] = value
			continue
		}
		t.Fatalf("header %s has unsupported kind", name)
	}
	delivery, err := amqpcompat.NewDelivery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return delivery, settlements
}

func httpForwardEnvelope(t *testing.T, targetURL string) amqpcompat.Envelope {
	t.Helper()
	forward := Forward{
		Target: ForwardHTTP, Status: "DELIVRD", QueueMsgID: "11111111-1111-4111-8111-111111111111",
		Level: 2, URL: targetURL, Method: "POST", Connector: "0000436949",
		IDSMSC: "436949", Sub: "001", Dlvrd: "001", SubmitDate: "2601020304",
		DoneDate: "2601020305", Err: "000", Text: "hello",
	}
	envelope, err := EncodeThrowerForward(forward)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func waitSettlement(t *testing.T, settlements chan settlementRecord) settlementRecord {
	t.Helper()
	select {
	case got := <-settlements:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("no settlement")
		return settlementRecord{}
	}
}

func TestThrowerConsumerAcksOnLegacyAck(t *testing.T) {
	received := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		received <- r.PostForm
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer server.Close()

	consumer, err := NewThrowerConsumer(server.Client(), nil, ThrowerConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	delivery, settlements := throwerDelivery(t, httpForwardEnvelope(t, server.URL))
	if err := consumer.Handle(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if got := waitSettlement(t, settlements); got.kind != "ack" {
		t.Fatalf("settlement = %+v", got)
	}
	form := <-received
	// The legacy callback's mandatory + level-2 argument set, including the
	// receipt-id connector quirk.
	for key, want := range map[string]string{
		"id": "11111111-1111-4111-8111-111111111111", "level": "2", "message_status": "DELIVRD",
		"connector": "0000436949", "id_smsc": "436949", "sub": "001", "dlvrd": "001",
		"subdate": "2601020304", "donedate": "2601020305", "err": "000", "text": "hello",
	} {
		if form.Get(key) != want {
			t.Errorf("form[%s] = %q, want %q", key, form.Get(key), want)
		}
	}
}

func TestThrowerConsumerRetriesThenPurges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("NACK")) // wrong body: Q-005 requires exact ACK/Jasmin
	}))
	defer server.Close()

	consumer, err := NewThrowerConsumer(server.Client(), nil, ThrowerConsumerConfig{MaxRetries: 2, RetryDelay: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	envelope := httpForwardEnvelope(t, server.URL)

	// Attempts 1 and 2 (count <= 2): delayed requeue.
	for attempt := 1; attempt <= 2; attempt++ {
		delivery, settlements := throwerDelivery(t, envelope)
		if err := consumer.Handle(context.Background(), delivery); !errors.Is(err, ErrDLRNotAcknowledged) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if got := waitSettlement(t, settlements); got.kind != "reject" || !got.requeue {
			t.Fatalf("attempt %d settlement = %+v", attempt, got)
		}
	}
	// Attempt 3 (count > 2): purged.
	delivery, settlements := throwerDelivery(t, envelope)
	_ = consumer.Handle(context.Background(), delivery)
	if got := waitSettlement(t, settlements); got.kind != "reject" || got.requeue {
		t.Fatalf("purge settlement = %+v", got)
	}
	// Tracker cleared: the next cycle retries again.
	delivery2, settlements2 := throwerDelivery(t, envelope)
	_ = consumer.Handle(context.Background(), delivery2)
	if got := waitSettlement(t, settlements2); got.kind != "reject" || !got.requeue {
		t.Fatalf("fresh cycle settlement = %+v", got)
	}
}

// Q-019: the legacy no-retry list for 404 responses is dead code — a 404
// retries like any other failure.
func TestThrowerConsumer404RetriesLikeAnyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()

	consumer, err := NewThrowerConsumer(server.Client(), nil, ThrowerConsumerConfig{MaxRetries: 3, RetryDelay: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	delivery, settlements := throwerDelivery(t, httpForwardEnvelope(t, server.URL))
	if err := consumer.Handle(context.Background(), delivery); !errors.Is(err, ErrDLRHTTPStatus) {
		t.Fatalf("error = %v", err)
	}
	if got := waitSettlement(t, settlements); got.kind != "reject" || !got.requeue {
		t.Fatalf("404 settlement = %+v, want delayed requeue (Q-019)", got)
	}
}

func TestThrowerConsumerSMPPSWithoutSinkRetries(t *testing.T) {
	consumer, err := NewThrowerConsumer(http.DefaultClient, nil, ThrowerConsumerConfig{MaxRetries: 1, RetryDelay: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	// Built in Go; the captured fixture corpus went with the Python reference.
	// A fully-formed SMPPs forward: the TON/NPI values must be the legacy enum
	// names, or the envelope decodes as invalid and is rejected as poison rather
	// than requeued -- which is not what this test is about.
	envelope, err := EncodeThrowerForward(Forward{
		Target:          ForwardSMPPS,
		Status:          "DELIVRD",
		QueueMsgID:      "11111111-1111-4111-8111-111111111111",
		SystemID:        "esme-1",
		SubDate:         "2601020304",
		SourceAddr:      "1111",
		DestinationAddr: "2222",
		SourceAddrTON:   "AddrTon.NATIONAL",
		SourceAddrNPI:   "AddrNpi.ISDN",
		DestAddrTON:     "AddrTon.INTERNATIONAL",
		DestAddrNPI:     "AddrNpi.ISDN",
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery, settlements := throwerDelivery(t, envelope)
	if err := consumer.Handle(context.Background(), delivery); err == nil {
		t.Fatal("want error without an SMPPS sink")
	}
	if got := waitSettlement(t, settlements); got.kind != "reject" || !got.requeue {
		t.Fatalf("settlement = %+v", got)
	}
}

func TestThrowerConsumerRejectsInvalidEnvelope(t *testing.T) {
	consumer, err := NewThrowerConsumer(http.DefaultClient, nil, ThrowerConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	// A lookup-leg envelope is outside the frozen thrower contract.
	properties, _ := amqpcompat.NewProperties("q-1", map[string]amqpcompat.Field{
		"type": amqpcompat.StringField("submit_sm_resp"), "smpp_msgid": amqpcompat.StringField("6AAD5"),
	})
	envelope, _ := amqpcompat.NewEnvelope("dlr.submit_sm_resp", properties, []byte("ESME_ROK"))
	delivery, settlements := throwerDelivery(t, envelope)
	if err := consumer.Handle(context.Background(), delivery); !errors.Is(err, ErrInvalidThrowerEnvelope) {
		t.Fatalf("error = %v", err)
	}
	if got := waitSettlement(t, settlements); got.kind != "reject" || got.requeue {
		t.Fatalf("settlement = %+v", got)
	}
}

// The frozen fixture's level-3 envelope drives a real callback end to end.
func TestThrowerConsumerThrowsLevel3CallbackForm(t *testing.T) {
	received := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		received <- r.PostForm
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer server.Close()

	// Built in Go rather than loaded from a captured fixture: the fixture corpus
	// went with the Python reference, but the assertion below is the part that
	// matters -- it pins the exact level-3 DLR callback form a customer's webhook
	// parses, including the id_smsc coding and the ACK/Jasmin contract.
	forward := Forward{
		Target:     ForwardHTTP,
		Status:     "DELIVRD",
		QueueMsgID: "11111111-1111-4111-8111-111111111111",
		Level:      3,
		URL:        server.URL,
		Method:     "POST",
		Connector:  "connector-a",
		IDSMSC:     "0000436949",
		Sub:        "001",
		Dlvrd:      "001",
		SubmitDate: "2601020304",
		DoneDate:   "2601020305",
		Err:        "000",
		Text:       "hello",
	}
	envelope, err := EncodeThrowerForward(forward)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := NewThrowerConsumer(server.Client(), nil, ThrowerConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	delivery, settlements := throwerDelivery(t, envelope)
	if err := consumer.Handle(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if got := waitSettlement(t, settlements); got.kind != "ack" {
		t.Fatalf("settlement = %+v", got)
	}
	form := <-received
	for key, want := range map[string]string{
		"id": "11111111-1111-4111-8111-111111111111", "level": "3", "message_status": "DELIVRD",
		"connector": "connector-a", "id_smsc": "0000436949", "sub": "001", "dlvrd": "001",
		"subdate": "2601020304", "donedate": "2601020305", "err": "000", "text": "hello",
	} {
		if form.Get(key) != want {
			t.Errorf("form[%s] = %q, want %q", key, form.Get(key), want)
		}
	}
}
