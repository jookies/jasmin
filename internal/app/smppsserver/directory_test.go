package smppsserver

import (
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/mtcredential"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
)

func TestDirectoryProjectsBindAuthAndCredential(t *testing.T) {
	max := 3
	no := false
	directory, err := NewDirectory([]UserConfig{
		{SystemID: "alice", Password: "secret", MaxBindings: &max, SetSourceAddress: &no},
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, ok := directory.ResolveUser("alice")
	if !ok {
		t.Fatal("alice not resolved")
	}
	// Bind auth: md5(password), enabled, authorized, default whitelist, max_bindings.
	if auth.PasswordDigest != smpps.PasswordHash("secret") {
		t.Fatal("password digest is not md5(password)")
	}
	if !auth.UserEnabled || !auth.GroupEnabled || !auth.BindAuthorized {
		t.Fatalf("auth flags = %+v", auth)
	}
	if auth.IPWhitelist != smpps.DefaultIPWhitelist {
		t.Fatalf("ip whitelist = %q, want default", auth.IPWhitelist)
	}
	if auth.MaxBindings == nil || *auth.MaxBindings != 3 {
		t.Fatalf("max bindings = %v", auth.MaxBindings)
	}
	// Credential: default-open, with set_source_address turned off.
	credential, ok := directory.ResolveCredential("alice")
	if !ok {
		t.Fatal("alice credential not resolved")
	}
	if !credential.Authorization(mtcredential.AuthSMPPSSend) {
		t.Fatal("smpps_send should default true")
	}
	if credential.Authorization(mtcredential.AuthSetSourceAddress) {
		t.Fatal("set_source_address should be false")
	}
}

func TestDirectoryDisabledAndUnauthorized(t *testing.T) {
	no := false
	directory, err := NewDirectory([]UserConfig{
		{SystemID: "u", Password: "p", Disabled: true, SMPPSSend: &no},
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := directory.ResolveUser("u")
	if auth.UserEnabled {
		t.Fatal("disabled user must not be enabled")
	}
	if auth.BindAuthorized {
		t.Fatal("smpps_send=false must clear BindAuthorized")
	}
	credential, _ := directory.ResolveCredential("u")
	if credential.Authorization(mtcredential.AuthSMPPSSend) {
		t.Fatal("smpps_send should be false on the credential too")
	}
}

func TestDirectoryValidation(t *testing.T) {
	negative := -1
	cases := map[string][]UserConfig{
		"empty system_id": {{SystemID: "", Password: "p"}},
		"duplicate":       {{SystemID: "u", Password: "p"}, {SystemID: "u", Password: "q"}},
		"bad whitelist":   {{SystemID: "u", Password: "p", IPWhitelist: "not-a-cidr"}},
		"negative max":    {{SystemID: "u", Password: "p", MaxBindings: &negative}},
		// A filter that does not compile must be a config error, not a silent
		// no-op: an operator who believes a fence is active while it is not is
		// exactly the exposure the filters exist to prevent.
		"bad destination filter": {{SystemID: "u", Password: "p", FilterDestinationAddress: "("}},
		// Python's re accepts lookahead; Go's RE2 does not. Reject loudly at
		// provisioning instead of degrading at match time.
		"python-only lookahead filter": {{SystemID: "u", Password: "p", FilterSourceAddress: "(?!spam)"}},
	}
	for name, users := range cases {
		if _, err := NewDirectory(users); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func boolPtr(value bool) *bool    { return &value }
func strPtr(value string) *string { return &value }

// TestDirectoryCredentialValueFilters proves the directory now carries REAL
// value filters into the credential ValidateSubmit consumes — previously it
// synthesized a permissive credential and every fence was a no-op over SMPPs
// (the destination fence held over HTTP but not over an SMPP bind).
func TestDirectoryCredentialValueFilters(t *testing.T) {
	cases := []struct {
		name    string
		user    UserConfig
		submit  mtcredential.SubmitRequest
		wantKey string // "" = accepted; else the refusing authorization/filter key
	}{
		{
			name:    "destination inside the fence is accepted",
			user:    UserConfig{SystemID: "u", Password: "p", FilterDestinationAddress: "^2547"},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("254722000111"), SourceAddr: []byte("INFO"), ShortMessage: []byte("hi")},
			wantKey: "",
		},
		{
			name:    "destination outside the fence is refused",
			user:    UserConfig{SystemID: "u", Password: "p", FilterDestinationAddress: "^2547"},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("38976123456"), SourceAddr: []byte("INFO"), ShortMessage: []byte("hi")},
			wantKey: mtcredential.FilterDestinationAddress,
		},
		{
			// re.match semantics: anchored at position 0 even without "^", so a
			// prefix fence cannot be dodged by prepending digits.
			name:    "fence matches from position 0 only",
			user:    UserConfig{SystemID: "u", Password: "p", FilterDestinationAddress: "2547"},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("12547000000"), ShortMessage: []byte("hi")},
			wantKey: mtcredential.FilterDestinationAddress,
		},
		{
			name:    "source outside the sender-ID whitelist is refused",
			user:    UserConfig{SystemID: "u", Password: "p", FilterSourceAddress: "^(INFO|ALERT)$"},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("2547"), SourceAddr: []byte("SPAM"), ShortMessage: []byte("hi")},
			wantKey: mtcredential.FilterSourceAddress,
		},
		{
			name:    "content outside the filter is refused",
			user:    UserConfig{SystemID: "u", Password: "p", FilterContent: "^[A-Z ]+$"},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("2547"), ShortMessage: []byte("lowercase")},
			wantKey: mtcredential.FilterContent,
		},
		{
			name:    "priority outside the filter is refused",
			user:    UserConfig{SystemID: "u", Password: "p", FilterPriority: "^0$"},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("2547"), PriorityFlag: 2, ShortMessage: []byte("hi")},
			wantKey: mtcredential.FilterPriority,
		},
		{
			name:    "smpps_send=false refuses every submit",
			user:    UserConfig{SystemID: "u", Password: "p", SMPPSSend: boolPtr(false)},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("2547"), ShortMessage: []byte("hi")},
			wantKey: mtcredential.AuthSMPPSSend,
		},
		{
			// Backward compatibility: a stored user with none of the credential
			// fields keeps the permissive legacy default — anywhere goes.
			name:    "no credential fields set: any destination accepted",
			user:    UserConfig{SystemID: "u", Password: "p"},
			submit:  mtcredential.SubmitRequest{DestinationAddr: []byte("999999999"), SourceAddr: []byte("ANY"), PriorityFlag: 3, ShortMessage: []byte("whatever")},
			wantKey: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			directory, err := NewDirectory([]UserConfig{tc.user})
			if err != nil {
				t.Fatal(err)
			}
			credential, ok := directory.ResolveCredential(tc.user.SystemID)
			if !ok {
				t.Fatal("credential not resolved")
			}
			err = mtcredential.ValidateSubmit(credential, tc.submit)
			if tc.wantKey == "" {
				if err != nil {
					t.Fatalf("ValidateSubmit = %v, want accepted", err)
				}
				return
			}
			var validation *mtcredential.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("ValidateSubmit = %v, want *ValidationError", err)
			}
			if validation.Key != tc.wantKey {
				t.Fatalf("refused on %q, want %q", validation.Key, tc.wantKey)
			}
		})
	}
}

