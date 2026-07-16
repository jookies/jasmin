package routingfilter

import (
	"fmt"
)

// FilterState represents the serializable state of a filter.
type FilterState struct {
	Kind   Kind           `json:"kind"`
	Params map[string]any `json:"params"`
}

// GetState returns the serializable state of the filter.
func GetFilterState(f Filter) FilterState {
	state := FilterState{
		Kind:   f.Kind(),
		Params: make(map[string]any),
	}

	switch filter := f.(type) {
	case connectorFilter:
		state.Params["connector_id"] = filter.connectorID
	case userFilter:
		state.Params["user_id"] = filter.userID
	case groupFilter:
		state.Params["group_id"] = filter.groupID
	case regexFilter:
		state.Params["pattern"] = filter.pattern.String()
	case dateIntervalFilter:
		state.Params["start"] = int(filter.start)
		state.Params["end"] = int(filter.end)
	case timeIntervalFilter:
		state.Params["start"] = filter.start
		state.Params["end"] = filter.end
	case tagFilter:
		state.Params["tag"] = filter.tag
	case *evalPyFilter:
		state.Params["py_code"] = filter.pyCode
	}

	return state
}

// FromFilterState reconstructs a filter from its state.
func FromFilterState(state FilterState) (Filter, error) {
	switch state.Kind {
	case KindTransparent:
		return NewTransparentFilter(), nil
	case KindConnector:
		cid, ok := state.Params["connector_id"].(string)
		if !ok {
			return nil, fmt.Errorf("missing connector_id in params")
		}
		return NewConnectorFilter(cid)
	case KindUser:
		uid, ok := state.Params["user_id"].(float64) // JSON numbers are float64
		if !ok {
			return nil, fmt.Errorf("missing user_id in params")
		}
		return NewUserFilter(int64(uid)), nil
	case KindGroup:
		gid, ok := state.Params["group_id"].(float64)
		if !ok {
			return nil, fmt.Errorf("missing group_id in params")
		}
		return NewGroupFilter(int64(gid)), nil
	case KindSourceAddr:
		pattern, ok := state.Params["pattern"].(string)
		if !ok {
			return nil, fmt.Errorf("missing pattern in params")
		}
		return NewSourceAddrFilter(pattern)
	case KindDestinationAddr:
		pattern, ok := state.Params["pattern"].(string)
		if !ok {
			return nil, fmt.Errorf("missing pattern in params")
		}
		return NewDestinationAddrFilter(pattern)
	case KindShortMessage:
		pattern, ok := state.Params["pattern"].(string)
		if !ok {
			return nil, fmt.Errorf("missing pattern in params")
		}
		return NewShortMessageFilter(pattern)
	case KindDateInterval:
		start, ok1 := state.Params["start"].(float64)
		end, ok2 := state.Params["end"].(float64)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("missing interval in params")
		}
		// dateValue is int, but NewDateIntervalFilter takes strings.
		// Let's use a specialized constructor or fix this.
		// Actually, I'll add a state-based constructor to filter.go or here.
		return dateIntervalFilter{filterBase: commonBase(KindDateInterval), start: dateValue(start), end: dateValue(end)}, nil
	case KindTimeInterval:
		start, ok1 := state.Params["start"].(float64)
		end, ok2 := state.Params["end"].(float64)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("missing interval in params")
		}
		return timeIntervalFilter{filterBase: commonBase(KindTimeInterval), start: int64(start), end: int64(end)}, nil
	case KindTag:
		tag, ok := state.Params["tag"].(string)
		if !ok {
			return nil, fmt.Errorf("missing tag in params")
		}
		return NewTagFilter(tag)
	case KindEvalPy:
		code, ok := state.Params["py_code"].(string)
		if !ok {
			return nil, fmt.Errorf("missing py_code in params")
		}
		return NewEvalPyFilter(code)
	default:
		return nil, fmt.Errorf("unknown filter kind: %s", state.Kind)
	}
}

// Special cases for filters that have complex internal state
func (f evalPyFilter) PyCode() string { return f.pyCode }

// Date and Time accessors for persistence
func (f dateIntervalFilter) Interval() (int, int) { return int(f.start), int(f.end) }
func (f timeIntervalFilter) Interval() (int64, int64) { return f.start, f.end }
func (f regexFilter) Pattern() string { return f.pattern.String() }
func (f connectorFilter) ConnectorID() string { return f.connectorID }
func (f userFilter) UserID() int64 { return f.userID }
func (f groupFilter) GroupID() int64 { return f.groupID }
func (f tagFilter) Tag() string { return f.tag }
