// Package smppsserver composes the SMPPS server into the gateway: an SMPPS user
// directory (bind auth + MT credential), the submit-ingestion handler over the
// shared MT pipeline, and the listener lifecycle.
package smppsserver

import (
	"errors"
	"fmt"

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

// Directory resolves a system_id into its bind auth state and MT credential.
// It implements both smpps.UserResolver and smppssubmit.CredentialResolver.
type Directory struct {
	auth        map[string]smpps.UserAuth
	credentials map[string]*mtcredential.Credential
}

// NewDirectory builds the directory from the user configs, validating each and
// rejecting duplicate system_ids.
func NewDirectory(users []UserConfig) (*Directory, error) {
	directory := &Directory{
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
	auth, ok := d.auth[systemID]
	return auth, ok
}

// ResolveCredential implements smppssubmit.CredentialResolver.
func (d *Directory) ResolveCredential(systemID string) (*mtcredential.Credential, bool) {
	credential, ok := d.credentials[systemID]
	return credential, ok
}

func authOrDefault(value *bool) bool {
	return value == nil || *value
}
