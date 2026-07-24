package dlr

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type throwerFixtureDocument struct {
	Cases []struct {
		ID         string `json:"id"`
		RoutingKey string `json:"routing_key"`
		Properties struct {
			MessageID string                     `json:"message-id"`
			Headers   map[string]json.RawMessage `json:"headers"`
		} `json:"properties"`
		Body struct {
			WireBase64 string `json:"wire_base64"`
		} `json:"body"`
	} `json:"cases"`
}

func TestGoldenDLRThrowerProjection(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "amqp", "baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document throwerFixtureDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, tc := range document.Cases {
		if tc.ID != "dlr_http_thrower" && tc.ID != "dlr_smpps_thrower" {
			continue
		}
		seen[tc.ID] = true
		headers := make(map[string]amqpcompat.Field, len(tc.Properties.Headers))
		for name, value := range tc.Properties.Headers {
			var text string
			if json.Unmarshal(value, &text) == nil {
				headers[name] = amqpcompat.StringField(text)
				continue
			}
			var number int64
			if err := json.Unmarshal(value, &number); err != nil {
				t.Fatalf("%s header %s: %v", tc.ID, name, err)
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
		forward, err := DecodeThrowerForward(envelope)
		if err != nil {
			t.Fatalf("%s: %v", tc.ID, err)
		}
		if forward.QueueMsgID != tc.Properties.MessageID {
			t.Fatalf("%s queue id=%q", tc.ID, forward.QueueMsgID)
		}

		switch tc.ID {
		case "dlr_http_thrower":
			callback, err := HTTPDLRCallbackFromForward(forward)
			if err != nil {
				t.Fatal(err)
			}
			if callback.MsgID != "11111111-1111-4111-8111-111111111111" || callback.Level != 3 ||
				callback.MessageStatus != "DELIVRD" || callback.Connector != "connector-a" ||
				callback.URL != "https://example.invalid/dlr" || callback.Method != "POST" ||
				callback.IDSMSC != "0000436949" || callback.Sub != "001" || callback.Dlvrd != "001" ||
				callback.SubDate != "2601020304" || callback.DoneDate != "2601020305" ||
				callback.Err != "000" || callback.Text != "hello" {
				t.Fatalf("http callback=%+v", callback)
			}
		case "dlr_smpps_thrower":
			params, err := SMPPSReceiptParamsFromForward(forward)
			if err != nil {
				t.Fatal(err)
			}
			if params.MsgID != "11111111-1111-4111-8111-111111111111" || params.MessageStatus != "DELIVRD" ||
				params.Err != "0" || params.SubDate != "2026-01-02 03:04:05.678901" ||
				params.SourceAddr != "1111" || params.DestAddr != "2222" ||
				params.SourceAddrTON != 1 || params.SourceAddrNPI != 1 || params.DestAddrTON != 1 || params.DestAddrNPI != 1 ||
				forward.SystemID != "client-a" {
				t.Fatalf("smpps params=%+v forward=%+v", params, forward)
			}
			pdu, err := BuildDeliverSMReceipt(params, time.Date(2026, 1, 2, 3, 5, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			wire, err := smppwire.Encode(pdu)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := smppwire.Decode(wire)
			if err != nil {
				t.Fatal(err)
			}
			wantText := "id:11111111-1111-4111-8111-111111111111 submit date:2601020304 done date:2601020305 stat:DELIVRD err:0"
			if decoded.SM == nil || string(decoded.SM.SourceAddress) != "2222" || string(decoded.SM.DestinationAddress) != "1111" ||
				decoded.SM.SourceAddressTON != 1 || decoded.SM.SourceAddressNPI != 1 || decoded.SM.DestinationAddressTON != 1 || decoded.SM.DestinationAddressNPI != 1 ||
				decoded.SM.ESMClass != esmClassSMSCDeliveryReceipt || string(decoded.SM.ShortMessage) != wantText ||
				string(decoded.SM.Optional.ReceiptedMessageID) != params.MsgID || decoded.SM.Optional.MessageState == nil || *decoded.SM.Optional.MessageState != msgStateDelivered {
				t.Fatalf("smpps receipt=%+v", decoded.SM)
			}
		}
	}
	if len(seen) != 2 || !seen["dlr_http_thrower"] || !seen["dlr_smpps_thrower"] {
		t.Fatalf("fixture coverage=%v, want both thrower cases", seen)
	}
}

func TestDecodeThrowerForwardRejectsMalformedEnvelope(t *testing.T) {
	validHeaders := map[string]amqpcompat.Field{
		"try-count": amqpcompat.IntegerField(0), "message_status": amqpcompat.StringField("ESME_ROK"),
		"level": amqpcompat.IntegerField(1), "url": amqpcompat.StringField("https://example.invalid/dlr"),
		"method": amqpcompat.StringField("POST"), "connector": amqpcompat.StringField("c"),
	}
	build := func(route, messageID, body string, headers map[string]amqpcompat.Field) amqpcompat.Envelope {
		t.Helper()
		properties, err := amqpcompat.NewProperties(messageID, headers)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := amqpcompat.NewEnvelope(route, properties, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return envelope
	}
	if _, err := DecodeThrowerForward(build("dlr_thrower.http", "m", "other", validHeaders)); err == nil {
		t.Fatal("body/message-id mismatch accepted")
	}
	if _, err := DecodeThrowerForward(build("dlr.submit_sm_resp", "m", "m", validHeaders)); err == nil {
		t.Fatal("non-thrower route accepted")
	}
	legacyArbitraryESME := make(map[string]amqpcompat.Field, len(validHeaders))
	for key, value := range validHeaders {
		legacyArbitraryESME[key] = value
	}
	legacyArbitraryESME["message_status"] = amqpcompat.StringField("ESME_BOGUS")
	if _, err := DecodeThrowerForward(build("dlr_thrower.http", "m", "m", legacyArbitraryESME)); err != nil {
		t.Fatalf("legacy arbitrary ESME_ status rejected: %v", err)
	}

	mutations := map[string]func(map[string]amqpcompat.Field){
		"invalid level":      func(h map[string]amqpcompat.Field) { h["level"] = amqpcompat.IntegerField(4) },
		"lowercase method":   func(h map[string]amqpcompat.Field) { h["method"] = amqpcompat.StringField("post") },
		"unknown status":     func(h map[string]amqpcompat.Field) { h["message_status"] = amqpcompat.StringField("BOGUS") },
		"negative try count": func(h map[string]amqpcompat.Field) { h["try-count"] = amqpcompat.IntegerField(-1) },
		"wrong method kind":  func(h map[string]amqpcompat.Field) { h["method"] = amqpcompat.IntegerField(1) },
		"missing connector":  func(h map[string]amqpcompat.Field) { delete(h, "connector") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			headers := make(map[string]amqpcompat.Field, len(validHeaders))
			for key, value := range validHeaders {
				headers[key] = value
			}
			mutate(headers)
			if _, err := DecodeThrowerForward(build("dlr_thrower.http", "m", "m", headers)); err == nil {
				t.Fatal("malformed envelope accepted")
			}
		})
	}

	emptyOptional := make(map[string]amqpcompat.Field, len(validHeaders)+7)
	for key, value := range validHeaders {
		emptyOptional[key] = value
	}
	emptyOptional["level"] = amqpcompat.IntegerField(3)
	for _, key := range []string{"id_smsc", "sub", "dlvrd", "subdate", "donedate", "err", "text"} {
		emptyOptional[key] = amqpcompat.StringField("")
	}
	if _, err := DecodeThrowerForward(build("dlr_thrower.http", "m", "m", emptyOptional)); err != nil {
		t.Fatalf("legacy empty optional fields rejected: %v", err)
	}
}

func TestThrowerEnvelopeSizeBoundPrecedesProjection(t *testing.T) {
	headers := map[string]amqpcompat.Field{"url": amqpcompat.StringField(strings.Repeat("x", amqpcompat.MaxHeaderBytes+1))}
	if _, err := amqpcompat.NewProperties("m", headers); err == nil {
		t.Fatal("oversized AMQP header table reached the DLR projection boundary")
	}
}

func TestSMPPSReceiptParamsFromForwardRejectsUnknownEnums(t *testing.T) {
	_, err := SMPPSReceiptParamsFromForward(Forward{Target: ForwardSMPPS, QueueMsgID: "m", Status: "DELIVRD", SubDate: "2026-01-02 03:04:05", SourceAddr: "1", DestinationAddr: "2", SourceAddrTON: "AddrTon.NOPE", SourceAddrNPI: "AddrNpi.ISDN", DestAddrTON: "AddrTon.INTERNATIONAL", DestAddrNPI: "AddrNpi.ISDN"})
	if err == nil {
		t.Fatal("unknown enum accepted")
	}
}
