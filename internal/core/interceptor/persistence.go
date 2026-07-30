package interceptor

import (
	"context"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

// InterceptorState represents the serializable state of an interceptor.
type InterceptorState struct {
	ScriptID   string                       `json:"script_id"`
	PyCode     string                       `json:"py_code"`
	Filters    []routingfilter.FilterState  `json:"filters"`
}

// GetState returns the serializable state of the interceptor.
func (i Interceptor) GetState() InterceptorState {
	filters := make([]routingfilter.FilterState, 0, len(i.filters))
	for _, f := range i.filters {
		filters = append(filters, routingfilter.GetFilterState(f))
	}

	return InterceptorState{
		ScriptID: i.script.IDValue,
		PyCode:   i.script.PyCode,
		Filters:  filters,
	}
}

// FromInterceptorState reconstructs an interceptor from its state.
func FromInterceptorState(s InterceptorState) (Interceptor, error) {
	script := Script{
		IDValue: s.ScriptID,
		PyCode:   s.PyCode,
	}

	filters := make([]routingfilter.Filter, 0, len(s.Filters))
	for _, fs := range s.Filters {
		f, err := routingfilter.FromFilterState(fs)
		if err != nil {
			return Interceptor{}, err
		}
		filters = append(filters, f)
	}

	return NewInterceptor(script, filters...)
}

// InterceptorRepository defines the persistence contract for interceptors.
type InterceptorRepository interface {
	Save(ctx context.Context, order int, i Interceptor) error
	LoadAll(ctx context.Context) (map[int]Interceptor, error)
	Delete(ctx context.Context, order int) error
}
