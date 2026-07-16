package storage

import (
	"context"
	"database/sql"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

type SQLiteBillingStore struct {
	db *sql.DB
}

func NewSQLiteBillingStore(db *sql.DB) *SQLiteBillingStore {
	return &SQLiteBillingStore{db: db}
}

func (s *SQLiteBillingStore) Init(ctx context.Context) error {
	// Enable WAL mode and Foreign Keys
	_, err := s.db.ExecContext(ctx, `
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
	`)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS groups (
			gid INTEGER PRIMARY KEY,
			balance REAL,
			submit_sm_count_quota INTEGER
		);
		CREATE TABLE IF NOT EXISTS users (
			uid INTEGER PRIMARY KEY,
			gid INTEGER,
			balance REAL,
			early_decrement_percent INTEGER,
			submit_sm_count_quota INTEGER,
			FOREIGN KEY(gid) REFERENCES groups(gid)
		);
	`)
	return err
}

type SQLiteUserRepository struct {
	store *SQLiteBillingStore
}

func NewSQLiteUserRepository(store *SQLiteBillingStore) *SQLiteUserRepository {
	return &SQLiteUserRepository{store: store}
}

func (r *SQLiteUserRepository) Save(ctx context.Context, u *billing.User) error {
	state := u.GetState()
	_, err := r.store.db.ExecContext(ctx, `
		INSERT INTO users (uid, gid, balance, early_decrement_percent, submit_sm_count_quota)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(uid) DO UPDATE SET
			gid = excluded.gid,
			balance = excluded.balance,
			early_decrement_percent = excluded.early_decrement_percent,
			submit_sm_count_quota = excluded.submit_sm_count_quota
	`, state.UID, state.GID, state.Balance, state.EarlyDecrementBalancePercent, state.SubmitSmCountQuota)
	return err
}

func (r *SQLiteUserRepository) Load(ctx context.Context, uid int64, groups map[int64]*billing.Group) (*billing.User, error) {
	var state billing.UserState
	err := r.store.db.QueryRowContext(ctx, `
		SELECT uid, gid, balance, early_decrement_percent, submit_sm_count_quota FROM users WHERE uid = ?
	`, uid).Scan(&state.UID, &state.GID, &state.Balance, &state.EarlyDecrementBalancePercent, &state.SubmitSmCountQuota)
	if err != nil {
		return nil, err
	}
	u := billing.NewUser(uid)
	u.LoadState(state)
	if state.GID != nil && groups != nil {
		if g, ok := groups[*state.GID]; ok {
			u.SetGroup(g)
		}
	}
	return u, nil
}

func (r *SQLiteUserRepository) List(ctx context.Context, groups map[int64]*billing.Group) ([]*billing.User, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT uid, gid, balance, early_decrement_percent, submit_sm_count_quota FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*billing.User
	for rows.Next() {
		var state billing.UserState
		if err := rows.Scan(&state.UID, &state.GID, &state.Balance, &state.EarlyDecrementBalancePercent, &state.SubmitSmCountQuota); err != nil {
			return nil, err
		}
		u := billing.NewUser(state.UID)
		u.LoadState(state)
		if state.GID != nil && groups != nil {
			if g, ok := groups[*state.GID]; ok {
				u.SetGroup(g)
			}
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return users, nil
}

func (r *SQLiteUserRepository) Delete(ctx context.Context, uid int64) error {
	_, err := r.store.db.ExecContext(ctx, `DELETE FROM users WHERE uid = ?`, uid)
	return err
}

type SQLiteGroupRepository struct {
	store *SQLiteBillingStore
}

func NewSQLiteGroupRepository(store *SQLiteBillingStore) *SQLiteGroupRepository {
	return &SQLiteGroupRepository{store: store}
}

func (r *SQLiteGroupRepository) Save(ctx context.Context, g *billing.Group) error {
	state := g.GetState()
	_, err := r.store.db.ExecContext(ctx, `
		INSERT INTO groups (gid, balance, submit_sm_count_quota)
		VALUES (?, ?, ?)
		ON CONFLICT(gid) DO UPDATE SET
			balance = excluded.balance,
			submit_sm_count_quota = excluded.submit_sm_count_quota
	`, state.GID, state.Balance, state.SubmitSmCountQuota)
	return err
}

func (r *SQLiteGroupRepository) Load(ctx context.Context, gid int64) (*billing.Group, error) {
	var state billing.GroupState
	err := r.store.db.QueryRowContext(ctx, `
		SELECT gid, balance, submit_sm_count_quota FROM groups WHERE gid = ?
	`, gid).Scan(&state.GID, &state.Balance, &state.SubmitSmCountQuota)
	if err != nil {
		return nil, err
	}
	g := billing.NewGroup(gid)
	g.LoadState(state)
	return g, nil
}

func (r *SQLiteGroupRepository) List(ctx context.Context) ([]*billing.Group, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT gid, balance, submit_sm_count_quota FROM groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*billing.Group
	for rows.Next() {
		var state billing.GroupState
		if err := rows.Scan(&state.GID, &state.Balance, &state.SubmitSmCountQuota); err != nil {
			return nil, err
		}
		g := billing.NewGroup(state.GID)
		g.LoadState(state)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (r *SQLiteGroupRepository) Delete(ctx context.Context, gid int64) error {
	_, err := r.store.db.ExecContext(ctx, `DELETE FROM groups WHERE gid = ?`, gid)
	return err
}
