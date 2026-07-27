package pyintercept_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/transport/pyintercept"
)

func mtRoutable(t *testing.T, source, destination, message string, tags ...string) routingfilter.Routable {
	t.Helper()
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		UserID:          7,
		SourceAddr:      routingfilter.BytesField{Present: source != "", Value: []byte(source)},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(destination)},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte(message)},
		Timestamp:       time.Unix(1_700_000_000, 0).UTC(),
		Tags:            tags,
	})
	if err != nil {
		t.Fatal(err)
	}
	return routable
}

func TestPyInterceptorScripts(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runner, err := pyintercept.NewRunner(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	if err := runner.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	t.Run("rewrite_destination", func(t *testing.T) {
		script := interceptor.Script{IDValue: "rewrite", PyCode: `routable.pdu.params['destination_addr'] = b'44' + routable.pdu.params['destination_addr']`}
		res, err := runner.Run(ctx, script, interceptor.Context{Routable: mtRoutable(t, "111", "7000", "hi")})
		if err != nil {
			t.Fatal(err)
		}
		if res.Action != interceptor.ActionContinue {
			t.Fatalf("action=%s want continue", res.Action)
		}
		if got := string(res.Routable.DestinationAddr().Value); got != "447000" {
			t.Fatalf("destination=%q want 447000", got)
		}
	})

	t.Run("add_tag", func(t *testing.T) {
		script := interceptor.Script{IDValue: "tag", PyCode: `routable.tags.append('premium')`}
		res, err := runner.Run(ctx, script, interceptor.Context{Routable: mtRoutable(t, "111", "7000", "hi")})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tag := range res.Routable.Tags() {
			if tag == "premium" {
				found = true
			}
		}
		if !found {
			t.Fatalf("tags=%v missing premium", res.Routable.Tags())
		}
	})

	t.Run("reject_by_content", func(t *testing.T) {
		script := interceptor.Script{IDValue: "block", PyCode: `
if b'STOP' in routable.pdu.params['short_message']:
    smpp_status = 88
    http_status = 400
`}
		res, err := runner.Run(ctx, script, interceptor.Context{Routable: mtRoutable(t, "111", "7000", "please STOP now")})
		if err != nil {
			t.Fatal(err)
		}
		if res.Action != interceptor.ActionReject {
			t.Fatalf("action=%s want reject", res.Action)
		}
		if res.SMPPStatus != 88 || res.HTTPStatus != 400 {
			t.Fatalf("statuses smpp=%d http=%d want 88/400", res.SMPPStatus, res.HTTPStatus)
		}
	})

	t.Run("reject_one_status_forces_both", func(t *testing.T) {
		// Legacy: setting only smpp_status forces http_status to 520.
		script := interceptor.Script{IDValue: "half", PyCode: `smpp_status = 100`}
		res, err := runner.Run(ctx, script, interceptor.Context{Routable: mtRoutable(t, "111", "7000", "x")})
		if err != nil {
			t.Fatal(err)
		}
		if res.Action != interceptor.ActionReject || res.SMPPStatus != 100 || res.HTTPStatus != 520 {
			t.Fatalf("half-status result action=%s smpp=%d http=%d want reject/100/520", res.Action, res.SMPPStatus, res.HTTPStatus)
		}
	})

	t.Run("pass_through_unchanged", func(t *testing.T) {
		script := interceptor.Script{IDValue: "noop", PyCode: `x = 1`}
		res, err := runner.Run(ctx, script, interceptor.Context{Routable: mtRoutable(t, "111", "7000", "hello")})
		if err != nil {
			t.Fatal(err)
		}
		if res.Action != interceptor.ActionContinue {
			t.Fatalf("action=%s want continue", res.Action)
		}
		if string(res.Routable.DestinationAddr().Value) != "7000" || string(res.Routable.ShortMessage().Value) != "hello" {
			t.Fatal("pass-through mutated the routable")
		}
	})

	t.Run("script_error_surfaces", func(t *testing.T) {
		script := interceptor.Script{IDValue: "boom", PyCode: `raise ValueError("boom")`}
		if _, err := runner.Run(ctx, script, interceptor.Context{Routable: mtRoutable(t, "111", "7000", "x")}); err == nil {
			t.Fatal("script error did not surface")
		}
	})
}
