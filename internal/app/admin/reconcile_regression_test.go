package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// TestDeleteGroupCascadesToItsUsers pins the oracle's group-removal semantics:
// perspective_group_remove (jasmin/routing/router.py) removes every user of the
// group before removing the group itself. Leaving members behind is not a
// cosmetic gap — a stored user whose group_id no longer resolves fails to
// install at the next boot, and until then a *disabled* group's members would
// authenticate again the moment the group went away.
func TestDeleteGroupCascadesToItsUsers(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	userProv := &recordingUserProvisioner{installed: map[string]string{}}
	groupProv := &fakeGroupProvisioner{installed: map[string]int64{}, users: userProv}
	groups, err := NewGroupService(store, groupProv, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	users, err := NewUserService(store, userProv, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}

	if err := groups.CreateGroup(ctx, "emea", `{"gid":"emea"}`); err != nil {
		t.Fatal(err)
	}
	for _, username := range []string{"alice", "bob"} {
		spec := `{"username":"` + username + `","group_id":"emea"}`
		if err := users.CreateUser(ctx, username, spec); err != nil {
			t.Fatal(err)
		}
	}
	// A user in a different group must survive the cascade.
	if err := groups.CreateGroup(ctx, "apac", `{"gid":"apac"}`); err != nil {
		t.Fatal(err)
	}
	if err := users.CreateUser(ctx, "carol", `{"username":"carol","group_id":"apac"}`); err != nil {
		t.Fatal(err)
	}

	if err := groups.DeleteGroup(ctx, "emea"); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	for _, username := range []string{"alice", "bob"} {
		if _, err := store.GetUser(ctx, username); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("stored user %q survived the cascade: err=%v", username, err)
		}
		if _, live := userProv.installed[username]; live {
			t.Fatalf("live user %q survived the cascade", username)
		}
	}
	if _, err := store.GetUser(ctx, "carol"); err != nil {
		t.Fatalf("user of an unrelated group was removed: %v", err)
	}
	if _, live := userProv.installed["carol"]; !live {
		t.Fatal("live user of an unrelated group was removed")
	}
}

// TestUserLoadAndApplyRepairsAfterFailedRemoval covers the reconcile that a
// rejected replacement spec leaves behind: CreateUser removes the live user
// before adding the replacement, so a failed add strands the store holding a
// user the directory no longer has. LoadAndApply must re-install it. Retaining
// the `applied` bookkeeping past a failed removal turned that into a permanent
// 401 that only a process restart cleared.
func TestUserLoadAndApplyRepairsAfterFailedRemoval(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	provisioner := &recordingUserProvisioner{installed: map[string]string{}}
	users, err := NewUserService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := users.CreateUser(ctx, "alice", `{"username":"alice"}`); err != nil {
		t.Fatal(err)
	}

	// Simulate the stranded state: the live user is gone, the store still has
	// it, and the service still believes it applied it.
	delete(provisioner.installed, "alice")
	provisioner.removeErr = errors.New("user not found")

	if err := users.LoadAndApply(ctx); err == nil {
		t.Fatal("expected the failed removal to be reported")
	}
	if _, live := provisioner.installed["alice"]; !live {
		t.Fatal("alice was not re-installed: a failed removal permanently blocked the re-add")
	}
}

// TestConnectorLoadAndApplyStopsBeforeRemoving pins that the reconcile stops a
// started connector first. smppc.Manager.Remove refuses a connector that is
// desired or not disconnected, so without the stop a started admin connector
// silently keeps its old config — or survives its own deletion — while stopped
// ones rebuild correctly, leaving the table half applied.
func TestConnectorLoadAndApplyStopsBeforeRemoving(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := &stopTrackingManager{
		configs: map[string]smppc.Config{},
		started: map[string]bool{},
	}
	service, err := NewService(store, manager, nil, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	config := smppc.Config{CID: "smsc1", Host: "a", Port: 1, SystemID: "u", Password: "p"}
	if err := service.CreateConnector(ctx, config, true); err != nil {
		t.Fatal(err)
	}

	if err := service.LoadAndApply(ctx); err != nil {
		t.Fatalf("reconcile of a started connector: %v", err)
	}
	if !manager.stopped["smsc1"] {
		t.Fatal("reconcile removed a started connector without stopping it first")
	}
	if _, live := manager.configs["smsc1"]; !live {
		t.Fatal("connector was not re-added after the reconcile")
	}
}

// recordingUserProvisioner is a UserProvisioner whose removal can be made to
// fail, so the reconcile's repair path is exercisable.
type recordingUserProvisioner struct {
	installed map[string]string
	removeErr error
}

func (p *recordingUserProvisioner) AddUser(username, specJSON string, _ int64) error {
	if _, exists := p.installed[username]; exists {
		return errors.New("duplicate user " + username)
	}
	p.installed[username] = specJSON
	return nil
}

func (p *recordingUserProvisioner) RemoveUser(username string) error {
	if p.removeErr != nil {
		return p.removeErr
	}
	delete(p.installed, username)
	return nil
}

func (p *recordingUserProvisioner) ConfigUserFloor() int64 { return 0 }

// stopTrackingManager models smppc.Manager's rule that a started connector
// cannot be removed, which is what makes the stop-before-remove ordering load
// bearing rather than defensive.
type stopTrackingManager struct {
	configs map[string]smppc.Config
	started map[string]bool
	stopped map[string]bool
}

func (m *stopTrackingManager) Add(cfg smppc.Config) error {
	if _, exists := m.configs[cfg.CID]; exists {
		return errors.New("duplicate connector " + cfg.CID)
	}
	m.configs[cfg.CID] = cfg
	return nil
}

func (m *stopTrackingManager) Update(cfg smppc.Config) error {
	m.configs[cfg.CID] = cfg
	return nil
}

func (m *stopTrackingManager) Remove(cid string) error {
	if m.started[cid] {
		return errors.New("connector must be stopped before removal")
	}
	delete(m.configs, cid)
	return nil
}

func (m *stopTrackingManager) Start(cid string) error {
	m.started[cid] = true
	return nil
}

func (m *stopTrackingManager) Stop(cid string) error {
	if m.stopped == nil {
		m.stopped = map[string]bool{}
	}
	m.stopped[cid] = true
	m.started[cid] = false
	return nil
}

func (m *stopTrackingManager) Status(cid string) (smppc.ManagedStatus, error) {
	if _, exists := m.configs[cid]; !exists {
		return smppc.ManagedStatus{}, errors.New("not found: " + cid)
	}
	return smppc.ManagedStatus{CID: cid, Desired: m.started[cid]}, nil
}

var _ = strings.TrimSpace
