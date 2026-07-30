package interceptor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/interceptor"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

type document struct {
	SchemaVersion  int       `json:"schema_version"`
	BaselineCommit string    `json:"baseline_commit"`
	Cases          []fixture `json:"cases"`
}
type fixture struct {
	ID       string   `json:"id"`
	Script   string   `json:"script"`
	Input    *input   `json:"input,omitempty"`
	Expected expected `json:"expected"`
}
type input struct {
	Locked []string `json:"locked"`
}
type expected struct {
	Routable   *pyRoutable `json:"routable"`
	SMPPStatus *int        `json:"smpp_status"`
	HTTPStatus *int        `json:"http_status"`
	Error      string      `json:"error"`
}
type pyRoutable struct {
	SourceAddr      string   `json:"source_addr"`
	DestinationAddr string   `json:"destination_addr"`
	ShortMessage    string   `json:"short_message"`
	Tags            []string `json:"tags"`
	Locked          []string `json:"locked"`
}

func TestGoldenInterceptor(t *testing.T) {
	doc := load(t)
	runner := &interceptor.PythonRunner{}

	for _, tc := range doc.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			rInput := routingfilter.RoutableInput{
				Direction:       routingfilter.MT,
				ConnectorID:     "abc",
				SourceAddr:      routingfilter.BytesField{Present: true, Value: []byte("20203060")},
				DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("123456")},
				ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte("hello world")},
				Timestamp:       time.Date(2023, 10, 27, 10, 0, 0, 0, time.UTC),
			}
			
			r, _ := routingfilter.NewRoutable(rInput)
			
			// Apply locks from fixture if provided
			if tc.Input != nil && tc.Input.Locked != nil {
				for _, f := range tc.Input.Locked {
					r.Lock(f)
				}
			}

			script := interceptor.Script{IDValue: tc.ID, PyCode: tc.Script}
			res, err := runner.Run(context.Background(), script, interceptor.Context{Routable: r})

			if tc.Expected.Error != "" {
				if err == nil {
					t.Errorf("expected error %q, got nil", tc.Expected.Error)
				} else if err.Error() != tc.Expected.Error {
					t.Errorf("error=%q want=%q", err.Error(), tc.Expected.Error)
				}
				return
			}

			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}

			// Verify routable
			if tc.Expected.Routable != nil {
				if string(res.Routable.SourceAddr().Value) != tc.Expected.Routable.SourceAddr {
					t.Errorf("source_addr=%q want=%q", string(res.Routable.SourceAddr().Value), tc.Expected.Routable.SourceAddr)
				}
				if string(res.Routable.DestinationAddr().Value) != tc.Expected.Routable.DestinationAddr {
					t.Errorf("destination_addr=%q want=%q", string(res.Routable.DestinationAddr().Value), tc.Expected.Routable.DestinationAddr)
				}
				if string(res.Routable.ShortMessage().Value) != tc.Expected.Routable.ShortMessage {
					t.Errorf("short_message=%q want=%q", string(res.Routable.ShortMessage().Value), tc.Expected.Routable.ShortMessage)
				}
				
				// Tags
				actualTags := res.Routable.Tags()
				if len(actualTags) != len(tc.Expected.Routable.Tags) {
					t.Errorf("tags=%v want=%v", actualTags, tc.Expected.Routable.Tags)
				}
			}

			// Verify statuses
			if tc.Expected.SMPPStatus != nil && res.SMPPStatus != *tc.Expected.SMPPStatus {
				t.Errorf("smpp_status=%v want=%v", res.SMPPStatus, *tc.Expected.SMPPStatus)
			}
			if tc.Expected.HTTPStatus != nil && res.HTTPStatus != *tc.Expected.HTTPStatus {
				t.Errorf("http_status=%v want=%v", res.HTTPStatus, *tc.Expected.HTTPStatus)
			}
		})
	}
}

func load(t *testing.T) document {
	t.Helper()
	p := filepath.Join("..", "..", "..", "compat", "fixtures", "interceptor", "baseline.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGoldenInterceptorRejection(t *testing.T) {
	runner := &interceptor.PythonRunner{}
	r, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction: routingfilter.MT,
		Timestamp: time.Now(),
	})

	script := interceptor.Script{IDValue: "reject", PyCode: "action = 'reject'; smpp_status = 64"}
	res, err := runner.Run(context.Background(), script, interceptor.Context{Routable: r})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if res.Action != interceptor.ActionReject {
		t.Errorf("action=%v want reject", res.Action)
	}
	if res.SMPPStatus != 64 {
		t.Errorf("smpp_status=%v want 64", res.SMPPStatus)
	}
}
