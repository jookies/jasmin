package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// TestAuthenticationSurvivesASlowAdminWrite is the regression for a
// process-wide authentication outage caused by one admin edit.
//
// beginReplaceUser held the directory write lock and the caller kept the
// replacement open across its durable admin-store write. Every submit
// authentication and every SMPPs bind takes that lock to read a password hash,
// so a slow or degraded admin store stopped the gateway authenticating anything
// at all — for as long as the write took, with no bound.
//
// The edit must remain invisible until Commit, and authentication must keep
// answering throughout.
func TestAuthenticationSurvivesASlowAdminWrite(t *testing.T) {
	directory, err := newRuntimeDirectory(Config{Users: []UserConfig{{
		Username:       "alice",
		ExternalID:     "alice-id",
		PasswordSHA256: sha256Hex("original"),
	}}})
	if err != nil {
		t.Fatal(err)
	}

	replacement, err := directory.beginReplaceUser(UserConfig{
		Username:       "alice",
		ExternalID:     "alice-id",
		PasswordSHA256: sha256Hex("rotated"),
	}, 1)
	if err != nil {
		t.Fatalf("begin replace: %v", err)
	}

	// This stands in for the durable write the caller performs while holding the
	// replacement open. Authentication must not be waiting on it.
	authenticated := make(chan error, 1)
	go func() {
		authenticated <- directory.Authenticate(context.Background(), "alice", "original")
	}()
	select {
	case err := <-authenticated:
		if err != nil {
			t.Fatalf("authentication during an open admin edit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authentication blocked behind an open admin edit: a slow admin store stops the whole gateway authenticating")
	}

	// Until Commit the edit is not visible: a reader must never see a change the
	// durable store has not accepted.
	if err := directory.Authenticate(context.Background(), "alice", "rotated"); err == nil {
		t.Error("the new password worked before the durable write committed")
	}

	replacement.Commit()
	if err := directory.Authenticate(context.Background(), "alice", "rotated"); err != nil {
		t.Errorf("the new password does not work after commit: %v", err)
	}
	if err := directory.Authenticate(context.Background(), "alice", "original"); err == nil {
		t.Error("the old password still works after commit")
	}
}

// A rolled-back edit must leave the directory exactly as it was, and must not
// leave the lock held.
func TestRolledBackAdminEditLeavesTheDirectoryUntouched(t *testing.T) {
	directory, err := newRuntimeDirectory(Config{Users: []UserConfig{{
		Username:       "bob",
		ExternalID:     "bob-id",
		PasswordSHA256: sha256Hex("original"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := directory.beginReplaceUser(UserConfig{
		Username:       "bob",
		ExternalID:     "bob-id",
		PasswordSHA256: sha256Hex("rotated"),
	}, 1)
	if err != nil {
		t.Fatalf("begin replace: %v", err)
	}
	replacement.Rollback()

	if err := directory.Authenticate(context.Background(), "bob", "original"); err != nil {
		t.Errorf("the original password stopped working after a rollback: %v", err)
	}
	if err := directory.Authenticate(context.Background(), "bob", "rotated"); err == nil {
		t.Error("a rolled-back password was installed anyway")
	}
	// A second edit proves the lock was released.
	done := make(chan struct{})
	go func() {
		defer close(done)
		next, beginErr := directory.beginReplaceUser(UserConfig{
			Username:       "bob",
			ExternalID:     "bob-id",
			PasswordSHA256: sha256Hex("third"),
		}, 0)
		if beginErr == nil {
			next.Commit()
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a rollback left the directory lock held")
	}
}
