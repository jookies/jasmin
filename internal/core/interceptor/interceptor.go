package interceptor

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

var (
	ErrInvalidInterceptorParameter = errors.New("invalid interceptor parameter")
)

type Action string

const (
	ActionContinue Action = "continue"
	ActionReject   Action = "reject"
)

type Script struct {
	IDValue string
	PyCode  string
}

func (s Script) ID() string { return s.IDValue }

type Context struct {
	Routable    routingfilter.Routable
	SMPPStatus  int
	HTTPStatus  int
}

type Result struct {
	Routable    routingfilter.Routable
	SMPPStatus  int
	HTTPStatus  int
	Action      Action
	ExecutionMS int64
}

type Runner interface {
	Run(ctx context.Context, script Script, req Context) (Result, error)
}

type Interceptor struct {
	filters []routingfilter.Filter
	script  Script
}

func NewInterceptor(script Script, filters ...routingfilter.Filter) (Interceptor, error) {
	if script.IDValue == "" {
		return Interceptor{}, fmt.Errorf("%w: empty script id", ErrInvalidInterceptorParameter)
	}
	// Filters can be empty (matching all)
	return Interceptor{
		filters: append([]routingfilter.Filter(nil), filters...),
		script:  script,
	}, nil
}

type entry struct {
	order int
	intcp Interceptor
}

type Table struct {
	entries []entry
}

type TableBuilder struct {
	interceptors map[int]Interceptor
}

func NewTableBuilder() *TableBuilder {
	return &TableBuilder{interceptors: make(map[int]Interceptor)}
}

func (b *TableBuilder) Add(order int, i Interceptor) error {
	if order < 0 {
		return fmt.Errorf("%w: negative order", ErrInvalidInterceptorParameter)
	}
	b.interceptors[order] = i
	return nil
}

func (b *TableBuilder) Build() *Table {
	orders := make([]int, 0, len(b.interceptors))
	for order := range b.interceptors {
		orders = append(orders, order)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(orders)))

	entries := make([]entry, 0, len(orders))
	for _, order := range orders {
		entries = append(entries, entry{order: order, intcp: b.interceptors[order]})
	}
	return &Table{entries: entries}
}

func (t *Table) Intercept(ctx context.Context, runner Runner, routable routingfilter.Routable) (Result, error) {
	current := routable.Clone()
	smppStatus := 0
	httpStatus := 0

	for _, e := range t.entries {
		matched := true
		for _, f := range e.intcp.filters {
			ok, err := f.Match(current)
			if err != nil {
				return Result{}, err
			}
			if !ok {
				matched = false
				break
			}
		}

		if matched {
			res, err := runner.Run(ctx, e.intcp.script, Context{
				Routable:   current,
				SMPPStatus: smppStatus,
				HTTPStatus: httpStatus,
			})
			if err != nil {
				// RI-005: Failure handling. 
				// Jasmin logs and continues with the next interceptor on script error, 
				// but let's be more robust: we'll log it and treat as "continue" without changes.
				// For now, we return error to let the caller decide.
				return Result{}, fmt.Errorf("interceptor %s failed: %w", e.intcp.script.ID(), err)
			}
			
			current = res.Routable
			smppStatus = res.SMPPStatus
			httpStatus = res.HTTPStatus

			if res.Action == ActionReject {
				return res, nil
			}
		}
	}

	return Result{
		Routable:   current,
		SMPPStatus: smppStatus,
		HTTPStatus: httpStatus,
		Action:     ActionContinue,
	}, nil
}
