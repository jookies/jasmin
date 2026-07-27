package outbound

import (
	"context"
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

// staticRunner is a fake interceptor.Runner that records the scripts it ran and
// optionally rewrites the destination, to prove the table wires order+filters.
type staticRunner struct {
	ran        []string
	rewriteTo  string
	rejectCode int
}

func (r *staticRunner) Run(_ context.Context, script interceptor.Script, req interceptor.Context) (interceptor.Result, error) {
	r.ran = append(r.ran, script.ID())
	if r.rejectCode != 0 {
		return interceptor.Result{Routable: req.Routable, SMPPStatus: r.rejectCode, HTTPStatus: 500, Action: interceptor.ActionReject}, nil
	}
	routable := req.Routable
	if r.rewriteTo != "" {
		routable, _ = routingfilter.NewRoutable(routingfilter.RoutableInput{
			Direction:       routingfilter.MT,
			DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(r.rewriteTo)},
			ShortMessage:    req.Routable.ShortMessage(),
		})
	}
	return interceptor.Result{Routable: routable, Action: interceptor.ActionContinue}, nil
}

func TestBuildInterceptorTableValidation(t *testing.T) {
	good := []InterceptorConfig{{Order: 10, PyCode: "x=1"}}
	if _, err := buildInterceptorTable(good, nil); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]InterceptorConfig{
		"empty py_code":  {{Order: 10, PyCode: ""}},
		"negative order": {{Order: -1, PyCode: "x=1"}},
		"dup order":      {{Order: 5, PyCode: "x=1"}, {Order: 5, PyCode: "y=2"}},
		"bad filter":     {{Order: 5, PyCode: "x=1", Filters: []FilterConfig{{Type: "destination_addr", Pattern: "("}}}},
	}
	for name, configs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := buildInterceptorTable(configs, nil); !errors.Is(err, ErrInvalidRuntimeConfig) {
				t.Fatalf("err=%v want ErrInvalidRuntimeConfig", err)
			}
		})
	}
}

func TestInterceptorTableFilterAndOrderApplied(t *testing.T) {
	// Two interceptors: a high-order one filtered to ^33 destinations that
	// rewrites, and a low-order catch-all. A ^33 message hits both (highest
	// first); a non-33 message hits only the catch-all.
	table, err := buildInterceptorTable([]InterceptorConfig{
		{Order: 10, PyCode: "a=1", Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^33"}}},
		{Order: 1, PyCode: "b=1"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &staticRunner{}
	frRoutable, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction: routingfilter.MT, DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("33123")},
		ShortMessage: routingfilter.BytesField{Present: true, Value: []byte("hi")},
	})
	if _, err := table.Intercept(context.Background(), runner, frRoutable); err != nil {
		t.Fatal(err)
	}
	if len(runner.ran) != 2 || runner.ran[0] != "mt-interceptor-10" || runner.ran[1] != "mt-interceptor-1" {
		t.Fatalf("french ran=%v want [10,1] in order", runner.ran)
	}

	runner.ran = nil
	usRoutable, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction: routingfilter.MT, DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("15551234")},
		ShortMessage: routingfilter.BytesField{Present: true, Value: []byte("hi")},
	})
	if _, err := table.Intercept(context.Background(), runner, usRoutable); err != nil {
		t.Fatal(err)
	}
	if len(runner.ran) != 1 || runner.ran[0] != "mt-interceptor-1" {
		t.Fatalf("us ran=%v want only catch-all", runner.ran)
	}
}
