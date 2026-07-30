package outbound

import (
	"fmt"
	"strconv"

	"github.com/pumpitspace/synevyr/internal/core/interceptor"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

// buildInterceptorTable builds the MT interception table from config: each
// entry's filters translate via the same rules as route filters, and its
// py_code becomes the interceptor script. Order collisions are rejected (a
// table can't hold two interceptors at one order).
func buildInterceptorTable(configs []InterceptorConfig, resolveUID uidResolver, groupResolvers ...gidResolver) (*interceptor.Table, error) {
	var resolveGID gidResolver
	if len(groupResolvers) > 0 {
		resolveGID = groupResolvers[0]
	}
	builder := interceptor.NewTableBuilder()
	seen := make(map[int]struct{}, len(configs))
	for index, entry := range configs {
		if entry.PyCode == "" {
			return nil, fmt.Errorf("%w: interceptor %d has empty py_code", ErrInvalidRuntimeConfig, index)
		}
		if entry.Order < 0 {
			return nil, fmt.Errorf("%w: interceptor %d has negative order", ErrInvalidRuntimeConfig, index)
		}
		if _, dup := seen[entry.Order]; dup {
			return nil, fmt.Errorf("%w: duplicate interceptor order %d", ErrInvalidRuntimeConfig, entry.Order)
		}
		seen[entry.Order] = struct{}{}
		filters, err := buildRouteFilters(entry.Filters, resolveUID, resolveGID)
		if err != nil {
			return nil, fmt.Errorf("%w: interceptor %d: %v", ErrInvalidRuntimeConfig, index, err)
		}
		script := interceptor.Script{IDValue: "mt-interceptor-" + strconv.Itoa(entry.Order), PyCode: entry.PyCode}
		intcp, err := interceptor.NewInterceptor(script, filters...)
		if err != nil {
			return nil, fmt.Errorf("%w: interceptor %d: %v", ErrInvalidRuntimeConfig, index, err)
		}
		if err := builder.Add(entry.Order, intcp); err != nil {
			return nil, fmt.Errorf("%w: interceptor %d: %v", ErrInvalidRuntimeConfig, index, err)
		}
	}
	return builder.Build(), nil
}

// BuildMOInterceptorTable builds the MO-direction interception table from
// config. It reuses the MT builder — an interceptor's direction lives in the
// routable evaluated at run time, not in the table — with no uid resolver, so
// MO interceptor filters may use source/destination/short_message/tag/date/time
// (a user filter is rejected, matching the MO context).
func BuildMOInterceptorTable(configs []InterceptorConfig) (*interceptor.Table, error) {
	return buildInterceptorTable(configs, nil)
}

// FilterConfig is one MT route filter. Type selects the dimension; the other
// fields carry that type's parameters (legacy jasmin.routing.Filters):
//
//	destination_addr / source_addr / short_message : Pattern (regex)
//	tag                                            : Value
//	user                                           : Username (resolved to uid)
//	date_interval                                  : Start, End (YYYY-MM-DD)
//	time_interval                                  : Start, End (HH:MM:SS)
//	eval_py                                        : Value (legacy Python expression body)
//
// The connector filter is MO-only and is not accepted on an MT route.
type FilterConfig struct {
	Type     string `json:"type"`
	Pattern  string `json:"pattern,omitempty"`
	Value    string `json:"value,omitempty"`
	Username string `json:"username,omitempty"`
	GroupID  string `json:"group_id,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
}

// uidResolver resolves a username to its internal billing uid. A false result
// means the username is not configured.
type uidResolver func(username string) (int64, bool)

// gidResolver resolves a legacy group name to the stable numeric id carried by
// the routable and compared by routingfilter.GroupFilter.
type gidResolver func(gid string) (int64, bool)

// buildRouteFilters translates a route's filter specs into engine filters,
// rejecting shapes an MT route cannot carry.
func buildRouteFilters(specs []FilterConfig, resolveUID uidResolver, groupResolvers ...gidResolver) ([]routingfilter.Filter, error) {
	var resolveGID gidResolver
	if len(groupResolvers) > 0 {
		resolveGID = groupResolvers[0]
	}
	if len(specs) == 0 {
		return nil, nil
	}
	filters := make([]routingfilter.Filter, 0, len(specs))
	for index, spec := range specs {
		filter, err := buildRouteFilter(spec, resolveUID, resolveGID)
		if err != nil {
			return nil, fmt.Errorf("filter %d: %w", index, err)
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

func buildRouteFilter(spec FilterConfig, resolveUID uidResolver, resolveGID gidResolver) (routingfilter.Filter, error) {
	switch spec.Type {
	case "destination_addr":
		return routingfilter.NewDestinationAddrFilter(spec.Pattern)
	case "source_addr":
		return routingfilter.NewSourceAddrFilter(spec.Pattern)
	case "short_message":
		return routingfilter.NewShortMessageFilter(spec.Pattern)
	case "tag":
		return routingfilter.NewTagFilter(spec.Value)
	case "date_interval":
		return routingfilter.NewDateIntervalFilter(spec.Start, spec.End)
	case "time_interval":
		return routingfilter.NewTimeIntervalFilter(spec.Start, spec.End)
	case "eval_py":
		return routingfilter.NewEvalPyFilter(spec.Value)
	case "user":
		if resolveUID == nil {
			return nil, fmt.Errorf("user filter unsupported in this context")
		}
		uid, ok := resolveUID(spec.Username)
		if !ok {
			return nil, fmt.Errorf("user filter references unknown username %q", spec.Username)
		}
		return routingfilter.NewUserFilter(uid), nil
	case "group":
		if resolveGID == nil {
			return nil, fmt.Errorf("group filter unsupported in this context")
		}
		gid, ok := resolveGID(spec.GroupID)
		if !ok {
			return nil, fmt.Errorf("group filter references unknown gid %q", spec.GroupID)
		}
		return routingfilter.NewGroupFilter(gid), nil
	case "connector":
		return nil, fmt.Errorf("connector filter is MO-only, not valid on an MT route")
	case "":
		return nil, fmt.Errorf("filter type is required")
	default:
		return nil, fmt.Errorf("unknown filter type %q", spec.Type)
	}
}