// TestDirectoryBindAuthorizationDistinctFromSMPPSSend covers the legacy split
// between SmppsCredential 'bind' (session) and MtMessagingCredential
// 'smpps_send' (per-submit), plus the compatibility fallback: an account
// stored before the split, where smpps_send carried both meanings, keeps
// failing at bind time.
func TestDirectoryBindAuthorizationDistinctFromSMPPSSend(t *testing.T) {
	cases := []struct {
		name         string
		bind         *bool
		smppsSend    *bool
		wantBindAuth bool
		wantSubmitOK bool
	}{
		{"both omitted: permissive", nil, nil, true, true},
		{"legacy conflation: smpps_send=false still blocks the bind", nil, boolPtr(false), false, false},
		{"bind=false blocks the session but not the credential", boolPtr(false), nil, false, true},
		{"receiver-style: bind=true smpps_send=false binds but cannot submit", boolPtr(true), boolPtr(false), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			directory, err := NewDirectory([]UserConfig{
				{SystemID: "u", Password: "p", Bind: tc.bind, SMPPSSend: tc.smppsSend},
			})
			if err != nil {
				t.Fatal(err)
			}
			auth, _ := directory.ResolveUser("u")
			if auth.BindAuthorized != tc.wantBindAuth {
				t.Fatalf("BindAuthorized = %v, want %v", auth.BindAuthorized, tc.wantBindAuth)
			}
			credential, _ := directory.ResolveCredential("u")
			if got := credential.Authorization(mtcredential.AuthSMPPSSend); got != tc.wantSubmitOK {
				t.Fatalf("smpps_send = %v, want %v", got, tc.wantSubmitOK)
			}
		})
	}
}

