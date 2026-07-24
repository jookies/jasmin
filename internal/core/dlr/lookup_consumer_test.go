package dlr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type scriptedCorrelator struct {
	submitEvents  []SubmitRespEvent
	deliverEvents []DeliverReceiptEvent
	err           error
}

func (s *scriptedCorrelator) OnSubmitResp(_ context.Context, ev SubmitRespEvent) error {
	s.submitEvents = append(s.submitEvents, ev)
	return s.err
}

func (s *scriptedCorrelator) OnDeliverReceipt(_ context.Context, ev DeliverReceiptEvent) error {
	s.deliverEvents = append(s.deliverEvents, ev)
	return s.err
}

type settlement struct {
	kind    string // "ack" or "reject"
	requeue bool
}

type settleRecorder struct{ settlements chan settlement }

func (r *settleRecorder) Ack(uint64, bool) error {
	r.settlements <- settlement{kind: "ack"}
	return nil
}

func (r *settleRecorder) Nack(_ uint64, _ bool, requeue bool) error {
	r.settlements <- settlement{kind: "reject", requeue: requeue}
	return nil
}

func (r *settleRecorder) Reject(_ uint64, requeue bool) error {
	r.settlements <- settlement{kind: "reject", requeue: requeue}
	return nil
}

// lookupDelivery builds a settleable delivery preserving header field kinds.
func lookupDelivery(t *testing.T, envelope amqpcompat.Envelope) (*amqpcompat.Delivery, chan settlement) {
	t.Helper()
	settlements := make(chan settlement, 2)
	raw := amqp.Delivery{
		Acknowledger: &settleRecorder{settlements: settlements},
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

func lookupEnvelope(t *testing.T, routingKey, messageID, body string, headers map[string]amqpcompat.Field) amqpcompat.Envelope {
	t.Helper()
	properties, err := amqpcompat.NewProperties(messageID, headers)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope(routingKey, properties, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func deliverReceiptEnvelope(t *testing.T, messageID string) amqpcompat.Envelope {
	t.Helper()
	return lookupEnvelope(t, "dlr.deliver_sm", messageID, "DELIVRD", map[string]amqpcompat.Field{
		"type":      amqpcompat.StringField("deliver_sm"),
		"cid":       amqpcompat.StringField("smpp-01"),
		"dlr_id":    amqpcompat.StringField("6aad5"),
		"dlr_ddate": amqpcompat.StringField("2101011201"),
		"dlr_sdate": amqpcompat.StringField("2101011200"),
		"dlr_sub":   amqpcompat.StringField("001"),
		"dlr_err":   amqpcompat.StringField("000"),
		"dlr_text":  amqpcompat.StringField("delivered"),
		"dlr_dlvrd": amqpcompat.StringField("001"),
	})
}

func expectSettlement(t *testing.T, settlements chan settlement, kind string, requeue bool) {
	t.Helper()
	select {
	case got := <-settlements:
		if got.kind != kind || got.requeue != requeue {
			t.Fatalf("settlement = %+v, want %s requeue=%v", got, kind, requeue)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no settlement, want %s requeue=%v", kind, requeue)
	}
}

func expectNoSettlement(t *testing.T, settlements chan settlement) {
	t.Helper()
	select {
	case got := <-settlements:
		t.Fatalf("unexpected settlement %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestLookupConsumerDecodesFrozenSubmitRespCase(t *testing.T) {
	correlator := &scriptedCorrelator{}
	consumer, err := NewLookupConsumer(correlator, LookupConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	envelope := loadThrowerFixtureEnvelope(t, "dlr_lookup_submit_sm_resp")
	delivery, settlements := lookupDelivery(t, envelope)
	if err := consumer.Handle(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	expectSettlement(t, settlements, "ack", false)
	if len(correlator.submitEvents) != 1 {
		t.Fatalf("submit events = %d", len(correlator.submitEvents))
	}
	event := correlator.submitEvents[0]
	if event.QueueMsgID != "11111111-1111-4111-8111-111111111111" ||
		event.SMPPMsgID != "436949" || event.Status != "ESME_ROK" {
		t.Fatalf("event = %+v", event)
	}
}

func TestLookupConsumerDecodesDeliverReceipt(t *testing.T) {
	correlator := &scriptedCorrelator{}
	consumer, _ := NewLookupConsumer(correlator, LookupConsumerConfig{})
	delivery, settlements := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
	if err := consumer.Handle(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	expectSettlement(t, settlements, "ack", false)
	event := correlator.deliverEvents[0]
	if event.CodedID != "6AAD5" || event.RawDLRID != "6aad5" || event.ConnectorID != "smpp-01" ||
		event.Status != "DELIVRD" || event.Sub != "001" || event.Dlvrd != "001" ||
		event.SubmitDate != "2101011200" || event.DoneDate != "2101011201" ||
		event.Err != "000" || event.Text != "delivered" {
		t.Fatalf("event = %+v", event)
	}
}

func TestLookupConsumerFinalErrorsRejectWithoutRequeue(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{"map invalid", fmt.Errorf("%w: bad sc", ErrDLRMapInvalid)},
		{"forward publish", fmt.Errorf("%w: amqp down", ErrForwardPublish)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			correlator := &scriptedCorrelator{err: testCase.err}
			consumer, _ := NewLookupConsumer(correlator, LookupConsumerConfig{})
			delivery, settlements := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
			if err := consumer.Handle(context.Background(), delivery); !errors.Is(err, testCase.err) {
				t.Fatalf("Handle error = %v", err)
			}
			expectSettlement(t, settlements, "reject", false)
		})
	}
}

func TestLookupConsumerMapNotFoundPolicyPerLeg(t *testing.T) {
	notFound := fmt.Errorf("%w: q", ErrDLRMapNotFound)

	// submit_sm_resp leg: reject immediately, no retry.
	correlator := &scriptedCorrelator{err: notFound}
	consumer, _ := NewLookupConsumer(correlator, LookupConsumerConfig{RetryDelay: 80 * time.Millisecond})
	submit := lookupEnvelope(t, "dlr.submit_sm_resp", "q-1", "ESME_ROK",
		map[string]amqpcompat.Field{"type": amqpcompat.StringField("submit_sm_resp"), "smpp_msgid": amqpcompat.StringField("6AAD5")})
	delivery, settlements := lookupDelivery(t, submit)
	_ = consumer.Handle(context.Background(), delivery)
	expectSettlement(t, settlements, "reject", false)

	// deliver_sm leg: the terminal receipt can race the mapping — delayed requeue.
	delivery2, settlements2 := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
	_ = consumer.Handle(context.Background(), delivery2)
	expectNoSettlement(t, settlements2)
	expectSettlement(t, settlements2, "reject", true)
}

func TestLookupConsumerRetriesTransientErrorsUntilCap(t *testing.T) {
	transient := errors.New("redis connection refused")
	correlator := &scriptedCorrelator{err: transient}
	consumer, _ := NewLookupConsumer(correlator, LookupConsumerConfig{MaxRetries: 2, RetryDelay: 10 * time.Millisecond})

	// Attempt 1: below the cap — delayed requeue.
	delivery, settlements := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
	_ = consumer.Handle(context.Background(), delivery)
	expectSettlement(t, settlements, "reject", true)

	// Attempt 2 (redelivery): at the cap — final reject, tracker cleared.
	delivery2, settlements2 := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
	_ = consumer.Handle(context.Background(), delivery2)
	expectSettlement(t, settlements2, "reject", false)

	// The tracker was cleared: a fresh message id cycle retries again.
	delivery3, settlements3 := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
	_ = consumer.Handle(context.Background(), delivery3)
	expectSettlement(t, settlements3, "reject", true)
}

func TestLookupConsumerTimerResetLeavesDuplicateUnsettled(t *testing.T) {
	transient := errors.New("redis down")
	correlator := &scriptedCorrelator{err: transient}
	consumer, _ := NewLookupConsumer(correlator, LookupConsumerConfig{MaxRetries: 3, RetryDelay: 40 * time.Millisecond})

	delivery, settlements := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
	_ = consumer.Handle(context.Background(), delivery)

	// A duplicate of the same message id while the timer pends: the legacy code
	// resets the timer and the duplicate delivery stays unsettled.
	duplicate, duplicateSettlements := lookupDelivery(t, deliverReceiptEnvelope(t, "6AAD5"))
	_ = consumer.Handle(context.Background(), duplicate)

	expectSettlement(t, settlements, "reject", true)
	expectNoSettlement(t, duplicateSettlements)
}

func TestLookupConsumerRejectsMalformedDeliveries(t *testing.T) {
	correlator := &scriptedCorrelator{}
	consumer, _ := NewLookupConsumer(correlator, LookupConsumerConfig{})

	// A parseable routing key that is not a lookup leg — the legacy dispatcher's
	// unknown-key reject. (Unparseable keys never reach Handle: the amqpcompat
	// boundary rejects them at delivery construction.)
	unknown := lookupEnvelope(t, "dlr_thrower.http", "q-1", "x", nil)
	delivery, settlements := lookupDelivery(t, unknown)
	if err := consumer.Handle(context.Background(), delivery); !errors.Is(err, ErrInvalidLookupDelivery) {
		t.Fatalf("error = %v", err)
	}
	expectSettlement(t, settlements, "reject", false)

	// ESME_ROK without smpp_msgid: the legacy success branch KeyErrors.
	missing := lookupEnvelope(t, "dlr.submit_sm_resp", "q-2", "ESME_ROK",
		map[string]amqpcompat.Field{"type": amqpcompat.StringField("submit_sm_resp")})
	delivery2, settlements2 := lookupDelivery(t, missing)
	if err := consumer.Handle(context.Background(), delivery2); !errors.Is(err, ErrInvalidLookupDelivery) {
		t.Fatalf("error = %v", err)
	}
	expectSettlement(t, settlements2, "reject", false)

	// A failed status without smpp_msgid is fine (the legacy code never reads it).
	failed := lookupEnvelope(t, "dlr.submit_sm_resp", "q-3", "ESME_RSYSERR",
		map[string]amqpcompat.Field{"type": amqpcompat.StringField("submit_sm_resp")})
	delivery3, settlements3 := lookupDelivery(t, failed)
	if err := consumer.Handle(context.Background(), delivery3); err != nil {
		t.Fatal(err)
	}
	expectSettlement(t, settlements3, "ack", false)
	if len(correlator.submitEvents) != 1 || correlator.submitEvents[0].SMPPMsgID != "" {
		t.Fatalf("events = %+v", correlator.submitEvents)
	}
}

// legacyDLRScript constructs the legacy DLR lookup content for the same inputs
// and dumps its identity, kind-tagged headers, and body.
const legacyDLRScript = `
import json, sys
from smpp.pdu.pdu_types import CommandId, CommandStatus
from jasmin.managers.content import DLR

request = json.load(sys.stdin)
if request["kind"] == "submit":
    content = DLR(pdu_type=CommandId.submit_sm_resp, msgid=request["msgid"],
                  status=getattr(CommandStatus, request["status"]),
                  smpp_msgid=request["smpp_msgid"].encode())
else:
    content = DLR(pdu_type=CommandId.deliver_sm, msgid=request["msgid"],
                  status=request["status"], cid=request["cid"],
                  dlr_details=request["dlr_details"])
body = content.body
if isinstance(body, bytes):
    body = body.decode()
headers = {}
for name, value in content.properties["headers"].items():
    if isinstance(value, bool) or not isinstance(value, int):
        headers[name] = {"kind": "str", "value": str(value)}
    else:
        headers[name] = {"kind": "int", "value": value}
print(json.dumps({"message_id": content.properties["message-id"], "body": body, "headers": headers}))
`

func TestLookupConsumerDifferentialAgainstLegacyDLRContent(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name    string
		request map[string]any
		routing string
		verify  func(t *testing.T, correlator *scriptedCorrelator)
	}{
		{
			name: "submit_sm_resp with zero-padded smpp msgid",
			request: map[string]any{"kind": "submit", "msgid": "q-1", "status": "ESME_ROK",
				"smpp_msgid": "00436949"},
			routing: "dlr.submit_sm_resp",
			verify: func(t *testing.T, correlator *scriptedCorrelator) {
				event := correlator.submitEvents[0]
				// The legacy constructor canonicalizes: upper + lstrip('0').
				if event.SMPPMsgID != "436949" || event.Status != "ESME_ROK" || event.QueueMsgID != "q-1" {
					t.Fatalf("event = %+v", event)
				}
			},
		},
		{
			name: "deliver_sm with mixed-kind receipt fields",
			request: map[string]any{"kind": "deliver", "msgid": "6AAD5", "status": "DELIVRD",
				"cid": "smpp-01", "dlr_details": map[string]any{
					"id": "6aad5", "sub": "001", "dlvrd": "001", "sdate": "2101011200",
					"ddate": "2101011201", "err": 0, "text": "delivered", "stat": "DELIVRD",
				}},
			routing: "dlr.deliver_sm",
			verify: func(t *testing.T, correlator *scriptedCorrelator) {
				event := correlator.deliverEvents[0]
				// The integer err field arrives as an AMQP integer and reads back
				// as its decimal string.
				if event.CodedID != "6AAD5" || event.RawDLRID != "6aad5" || event.Err != "0" ||
					event.Status != "DELIVRD" || event.Text != "delivered" {
					t.Fatalf("event = %+v", event)
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := json.Marshal(testCase.request)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, pythonPath, "-c", legacyDLRScript)
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			command.Stdin = bytes.NewReader(request)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			var oracle struct {
				MessageID string `json:"message_id"`
				Body      string `json:"body"`
				Headers   map[string]struct {
					Kind  string `json:"kind"`
					Value any    `json:"value"`
				} `json:"headers"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
				t.Fatalf("oracle output %q: %v", output, err)
			}
			headers := make(map[string]amqpcompat.Field, len(oracle.Headers))
			for name, value := range oracle.Headers {
				if value.Kind == "int" {
					headers[name] = amqpcompat.IntegerField(int64(value.Value.(float64)))
				} else {
					headers[name] = amqpcompat.StringField(value.Value.(string))
				}
			}
			envelope := lookupEnvelope(t, testCase.routing, oracle.MessageID, oracle.Body, headers)

			correlator := &scriptedCorrelator{}
			consumer, _ := NewLookupConsumer(correlator, LookupConsumerConfig{})
			delivery, settlements := lookupDelivery(t, envelope)
			if err := consumer.Handle(context.Background(), delivery); err != nil {
				t.Fatal(err)
			}
			expectSettlement(t, settlements, "ack", false)
			testCase.verify(t, correlator)
		})
	}
}
