package interceptor

import (
	"context"
	"sync/atomic"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

// AtomicTable holds a live-swappable interception table. Reads on the message
// path are lock-free; admin mutations rebuild a whole Table and swap it in, so
// a message is always intercepted by exactly one coherent table — never a
// half-applied one.
type AtomicTable struct {
	table atomic.Pointer[Table]
}

// NewAtomicTable seeds the holder. A nil initial table is replaced by an empty
// one so Intercept is always safe to call.
func NewAtomicTable(initial *Table) *AtomicTable {
	holder := &AtomicTable{}
	if initial == nil {
		initial = NewTableBuilder().Build()
	}
	holder.table.Store(initial)
	return holder
}

// Store swaps in a new table.
func (a *AtomicTable) Store(table *Table) {
	if table == nil {
		table = NewTableBuilder().Build()
	}
	a.table.Store(table)
}

// Load returns the live table.
func (a *AtomicTable) Load() *Table { return a.table.Load() }

// Intercept runs the live table, so an AtomicTable is a drop-in for a Table
// wherever interception is consumed.
func (a *AtomicTable) Intercept(ctx context.Context, runner Runner, routable routingfilter.Routable) (Result, error) {
	return a.table.Load().Intercept(ctx, runner, routable)
}
