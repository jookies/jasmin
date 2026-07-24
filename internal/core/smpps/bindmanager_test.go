package smpps

import "testing"

type testBind struct {
	name string
	t    BindType
}

func (b *testBind) BindType() BindType { return b.t }

// deliverN returns the names of N consecutive delivery selections.
func deliverN(m *BindManager, n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		b := m.GetNextBindingForDelivery()
		if b == nil {
			out = append(out, "nil")
			continue
		}
		out = append(out, b.(*testBind).name)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBindManager_RoundRobin(t *testing.T) {
	m := NewBindManager()
	r1 := &testBind{"r1", BindReceiver}
	r2 := &testBind{"r2", BindReceiver}
	t1 := &testBind{"t1", BindTransceiver}
	m.Add(r1)
	m.Add(r2)
	m.Add(t1)

	// receiver binds first, then transceiver, then cycle evenly.
	got := deliverN(m, 6)
	want := []string{"r1", "r2", "t1", "r1", "r2", "t1"}
	if !eq(got, want) {
		t.Errorf("round-robin = %v, want %v", got, want)
	}
}

func TestBindManager_NoDeliverers(t *testing.T) {
	m := NewBindManager()
	// A transmitter-only system_id has no deliverers.
	m.Add(&testBind{"tx1", BindTransmitter})
	if b := m.GetNextBindingForDelivery(); b != nil {
		t.Errorf("transmitter-only: got %v, want nil", b)
	}
	if b := NewBindManager().GetNextBindingForDelivery(); b != nil {
		t.Errorf("empty manager: got %v, want nil", b)
	}
}

func TestBindManager_RemoveSkipsStaleHistory(t *testing.T) {
	m := NewBindManager()
	r1 := &testBind{"r1", BindReceiver}
	r2 := &testBind{"r2", BindReceiver}
	t1 := &testBind{"t1", BindTransceiver}
	m.Add(r1)
	m.Add(r2)
	m.Add(t1)

	// Prime the history with one full cycle.
	_ = deliverN(m, 3) // r1, r2, t1 -> history [r1,r2,t1]

	// r2 unbinds; subsequent deliveries must skip it.
	m.Remove(r2)
	got := deliverN(m, 4)
	for _, name := range got {
		if name == "r2" {
			t.Errorf("delivered to removed binding r2: sequence %v", got)
		}
	}
	// Only r1 and t1 remain, cycling.
	want := []string{"r1", "t1", "r1", "t1"}
	if !eq(got, want) {
		t.Errorf("after remove = %v, want %v", got, want)
	}
}

func TestBindManager_Counts(t *testing.T) {
	m := NewBindManager()
	m.Add(&testBind{"r1", BindReceiver})
	m.Add(&testBind{"t1", BindTransceiver})
	m.Add(&testBind{"t2", BindTransceiver})
	m.Add(&testBind{"x1", BindTransmitter})
	if m.Count() != 4 {
		t.Errorf("Count = %d, want 4", m.Count())
	}
	if m.CountByType(BindTransceiver) != 2 || m.CountByType(BindReceiver) != 1 || m.CountByType(BindTransmitter) != 1 {
		t.Errorf("CountByType wrong: rx=%d trx=%d tx=%d", m.CountByType(BindReceiver), m.CountByType(BindTransceiver), m.CountByType(BindTransmitter))
	}
}

func TestBindManager_TransceiverOnlyDelivers(t *testing.T) {
	m := NewBindManager()
	t1 := &testBind{"t1", BindTransceiver}
	m.Add(t1)
	// A lone transceiver is a valid deliverer.
	if got := deliverN(m, 3); !eq(got, []string{"t1", "t1", "t1"}) {
		t.Errorf("transceiver-only delivery = %v", got)
	}
}
