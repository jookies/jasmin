// Package smppsserver composes the SMPPS server into the gateway: an SMPPS user
// directory (bind auth + MT credential), the submit-ingestion handler over the
// shared MT pipeline, and the listener lifecycle.
package smppsserver

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pumpitspace/jasmin/internal/core/mtcredential"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
)

var ErrInvalidConfig = errors.New("smppsserver: invalid configuration")

// UserConfig is one SMPPS user (system_id) with its bind credentials and MT
// authorizations. Password is plaintext here and md5-digested for bind auth,
// matching the legacy SmppsCredential (md5 of the stored password).
type UserConfig struct {
	SystemID    string `json:"system_id"`
	Password    string `json:"password"`
	Disabled    bool   `json:"disabled,omitempty"`     // default enabled
	IPWhitelist string `json:"ip_whitelist,omitempty"` // default DefaultIPWhitelist (any IPv4)
	MaxBindings *int   `json:"max_bindings,omitempty"` // nil = unlimited

	// MT authorizations (all default true, matching a permissive SmppsCredential).
	// A pointer distinguishes "omitted → default true" from "explicit false".
	SMPPSSend        *bool `json:"smpps_send,omitempty"`
	SetDLRLevel      *bool `json:"set_dlr_level,omitempty"`
	SetSourceAddress *bool `json:"set_source_address,omitempty"`
	SetPriority      *bool `json:"set_priority,omitempty"`
}

// directorySnapshot is an immutable resolution table, swapped atomically so
// bind attempts never read a half-updated map.
type directorySnapshot struct {
	auth        map[string]smpps.UserAuth
	credentials map[string]*mtcredential.Credential
}

// Directory resolves a system_id into its bind auth state and MT credential.
// It implements both smpps.UserResolver and smppssubmit.CredentialResolver.
//
// The server and submit handler hold this pointer for the process lifetime, so
// admin user provisioning swaps the snapshot inside rather than replacing the
// Directory itself.
type Directory struct {
	snapshot atomic.Pointer[directorySnapshot]
	// configUsers are the config-owned system_ids admin may not displace.
	// applyMu serialises rebuilds.
	configUsers []UserConfig
	applyMu     sync.Mutex
}

// NewDirectory builds the directory from the user configs, validating each and
// rejecting duplicate system_ids.
func NewDirectory(users []UserConfig) (*Directory, error) {
	snapshot, err := buildDirectorySnapshot(users)
	if err != nil {
		return nil, err
	}
	directory := &Directory{configUsers: append([]UserConfig(nil), users...)}
	directory.snapshot.Store(snapshot)
	return directory, nil
}

// ApplyUsers rebuilds the directory from the config users plus the supplied
// admin users and swaps it in. Config system_ids are reserved; a duplicate or
// invalid entry returns an error and leaves the live directory untouched, so
// the caller can persist only on success.
//
// Sessions already bound are unaffected: auth is resolved at bind time. Removing
// a user prevents new binds but does not tear down an existing one.
func (d *Directory) ApplyUsers(adminUsers []UserConfig) error {
	d.applyMu.Lock()
	defer d.applyMu.Unlock()
	reserved := make(map[string]struct{}, len(d.configUsers))
	for _, user := range d.configUsers {
		reserved[user.SystemID] = struct{}{}
	}
	for _, user := range adminUsers {
		if _, clash := reserved[user.SystemID]; clash {
			return fmt.Errorf("%w: system_id %q is config-owned", ErrSystemIDReserved, user.SystemID)
		}
	}
	combined := make([]UserConfig, 0, len(d.configUsers)+len(adminUsers))
	combined = append(combined, d.configUsers...)
	combined = append(combined, adminUsers...)
	snapshot, err := buildDirectorySnapshot(combined)
	if err != nil {
		return err
	}
	d.snapshot.Store(snapshot)
	return nil
}

// ErrSystemIDReserved reports an admin SMPPs user colliding with a config user.
var ErrSystemIDReserved = errors.New("smppsserver: system_id is config-owned")

// buildDirectorySnapshot validates every user and produces the resolution table.
// It returns a complete snapshot or an error — never a partial one.
func buildDirectorySnapshot(users []UserConfig) (*directorySnapshot, error) {
	directory := &directorySnapshot{
		auth:        make(map[string]smpps.UserAuth, len(users)),
		credentials: make(map[string]*mtcredential.Credential, len(users)),
	}
	for index, user := range users {
		if user.SystemID == "" {
			return nil, fmt.Errorf("%w: user %d has empty system_id", ErrInvalidConfig, index)
		}
		if _, duplicate := directory.auth[user.SystemID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate system_id %q", ErrInvalidConfig, user.SystemID)
		}
		whitelist := user.IPWhitelist
		if whitelist == "" {
			whitelist = smpps.DefaultIPWhitelist
		}
		if err := smpps.ValidateWhitelist(whitelist); err != nil {
			return nil, fmt.Errorf("%w: user %q ip_whitelist: %v", ErrInvalidConfig, user.SystemID, err)
		}
		if user.MaxBindings != nil && *user.MaxBindings < 0 {
			return nil, fmt.Errorf("%w: user %q max_bindings must be non-negative", ErrInvalidConfig, user.SystemID)
		}

		var maxBindings *int
		if user.MaxBindings != nil {
			value := *user.MaxBindings
			maxBindings = &value
		}
		directory.auth[user.SystemID] = smpps.UserAuth{
			PasswordDigest: smpps.PasswordHash(user.Password),
			UserEnabled:    !user.Disabled,
			GroupEnabled:   true,
			BindAuthorized: authOrDefault(user.SMPPSSend),
			IPWhitelist:    whitelist,
			MaxBindings:    maxBindings,
		}

		credential := mtcredential.New(true)
		credential.SetAuthorization(mtcredential.AuthSMPPSSend, authOrDefault(user.SMPPSSend))
		credential.SetAuthorization(mtcredential.AuthSetDLRLevel, authOrDefault(user.SetDLRLevel))
		credential.SetAuthorization(mtcredential.AuthSetSourceAddress, authOrDefault(user.SetSourceAddress))
		credential.SetAuthorization(mtcredential.AuthSetPriority, authOrDefault(user.SetPriority))
		directory.credentials[user.SystemID] = credential
	}
	return directory, nil
}

// ResolveUser implements smpps.UserResolver.
func (d *Directory) ResolveUser(systemID string) (smpps.UserAuth, bool) {
	auth, ok := d.snapshot.Load().auth[systemID]
	return auth, ok
}

// ResolveCredential implements smppssubmit.CredentialResolver.
func (d *Directory) ResolveCredential(systemID string) (*mtcredential.Credential, bool) {
	credential, ok := d.snapshot.Load().credentials[systemID]
	return credential, ok
}

func authOrDefault(value *bool) bool {
	return value == nil || *value
}
