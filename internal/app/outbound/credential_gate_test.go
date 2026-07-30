package outbound

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/mtcredential"
)

// TestFrontDoorEnforcesUserCredentials covers the gap that made this gate
// necessary: the HTTP front door checked the password and nothing else, so a
// user with http_send revoked, a destination value filter, or a default source
// address had all three reported as set and none of them applied.
func TestFrontDoorEnforcesUserCredentials(t *testing.T) {
	digest := sha256.Sum256([]byte("secret"))
	hexDigest := hex.EncodeToString(digest[:])
	no, yes := false, true
	defaultSource := "1234"

	directory, err := newRuntimeDirectory(Config{Users: []UserConfig{
		{
			Username: "denied", ExternalID: "denied", PasswordSHA256: hexDigest,
			MTCredential: &MTCredentialConfig{HTTPSend: &no},
		},
		{
			Username: "filtered", ExternalID: "filtered", PasswordSHA256: hexDigest,
			MTCredential: &MTCredentialConfig{FilterDestinationAddress: "^44"},
		},
		{
			Username: "defaulted", ExternalID: "defaulted", PasswordSHA256: hexDigest,
			MTCredential: &MTCredentialConfig{DefaultSourceAddress: &defaultSource},
		},
		{
			Username: "priority", ExternalID: "priority", PasswordSHA256: hexDigest,
			MTCredential: &MTCredentialConfig{SetPriority: &no, HTTPSend: &yes},
		},
		{Username: "plain", ExternalID: "plain", PasswordSHA256: hexDigest},
	}})
	if err != nil {
		t.Fatal(err)
	}

	credentialFor := func(username string) *mtcredential.Credential {
		credential, ok := directory.ResolveCredential(username)
		if !ok {
			t.Fatalf("no credential for %q", username)
		}
		return credential
	}

	t.Run("http_send revoked refuses the send", func(t *testing.T) {
		err := mtcredential.ValidateSend(credentialFor("denied"), mtcredential.SendRequest{
			Destination: []byte("447700900000"),
		})
		var validation *mtcredential.ValidationError
		if err == nil {
			t.Fatal("a user with http_send revoked was allowed to send")
		}
		if !asValidation(err, &validation) || validation.Key != mtcredential.AuthHTTPSend {
			t.Fatalf("err = %v, want an http_send authorization failure", err)
		}
	})

	t.Run("destination value filter applies", func(t *testing.T) {
		credential := credentialFor("filtered")
		if err := mtcredential.ValidateSend(credential, mtcredential.SendRequest{
			Destination: []byte("447700900000"),
		}); err != nil {
			t.Fatalf("a matching destination was refused: %v", err)
		}
		if err := mtcredential.ValidateSend(credential, mtcredential.SendRequest{
			Destination: []byte("33123456789"),
		}); err == nil {
			t.Fatal("a destination outside the user's filter was accepted")
		}
	})

	t.Run("default source address is provisioned", func(t *testing.T) {
		source, ok := credentialFor("defaulted").DefaultSourceAddress()
		if !ok || string(source) != "1234" {
			t.Fatalf("default source = %q/%v, want 1234", source, ok)
		}
	})

	t.Run("set_priority revoked refuses a priority", func(t *testing.T) {
		credential := credentialFor("priority")
		if err := mtcredential.ValidateSend(credential, mtcredential.SendRequest{
			Destination: []byte("447700900000"),
		}); err != nil {
			t.Fatalf("a request without a priority was refused: %v", err)
		}
		if err := mtcredential.ValidateSend(credential, mtcredential.SendRequest{
			Destination: []byte("447700900000"), Priority: []byte("2"), HasPriority: true,
		}); err == nil {
			t.Fatal("a user without set_priority was allowed to set one")
		}
	})

	t.Run("a user with no credential block keeps the permissive default", func(t *testing.T) {
		credential := credentialFor("plain")
		if err := mtcredential.ValidateSend(credential, mtcredential.SendRequest{
			Destination: []byte("33123456789"), Priority: []byte("3"), HasPriority: true,
		}); err != nil {
			t.Fatalf("an unprovisioned user was refused: %v", err)
		}
	})
}

func asValidation(err error, target **mtcredential.ValidationError) bool {
	validation, ok := err.(*mtcredential.ValidationError)
	if ok {
		*target = validation
	}
	return ok
}
