package routingtable

import (
	"sync/atomic"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

// AtomicTable is a routing table that can be swapped live: the submit path
// reads through Select while the admin plane rebuilds and Stores a new table.
// Reads are lock-free (atomic pointer load), so live re-provisioning never
// blocks or races the hot submit path.
type AtomicTable struct {
	current atomic.Pointer[Table]
}

// NewAtomicTable seeds the holder with an initial table.
func NewAtomicTable(initial Table) *AtomicTable {
	holder := &AtomicTable{}
	holder.Store(initial)
	return holder
}

// Store atomically replaces the active table.
func (a *AtomicTable) Store(table Table) {
	stored := table
	a.current.Store(&stored)
}

// Select routes against the currently-active table.
func (a *AtomicTable) Select(routable routingfilter.Routable) (Route, bool, error) {
	return a.current.Load().Select(routable)
}
