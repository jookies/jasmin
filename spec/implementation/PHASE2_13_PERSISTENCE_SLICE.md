# Phase 2.13 — Persistence foundations and SQLite repository for Users and Groups

Baseline: `2e9ead1a0118838d78076935f99478f654b03517`.

## Included
- P-002: Persistence of Groups (SQLite).
- P-003: Persistence of Users (SQLite), including credentials, balance/counts, and routing/interceptor references.
- P-010: Periodic persistence logic (timer-based save for quotas).
- J-003/J-004: Basic data structure compatibility for CLI list/show commands.

## Excluded
- JCLI interactive prompts and networking.
- Profile loading/import from legacy Pickle (P-008).
- Persistence of Routes (P-004, P-005) - deferred to Phase 2.14.
- Persistence of Interceptors (P-006, P-007) - deferred to Phase 2.14.

## Production API

### package `internal/core/persistence`
- `type Repository interface { ... }`
- `type UserRepository interface { Save(u *User) error; Load(uid string) (*User, error); List() ([]*User, error); ... }`
- `type GroupRepository interface { Save(g *Group) error; Load(gid string) (*Group, error); List() ([]*Group, error); ... }`

### package `internal/infra/storage`
- `type SQLiteUserRepository struct { ... }`
- `type SQLiteGroupRepository struct { ... }`

## Tasks
1. [x] Define repository interfaces in `internal/core/billing/repository.go` (or a more central place).
2. [x] Implement SQLite schema and repository for Groups.
3. [x] Implement SQLite schema and repository for Users.
4. [x] Implement `B-009`/`P-010` Persistence Timer in a background worker.
5. [x] Create fixtures for persistence round-trip (verified via `TestSQLitePersistence`).
6. [x] Verification and Ralph audit.

LoopKey: 355b03c048a1
