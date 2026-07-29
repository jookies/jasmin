package admin

import (
	"context"
	"testing"
)

func TestProfileScopeRestoresOnlySelectedEntityFamily(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	profiles, err := NewProfileService(store, func() string { return "now" })
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGroup(ctx, StoredGroup{GID: "g", Number: 1, SpecJSON: `{"gid":"g","disabled":false}`}, "now"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertUser(ctx, StoredUser{Username: "alice", UID: 1, SpecJSON: `{"username":"alice","external_id":"a","password_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`}, "now"); err != nil {
		t.Fatal(err)
	}
	if err := profiles.SaveScope(ctx, "users-only", "users"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGroup(ctx, StoredGroup{GID: "g", Number: 1, SpecJSON: `{"gid":"g","disabled":true}`}, "later"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertUser(ctx, StoredUser{Username: "alice", UID: 1, SpecJSON: `{"username":"changed"}`}, "later"); err != nil {
		t.Fatal(err)
	}
	if err := profiles.LoadScope(ctx, "users-only", "users"); err != nil {
		t.Fatal(err)
	}
	group, err := store.GetGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if group.SpecJSON != `{"gid":"g","disabled":true}` {
		t.Fatalf("unselected group was restored: %s", group.SpecJSON)
	}
	user, err := store.GetUser(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if user.SpecJSON == `{"username":"changed"}` {
		t.Fatalf("selected user was not restored: %s", user.SpecJSON)
	}
}
