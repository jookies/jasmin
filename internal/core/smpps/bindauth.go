package smpps

import "crypto/md5"

// SMPP 3.4 command_status values used at bind time.
const (
	StatusROK             uint32 = 0x00000000 // ESME_ROK
	StatusBindFailed      uint32 = 0x0000000D // ESME_RBINDFAIL
	StatusInvalidPassword uint32 = 0x0000000E // ESME_RINVPASWD
)

// PasswordHash returns the unsalted md5 digest Jasmin stores for a user password
// (md5(password).digest()). Passwords are ASCII in Jasmin; non-ASCII cannot exist in
// stored data.
func PasswordHash(password string) [16]byte {
	return md5.Sum([]byte(password))
}

// UserAuth is the smpps-relevant authentication/authorization state of a user, projected
// from the domain model by the caller (the user store). IPWhitelist should be the
// effective value — DefaultIPWhitelist when the user set none.
type UserAuth struct {
	PasswordDigest [16]byte // md5(password): the stored credential
	UserEnabled    bool
	GroupEnabled   bool
	BindAuthorized bool   // SmppsCredential authorization 'bind'
	IPWhitelist    string // SmppsCredential authorization 'ip'
	MaxBindings    *int   // SmppsCredential quota 'max_bindings' (nil = unlimited)
}

// AuthorizeBind runs Jasmin's doBindRequest credential checks in order — password (md5 +
// user/group enabled) → IP whitelist → bind authorization → max_bindings quota — and
// returns ESME_ROK when the bind is allowed, or the SMPP command status to reject with.
//
// currentBindCount is the user's total current binds across receiver/transmitter/
// transceiver (Jasmin's max_bindings cap is global per user, not per bind type). The
// OPEN session-state check that yields ESME_RALYBND is the session FSM's concern, not this.
//
// Password failure never distinguishes wrong-password from disabled user/group — all
// three collapse to ESME_RINVPASWD, matching authenticateUser returning None.
func AuthorizeBind(u UserAuth, password, peerIP string, currentBindCount int) uint32 {
	if PasswordHash(password) != u.PasswordDigest || !u.UserEnabled || !u.GroupEnabled {
		return StatusInvalidPassword
	}
	if !IsIPAllowed(peerIP, u.IPWhitelist) {
		return StatusBindFailed
	}
	if !u.BindAuthorized {
		return StatusBindFailed
	}
	if u.MaxBindings != nil && currentBindCount >= *u.MaxBindings {
		return StatusBindFailed
	}
	return StatusROK
}
