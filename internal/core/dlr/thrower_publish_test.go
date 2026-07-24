package dlr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

// loadThrowerFixtureEnvelope rebuilds one frozen thrower envelope from the
// committed AMQP fixture.
func loadThrowerFixtureEnvelope(t *testing.T, caseID string) amqpcompat.Envelope {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "compat", "fixtures", "amqp", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document throwerFixtureDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, tc := range document.Cases {
		if tc.ID != caseID {
			continue
		}
		headers := make(map[string]amqpcompat.Field, len(tc.Properties.Headers))
		for name, value := range tc.Properties.Headers {
			var text string
			if json.Unmarshal(value, &text) == nil {
				headers[name] = amqpcompat.StringField(text)
				continue
			}
			var number int64
			if err := json.Unmarshal(value, &number); err != nil {
				t.Fatalf("header %s: %v", name, err)
			}
			headers[name] = amqpcompat.IntegerField(number)
		}
		properties, err := amqpcompat.NewProperties(tc.Properties.MessageID, headers)
		if err != nil {
			t.Fatal(err)
		}
		body, err := base64.StdEncoding.DecodeString(tc.Body.WireBase64)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := amqpcompat.NewEnvelope(tc.RoutingKey, properties, body)
		if err != nil {
			t.Fatal(err)
		}
		return envelope
	}
	t.Fatalf("fixture case %s not found", caseID)
	return amqpcompat.Envelope{}
}

func envelopesEqual(t *testing.T, got, want amqpcompat.Envelope) {
	t.Helper()
	if got.RoutingKey() != want.RoutingKey() {
		t.Fatalf("routing key = %s, want %s", got.RoutingKey(), want.RoutingKey())
	}
	if got.Properties().MessageID() != want.Properties().MessageID() {
		t.Fatalf("message-id = %s, want %s", got.Properties().MessageID(), want.Properties().MessageID())
	}
	if !bytes.Equal(got.Body(), want.Body()) {
		t.Fatalf("body = %q, want %q", got.Body(), want.Body())
	}
	gotHeaders, wantHeaders := got.Properties().Headers(), want.Properties().Headers()
	if !reflect.DeepEqual(gotHeaders, wantHeaders) {
		t.Fatalf("headers diverge:\n got %#v\nwant %#v", gotHeaders, wantHeaders)
	}
}

// The frozen fixture is the publication oracle: decoding a frozen envelope and
// re-encoding the Forward must reproduce the envelope exactly.
func TestEncodeThrowerForwardReproducesFrozenFixture(t *testing.T) {
	for _, caseID := range []string{"dlr_http_thrower", "dlr_smpps_thrower"} {
		t.Run(caseID, func(t *testing.T) {
			fixture := loadThrowerFixtureEnvelope(t, caseID)
			forward, err := DecodeThrowerForward(fixture)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeThrowerForward(forward)
			if err != nil {
				t.Fatal(err)
			}
			envelopesEqual(t, encoded, fixture)
		})
	}
}

