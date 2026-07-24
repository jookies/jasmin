package smpps

// BindType is the SMPP bind type of a bound session.
type BindType uint8

const (
	BindReceiver BindType = iota
	BindTransmitter
	BindTransceiver
)

// Binding is one bound smpps session for a system_id. The manager identifies bindings by
// interface identity (use pointer types), so a session must be the same value across calls.
type Binding interface {
	BindType() BindType
}

// BindManager tracks the bound sessions for a single system_id and selects which
// receiver/transceiver session an inbound MO/DLR is delivered down, matching
// smpp.twisted's SMPPBindManager. Not safe for concurrent use; the session layer serializes.
type BindManager struct {
	binds   map[BindType][]Binding
	history []Binding // delivery round-robin history; front = oldest used
}

// NewBindManager returns an empty manager.
func NewBindManager() *BindManager {
	return &BindManager{
		binds: map[BindType][]Binding{BindReceiver: nil, BindTransmitter: nil, BindTransceiver: nil},
	}
}

// Add records a new bound session (append order per type is preserved).
func (m *BindManager) Add(b Binding) {
	t := b.BindType()
	m.binds[t] = append(m.binds[t], b)
}

// Remove drops a bound session. A stale reference still sitting in the delivery history is
// cleaned up lazily when GetNextBindingForDelivery next pops it.
func (m *BindManager) Remove(b Binding) {
	t := b.BindType()
	list := m.binds[t]
	for i, x := range list {
		if x == b {
			m.binds[t] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

// Count returns the total number of bound sessions across all bind types (the value
// Jasmin's max_bindings quota compares against).
func (m *BindManager) Count() int {
	return len(m.binds[BindReceiver]) + len(m.binds[BindTransmitter]) + len(m.binds[BindTransceiver])
}

// CountByType returns the number of bound sessions of one bind type.
func (m *BindManager) CountByType(t BindType) int { return len(m.binds[t]) }

// GetNextBindingForDelivery selects the next receiver/transceiver session to deliver an MO
// or DLR down, so traffic spreads evenly across the deliverers (smpp.twisted
// getNextBindingForDelivery). It prefers a not-yet-used deliverer; otherwise it reuses the
// oldest-used binding that is still bound. Returns nil when there is no deliverer.
func (m *BindManager) GetNextBindingForDelivery() Binding {
	var binding Binding

	// While we have more receiver+transceiver binds than history entries, prefer one that
	// has not yet been delivered on (receiver binds first, then transceiver).
	delivererCount := len(m.binds[BindReceiver]) + len(m.binds[BindTransceiver])
	if len(m.history) < delivererCount {
		binding = m.firstUnusedDeliverer()
	}

	// Otherwise reuse the oldest-used binding that is still bound, discarding stale ones.
	for binding == nil && len(m.history) > 0 {
		oldest := m.history[0]
		m.history = m.history[1:]
		if m.isBound(oldest) {
			binding = oldest
		}
	}

	if binding != nil {
		m.history = append(m.history, binding)
	}
	return binding
}

func (m *BindManager) firstUnusedDeliverer() Binding {
	for _, b := range m.binds[BindReceiver] {
		if !m.inHistory(b) {
			return b
		}
	}
	for _, b := range m.binds[BindTransceiver] {
		if !m.inHistory(b) {
			return b
		}
	}
	return nil
}

func (m *BindManager) inHistory(b Binding) bool {
	for _, h := range m.history {
		if h == b {
			return true
		}
	}
	return false
}

func (m *BindManager) isBound(b Binding) bool {
	for _, x := range m.binds[b.BindType()] {
		if x == b {
			return true
		}
	}
	return false
}
