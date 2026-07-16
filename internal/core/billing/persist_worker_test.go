package billing

import (
	"context"
	"testing"
	"time"
)

type mockUserRepo struct {
	saved []*User
}
func (m *mockUserRepo) Save(ctx context.Context, u *User) error { m.saved = append(m.saved, u); return nil }
func (m *mockUserRepo) Load(ctx context.Context, uid int64, groups map[int64]*Group) (*User, error) { return nil, nil }
func (m *mockUserRepo) List(ctx context.Context, groups map[int64]*Group) ([]*User, error) { return nil, nil }
func (m *mockUserRepo) Delete(ctx context.Context, uid int64) error { return nil }

type mockGroupRepo struct {
	saved []*Group
}
func (m *mockGroupRepo) Save(ctx context.Context, g *Group) error { m.saved = append(m.saved, g); return nil }
func (m *mockGroupRepo) Load(ctx context.Context, gid int64) (*Group, error) { return nil, nil }
func (m *mockGroupRepo) List(ctx context.Context) ([]*Group, error) { return nil, nil }
func (m *mockGroupRepo) Delete(ctx context.Context, gid int64) error { return nil }

func TestPersistWorker(t *testing.T) {
	uRepo := &mockUserRepo{}
	gRepo := &mockGroupRepo{}
	
	u := NewUser(1)
	g := NewGroup(2)
	
	worker := NewPersistWorker(uRepo, gRepo, 10*time.Millisecond, func() []*User { return []*User{u} }, func() []*Group { return []*Group{g} })
	
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	
	go worker.Start(ctx)
	
	time.Sleep(30 * time.Millisecond)
	
	if len(uRepo.saved) == 0 {
		t.Error("User not saved by worker")
	}
	if len(gRepo.saved) == 0 {
		t.Error("Group not saved by worker")
	}
}
