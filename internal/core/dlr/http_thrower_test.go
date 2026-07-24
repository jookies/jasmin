package dlr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSendHTTPDLR_POST_Level1_Ack(t *testing.T) {
	var got url.Values
	var ua, ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		ua = r.Header.Get("User-Agent")
		ct = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()

	cb := HTTPDLRCallback{MsgID: "m1", URL: srv.URL, Method: "POST", Level: 1, MessageStatus: "ESME_ROK", Connector: "c1"}
	if err := SendHTTPDLR(context.Background(), srv.Client(), cb); err != nil {
		t.Fatalf("SendHTTPDLR: %v", err)
	}
	if got.Get("id") != "m1" || got.Get("level") != "1" || got.Get("message_status") != "ESME_ROK" || got.Get("connector") != "c1" {
		t.Errorf("mandatory args wrong: %v", got)
	}
	if _, present := got["id_smsc"]; present {
		t.Errorf("level 1 must not send level-2 fields, got id_smsc")
	}
	if ua != "Jasmin gateway/1.0 DLRThrower" {
		t.Errorf("User-Agent = %q", ua)
	}
	if ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSendHTTPDLR_GET_Level2_Ack(t *testing.T) {
	var q url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		q = r.URL.Query()
		_, _ = w.Write([]byte("ACK/Jasmin\n"))
	}))
	defer srv.Close()

	cb := HTTPDLRCallback{
		MsgID: "m2", URL: srv.URL, Method: "GET", Level: 2, MessageStatus: "DELIVRD", Connector: "6aad5",
		IDSMSC: "6AAD5", Sub: "001", Dlvrd: "001", SubDate: "2101011200", DoneDate: "2101011201", Err: "000", Text: "ok",
	}
	if err := SendHTTPDLR(context.Background(), srv.Client(), cb); err != nil {
		t.Fatalf("SendHTTPDLR: %v", err)
	}
	for k, want := range map[string]string{
		"id": "m2", "level": "2", "message_status": "DELIVRD", "connector": "6aad5",
		"id_smsc": "6AAD5", "sub": "001", "dlvrd": "001", "subdate": "2101011200",
		"donedate": "2101011201", "err": "000", "text": "ok",
	} {
		if q.Get(k) != want {
			t.Errorf("query %q = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestSendHTTPDLR_GET_MergesExistingQuery(t *testing.T) {
	var q url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer srv.Close()

	cb := HTTPDLRCallback{MsgID: "m3", URL: srv.URL + "/cb?tenant=acme", Method: "GET", Level: 1, MessageStatus: "ESME_ROK", Connector: "c"}
	if err := SendHTTPDLR(context.Background(), srv.Client(), cb); err != nil {
		t.Fatalf("SendHTTPDLR: %v", err)
	}
	if q.Get("tenant") != "acme" || q.Get("id") != "m3" {
		t.Errorf("expected merged query with tenant + id, got %v", q)
	}
}

func TestSendHTTPDLR_NotAcknowledged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("OK")) // not ACK/Jasmin
	}))
	defer srv.Close()
	cb := HTTPDLRCallback{MsgID: "m", URL: srv.URL, Method: "POST", Level: 1, MessageStatus: "ESME_ROK", Connector: "c"}
	if err := SendHTTPDLR(context.Background(), srv.Client(), cb); !errors.Is(err, ErrDLRNotAcknowledged) {
		t.Errorf("got %v, want ErrDLRNotAcknowledged", err)
	}
}

func TestSendHTTPDLR_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("ACK/Jasmin")) // body is ACK but status is 500
	}))
	defer srv.Close()
	cb := HTTPDLRCallback{MsgID: "m", URL: srv.URL, Method: "POST", Level: 1, MessageStatus: "ESME_ROK", Connector: "c"}
	if err := SendHTTPDLR(context.Background(), srv.Client(), cb); !errors.Is(err, ErrDLRHTTPStatus) {
		t.Errorf("got %v, want ErrDLRHTTPStatus", err)
	}
}

func TestHTTPDLRCallbackFromForward(t *testing.T) {
	// A level-2 deliver-leg forward projects into a full callback.
	f := Forward{
		Target: ForwardHTTP, Status: "DELIVRD", QueueMsgID: "q", Level: 2,
		URL: "http://cb", Method: "POST", Connector: "6aad5",
		IDSMSC: "6AAD5", Sub: "001", Dlvrd: "001", SubmitDate: "s", DoneDate: "d", Err: "000", Text: "t",
	}
	cb, err := HTTPDLRCallbackFromForward(f)
	if err != nil {
		t.Fatalf("from forward: %v", err)
	}
	if cb.MsgID != "q" || cb.Level != 2 || cb.MessageStatus != "DELIVRD" || cb.Connector != "6aad5" ||
		cb.IDSMSC != "6AAD5" || cb.SubDate != "s" || cb.DoneDate != "d" {
		t.Errorf("projection wrong: %+v", cb)
	}
	// A non-http forward is rejected.
	if _, err := HTTPDLRCallbackFromForward(Forward{Target: ForwardSMPPS}); err == nil {
		t.Errorf("expected error for smpps forward")
	}
}

func TestSendHTTPDLR_ConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // server down -> connection error
	cb := HTTPDLRCallback{MsgID: "m", URL: addr, Method: "POST", Level: 1, MessageStatus: "ESME_ROK", Connector: "c"}
	if err := SendHTTPDLR(context.Background(), &http.Client{}, cb); err == nil ||
		errors.Is(err, ErrDLRNotAcknowledged) || errors.Is(err, ErrDLRHTTPStatus) {
		t.Errorf("expected a transport error, got %v", err)
	}
}
