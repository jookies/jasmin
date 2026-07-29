package admin

import (
	"context"
	"errors"
	"testing"
)

// fakeUserProvisioner records live-directory installs/removes and can reject.
type fakeUserProvisioner struct {
	installed  map[string]int64 // username -> uid currently live
	floor      int64
	addErr     error
	pruneCalls int
}

type quotaSafeUserProvisioner struct {
	*fakeUserProvisioner
	replaced   []string
	committed  int
	rolledBack int
}

type fakeLiveReplacement struct {
	commit   func()
	rollback func()
}

func (replacement fakeLiveReplacement) Commit() {
	if replacement.commit != nil {
		replacement.commit()
	}
}

func (replacement fakeLiveReplacement) Rollback() {
	if replacement.rollback != nil {
		replacement.rollback()
	}
}

func (p *quotaSafeUserProvisioner) BeginReplaceUser(username, _ string, uid int64) (LiveReplacement, error) {
	p.installed[username] = uid
	p.replaced = append(p.replaced, username)
	return fakeLiveReplacement{
		commit:   func() { p.committed++ },
		rollback: func() { p.rolledBack++ },
	}, nil
}

func newFakeUserProvisioner(floor int64) *fakeUserProvisioner {
	return &fakeUserProvisioner{installed: map[string]int64{}, floor: floor}
}

func (p *fakeUserProvisioner) AddUser(username, _ string, uid int64) error {
	if p.addErr != nil {
		return p.addErr
	}
	p.installed[username] = uid
	return nil
}
func (p *fakeUserProvisioner) RemoveUser(username string) error {
	delete(p.installed, username)
	return nil
}
func (p *fakeUserProvisioner) ConfigUserFloor() int64 { return p.floor }
func (p *fakeUserProvisioner) PruneDeletedQuotas(context.Context) (int64, error) {
	p.pruneCalls++
	return 1, nil
}

