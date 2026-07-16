package interceptor_test

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

type mockRunner struct {
	runFn func(script interceptor.Script, req interceptor.Context) (interceptor.Result, error)
}

func (m *mockRunner) Run(ctx context.Context, script interceptor.Script, req interceptor.Context) (interceptor.Result, error) {
	if m.runFn != nil {
		return m.runFn(script, req)
	}
	return interceptor.Result{
		Routable:   req.Routable,
		SMPPStatus: req.SMPPStatus,
		HTTPStatus: req.HTTPStatus,
		Action:     interceptor.ActionContinue,
	}, nil
}

func TestInterceptorTableMatch(t *testing.T) {
	b := interceptor.NewTableBuilder()
	
	// Interceptor 1: matches source_addr=^123, adds tag "first"
	f1, _ := routingfilter.NewSourceAddrFilter("^123")
	s1 := interceptor.Script{IDValue: "s1", PyCode: "routable.AddTag('first')"}
	i1, _ := interceptor.NewInterceptor(s1, f1)
	_ = b.Add(100, i1)

	// Interceptor 2: matches all, adds tag "second"
	s2 := interceptor.Script{IDValue: "s2", PyCode: "routable.AddTag('second')"}
	i2, _ := interceptor.NewInterceptor(s2)
	_ = b.Add(50, i2)

	table := b.Build()

	r, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:  routingfilter.MT,
		SourceAddr: routingfilter.BytesField{Present: true, Value: []byte("12345")},
		Timestamp:  time.Now(),
	})

	runner := &mockRunner{
		runFn: func(script interceptor.Script, req interceptor.Context) (interceptor.Result, error) {
			newR := req.Routable
			if script.IDValue == "s1" {
				_ = newR.AddTag("first")
			} else if script.IDValue == "s2" {
				_ = newR.AddTag("second")
			}
			return interceptor.Result{
				Routable: newR,
				Action:   interceptor.ActionContinue,
			}, nil
		},
	}

	res, err := table.Intercept(context.Background(), runner, r)
	if err != nil {
		t.Fatalf("Intercept failed: %v", err)
	}

	tags := res.Routable.Tags()
	if len(tags) != 2 {
		t.Errorf("tags=%v want 2", tags)
	}
}

func TestInterceptorRejection(t *testing.T) {
	b := interceptor.NewTableBuilder()
	
	s1 := interceptor.Script{IDValue: "s1", PyCode: "smpp_status = 64; action = 'reject'"}
	i1, _ := interceptor.NewInterceptor(s1)
	_ = b.Add(100, i1)

	// This one should NOT run
	s2 := interceptor.Script{IDValue: "s2", PyCode: "routable.AddTag('should-not-be-here')"}
	i2, _ := interceptor.NewInterceptor(s2)
	_ = b.Add(50, i2)

	table := b.Build()

	r, _ := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction: routingfilter.MT,
		Timestamp: time.Now(),
	})

	runner := &mockRunner{
		runFn: func(script interceptor.Script, req interceptor.Context) (interceptor.Result, error) {
			if script.IDValue == "s1" {
				return interceptor.Result{
					Routable:   req.Routable,
					SMPPStatus: 64,
					HTTPStatus: 500,
					Action:     interceptor.ActionReject,
				}, nil
			}
			t.Errorf("script %s should not run", script.IDValue)
			return interceptor.Result{}, nil
		},
	}

	res, err := table.Intercept(context.Background(), runner, r)
	if err != nil {
		t.Fatalf("Intercept failed: %v", err)
	}

	if res.Action != interceptor.ActionReject {
		t.Errorf("action=%v want reject", res.Action)
	}
	if res.SMPPStatus != 64 {
		t.Errorf("smpp_status=%v want 64", res.SMPPStatus)
	}
}
