package billing

import (
	"context"
	"testing"
	"time"
)

type mockUserRepo struct {
	saved chan *User
}

func (m *mockUserRepo) Save(ctx context.Context, u *User) error {
	select {
	case m.saved <- u:
	default:
	}
	return nil
}
func (m *mockUserRepo) Load(ctx context.Context, uid int64, groups map[int64]*Group) (*User, error) {
	return nil, nil
}
func (m *mockUserRepo) List(ctx context.Context, groups map[int64]*Group) ([]*User, error) {
	return nil, nil
}
func (m *mockUserRepo) Delete(ctx context.Context, uid int64) error { return nil }

type mockGroupRepo struct {
	saved chan *Group
}

func (m *mockGroupRepo) Save(ctx context.Context, g *Group) error {
	select {
	case m.saved <- g:
	default:
	}
	return nil
}
func (m *mockGroupRepo) Load(ctx context.Context, gid int64) (*Group, error) { return nil, nil }
func (m *mockGroupRepo) List(ctx context.Context) ([]*Group, error)          { return nil, nil }
func (m *mockGroupRepo) Delete(ctx context.Context, gid int64) error         { return nil }

func TestPersistWorker(t *testing.T) {
	uRepo := &mockUserRepo{saved: make(chan *User, 1)}
	gRepo := &mockGroupRepo{saved: make(chan *Group, 1)}

	u := NewUser(1)
	g := NewGroup(2)

	worker := NewPersistWorker(uRepo, gRepo, 10*time.Millisecond, func() []*User { return []*User{u} }, func() []*Group { return []*Group{g} })

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- worker.Start(ctx) }()

	select {
	case got := <-uRepo.saved:
		if got != u {
			t.Fatalf("saved user = %p, want %p", got, u)
		}
	case <-ctx.Done():
		t.Fatal("User not saved by worker")
	}
	select {
	case got := <-gRepo.saved:
		if got != g {
			t.Fatalf("saved group = %p, want %p", got, g)
		}
	case <-ctx.Done():
		t.Fatal("Group not saved by worker")
	}

	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("worker stopped with %v, want context.Canceled", err)
	}
}
