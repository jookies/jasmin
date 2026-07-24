package mo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestEncodeTLVParams_ByteExact(t *testing.T) {
	// Order is preserved and spacing matches json.dumps default.
	params := []TLVParam{
		{"sar_msg_ref_num", "1"},
		{"sar_total_segments", "3"},
		{"receipted_message_id", "36616164"},
		{"message_state", "DELIVERED"},
	}
	got := encodeTLVParams(params)
	want := `{"sar_msg_ref_num": "1", "sar_total_segments": "3", "receipted_message_id": "36616164", "message_state": "DELIVERED"}`
	if got != want {
		t.Errorf("encodeTLVParams =\n %s\nwant\n %s", got, want)
	}
	if encodeTLVParams(nil) != "{}" {
		t.Errorf("empty params should be {}, got %q", encodeTLVParams(nil))
	}
}

func TestEncodeCustomTLVs_ByteExact(t *testing.T) {
	tlvs := []CustomTLV{
		{Tag: 5140, Length: 4, Type: "OctetString", Value: "00000001"},
		{Tag: 5144, Length: 1, Type: "Int1", Value: "0a"},
	}
	got := encodeCustomTLVs(tlvs)
	want := `[{"tag": 5140, "length": 4, "type": "OctetString", "value": "00000001"}, {"tag": 5144, "length": 1, "type": "Int1", "value": "0a"}]`
	if got != want {
		t.Errorf("encodeCustomTLVs =\n %s\nwant\n %s", got, want)
	}
	if encodeCustomTLVs(nil) != "[]" {
		t.Errorf("empty custom tlvs should be [], got %q", encodeCustomTLVs(nil))
	}
}

func TestSendMO_IncludesTLVArgs(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()

	d := Delivery{
		MsgID: "m", From: "a", To: "b", OriginConnector: "c", Content: []byte("x"),
		TLVParams:  []TLVParam{{"network_error_code", "030000"}},
		CustomTLVs: []CustomTLV{{Tag: 0x1454, Length: 4, Type: "OctetString", Value: "00000001"}},
		URL:        srv.URL, Method: "POST",
	}
	if err := SendMO(context.Background(), srv.Client(), d); err != nil {
		t.Fatalf("SendMO: %v", err)
	}
	if got.Get("tlv_params") != `{"network_error_code": "030000"}` {
		t.Errorf("tlv_params arg = %q", got.Get("tlv_params"))
	}
	if got.Get("custom_tlvs") != `[{"tag": 5204, "length": 4, "type": "OctetString", "value": "00000001"}]` {
		t.Errorf("custom_tlvs arg = %q", got.Get("custom_tlvs"))
	}
}

func TestSendMO_OmitsEmptyTLVs(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()
	d := Delivery{MsgID: "m", From: "a", To: "b", OriginConnector: "c", Content: []byte("x"), URL: srv.URL, Method: "POST"}
	if err := SendMO(context.Background(), srv.Client(), d); err != nil {
		t.Fatalf("SendMO: %v", err)
	}
	if _, present := got["tlv_params"]; present {
		t.Errorf("tlv_params must be absent when empty")
	}
	if _, present := got["custom_tlvs"]; present {
		t.Errorf("custom_tlvs must be absent when empty")
	}
}
