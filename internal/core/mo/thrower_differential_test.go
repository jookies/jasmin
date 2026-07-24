package mo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// moOracleScript builds a real RoutedDeliverSmContent (pickled deliver_sm plus
// pickled HttpConnector list) and computes the exact form arguments the legacy
// deliverSmThrower would send, urlencoded the way treq encodes them.
const moOracleScript = `
import base64, binascii, io, json, pickle, sys
from urllib.parse import urlencode
import jasmin.protocols.smpp.operations  # installs codec patches
from smpp.pdu.pdu_encoding import PDUEncoder, DataCodingEncoder
from smpp.pdu.constants import priority_flag_name_map
from jasmin.routing.jasminApi import HttpConnector

request = json.load(sys.stdin)
wire = binascii.unhexlify(request["deliver_hex"])
pdu = PDUEncoder().decode(io.BytesIO(wire))
connectors = [HttpConnector(c["cid"], c["baseurl"], c["method"]) for c in request["connectors"]]

body_pickle = pickle.dumps(pdu, protocol=2)
connectors_pickle = pickle.dumps(connectors, protocol=2)

# The thrower's argument construction (routing/throwers.py http_deliver_sm_callback).
params = pdu.params
args = {
    "id": request["msgid"],
    "from": params["source_addr"],
    "to": params["destination_addr"],
    "origin-connector": request["src_cid"],
}
if "short_message" in params and params["short_message"] is not None and len(params["short_message"]) > 0:
    args["content"] = params["short_message"]
elif "message_payload" in params and params["message_payload"] is not None:
    args["content"] = params["message_payload"]
elif "short_message" in params and params["short_message"] is not None:
    args["content"] = params["short_message"]
if isinstance(args["content"], bytes):
    args["binary"] = binascii.hexlify(args["content"])
else:
    args["binary"] = binascii.hexlify(args["content"].encode())
if params.get("priority_flag") is not None:
    args["priority"] = priority_flag_name_map[params["priority_flag"].name]
if params.get("data_coding") is not None:
    args["coding"] = DataCodingEncoder().encode(params["data_coding"])
if params.get("validity_period") is not None:
    args["validity"] = params["validity_period"]

standard_optional_params = [
    "user_message_reference", "source_port", "destination_port",
    "sar_msg_ref_num", "sar_total_segments", "sar_segment_seqnum",
    "payload_type", "privacy_indicator", "callback_num",
    "language_indicator", "its_session_info", "network_error_code",
    "message_state", "receipted_message_id",
]
from enum import Enum
tlv_params = {}
for name in standard_optional_params:
    if name in params and params[name] is not None:
        value = params[name]
        if isinstance(value, bytes):
            tlv_params[name] = binascii.hexlify(value).decode()
        elif isinstance(value, Enum):
            tlv_params[name] = value.name
        else:
            tlv_params[name] = str(value)
if tlv_params:
    args["tlv_params"] = json.dumps(tlv_params)
custom_tlvs_list = getattr(pdu, "custom_tlvs", [])
if custom_tlvs_list:
    custom_tlvs_data = []
    for tlv in custom_tlvs_list:
        if len(tlv) >= 4:
            tag, length, value_type, value = tlv[0], tlv[1], tlv[2], tlv[3]
            if isinstance(value, bytes):
                value = binascii.hexlify(value).decode()
            custom_tlvs_data.append({"tag": tag, "length": length, "type": value_type, "value": value})
    if custom_tlvs_data:
        args["custom_tlvs"] = json.dumps(custom_tlvs_data)

print(json.dumps({
    "body_pickle": base64.b64encode(body_pickle).decode(),
    "connectors_pickle": base64.b64encode(connectors_pickle).decode(),
    "form": urlencode(args),
}))
`