func TestDirectoryGroupDisabled(t *testing.T) {
	directory, err := NewDirectory([]UserConfig{
		{SystemID: "grouped", Password: "p", GroupDisabled: true},
		{SystemID: "plain", Password: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth, _ := directory.ResolveUser("grouped"); auth.GroupEnabled {
		t.Fatal("group_disabled=true must clear GroupEnabled")
	}
	if auth, _ := directory.ResolveUser("plain"); !auth.GroupEnabled {
		t.Fatal("omitted group_disabled must keep GroupEnabled (compat default)")
	}
}

// TestDirectoryDefaultSourceAddress checks the credential carries the user
// default with Python's None/"" distinction intact: nil pointer is unset,
// explicit "" is a set-but-empty default.
func TestDirectoryDefaultSourceAddress(t *testing.T) {
	cases := []struct {
		name      string
		value     *string
		wantSet   bool
		wantValue string
	}{
		{"omitted: unset (legacy None)", nil, false, ""},
		{"set: substituted for an empty source", strPtr("SENDERID"), true, "SENDERID"},
		{"explicit empty: set but empty, not unset", strPtr(""), true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			directory, err := NewDirectory([]UserConfig{
				{SystemID: "u", Password: "p", DefaultSourceAddress: tc.value},
			})
			if err != nil {
				t.Fatal(err)
			}
			credential, _ := directory.ResolveCredential("u")
			value, set := credential.DefaultSourceAddress()
			if set != tc.wantSet {
				t.Fatalf("default source set = %v, want %v", set, tc.wantSet)
			}
			if set && string(value) != tc.wantValue {
				t.Fatalf("default source = %q, want %q", value, tc.wantValue)
			}
			// The smpps substitution flavour: an empty source_addr picks up the
			// default when one is set, and stays empty when it is not.
			applied := credential.ApplyDefaultSourceAddressSubmit(nil)
			if tc.wantSet && string(applied) != tc.wantValue {
				t.Fatalf("applied = %q, want %q", applied, tc.wantValue)
			}
			if !tc.wantSet && applied != nil {
				t.Fatalf("applied = %q, want untouched nil", applied)
			}
		})
	}
}

func TestDirectoryUnknownSystemID(t *testing.T) {
	directory, _ := NewDirectory([]UserConfig{{SystemID: "u", Password: "p"}})
	if _, ok := directory.ResolveUser("ghost"); ok {
		t.Fatal("unknown system_id must not resolve")
	}
	if _, ok := directory.ResolveCredential("ghost"); ok {
		t.Fatal("unknown credential must not resolve")
	}
}
