package admin

import (
	"context"
	"testing"
)

type fakeGroupProvisioner struct {
	installed    map[string]int64
	floor        int64
	removedUsers []string
	replaced     []string
	committed    int
	rolledBack   int
	pruneCalls   int
	// users mirrors the real wiring, where the group and user provisioners are
	// two faces of the same outbound runtime, so a cascade delete actually
	// reaches live users instead of only being recorded.
	users *recordingUserProvisioner
}

func (p *fakeGroupProvisioner) RemoveUser(username string) error {
	p.removedUsers = append(p.removedUsers, username)
	if p.users == nil {
		return nil
	}
	return p.users.RemoveUser(username)
}

func (p *fakeGroupProvisioner) AddGroup(gid, _ string, number int64) error {
	p.installed[gid] = number
	return nil
}

func (p *fakeGroupProvisioner) BeginReplaceGroup(gid, _ string, number int64) (LiveReplacement, error) {
	p.installed[gid] = number
	p.replaced = append(p.replaced, gid)
	return fakeLiveReplacement{
		commit:   func() { p.committed++ },
		rollback: func() { p.rolledBack++ },
	}, nil
}

func (p *fakeGroupProvisioner) RemoveGroup(gid string) error {
	delete(p.installed, gid)
	return nil
}

func (p *fakeGroupProvisioner) ConfigGroupFloor() int64 { return p.floor }
func (p *fakeGroupProvisioner) PruneDeletedQuotas(context.Context) (int64, error) {
	p.pruneCalls++
	return 1, nil
}

func TestGroupServiceLoadAndApplyReconcilesLiveGroups(t *testing.T) {
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	provisioner := &fakeGroupProvisioner{installed: map[string]int64{}}
	service, err := NewGroupService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CreateGroup(context.Background(), "old", `{"gid":"old"}`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteGroup(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGroup(context.Background(), StoredGroup{
		GID:      "restored",
		Number:   7,
		SpecJSON: `{"gid":"restored"}`,
	}, "snapshot"); err != nil {
		t.Fatal(err)
	}

	if err := service.LoadAndApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := provisioner.installed["old"]; ok {
		t.Fatal("group removed by the restored profile is still live")
	}
	if provisioner.installed["restored"] != 7 {
		t.Fatalf("restored group number=%d want 7", provisioner.installed["restored"])
	}
}

func TestGroupServiceUsesQuotaSafeReplacement(t *testing.T) {
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provisioner := &fakeGroupProvisioner{installed: map[string]int64{}}
	service, err := NewGroupService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CreateGroup(context.Background(), "premium", `{"gid":"premium","balance":1000}`); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateGroup(context.Background(), "premium", `{"gid":"premium","balance":2000}`); err != nil {
		t.Fatal(err)
	}
	if len(provisioner.replaced) != 1 || provisioner.replaced[0] != "premium" {
		t.Fatalf("replacement calls=%v want [premium]", provisioner.replaced)
	}
	if provisioner.committed != 1 || provisioner.rolledBack != 0 {
		t.Fatalf("replacement commit=%d rollback=%d want 1/0", provisioner.committed, provisioner.rolledBack)
	}
}

func TestGroupServiceRollsBackLiveReplacementWhenPersistenceFails(t *testing.T) {
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provisioner := &fakeGroupProvisioner{installed: map[string]int64{}}
	service, err := NewGroupService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	original := `{"gid":"premium","balance":1000}`
	if err := service.CreateGroup(context.Background(), "premium", original); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER fail_group_replace
		BEFORE UPDATE ON admin_groups
		BEGIN
			SELECT RAISE(FAIL, 'forced group update failure');
		END`); err != nil {
		t.Fatal(err)
	}

	if err := service.CreateGroup(context.Background(), "premium", `{"gid":"premium","balance":2000}`); err == nil {
		t.Fatal("replacement unexpectedly persisted")
	}
	if provisioner.committed != 0 || provisioner.rolledBack != 1 {
		t.Fatalf("replacement commit=%d rollback=%d want 0/1", provisioner.committed, provisioner.rolledBack)
	}
	stored, err := store.GetGroup(context.Background(), "premium")
	if err != nil {
		t.Fatal(err)
	}
	if stored.SpecJSON != original {
		t.Fatalf("stored spec changed after failed replacement: %s", stored.SpecJSON)
	}
	if _, live := provisioner.installed["premium"]; !live {
		t.Fatal("failed replacement removed the existing live group")
	}
}
