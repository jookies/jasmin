package routepolicy

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"unicode/utf8"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

type Kind string

const (
	Random        Kind = "random"
	Failover      Kind = "failover"
	BestQuality   Kind = "bestquality"
	MaxConnectors      = 1024
)

var (
	ErrInvalidPolicy  = errors.New("invalid route policy")
	ErrNotImplemented = errors.New("route policy not implemented")
	ErrNoConnectors   = errors.New("route cannot have zero connectors")
	ErrIndex          = errors.New("connector index out of range")
)

type Route struct {
	kind       Kind
	direction  routingfilter.Direction
	connectors []routingtable.Connector
	rate       float64
	filters    []routingfilter.Filter
}

func New(kind Kind, direction routingfilter.Direction, connectors []routingtable.Connector, rate float64, filters ...routingfilter.Filter) (Route, error) {
	if kind == BestQuality {
		return Route{}, ErrNotImplemented
	}
	if kind != Random && kind != Failover {
		return Route{}, ErrInvalidPolicy
	}
	if direction != routingfilter.MT && direction != routingfilter.MO {
		return Route{}, ErrInvalidPolicy
	}
	if len(connectors) == 0 {
		return Route{}, ErrNoConnectors
	}
	if len(connectors) > MaxConnectors {
		return Route{}, ErrInvalidPolicy
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return Route{}, ErrInvalidPolicy
	}
	if len(filters) > routingtable.MaxFiltersPerRoute {
		return Route{}, ErrInvalidPolicy
	}
	cc := append([]routingtable.Connector(nil), connectors...)
	for _, c := range cc {
		if c.ID() == "" || len(c.ID()) > routingfilter.MaxIDBytes || !utf8.ValidString(c.ID()) {
			return Route{}, ErrInvalidPolicy
		}
		// MT terminates either upstream (an SMPP client connector hands the
		// message to a carrier) or here (a termination connector delivers it to
		// a local application and synthesizes the receipt). Both are valid pool
		// members; nothing else can carry an MT message.
		if direction == routingfilter.MT && c.Type() != routingtable.SMPPC && c.Type() != routingtable.TERM {
			return Route{}, ErrInvalidPolicy
		}
		if direction == routingfilter.MO && c.Type() != routingtable.HTTP && c.Type() != routingtable.SMPPS {
			return Route{}, ErrInvalidPolicy
		}
	}
	if kind == Failover && direction == routingfilter.MO {
		for _, c := range cc[1:] {
			if c.Type() != cc[0].Type() {
				return Route{}, ErrInvalidPolicy
			}
		}
	}
	ff := append([]routingfilter.Filter(nil), filters...)
	for _, f := range ff {
		if f == nil || !supports(f.Directions(), direction) {
			return Route{}, ErrInvalidPolicy
		}
	}
	if direction == routingfilter.MO {
		rate = 0
	}
	return Route{kind: kind, direction: direction, connectors: cc, rate: rate, filters: ff}, nil
}

func (r Route) Kind() Kind    { return r.kind }
func (r Route) Rate() float64 { return r.rate }
func (r Route) Connectors() []routingtable.Connector {
	return append([]routingtable.Connector(nil), r.connectors...)
}
func (r Route) Match(x routingfilter.Routable) (bool, error) {
	for _, f := range r.filters {
		ok, e := f.Match(x)
		if e != nil {
			return false, e
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
func (r Route) Choose(index int) (routingtable.Connector, error) {
	if r.kind != Random || index < 0 || index >= len(r.connectors) {
		return routingtable.Connector{}, ErrIndex
	}
	return r.connectors[index], nil
}
func (r Route) NewAttempt() *Attempt {
	return &Attempt{connectors: append([]routingtable.Connector(nil), r.connectors...)}
}

type Attempt struct {
	mu         sync.Mutex
	connectors []routingtable.Connector
	next       int
}

func (a *Attempt) Next() (routingtable.Connector, bool) {
	return a.NextAvailable(nil)
}

// NextAvailable preserves configured failover order while skipping connectors
// that are not currently observed as available. A nil predicate accepts all.
func (a *Attempt) NextAvailable(available func(routingtable.Connector) bool) (routingtable.Connector, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for a.next < len(a.connectors) {
		connector := a.connectors[a.next]
		a.next++
		if available == nil || available(connector) {
			return connector, true
		}
	}
	return routingtable.Connector{}, false
}
func (a *Attempt) String() string {
	return fmt.Sprintf("failover-attempt(%d/%d)", a.next, len(a.connectors))
}
func supports(values []routingfilter.Direction, want routingfilter.Direction) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
