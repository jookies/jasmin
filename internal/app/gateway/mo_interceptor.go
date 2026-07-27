package gateway

import (
	"context"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// moInterceptorAdapter bridges the smppc MO-interception seam to the interceptor
// engine: it builds an MO routable from the deliver_sm fields, runs the table
// through the shared script runner, and maps the result back. This keeps the
// smppc core decoupled from the interceptor runtime.
type moInterceptorAdapter struct {
	table  *interceptor.Table
	runner interceptor.Runner
	now    func() time.Time
}

func newMOInterceptorAdapter(table *interceptor.Table, runner interceptor.Runner) *moInterceptorAdapter {
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