func TestEncodeThrowerForwardRoundTrips(t *testing.T) {
	forwards := []Forward{
		{
			Target: ForwardHTTP, Status: "ESME_RINVNUMDESTS", QueueMsgID: "q-1", Level: 1,
			URL: "http://cb.example/x", Method: "GET", Connector: "abc",
		},
		{
			Target: ForwardHTTP, Status: "DELIVRD", QueueMsgID: "q-2", Level: 2,
			URL: "http://cb.example/y", Method: "POST",
			Connector: "0000436949", IDSMSC: "436949", Sub: "001", Dlvrd: "001",
			SubmitDate: "2601020304", DoneDate: "2601020305", Err: "000", Text: "hello",
		},
		{
			Target: ForwardSMPPS, Status: "UNDELIV", QueueMsgID: "q-3", Err: "000",
			SystemID: "client-a", SourceAddr: "1111", DestinationAddr: "2222",
			SubDate: "2026-01-02 03:04:05", SourceAddrTON: "AddrTon.INTERNATIONAL",
			SourceAddrNPI: "AddrNpi.ISDN", DestAddrTON: "AddrTon.INTERNATIONAL", DestAddrNPI: "AddrNpi.ISDN",
		},
		{
			Target: ForwardSMPPS, Status: "ESME_RSYSERR", QueueMsgID: "q-4", Err: "99", ErrIsInteger: true,
			SystemID: "client-a", SourceAddr: "1111", DestinationAddr: "2222",
			SubDate: "2026-01-02 03:04:05", SourceAddrTON: "AddrTon.NATIONAL",
			SourceAddrNPI: "AddrNpi.ISDN", DestAddrTON: "AddrTon.NATIONAL", DestAddrNPI: "AddrNpi.ISDN",
		},
	}
	for _, forward := range forwards {
		encoded, err := EncodeThrowerForward(forward)
		if err != nil {
			t.Fatalf("%+v: %v", forward, err)
		}
		decoded, err := DecodeThrowerForward(encoded)
		if err != nil {
			t.Fatalf("%+v: %v", forward, err)
		}
		if forward.Target == ForwardHTTP && forward.Level == 1 {
			// Level-1 receipt fields are written as the constructor's '' and not
			// read back; QueueMsgID is carried by message-id/body.
			forward.IDSMSC, forward.Sub, forward.Dlvrd, forward.SubmitDate, forward.DoneDate, forward.Err, forward.Text = "", "", "", "", "", "", ""
		}
		if !reflect.DeepEqual(decoded, forward) {
			t.Fatalf("round trip diverges:\n got %+v\nwant %+v", decoded, forward)
		}
	}
}

func TestEncodeThrowerForwardRejectsConstructorViolations(t *testing.T) {
	base := Forward{Target: ForwardHTTP, Status: "DELIVRD", QueueMsgID: "q", Level: 2, URL: "http://x", Method: "POST"}
	bad := []func(Forward) Forward{
		func(f Forward) Forward { f.Status = "BOGUS"; return f },
		func(f Forward) Forward { f.Level = 4; return f },
		func(f Forward) Forward { f.Method = "PUT"; return f },
		func(f Forward) Forward { f.QueueMsgID = ""; return f },
		func(f Forward) Forward { f.Target = 0; return f },
	}
	for index, mutate := range bad {
		if _, err := EncodeThrowerForward(mutate(base)); err == nil {
			t.Errorf("case %d: want error", index)
		}
	}
}

// pythonContentScript builds the legacy content object for the same
// parameters and dumps its body plus kind-tagged headers.
const pythonContentScript = `
import json, sys
from jasmin.managers.content import DLRContentForHttpapi, DLRContentForSmpps

request = json.load(sys.stdin)
kind = request["kind"]
a = request["args"]
if kind == "http":
    content = DLRContentForHttpapi(a["message_status"], a["msgid"], a["dlr_url"],
                                   a["dlr_level"], dlr_connector=a.get("dlr_connector", "unknown"),
                                   id_smsc=a.get("id_smsc", ""), sub=a.get("sub", ""),
                                   dlvrd=a.get("dlvrd", ""), subdate=a.get("subdate", ""),
                                   donedate=a.get("donedate", ""), err=a.get("err", ""),
                                   text=a.get("text", ""), method=a.get("method", "POST"))
else:
    kwargs = {}
    if "err" in a:
        kwargs["err"] = a["err"]
    content = DLRContentForSmpps(a["message_status"], a["msgid"], a["system_id"],
                                 a["source_addr"], a["destination_addr"], a["sub_date"],
                                 a["source_addr_ton"], a["source_addr_npi"],
                                 a["dest_addr_ton"], a["dest_addr_npi"], **kwargs)

body = content.body
if isinstance(body, bytes):
    body = body.decode()
headers = {}
for name, value in content.properties["headers"].items():
    if isinstance(value, bool) or not isinstance(value, int):
        headers[name] = {"kind": "str", "value": str(value)}
    else:
        headers[name] = {"kind": "int", "value": value}
print(json.dumps({"body": body, "message_id": content.properties["message-id"], "headers": headers}))
`

