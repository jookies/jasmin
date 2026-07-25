package smppsserver

import (
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
	}
	for name, users := range cases {
		if _, err := NewDirectory(users); err == nil {
			t.Errorf("%s: expected error", name)
		}
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
