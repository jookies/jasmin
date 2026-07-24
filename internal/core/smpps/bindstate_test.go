package smpps

import "testing"

func TestCommandAllowed_UnsupportedCommand(t *testing.T) {
	// Commands outside Jasmin's accepted set are a fatal ESME_RSYSERR in every state.
	unsupported := []uint32{
		0x00000003, // query_sm
		0x00000005, // deliver_sm (server->client, not an accepted inbound request)
		0x00000007, // replace_sm
		0x00000008, // cancel_sm
	}
	for _, state := range []SessionState{StateOpen, StateBoundTX, StateBoundRX, StateBoundTRX, StateUnbound} {
		for _, cmd := range unsupported {
			allowed, status := CommandAllowed(state, cmd)
			if allowed || status != StatusSystemError {
				t.Errorf("state %s cmd %#x = (%v,%#x), want (false,RSYSERR)", state, cmd, allowed, status)
			}
		}
	}
}

func TestCommandAllowed_Bind(t *testing.T) {
	binds := []uint32{CommandBindReceiver, CommandBindTransmitter, CommandBindTransceiver}
	for _, cmd := range binds {
		// Allowed only from OPEN.
		if allowed, status := CommandAllowed(StateOpen, cmd); !allowed || status != StatusROK {
			t.Errorf("bind %#x from OPEN = (%v,%#x), want (true,ROK)", cmd, allowed, status)
		}
		// Already bound (or unbound) -> ESME_RALYBND.
		for _, state := range []SessionState{StateBoundRX, StateBoundTX, StateBoundTRX, StateUnbound} {
			if allowed, status := CommandAllowed(state, cmd); allowed || status != StatusAlreadyBound {
				t.Errorf("bind %#x from %s = (%v,%#x), want (false,RALYBND)", cmd, state, allowed, status)
			}
		}
	}
}

func TestCommandAllowed_SubmitAndData(t *testing.T) {
	for _, cmd := range []uint32{CommandSubmitSM, CommandDataSM} {
		// Allowed from BOUND_TX and BOUND_TRX.
		for _, state := range []SessionState{StateBoundTX, StateBoundTRX} {
			if allowed, status := CommandAllowed(state, cmd); !allowed || status != StatusROK {
				t.Errorf("%#x from %s = (%v,%#x), want (true,ROK)", cmd, state, allowed, status)
			}
		}
		// Rejected from BOUND_RX (the explicit Jasmin rule), OPEN and UNBOUND -> RINVBNDSTS.
		for _, state := range []SessionState{StateBoundRX, StateOpen, StateUnbound} {
			if allowed, status := CommandAllowed(state, cmd); allowed || status != StatusInvalidBindStatus {
				t.Errorf("%#x from %s = (%v,%#x), want (false,RINVBNDSTS)", cmd, state, allowed, status)
			}
		}
	}
}

func TestCommandAllowed_Unbind(t *testing.T) {
	// Valid only from a bound state.
	for _, state := range []SessionState{StateBoundRX, StateBoundTX, StateBoundTRX} {
		if allowed, status := CommandAllowed(state, CommandUnbind); !allowed || status != StatusROK {
			t.Errorf("unbind from %s = (%v,%#x), want (true,ROK)", state, allowed, status)
		}
	}
	// Invalid from OPEN / UNBOUND -> RINVBNDSTS.
	for _, state := range []SessionState{StateOpen, StateUnbound} {
		if allowed, status := CommandAllowed(state, CommandUnbind); allowed || status != StatusInvalidBindStatus {
			t.Errorf("unbind from %s = (%v,%#x), want (false,RINVBNDSTS)", state, allowed, status)
		}
	}
}

func TestCommandAllowed_EnquireLinkAnyState(t *testing.T) {
	for _, state := range []SessionState{StateOpen, StateBoundRX, StateBoundTX, StateBoundTRX, StateUnbound} {
		if allowed, status := CommandAllowed(state, CommandEnquireLink); !allowed || status != StatusROK {
			t.Errorf("enquire_link from %s = (%v,%#x), want (true,ROK)", state, allowed, status)
		}
	}
}

func TestStateForBind(t *testing.T) {
	cases := []struct {
		cmd   uint32
		state SessionState
		ok    bool
	}{
		{CommandBindReceiver, StateBoundRX, true},
		{CommandBindTransmitter, StateBoundTX, true},
		{CommandBindTransceiver, StateBoundTRX, true},
		{CommandSubmitSM, 0, false},
		{CommandEnquireLink, 0, false},
	}
	for _, c := range cases {
		state, ok := StateForBind(c.cmd)
		if ok != c.ok || (ok && state != c.state) {
			t.Errorf("StateForBind(%#x) = (%s,%v), want (%s,%v)", c.cmd, state, ok, c.state, c.ok)
		}
	}
}

func TestBindTypeForCommand(t *testing.T) {
	cases := []struct {
		cmd uint32
		bt  BindType
		ok  bool
	}{
		{CommandBindReceiver, BindReceiver, true},
		{CommandBindTransmitter, BindTransmitter, true},
		{CommandBindTransceiver, BindTransceiver, true},
		{CommandUnbind, 0, false},
	}
	for _, c := range cases {
		bt, ok := BindTypeForCommand(c.cmd)
		if ok != c.ok || (ok && bt != c.bt) {
			t.Errorf("BindTypeForCommand(%#x) = (%v,%v), want (%v,%v)", c.cmd, bt, ok, c.bt, c.ok)
		}
	}
}

func TestSessionState_String(t *testing.T) {
	want := map[SessionState]string{
		StateOpen: "OPEN", StateBoundRX: "BOUND_RX", StateBoundTX: "BOUND_TX",
		StateBoundTRX: "BOUND_TRX", StateUnbound: "UNBOUND",
	}
	for state, name := range want {
		if state.String() != name {
			t.Errorf("%d.String() = %q, want %q", state, state.String(), name)
		}
	}
}
