package admin

import (
	"context"
	"errors"
	"testing"
)

// Deleting a customer used to remove their MT account and leave a matching SMPPs
// bind account behind. The consequence was not a traffic leak -- ResolveCredential
// fails for the missing MT user, so submits are answered ESME_RSYSERR -- but the
// offboarding was incomplete in a way that generates a support ticket: the
// credentials still authenticated a bind, the session held a max_bindings slot
// and appeared live in the console, and the customer saw a *server error* rather
// than "your account is closed".
type recordingSMPPsRemover struct {
	deleted []string
	err     error
}

func (r *recordingSMPPsRemover) DeleteUser(_ context.Context, systemID string) error {
	r.deleted = append(r.deleted, systemID)
	return r.err
}

func TestDeleteUserCascadesToTheSMPPsBindAccount(t *testing.T) {
	service, _ := newUserServiceForCascade(t)
	remover := &recordingSMPPsRemover{}
	service.SetSMPPsAccounts(remover)

	if err := service.DeleteUser(context.Background(), "customer-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(remover.deleted) != 1 || remover.deleted[0] != "customer-a" {
		t.Fatalf("SMPPs bind account not removed; deleted=%v", remover.deleted)
	}
}

// Most MT users have no bind account at all, so a missing one is the normal case
// and must not fail the delete.
func TestDeleteUserSucceedsWhenThereIsNoBindAccount(t *testing.T) {
	service, _ := newUserServiceForCascade(t)
	service.SetSMPPsAccounts(&recordingSMPPsRemover{err: ErrSMPPsUserNotFound})

	if err := service.DeleteUser(context.Background(), "customer-a"); err != nil {
		t.Fatalf("delete with no bind account should succeed, got %v", err)
	}
}

// A real failure removing the bind account must abort the delete rather than
// proceed and leave working bind credentials for a deleted account -- the exact
// state this cascade exists to prevent.
func TestDeleteUserAbortsWhenTheBindAccountCannotBeRemoved(t *testing.T) {
	service, store := newUserServiceForCascade(t)
	boom := errors.New("store unavailable")
	service.SetSMPPsAccounts(&recordingSMPPsRemover{err: boom})

	err := service.DeleteUser(context.Background(), "customer-a")
	if err == nil {
		t.Fatal("delete should fail when the bind account cannot be removed")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap the underlying failure", err)
	}
	// The MT user must still be there, so a retry converges.
	if _, getErr := store.GetUser(context.Background(), "customer-a"); getErr != nil {
		t.Errorf("MT user was removed despite the abort: %v", getErr)
	}
}

// A deployment with no SMPPs server leaves the cascade unset; delete must work.
func TestDeleteUserWithoutACascadeConfigured(t *testing.T) {
	service, _ := newUserServiceForCascade(t)
	if err := service.DeleteUser(context.Background(), "customer-a"); err != nil {
		t.Fatalf("delete without a cascade should succeed, got %v", err)
	}
}

// newUserServiceForCascade builds a service with one existing user, reusing the
// package's own harness so the setup stays in step with it.
func newUserServiceForCascade(t *testing.T) (*UserService, *Store) {
	t.Helper()
	service, _, store := newUserService(t, 0)
	if err := service.CreateUser(context.Background(), "customer-a", userSpec("customer-a")); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return service, store
}
