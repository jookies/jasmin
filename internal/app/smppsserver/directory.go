// Package smppsserver composes the SMPPS server into the gateway: an SMPPS user
// directory (bind auth + MT credential), the submit-ingestion handler over the
// shared MT pipeline, and the listener lifecycle.
package smppsserver

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pumpitspace/synevyr/internal/core/mtcredential"
	"github.com/pumpitspace/synevyr/internal/core/smpps"
)

var ErrInvalidConfig = errors.New("smppsserver: invalid configuration")

// UserConfig is one SMPPS user (system_id) with its bind credentials and MT
// credential (authorizations, value filters, default source address). Password
// is plaintext here and md5-digested for bind auth, matching the legacy
// SmppsCredential (md5 of the stored password).
type UserConfig struct {
	SystemID      string `json:"system_id"`
	Password      string `json:"password"`
	Disabled      bool   `json:"disabled,omitempty"`       // default enabled
	GroupDisabled bool   `json:"group_disabled,omitempty"` // default enabled; see buildDirectorySnapshot
	IPWhitelist   string `json:"ip_whitelist,omitempty"`   // default DefaultIPWhitelist (any IPv4)
	MaxBindings   *int   `json:"max_bindings,omitempty"`   // nil = unlimited

	// Bind is the SmppsCredential 'bind' authorization (jasminApi.py:SmppsCredential),
	// which gates the session itself. It is NOT the same thing as smpps_send below,
	// which gates each submit_sm: legacy lets a user bind but refuse every submit
	// (bind=True, smpps_send=False), e.g. a receiver-only account. When omitted,
	// bind falls back to smpps_send's effective value — the two were historically
	// conflated here (and jcli still mirrors legacy 'bind' into smpps_send), so a
	// stored account that set smpps_send=false to block a user keeps being blocked
	// at bind time, with the same observable failure mode as before.
	Bind *bool `json:"bind,omitempty"`

	// MT authorizations (all default true, matching a permissive SmppsCredential).
	// A pointer distinguishes "omitted → default true" from "explicit false".
	SMPPSSend        *bool `json:"smpps_send,omitempty"`
	SetDLRLevel      *bool `json:"set_dlr_level,omitempty"`
	SetSourceAddress *bool `json:"set_source_address,omitempty"`
	SetPriority      *bool `json:"set_priority,omitempty"`

	// MT value filters — regex sources for MtMessagingCredential.value_filters
	// (jasminApi.py:126), enforced per submit_sm by SmppsCredentialValidator
	// (jasmin/protocols/smpp/validation.py:_checkSendFilters). Empty means the
	// legacy default pattern (".*" / "^[0-3]$" / `^\d+$`), i.e. permissive.
	// Gotcha: patterns are Go RE2 — backreferences/lookaround that Python's re
	// accepts are rejected at config validation, not silently at match time.
	// Matching is re.match semantics: anchored at the start only, so a
	// destination fence must be written "^2547" style; ".*2547" is a substring
	// match, and "2547" alone still only matches from position 0.
	FilterDestinationAddress string `json:"filter_destination_address,omitempty"`
	FilterSourceAddress      string `json:"filter_source_address,omitempty"`
	FilterPriority           string `json:"filter_priority,omitempty"`
	// FilterValidityPeriod is stored for MtMessagingCredential parity, but the
	// smpps validator does not consult it — legacy SmppsCredentialValidator has
	// no validity_period check either (only the HTTP validator does).
	FilterValidityPeriod string `json:"filter_validity_period,omitempty"`
	FilterContent        string `json:"filter_content,omitempty"`

	// DefaultSourceAddress is substituted for an absent OR empty source_addr
	// (updatePDUWithUserDefaults, smpps flavour). A nil pointer means "unset"
	// (Python None), which is not the same as "".
	DefaultSourceAddress *string `json:"default_source_address,omitempty"`
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
		// bind (session) and smpps_send (per-submit) are distinct legacy
		// authorizations; when bind is omitted it inherits smpps_send's
		// effective value so accounts stored before the split keep failing at
		// the same place they always did (see the UserConfig.Bind comment).
		bindAuthorized := authOrDefault(user.SMPPSSend)
		if user.Bind != nil {
			bindAuthorized = *user.Bind
		}
		directory.auth[user.SystemID] = smpps.UserAuth{
			PasswordDigest: smpps.PasswordHash(user.Password),
			UserEnabled:    !user.Disabled,
			GroupEnabled:   !user.GroupDisabled,
			BindAuthorized: bindAuthorized,
			IPWhitelist:    whitelist,
			MaxBindings:    maxBindings,
		}

		// The credential starts at Jasmin's permissive default
		// (MtMessagingCredential(default_authorizations=True)) and is tightened
		// only by fields the operator actually set. Backward-compatibility rule:
		// a stored user with none of the credential fields behaves exactly as it
		// did before these fields existed — permissive — because silently
		// tightening a live gateway is its own outage. Absence always means
		// "legacy default", never "deny".
		credential := mtcredential.New(true)
		credential.SetAuthorization(mtcredential.AuthSMPPSSend, authOrDefault(user.SMPPSSend))
		credential.SetAuthorization(mtcredential.AuthSetDLRLevel, authOrDefault(user.SetDLRLevel))
		credential.SetAuthorization(mtcredential.AuthSetSourceAddress, authOrDefault(user.SetSourceAddress))
		credential.SetAuthorization(mtcredential.AuthSetPriority, authOrDefault(user.SetPriority))
		// Value filters are the destination/sender fence ValidateSubmit enforces
		// per submit_sm. A pattern that does not compile is a config error, not a
		// silent no-op: a fence the operator believes is active but is not is
		// exactly the AIT/grey-route exposure this credential exists to prevent.
		for _, filter := range []struct{ key, pattern string }{
			{mtcredential.FilterDestinationAddress, user.FilterDestinationAddress},
			{mtcredential.FilterSourceAddress, user.FilterSourceAddress},
			{mtcredential.FilterPriority, user.FilterPriority},
			{mtcredential.FilterValidityPeriod, user.FilterValidityPeriod},
			{mtcredential.FilterContent, user.FilterContent},
		} {
			if filter.pattern == "" {
				continue // omitted → keep the legacy default pattern
			}
			if err := credential.SetValueFilter(filter.key, filter.pattern); err != nil {
				return nil, fmt.Errorf("%w: user %q %s filter: %v", ErrInvalidConfig, user.SystemID, filter.key, err)
			}
		}
		if user.DefaultSourceAddress != nil {
			credential.SetDefaultSourceAddress([]byte(*user.DefaultSourceAddress))
		}
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
