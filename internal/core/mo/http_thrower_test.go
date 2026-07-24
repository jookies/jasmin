package mo

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func bptr(b byte) *byte { return &b }

func TestSendMO_POST_MandatoryArgs(t *testing.T) {
	var got url.Values
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		ua = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()

	content := []byte("hello MO")
	d := Delivery{MsgID: "m1", From: "12345", To: "447700900000", OriginConnector: "smpp-in", Content: content, URL: srv.URL, Method: "POST"}
	if err := SendMO(context.Background(), srv.Client(), d); err != nil {
		t.Fatalf("SendMO: %v", err)
	}
	for k, want := range map[string]string{
		"id": "m1", "from": "12345", "to": "447700900000", "origin-connector": "smpp-in",
		"content": "hello MO", "binary": hex.EncodeToString(content),
	} {
		if got.Get(k) != want {
			t.Errorf("arg %q = %q, want %q", k, got.Get(k), want)
		}
	}
	// Optionals absent when unset.
	if _, present := got["priority"]; present {
		t.Errorf("priority should be absent when nil")
	}
	if ua != "Jasmin gateway/1.0 deliverSmHttpThrower" {
		t.Errorf("User-Agent = %q", ua)
	}
}

func TestSendMO_GET_WithOptionals(t *testing.T) {
	var q url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		q = r.URL.Query()
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()

	d := Delivery{
		MsgID: "m2", From: "SENDER", To: "999", OriginConnector: "c", Content: []byte("x"),
		Priority: bptr(2), Coding: bptr(8), Validity: "000000000100000R", URL: srv.URL, Method: "GET",
	}
	if err := SendMO(context.Background(), srv.Client(), d); err != nil {
		t.Fatalf("SendMO: %v", err)
	}
	if q.Get("priority") != "2" || q.Get("coding") != "\x08" || q.Get("validity") != "000000000100000R" {
		t.Errorf("optionals wrong: priority=%q coding=%q validity=%q", q.Get("priority"), q.Get("coding"), q.Get("validity"))
	}
}

func TestSendMO_BinaryContentHex(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()

	binary := []byte{0x00, 0x1b, 0xff, 0x80}
	d := Delivery{MsgID: "m", From: "a", To: "b", OriginConnector: "c", Content: binary, URL: srv.URL, Method: "POST"}
	if err := SendMO(context.Background(), srv.Client(), d); err != nil {
		t.Fatalf("SendMO: %v", err)
	}
	if got.Get("binary") != "001bff80" {
		t.Errorf("binary = %q, want 001bff80", got.Get("binary"))
	}
}

func TestSendMO_NotAcknowledged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("NOPE"))
	}))
	defer srv.Close()
	d := Delivery{MsgID: "m", From: "a", To: "b", OriginConnector: "c", Content: []byte("x"), URL: srv.URL, Method: "POST"}
	if err := SendMO(context.Background(), srv.Client(), d); !errors.Is(err, ErrMONotAcknowledged) {
		t.Errorf("got %v, want ErrMONotAcknowledged", err)
	}
}

func TestSendMO_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()
	d := Delivery{MsgID: "m", From: "a", To: "b", OriginConnector: "c", Content: []byte("x"), URL: srv.URL, Method: "POST"}
	if err := SendMO(context.Background(), srv.Client(), d); !errors.Is(err, ErrMOHTTPStatus) {
		t.Errorf("got %v, want ErrMOHTTPStatus", err)
	}
}

func TestSendMO_ConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()
	d := Delivery{MsgID: "m", From: "a", To: "b", OriginConnector: "c", Content: []byte("x"), URL: addr, Method: "POST"}
	if err := SendMO(context.Background(), &http.Client{}, d); err == nil ||
		errors.Is(err, ErrMONotAcknowledged) || errors.Is(err, ErrMOHTTPStatus) {
		t.Errorf("expected a transport error, got %v", err)
	}
}
