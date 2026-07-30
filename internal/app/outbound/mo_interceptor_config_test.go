package outbound

import (
	"context"
	"errors"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/interceptor"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

func TestBuildMOInterceptorTableRunsFilteredScript(t *testing.T) {
	// An MO interceptor filtered to ^2255 destinations runs (and can reject) on
	// a matching MO routable.
	table, err := BuildMOInterceptorTable([]InterceptorConfig{
		{Order: 10, PyCode: "x=1", Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^2255"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &staticRunner{rejectCode: 0x400}
	routable, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MO,
		ConnectorID:     "smsc-in",
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("2255")},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte("STOP")},
	})
	result, err := table.Intercept(context.Background(), runner, routable)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.ran) != 1 || result.Action != interceptor.ActionReject {
		t.Fatalf("ran=%v action=%v want one run + reject", runner.ran, result.Action)
	}
}

func TestBuildMOInterceptorTableRejectsUserFilter(t *testing.T) {
	// MO interceptors carry no uid resolver, so a user filter is invalid.
	_, err := BuildMOInterceptorTable([]InterceptorConfig{
		{Order: 1, PyCode: "x=1", Filters: []FilterConfig{{Type: "user", Username: "alice"}}},
	})
	if !errors.Is(err, ErrInvalidRuntimeConfig) {
		t.Fatalf("err=%v want ErrInvalidRuntimeConfig", err)
	}
}

func TestBuildMOInterceptorTableRejectsDuplicateOrder(t *testing.T) {
	_, err := BuildMOInterceptorTable([]InterceptorConfig{
		{Order: 5, PyCode: "x=1"}, {Order: 5, PyCode: "y=2"},
	})
	if !errors.Is(err, ErrInvalidRuntimeConfig) {
		t.Fatalf("err=%v want ErrInvalidRuntimeConfig", err)
	}
}

func TestBuildMOInterceptorTableEmptyIsNoop(t *testing.T) {
	table, err := BuildMOInterceptorTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	routable, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MO,
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("2255")},
	})
	runner := &staticRunner{}
	result, err := table.Intercept(context.Background(), runner, routable)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.ran) != 0 || result.Action != interceptor.ActionContinue {
		t.Fatalf("empty table ran=%v action=%v want no run + continue", runner.ran, result.Action)
	}
}
