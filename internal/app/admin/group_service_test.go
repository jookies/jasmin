package admin

import (
	"context"
	"testing"
)

type fakeGroupProvisioner struct {
	installed    map[string]int64
	floor        int64
	removedUsers []string
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

func (p *fakeGroupProvisioner) RemoveGroup(gid string) error {
	delete(p.installed, gid)
	return nil
}

func (p *fakeGroupProvisioner) ConfigGroupFloor() int64 { return p.floor }

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
