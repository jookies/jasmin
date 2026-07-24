package smpps

// This file ports the server-side bind-state command gate (SMPP_MATRIX S-003): which
// inbound request commands are permitted in each session state, and the exact SMPP
// command_status to reject with otherwise. Three decisions come straight from Jasmin's
// in-repo protocol.py: the accepted-command set → ESME_RSYSERR (PDURequestReceived), a
// submit_sm/data_sm while BOUND_RX → ESME_RINVBNDSTS (PDUDataRequestReceived), and a bind
// while not OPEN → ESME_RALYBND (doBindRequest). The remaining state gating (submit needs a
// transmit-capable bind; enquire_link any state; unbind needs a bound state) is standard
// SMPP 3.4 implemented by the vendored smpp.twisted server base, encoded here per spec.

// Additional SMPP 3.4 command_status values used by the bind-state gate (StatusROK,
// StatusBindFailed and StatusInvalidPassword are defined in bindauth.go).
const (
	StatusInvalidBindStatus uint32 = 0x00000004 // ESME_RINVBNDSTS
	StatusAlreadyBound      uint32 = 0x00000005 // ESME_RALYBND
	StatusSystemError       uint32 = 0x00000008 // ESME_RSYSERR
)

// SMPP 3.4 command_id values for the server-relevant request commands. Values align with
// internal/transport/smppwire where that package defines them; the set is kept local so
// this pure gate has no transport dependency (as with BindType in bindmanager.go).
const (
	CommandBindReceiver    uint32 = 0x00000001
	CommandBindTransmitter uint32 = 0x00000002
	CommandSubmitSM        uint32 = 0x00000004
	CommandUnbind          uint32 = 0x00000006
	CommandBindTransceiver uint32 = 0x00000009
	CommandEnquireLink     uint32 = 0x00000015
	CommandDataSM          uint32 = 0x00000103
	CommandUnbindResp      uint32 = 0x80000006
)

// SessionState is the bind state of a customer-facing smpps session.
type SessionState uint8

const (
	StateOpen     SessionState = iota // connected, not yet bound
	StateBoundRX                      // bound as receiver
	StateBoundTX                      // bound as transmitter
	StateBoundTRX                     // bound as transceiver
	StateUnbound                      // unbound (post-unbind), awaiting disconnect
)

func (s SessionState) String() string {
	switch s {
	case StateOpen:
		return "OPEN"
	case StateBoundRX:
		return "BOUND_RX"
	case StateBoundTX:
		return "BOUND_TX"
	case StateBoundTRX:
		return "BOUND_TRX"
	case StateUnbound:
		return "UNBOUND"
	default:
		return "INVALID"
	}
}

// isAcceptedRequest reports whether command is in Jasmin's accepted inbound-request set
// (protocol.py PDURequestReceived). Anything outside it is a fatal ESME_RSYSERR.
func isAcceptedRequest(command uint32) bool {
	switch command {
	case CommandSubmitSM, CommandBindTransmitter, CommandBindReceiver, CommandBindTransceiver,
		CommandUnbind, CommandUnbindResp, CommandEnquireLink, CommandDataSM:
		return true
	}
	return false
}

// CommandAllowed reports whether an inbound SMPP request command is permitted in the given
// session state. When it is not, status is the SMPP command_status to reject with; when it
// is, allowed is true and status is StatusROK. It is the pure state gate only — bind
// authentication (AuthorizeBind) and submit credential checks run afterward for allowed
// commands.
func CommandAllowed(state SessionState, command uint32) (allowed bool, status uint32) {
	if !isAcceptedRequest(command) {
		return false, StatusSystemError // Jasmin: unsupported pdu type
	}
	switch command {
	case CommandBindReceiver, CommandBindTransmitter, CommandBindTransceiver:
		// A bind is only valid from OPEN; otherwise the session is already bound.
		if state != StateOpen {
			return false, StatusAlreadyBound
		}
		return true, StatusROK
	case CommandSubmitSM, CommandDataSM:
		// Submitting requires a transmit-capable bind. BOUND_RX is rejected explicitly by
		// Jasmin; OPEN / UNBOUND are rejected by the same not-bound-for-transmit rule. Only
		// BOUND_TX / BOUND_TRX may submit.
		if state == StateBoundTX || state == StateBoundTRX {
			return true, StatusROK
		}
		return false, StatusInvalidBindStatus
	case CommandUnbind:
		// Unbind is valid only from a bound state.
		if state == StateBoundRX || state == StateBoundTX || state == StateBoundTRX {
			return true, StatusROK
		}
		return false, StatusInvalidBindStatus
	case CommandEnquireLink, CommandUnbindResp:
		// enquire_link is valid in any state; unbind_resp answers a server-initiated unbind.
		return true, StatusROK
	default:
		return false, StatusSystemError
	}
}

// StateForBind returns the bound session state a successful bind of the given command
// transitions to. ok is false when command is not a bind request.
func StateForBind(command uint32) (state SessionState, ok bool) {
	switch command {
	case CommandBindReceiver:
		return StateBoundRX, true
	case CommandBindTransmitter:
		return StateBoundTX, true
	case CommandBindTransceiver:
		return StateBoundTRX, true
	}
	return 0, false
}

// BindTypeForCommand maps a bind request command to the BindType used by BindManager. ok is
// false when command is not a bind request.
func BindTypeForCommand(command uint32) (BindType, bool) {
	switch command {
	case CommandBindReceiver:
		return BindReceiver, true
	case CommandBindTransmitter:
		return BindTransmitter, true
	case CommandBindTransceiver:
		return BindTransceiver, true
	}
	return 0, false
}
