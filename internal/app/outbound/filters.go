package outbound

import (
	"fmt"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

// FilterConfig is one MT route filter. Type selects the dimension; the other
// fields carry that type's parameters (legacy jasmin.routing.Filters):
//
//	destination_addr / source_addr / short_message : Pattern (regex)
//	tag                                            : Value
//	user                                           : Username (resolved to uid)
//	date_interval                                  : Start, End (YYYY-MM-DD)
//	time_interval                                  : Start, End (HH:MM:SS)
//
// The connector filter is MO-only and is not accepted on an MT route. Group
// filters are deferred until the user/group config model exists (the routable
// carries GroupID, but config users have no group today).
type FilterConfig struct {
	Type     string `json:"type"`
	Pattern  string `json:"pattern,omitempty"`
	Value    string `json:"value,omitempty"`
	Username string `json:"username,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
}

// uidResolver resolves a username to its internal billing uid. A false result
// means the username is not configured.
type uidResolver func(username string) (int64, bool)

// buildRouteFilters translates a route's filter specs into engine filters,
// rejecting shapes an MT route cannot carry.
func buildRouteFilters(specs []FilterConfig, resolveUID uidResolver) ([]routingfilter.Filter, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	filters := make([]routingfilter.Filter, 0, len(specs))
	for index, spec := range specs {
		filter, err := buildRouteFilter(spec, resolveUID)
		if err != nil {
			return nil, fmt.Errorf("filter %d: %w", index, err)
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

func buildRouteFilter(spec FilterConfig, resolveUID uidResolver) (routingfilter.Filter, error) {
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
	case "user":
		if resolveUID == nil {
			return nil, fmt.Errorf("user filter unsupported in this context")
		}
		uid, ok := resolveUID(spec.Username)
		if !ok {
			return nil, fmt.Errorf("user filter references unknown username %q", spec.Username)
		}
		return routingfilter.NewUserFilter(uid), nil
	case "connector":
		return nil, fmt.Errorf("connector filter is MO-only, not valid on an MT route")
	case "":
		return nil, fmt.Errorf("filter type is required")
	default:
		return nil, fmt.Errorf("unknown filter type %q", spec.Type)
	}
}
