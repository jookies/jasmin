package billing_test

import (
	"errors"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/billing"
)

func TestManagerIndexesUsersByUsernameAndID(t *testing.T) {
	manager := billing.NewManager()
	user := billing.NewUser(42)
	if err := manager.AddUser("alice", user); err != nil {
		t.Fatal(err)
	}
	byName, err := manager.GetUser("alice")
	if err != nil || byName != user {
		t.Fatalf("GetUser: user=%p error=%v", byName, err)
	}
	byID, err := manager.GetUserByID("42")
	if err != nil || byID != user {
		t.Fatalf("GetUserByID: user=%p error=%v", byID, err)
	}
}

func TestManagerUsernameReplacementRemovesStaleID(t *testing.T) {
	manager := billing.NewManager()
	if err := manager.AddUser("alice", billing.NewUser(1)); err != nil {
		t.Fatal(err)
	}
	replacement := billing.NewUser(2)
	if err := manager.AddUser("alice", replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetUserByID("1"); !errors.Is(err, billing.ErrUserNotFound) {
		t.Fatalf("stale ID error=%v", err)
	}
	got, err := manager.GetUserByID("2")
	if err != nil || got != replacement {
		t.Fatalf("replacement user=%p error=%v", got, err)
	}
}

func TestManagerSupportsOpaqueLegacyIDAndRejectsDuplicateOwner(t *testing.T) {
	manager := billing.NewManager()
	alice := billing.NewUser(1)
	if err := manager.AddUserWithID("alice", "alice_1", alice); err != nil {
		t.Fatal(err)
	}
	got, err := manager.GetUserByID("alice_1")
	if err != nil || got != alice {
		t.Fatalf("opaque user=%p error=%v", got, err)
	}
	if err := manager.AddUserWithID("bob", "alice_1", billing.NewUser(2)); !errors.Is(err, billing.ErrUserIDConflict) {
		t.Fatalf("duplicate ID error=%v", err)
	}
	got, err = manager.GetUserByID("alice_1")
	if err != nil || got != alice {
		t.Fatalf("duplicate registration changed owner: user=%p error=%v", got, err)
	}
}