func TestEncodeThrowerForwardDifferentialAgainstLegacyContent(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name    string
		forward Forward
		kind    string
		args    map[string]any
	}{
		{
			name: "http submit leg level 1 defaults",
			forward: Forward{Target: ForwardHTTP, Status: "ESME_RTHROTTLED", QueueMsgID: "q-1",
				Level: 1, URL: "http://cb/x", Method: "GET", Connector: "abc"},
			kind: "http",
			args: map[string]any{"message_status": "ESME_RTHROTTLED", "msgid": "q-1",
				"dlr_url": "http://cb/x", "dlr_level": 1, "dlr_connector": "abc", "method": "GET"},
		},
		{
			name: "http deliver leg level 2 with receipt-id connector quirk",
			forward: Forward{Target: ForwardHTTP, Status: "DELIVRD", QueueMsgID: "q-2",
				Level: 2, URL: "http://cb/y", Method: "POST", Connector: "0000436949",
				IDSMSC: "436949", Sub: "001", Dlvrd: "001", SubmitDate: "2601020304",
				DoneDate: "2601020305", Err: "000", Text: "hello"},
			kind: "http",
			args: map[string]any{"message_status": "DELIVRD", "msgid": "q-2",
				"dlr_url": "http://cb/y", "dlr_level": 2, "dlr_connector": "0000436949",
				"id_smsc": "436949", "sub": "001", "dlvrd": "001", "subdate": "2601020304",
				"donedate": "2601020305", "err": "000", "text": "hello", "method": "POST"},
		},
		{
			name: "smpps submit leg constructor default err",
			forward: Forward{Target: ForwardSMPPS, Status: "ESME_RSYSERR", QueueMsgID: "q-3",
				SystemID: "client-a", SourceAddr: "1111", DestinationAddr: "2222",
				SubDate: "2026-01-02 03:04:05", SourceAddrTON: "AddrTon.INTERNATIONAL",
				SourceAddrNPI: "AddrNpi.ISDN", DestAddrTON: "AddrTon.INTERNATIONAL", DestAddrNPI: "AddrNpi.ISDN"},
			kind: "smpps",
			args: map[string]any{"message_status": "ESME_RSYSERR", "msgid": "q-3", "system_id": "client-a",
				"source_addr": "1111", "destination_addr": "2222", "sub_date": "2026-01-02 03:04:05",
				"source_addr_ton": "AddrTon.INTERNATIONAL", "source_addr_npi": "AddrNpi.ISDN",
				"dest_addr_ton": "AddrTon.INTERNATIONAL", "dest_addr_npi": "AddrNpi.ISDN"},
		},
		{
			name: "smpps deliver leg string err",
			forward: Forward{Target: ForwardSMPPS, Status: "UNDELIV", QueueMsgID: "q-4", Err: "000",
				SystemID: "client-a", SourceAddr: "1111", DestinationAddr: "2222",
				SubDate: "2026-01-02 03:04:05", SourceAddrTON: "AddrTon.NATIONAL",
				SourceAddrNPI: "AddrNpi.ISDN", DestAddrTON: "AddrTon.NATIONAL", DestAddrNPI: "AddrNpi.ISDN"},
			kind: "smpps",
			args: map[string]any{"message_status": "UNDELIV", "msgid": "q-4", "err": "000",
				"system_id": "client-a", "source_addr": "1111", "destination_addr": "2222",
				"sub_date": "2026-01-02 03:04:05", "source_addr_ton": "AddrTon.NATIONAL",
				"source_addr_npi": "AddrNpi.ISDN", "dest_addr_ton": "AddrTon.NATIONAL", "dest_addr_npi": "AddrNpi.ISDN"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := json.Marshal(map[string]any{"kind": testCase.kind, "args": testCase.args})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, pythonPath, "-c", pythonContentScript)
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			command.Stdin = bytes.NewReader(request)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			var oracle struct {
				Body      string `json:"body"`
				MessageID string `json:"message_id"`
				Headers   map[string]struct {
					Kind  string `json:"kind"`
					Value any    `json:"value"`
				} `json:"headers"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
				t.Fatalf("oracle output %q: %v", output, err)
			}

			envelope, err := EncodeThrowerForward(testCase.forward)
			if err != nil {
				t.Fatal(err)
			}
			if string(envelope.Body()) != oracle.Body || envelope.Properties().MessageID() != oracle.MessageID {
				t.Fatalf("identity diverges: body %q vs %q, id %q vs %q",
					envelope.Body(), oracle.Body, envelope.Properties().MessageID(), oracle.MessageID)
			}
			headers := envelope.Properties().Headers()
			if len(headers) != len(oracle.Headers) {
				t.Fatalf("header count %d vs %d (%v)", len(headers), len(oracle.Headers), oracle.Headers)
			}
			for name, want := range oracle.Headers {
				field, ok := headers[name]
				if !ok {
					t.Fatalf("missing header %s", name)
				}
				switch want.Kind {
				case "int":
					value, isInt := field.Integer()
					if !isInt || float64(value) != want.Value.(float64) {
						t.Errorf("header %s = %v, want int %v", name, field, want.Value)
					}
				default:
					value, isString := field.String()
					if !isString || value != want.Value.(string) {
						t.Errorf("header %s = %v, want string %q", name, field, want.Value)
					}
				}
			}
		})
	}
}

type recordingAMQPPublisher struct {
	exchanges []string
	envelopes []amqpcompat.Envelope
}

func (r *recordingAMQPPublisher) Publish(_ context.Context, exchange, _ string, message amqpcompat.Envelope) error {
	r.exchanges = append(r.exchanges, exchange)
	r.envelopes = append(r.envelopes, message)
	return nil
}

// Both correlation legs drive the production publisher end to end: the
// terminal receipt becomes a legacy dlr_thrower.http envelope on the
// messaging exchange with the raw receipt id in the connector header (the
// level-2 quirk) and the coded id in id_smsc.
func TestCorrelatorPublishesLegacyEnvelopes(t *testing.T) {
	_, client, _ := newCorrelator(t, Config{})
	amqp := &recordingAMQPPublisher{}
	publisher, err := NewForwardPublisher(amqp)
	if err != nil {
		t.Fatal(err)
	}
	correlator := NewCorrelator(client, publisher, Config{})
	ctx := context.Background()

	writeHTTPDLR(t, client, "q", 2)
	if err := c2OnSubmitResp(correlator, ctx); err != nil {
		t.Fatalf("submit leg: %v", err)
	}
	if err := correlator.OnDeliverReceipt(ctx, DeliverReceiptEvent{
		RawDLRID: "6aad5", Base: MsgIDBaseSame, ConnectorID: "smpp-01", Status: "DELIVRD",
		Sub: "001", Dlvrd: "001", SubmitDate: "2101011200", DoneDate: "2101011201", Err: "000", Text: "delivered",
	}); err != nil {
		t.Fatalf("deliver leg: %v", err)
	}

	if len(amqp.envelopes) != 1 {
		t.Fatalf("published %d envelopes, want 1 (level-2 request skips the level-1 forward)", len(amqp.envelopes))
	}
	if amqp.exchanges[0] != "messaging" {
		t.Fatalf("exchange = %s", amqp.exchanges[0])
	}
	envelope := amqp.envelopes[0]
	if envelope.RoutingKey() != "dlr_thrower.http" {
		t.Fatalf("routing key = %s", envelope.RoutingKey())
	}
	headers := envelope.Properties().Headers()
	if connector, _ := headers["connector"].String(); connector != "6aad5" {
		t.Errorf("connector header = %q, want raw receipt id", connector)
	}
	if idSMSC, _ := headers["id_smsc"].String(); idSMSC != "6AAD5" {
		t.Errorf("id_smsc header = %q, want coded id", idSMSC)
	}
	if level, _ := headers["level"].Integer(); level != 2 {
		t.Errorf("level header = %d, want 2", level)
	}
	// The published envelope is consumable by the frozen projection.
	forward, err := DecodeThrowerForward(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if forward.Status != "DELIVRD" || forward.Text != "delivered" {
		t.Fatalf("decoded forward = %+v", forward)
	}
}

func c2OnSubmitResp(correlator *Correlator, ctx context.Context) error {
	return correlator.OnSubmitResp(ctx, SubmitRespEvent{QueueMsgID: "q", SMPPMsgID: "6AAD5", Status: "ESME_ROK"})
}