func newUserService(t *testing.T, floor int64) (*UserService, *fakeUserProvisioner, *Store) {
	t.Helper()
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	prov := newFakeUserProvisioner(floor)
	svc, err := NewUserService(store, prov, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	return svc, prov, store
}

func userSpec(username string) string {
	return `{"username":"` + username + `","external_id":"` + username + `","password_sha256":"` +
		"5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8" + `"}`
}

func TestUserServiceAssignsStableUIDsAboveFloor(t *testing.T) {
	svc, prov, store := newUserService(t, 3) // 3 config users → admin uids start at 4
	if err := svc.CreateUser(context.Background(), "alice", userSpec("alice")); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateUser(context.Background(), "bob", userSpec("bob")); err != nil {
		t.Fatal(err)
	}
	if prov.installed["alice"] != 4 || prov.installed["bob"] != 5 {
		t.Fatalf("uids = alice:%d bob:%d want 4,5", prov.installed["alice"], prov.installed["bob"])
	}
	stored, _ := store.GetUser(context.Background(), "alice")
	if stored.UID != 4 {
		t.Fatalf("persisted alice uid=%d want 4", stored.UID)
	}
}

func TestUserServiceReplaceKeepsUID(t *testing.T) {
	svc, prov, _ := newUserService(t, 0)
	if err := svc.CreateUser(context.Background(), "alice", userSpec("alice")); err != nil {
		t.Fatal(err)
	}
	firstUID := prov.installed["alice"]
	// Replace alice — uid must be preserved, and the old live user removed then
	// re-added (so the directory's duplicate guard doesn't reject it).
	if err := svc.CreateUser(context.Background(), "alice", userSpec("alice")); err != nil {
		t.Fatal(err)
	}
	if prov.installed["alice"] != firstUID {
		t.Fatalf("uid changed on replace: %d -> %d", firstUID, prov.installed["alice"])
	}
}

func TestUserServiceUsesQuotaSafeReplacement(t *testing.T) {
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provisioner := &quotaSafeUserProvisioner{fakeUserProvisioner: newFakeUserProvisioner(0)}
	service, err := NewUserService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CreateUser(context.Background(), "alice", userSpec("alice")); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateUser(context.Background(), "alice", userSpec("alice")); err != nil {
		t.Fatal(err)
	}
	if len(provisioner.replaced) != 1 || provisioner.replaced[0] != "alice" {
		t.Fatalf("replacement calls=%v want [alice]", provisioner.replaced)
	}
	if provisioner.committed != 1 || provisioner.rolledBack != 0 {
		t.Fatalf("replacement commit=%d rollback=%d want 1/0", provisioner.committed, provisioner.rolledBack)
	}
}

func TestUserServiceRollsBackLiveReplacementWhenPersistenceFails(t *testing.T) {
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provisioner := &quotaSafeUserProvisioner{fakeUserProvisioner: newFakeUserProvisioner(0)}
	service, err := NewUserService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	original := userSpec("alice")
	if err := service.CreateUser(context.Background(), "alice", original); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER fail_user_replace
		BEFORE UPDATE ON admin_users
		BEGIN
			SELECT RAISE(FAIL, 'forced user update failure');
		END`); err != nil {
		t.Fatal(err)
	}

	if err := service.CreateUser(context.Background(), "alice", `{"username":"alice","external_id":"alice","password_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`); err == nil {
		t.Fatal("replacement unexpectedly persisted")
	}
	if provisioner.committed != 0 || provisioner.rolledBack != 1 {
		t.Fatalf("replacement commit=%d rollback=%d want 0/1", provisioner.committed, provisioner.rolledBack)
	}
	stored, err := store.GetUser(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if stored.SpecJSON != original {
		t.Fatalf("stored spec changed after failed replacement: %s", stored.SpecJSON)
	}
	if _, live := provisioner.installed["alice"]; !live {
		t.Fatal("failed replacement removed the existing live user")
	}
}

func TestUserServiceRejectedNotPersisted(t *testing.T) {
	svc, prov, store := newUserService(t, 0)
	prov.addErr = errors.New("bad user spec")
	if err := svc.CreateUser(context.Background(), "alice", userSpec("alice")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v want ErrInvalidRequest", err)
	}
	if _, err := store.GetUser(context.Background(), "alice"); !errors.Is(err, ErrUserNotFound) {
		t.Fatal("rejected user was persisted")
	}
}

func TestUserServiceDeleteAndReload(t *testing.T) {
	svc, provisioner, store := newUserService(t, 0)
	if err := svc.CreateUser(context.Background(), "alice", userSpec("alice")); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateUser(context.Background(), "bob", userSpec("bob")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUser(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if provisioner.pruneCalls != 1 {
		t.Fatalf("deleted-quota prune calls=%d want=1", provisioner.pruneCalls)
	}
	if err := svc.DeleteUser(context.Background(), "alice"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("second delete err=%v want ErrUserNotFound", err)
	}

	// Restart replay into a fresh directory: only bob survives, with its uid.
	freshProv := newFakeUserProvisioner(0)
	reloaded, err := NewUserService(store, freshProv, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.LoadAndApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := freshProv.installed["bob"]; !ok || len(freshProv.installed) != 1 {
		t.Fatalf("reload installed=%v want only bob", freshProv.installed)
	}
}

func TestUserServiceLoadAndApplyReconcilesLiveUsers(t *testing.T) {
	svc, provisioner, store := newUserService(t, 0)
	if err := svc.CreateUser(context.Background(), "old", userSpec("old")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertUser(context.Background(), StoredUser{
		Username: "restored",
		UID:      9,
		SpecJSON: userSpec("restored"),
	}, "snapshot"); err != nil {
		t.Fatal(err)
	}

	if err := svc.LoadAndApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := provisioner.installed["old"]; ok {
		t.Fatal("user removed by the restored profile is still live")
	}
	if provisioner.installed["restored"] != 9 {
		t.Fatalf("restored user uid=%d want 9", provisioner.installed["restored"])
	}
}
