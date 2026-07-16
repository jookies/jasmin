package picklecompat_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

func TestPickleBridgeRoundTrip(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	defer bridge.Close()

	// Test case: simple dictionary
	// In Python: pickle.dumps({"a": 1, "b": b"binary"})
	// Protocol 2: b'\x80\x02}q\x00(X\x01\x00\x00\x0aaK\x01X\x01\x00\x00\x00bC\x06binaryq\x01u.'
	pickleData := "\x80\x02}q\x00(X\x01\x00\x00\x00aK\x01X\x01\x00\x00\x00bC\x06binaryq\x01u."

	decoded, err := bridge.Decode(ctx, []byte(pickleData))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal(decoded, &res); err != nil {
		t.Fatal(err)
	}

	if res["a"].(float64) != 1 {
		t.Errorf("got a = %v, want 1", res["a"])
	}
	b := res["b"].(map[string]any)
	if b["__type__"] != "bytes" {
		t.Errorf("got b type = %v, want bytes", b["__type__"])
	}

	// Test Encode back
	encoded, err := bridge.Encode(ctx, res)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Decode again to verify roundtrip
	decoded2, err := bridge.Decode(ctx, encoded)
	if err != nil {
		t.Fatal(err)
	}

	if string(decoded) != string(decoded2) {
		t.Errorf("roundtrip mismatch:\n%s\nvs\n%s", string(decoded), string(decoded2))
	}
}

func TestAMQPFixtureDecoding(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	// Case: submit_sm_httpapi from baseline.json
	wireBase64 := "gAJjc21wcC5wZHUub3BlcmF0aW9ucwpTdWJtaXRTTQpxACmBcQF9cQIoWAIAAABpZHEDY3NtcHAucGR1LnBkdV90eXBlcwpDb21tYW5kSWQKcQRLCIVxBVJxBlgGAAAAc2VxTnVtcQdLB1gGAAAAc3RhdHVzcQhjc21wcC5wZHUucGR1X3R5cGVzCkNvbW1hbmRTdGF0dXMKcQlLAYVxClJxC1gLAAAAY3VzdG9tX3RsdnNxDF1xDVgGAAAAcGFyYW1zcQ59cQ8oWAsAAABzb3VyY2VfYWRkcnEQY19jb2RlY3MKZW5jb2RlCnERWAQAAAAxMTExcRJYBgAAAGxhdGluMXEThnEUUnEVWBAAAABkZXN0aW5hdGlvbl9hZGRycRZoEVgEAAAAMjIyMnEXaBOGcRhScRlYDQAAAHNob3J0X21lc3NhZ2VxGmgRWAUAAABoZWxsb3EbaBOGcRxScR1YDAAAAHNlcnZpY2VfdHlwZXEeTlgPAAAAc291cmNlX2FkZHJfdG9ucR9OWA8AAABzb3VyY2VfYWRkcl9ucGlxIE5YDQAAAGRlc3RfYWRkcl90b25xIU5YDQAAAGRlc3RfYWRkcl9ucGlxIk5YCQAAAGVzbV9jbGFzc3EjTlgLAAAAcHJvdG9jb2xfaWRxJE5YDQAAAHByaW9yaXR5X2ZsYWdxJU5YFgAAAHNjaGVkdWxlX2RlbGl2ZXJ5X3RpbWVxJk5YDwAAAHZhbGlkaXR5X3BlcmlvZHEnTlgTAAAAcmVnaXN0ZXJlZF9kZWxpdmVyeXEoTlgXAAAAcmVwbGFjZV9pZl9wcmVzZW50X2ZsYWdxKU5YCwAAAGRhdGFfY29kaW5ncSpOWBEAAABzbV9kZWZhdWx0X21zZ19pZHErTnV1Yi4="
	data, _ := base64.StdEncoding.DecodeString(wireBase64)

	decoded, err := bridge.Decode(ctx, data)
	if err != nil {
		t.Fatalf("Decode fixture: %v", err)
	}

	var sm picklecompat.SubmitSM
	if err := json.Unmarshal(decoded, &sm); err != nil {
		t.Fatal(err)
	}

	if sm.ClassName != "smpp.pdu.operations.SubmitSM" {
		t.Errorf("got class %v, want smpp.pdu.operations.SubmitSM", sm.ClassName)
	}

	if string(sm.Params.ShortMessage) != "hello" {
		t.Errorf("got message %q, want hello", string(sm.Params.ShortMessage))
	}
	if string(sm.Params.SourceAddr) != "1111" {
		t.Errorf("got source %q, want 1111", string(sm.Params.SourceAddr))
	}
	if string(sm.Params.DestinationAddr) != "2222" {
		t.Errorf("got dest %q, want 2222", string(sm.Params.DestinationAddr))
	}

	// Case: submit_sm_resp
	respBase64 := "gAJjc21wcC5wZHUub3BlcmF0aW9ucwpTdWJtaXRTTVJlc3AKcQApgXEBfXECKFgCAAAAaWRxA2NzbXBwLnBkdS5wZHVfdHlwZXMKQ29tbWFuZElkCnEESwmFcQVScQZYBgAAAHNlcU51bXEHSwdYBgAAAHN0YXR1c3EIY3NtcHAucGR1LnBkdV90eXBlcwpDb21tYW5kU3RhdHVzCnEJSwGFcQpScQtYCwAAAGN1c3RvbV90bHZzcQxdcQ1YBgAAAHBhcmFtc3EOfXEPWAoAAABtZXNzYWdlX2lkcRBjX2NvZGVjcwplbmNvZGUKcRFYCgAAADAwMDA0MzY5NDlxElgGAAAAbGF0aW4xcROGcRRScRVzdWIu"
	respData, _ := base64.StdEncoding.DecodeString(respBase64)

	decodedResp, err := bridge.Decode(ctx, respData)
	if err != nil {
		t.Fatalf("Decode resp: %v", err)
	}

	var smResp picklecompat.SubmitSMResp
	if err := json.Unmarshal(decodedResp, &smResp); err != nil {
		t.Fatal(err)
	}

	if smResp.ClassName != "smpp.pdu.operations.SubmitSMResp" {
		t.Errorf("got class %v, want smpp.pdu.operations.SubmitSMResp", smResp.ClassName)
	}
	if string(smResp.Params.MessageID) != "0000436949" {
		t.Errorf("got message id %q, want 0000436949", string(smResp.Params.MessageID))
	}
}
