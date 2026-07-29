package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStandbyHealthServer(t *testing.T) {
	server, listener, err := newStandbyHealthServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("create standby server: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-done
	})

	baseURL := "http://" + listener.Addr().String()
	for _, test := range []struct {
		path       string
		statusCode int
	}{
		{path: "/live", statusCode: http.StatusOK},
		{path: "/ready", statusCode: http.StatusServiceUnavailable},
	} {
		response, err := http.Get(baseURL + test.path)
		if err != nil {
			t.Fatalf("GET %s: %v", test.path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", test.path, readErr)
		}
		if response.StatusCode != test.statusCode {
			t.Errorf("%s status=%d, want %d", test.path, response.StatusCode, test.statusCode)
		}
		if got := response.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("%s Content-Type=%q", test.path, got)
		}
		if !strings.Contains(string(body), `"ready":false`) {
			t.Errorf("%s body=%q", test.path, body)
		}
	}

	request, err := http.NewRequest(http.MethodPost, baseURL+"/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /live: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /live status=%d", response.StatusCode)
	}
	if got := response.Header.Get("Allow"); got != http.MethodGet {
		t.Errorf("POST /live Allow=%q", got)
	}
}
