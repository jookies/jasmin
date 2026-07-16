package billing

import "context"

// UserRepository defines the persistence contract for users.
type UserRepository interface {
	Save(ctx context.Context, u *User) error
	Load(ctx context.Context, uid int64, groups map[int64]*Group) (*User, error)
	List(ctx context.Context, groups map[int64]*Group) ([]*User, error)
	Delete(ctx context.Context, uid int64) error
}

// GroupRepository defines the persistence contract for groups.
type GroupRepository interface {
	Save(ctx context.Context, g *Group) error
	Load(ctx context.Context, gid int64) (*Group, error)
	List(ctx context.Context) ([]*Group, error)
	Delete(ctx context.Context, gid int64) error
}
