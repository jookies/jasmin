package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// moInterceptorAdapter bridges the smppc MO-interception seam to the interceptor
// engine: it builds an MO routable from the deliver_sm fields, runs the table
// through the shared script runner, and maps the result back. This keeps the
// smppc core decoupled from the interceptor runtime.
type moInterceptorAdapter struct {
	table  interceptionTable
	runner interceptor.Runner
	now    func() time.Time
}

// interceptionTable is the read seam: a fixed table or a live-swappable one.
type interceptionTable interface {
	Intercept(ctx context.Context, runner interceptor.Runner, routable routingfilter.Routable) (interceptor.Result, error)
}

// ErrMOInterceptionUnavailable reports admin MO interceptors applied to a
// runtime that is not running MO interception at all — the entries would be
// silently inert otherwise.
var ErrMOInterceptionUnavailable = errors.New("gateway: MO interception is not enabled")

// ApplyAdminMOInterceptors rebuilds the MO interception table from the config
// interceptors plus the supplied admin ones and swaps it live, mirroring
// outbound.ApplyAdminMTInterceptors. A reserved order or a bad spec leaves the
// active table untouched.
func (runtime *Runtime) ApplyAdminMOInterceptors(adminInterceptors []outbound.InterceptorConfig) error {
	runtime.moInterceptorsMu.Lock()
	defer runtime.moInterceptorsMu.Unlock()
	if runtime.moInterceptors == nil {
		return ErrMOInterceptionUnavailable
	}
	reserved := make(map[int]struct{}, len(runtime.configMOInterceptors))
	for _, entry := range runtime.configMOInterceptors {
		reserved[entry.Order] = struct{}{}
	}
	for _, entry := range adminInterceptors {
		if _, clash := reserved[entry.Order]; clash {
			return fmt.Errorf("%w: order %d", outbound.ErrInterceptorOrderReserved, entry.Order)
		}
	}
	combined := make([]outbound.InterceptorConfig, 0, len(runtime.configMOInterceptors)+len(adminInterceptors))
	combined = append(combined, runtime.configMOInterceptors...)
	combined = append(combined, adminInterceptors...)
	table, err := outbound.BuildMOInterceptorTable(combined)
	if err != nil {
		return err
	}
	runtime.moInterceptors.Store(table)
	return nil
}

func newMOInterceptorAdapter(table interceptionTable, runner interceptor.Runner) *moInterceptorAdapter {
	return &moInterceptorAdapter{table: table, runner: runner, now: time.Now}
}

// InterceptMO satisfies smppc.MOInterceptor.
func (a *moInterceptorAdapter) InterceptMO(ctx context.Context, in smppc.MOInterceptData) (smppc.MOInterceptResult, error) {
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MO,
		ConnectorID:     in.ConnectorID,
		SourceAddr:      routingfilter.BytesField{Present: len(in.SourceAddr) > 0, Value: in.SourceAddr},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: in.DestinationAddr},
		ShortMessage:    routingfilter.BytesField{Present: len(in.ShortMessage) > 0, Value: in.ShortMessage},
		MessagePayload:  routingfilter.BytesField{Present: len(in.MessagePayload) > 0, Value: in.MessagePayload},
		Timestamp:       a.now(),
	})
	if err != nil {
		return smppc.MOInterceptResult{}, err
	}
	result, err := a.table.Intercept(ctx, a.runner, routable)
	if err != nil {
		return smppc.MOInterceptResult{}, err
	}
	if result.Action == interceptor.ActionReject {
		return smppc.MOInterceptResult{Reject: true}, nil
	}
	return smppc.MOInterceptResult{
		SourceAddr:      result.Routable.SourceAddr().Value,
		DestinationAddr: result.Routable.DestinationAddr().Value,
		ShortMessage:    result.Routable.ShortMessage().Value,
	}, nil
}

var _ smppc.MOInterceptor = (*moInterceptorAdapter)(nil)