func TestMOThrowerDifferentialAgainstLegacyArgs(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	cases := []struct {
		name       string
		deliverHex string
	}{
		{
			// deliver_sm: 1111 -> 2222, "hello", plus source_port 8080,
			// message_state DELIVRD, and vendor TLV 0x1401 "test".
			name:       "mo with vendor and standard TLVs",
			deliverHex: deliverFrameHex(t, "68656c6c6f", "020a00021f9004270001021401000474657374"),
		},
		{
			// Binary content with a NUL and high bytes.
			name:       "binary content",
			deliverHex: deliverFrameHex(t, "00ff41", ""),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			received := make(chan url.Values, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				received <- r.PostForm
				_, _ = w.Write([]byte("ACK/Jasmin"))
			}))
			defer server.Close()

			request, err := json.Marshal(map[string]any{
				"deliver_hex": testCase.deliverHex,
				"msgid":       "mo-1",
				"src_cid":     "smpp-01",
				"connectors": []map[string]string{
					{"cid": "http-dst", "baseurl": server.URL, "method": "POST"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, pythonPath, "-c", moOracleScript)
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			command.Stdin = bytes.NewReader(request)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			var oracle struct {
				BodyPickle       string `json:"body_pickle"`
				ConnectorsPickle string `json:"connectors_pickle"`
				Form             string `json:"form"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
				t.Fatalf("oracle output %q: %v", output, err)
			}
			wantForm, err := url.ParseQuery(oracle.Form)
			if err != nil {
				t.Fatal(err)
			}
			bodyPickle, err := base64.StdEncoding.DecodeString(oracle.BodyPickle)
			if err != nil {
				t.Fatal(err)
			}
			connectorsPickle, err := base64.StdEncoding.DecodeString(oracle.ConnectorsPickle)
			if err != nil {
				t.Fatal(err)
			}

			consumer, err := NewThrowerConsumer(bridge, server.Client(), ThrowerConsumerConfig{})
			if err != nil {
				t.Fatal(err)
			}
			delivery, settlements := moThrowerDelivery(t, "mo-1", "simple", "smpp-01", connectorsPickle, bodyPickle)
			if err := consumer.Handle(context.Background(), delivery); err != nil {
				t.Fatal(err)
			}
			if settled := <-settlements; settled != "ack" {
				t.Fatalf("settlement = %s", settled)
			}
			gotForm := <-received
			if !reflect.DeepEqual(gotForm, wantForm) {
				t.Fatalf("form diverges:\n  go %v\n  py %v", gotForm, wantForm)
			}
		})
	}
}

// deliverFrameHex composes a deliver_sm frame via the Go codec plus a spliced
// TLV section, returning full-frame hex for the Python oracle to decode.
func deliverFrameHex(t *testing.T, shortMessageHex, tlvSectionHex string) string {
	t.Helper()
	shortMessage, err := hex.DecodeString(shortMessageHex)
	if err != nil {
		t.Fatal(err)
	}
	body := &smppwire.SMBody{
		SourceAddress:      []byte("1111"),
		DestinationAddress: []byte("2222"),
		ShortMessage:       shortMessage,
	}
	frame, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 5},
		SM:     body,
	})
	if err != nil {
		t.Fatal(err)
	}
	section, err := hex.DecodeString(tlvSectionHex)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame, section...)
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(frame)))
	return hex.EncodeToString(frame)
}

func moThrowerDelivery(t *testing.T, messageID, routeType, srcCID string, connectorsPickle, bodyPickle []byte) (*amqpcompat.Delivery, chan string) {
	t.Helper()
	settlements := make(chan string, 2)
	raw := amqp.Delivery{
		Acknowledger: &moSettleRecorder{settlements: settlements},
		MessageId:    messageID,
		RoutingKey:   "deliver_sm_thrower.http",
		Body:         bodyPickle,
		Headers: amqp.Table{
			"route-type":       routeType,
			"src-connector-id": srcCID,
			"dst-connectors":   connectorsPickle,
			"try-count":        int64(0),
		},
	}
	delivery, err := amqpcompat.NewDelivery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return delivery, settlements
}

type moSettleRecorder struct{ settlements chan string }

func (r *moSettleRecorder) Ack(uint64, bool) error { r.settlements <- "ack"; return nil }
func (r *moSettleRecorder) Nack(_ uint64, _ bool, requeue bool) error {
	if requeue {
		r.settlements <- "requeue"
	} else {
		r.settlements <- "reject"
	}
	return nil
}
func (r *moSettleRecorder) Reject(_ uint64, requeue bool) error {
	if requeue {
		r.settlements <- "requeue"
	} else {
		r.settlements <- "reject"
	}
	return nil
}

type fakeRoutedDecoder struct {
	result picklecompat.RoutedDeliverSM
	err    error
}

func (f *fakeRoutedDecoder) DecodeRoutedDeliverSM(context.Context, []byte, []byte) (picklecompat.RoutedDeliverSM, error) {
	return f.result, f.err
}

func routedResult(urls []string) picklecompat.RoutedDeliverSM {
	connectors := make([]picklecompat.MOConnector, 0, len(urls))
	for i, target := range urls {
		connectors = append(connectors, picklecompat.MOConnector{
			CID: "dst", Type: "http", BaseURL: target, Method: "POST",
		})
		_ = i
	}
	return picklecompat.RoutedDeliverSM{
		Connectors: connectors,
		Body: smppwire.SMBody{
			SourceAddress:      []byte("1111"),
			DestinationAddress: []byte("2222"),
			ShortMessage:       []byte("hi"),
		},
	}
}

func TestMOThrowerFailoverAcksOnSecondConnector(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer good.Close()

	decoder := &fakeRoutedDecoder{result: routedResult([]string{bad.URL, good.URL})}
	consumer, err := NewThrowerConsumer(decoder, http.DefaultClient, ThrowerConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	delivery, settlements := moThrowerDelivery(t, "mo-f1", "failover", "smpp-01", []byte("pkl"), []byte("pkl"))
	if err := consumer.Handle(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if settled := <-settlements; settled != "ack" {
		t.Fatalf("settlement = %s", settled)
	}
}

func TestMOThrowerFailoverAllFailRejectsWithoutRequeue(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()

	decoder := &fakeRoutedDecoder{result: routedResult([]string{bad.URL, bad.URL})}
	consumer, _ := NewThrowerConsumer(decoder, http.DefaultClient, ThrowerConsumerConfig{})
	delivery, settlements := moThrowerDelivery(t, "mo-f2", "failover", "smpp-01", []byte("pkl"), []byte("pkl"))
	if err := consumer.Handle(context.Background(), delivery); err == nil {
		t.Fatal("want error when every connector fails")
	}
	// The legacy code leaves this delivery unsettled forever; rejecting without
	// requeue is the conscious correction.
	if settled := <-settlements; settled != "reject" {
		t.Fatalf("settlement = %s", settled)
	}
}

func TestMOThrowerSimpleRetriesThenPurges(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("NOPE"))
	}))
	defer bad.Close()

	decoder := &fakeRoutedDecoder{result: routedResult([]string{bad.URL})}
	consumer, _ := NewThrowerConsumer(decoder, http.DefaultClient, ThrowerConsumerConfig{MaxRetries: 1, RetryDelay: 10 * time.Millisecond})

	delivery, settlements := moThrowerDelivery(t, "mo-s1", "simple", "smpp-01", []byte("pkl"), []byte("pkl"))
	_ = consumer.Handle(context.Background(), delivery)
	if settled := <-settlements; settled != "requeue" {
		t.Fatalf("attempt 1 settlement = %s", settled)
	}
	delivery2, settlements2 := moThrowerDelivery(t, "mo-s1", "simple", "smpp-01", []byte("pkl"), []byte("pkl"))
	_ = consumer.Handle(context.Background(), delivery2)
	if settled := <-settlements2; settled != "reject" {
		t.Fatalf("attempt 2 settlement = %s", settled)
	}
}

func TestMOThrowerRejectsNonHTTPAndSMPPSLegs(t *testing.T) {
	decoder := &fakeRoutedDecoder{result: picklecompat.RoutedDeliverSM{
		Connectors: []picklecompat.MOConnector{{CID: "dst", Type: "smpps"}},
		Body:       smppwire.SMBody{ShortMessage: []byte("hi"), DestinationAddress: []byte("2")},
	}}
	consumer, _ := NewThrowerConsumer(decoder, http.DefaultClient, ThrowerConsumerConfig{MaxRetries: 1, RetryDelay: 5 * time.Millisecond})

	// Non-http first destination connector: legacy rejects outright.
	delivery, settlements := moThrowerDelivery(t, "mo-x1", "simple", "smpp-01", []byte("pkl"), []byte("pkl"))
	if err := consumer.Handle(context.Background(), delivery); err == nil {
		t.Fatal("want error for non-http connector")
	}
	if settled := <-settlements; settled != "reject" {
		t.Fatalf("settlement = %s", settled)
	}

	// SMPPS thrower leg without a session sink: retry path.
	settlements2 := make(chan string, 2)
	raw := amqp.Delivery{
		Acknowledger: &moSettleRecorder{settlements: settlements2},
		MessageId:    "mo-x2",
		RoutingKey:   "deliver_sm_thrower.smpps",
		Body:         []byte("pkl"),
		Headers:      amqp.Table{},
	}
	delivery2, err := amqpcompat.NewDelivery(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Handle(context.Background(), delivery2); err == nil {
		t.Fatal("want error for smpps leg without sink")
	}
	if settled := <-settlements2; settled != "requeue" {
		t.Fatalf("settlement = %s", settled)
	}
}
