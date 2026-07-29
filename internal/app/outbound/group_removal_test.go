package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestRemovedGroupDoesNotReEnableItsUsers guards the suspension control against
// the most tempting way to defeat it: deleting the group.
//
// removeGroup drops the group from groups/groupIDs/groupDisabled but cannot
// reach userGroup, so a member still resolves a gid that no longer has an
// entry. Reading the zero value there means "not disabled", which is exactly
// backwards — an operator who suspends a group and later tidies it away would
// silently hand every member their access back. Authentication therefore fails
// closed on a dangling reference; the oracle never reaches this state at all,
// because perspective_group_remove cascades to the users.
func TestRemovedGroupDoesNotReEnableItsUsers(t *testing.T) {
	digest := sha256.Sum256([]byte("secret"))
	hexDigest := hex.EncodeToString(digest[:])

	directory, err := newRuntimeDirectory(Config{
		Groups: []GroupConfig{{GID: "suspended", Disabled: true}},
		Users: []UserConfig{{
			Username:       "alice",
			ExternalID:     "alice-id",
			PasswordSHA256: hexDigest,
			GroupID:        "suspended",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := directory.Authenticate(context.Background(), "alice", "secret"); err == nil {
		t.Fatal("a member of a disabled group authenticated")
	}

	if err := directory.removeGroup("suspended"); err != nil {
		t.Fatalf("remove group: %v", err)
	}

	if err := directory.Authenticate(context.Background(), "alice", "secret"); err == nil {
		t.Fatal("deleting the group re-enabled its suspended member")
	}
}
