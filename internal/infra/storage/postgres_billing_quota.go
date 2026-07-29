package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pumpitspace/jasmin/internal/core/billing"
)

//go:embed migrations/0002_billing_quota.sql
var billingQuotaMigrations embed.FS

// PostgresQuotaStore is the durable home of prepaid balances and
// submit_sm_count quotas.
//
// PostgreSQL, not the node-local admin SQLite of ADR-001: that ADR keeps
// SQLite for *provisioning* state precisely because provisioning is node-local
// operational state. Spent balance is the mirror image — it is consumption
// state in the same class as the submit outbox, and two gateway nodes each
// holding their own copy would each grant the customer the full balance, which
// is the bug this store exists to close. The outbound runtime already requires
// this database, so nothing new has to be deployed.
type PostgresQuotaStore struct {
	db    *sql.DB
	owned bool
	now   func() time.Time
}

// NewPostgresQuotaStore wraps a database handle owned by the caller.
func NewPostgresQuotaStore(db *sql.DB) (*PostgresQuotaStore, error) {
	if db == nil {
		return nil, errors.New("nil PostgreSQL database")
	}
	return &PostgresQuotaStore{db: db, now: time.Now}, nil
}

// OpenPostgresQuotaStore opens a small dedicated pool for the quota lane. It is
// deliberately narrow: the whole workload is one batched UPSERT per flush
// interval plus a single full read at boot, so it must not be able to starve
// the submit path of connections.
func OpenPostgresQuotaStore(ctx context.Context, dsn string) (*PostgresQuotaStore, error) {
	if dsn == "" {
		return nil, errors.New("empty PostgreSQL DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect PostgreSQL billing quota store: %w", err)
	}
	return &PostgresQuotaStore{db: db, owned: true, now: time.Now}, nil
}

// Close releases the pool when this store opened it. A caller-supplied handle
// is left alone.
func (store *PostgresQuotaStore) Close() error {
	if store == nil || !store.owned {
		return nil
	}
	return store.db.Close()
}

// Migrate applies the embedded schema whole, following the submit-transaction
// store's pattern: one file, every statement CREATE TABLE IF NOT EXISTS, no
// migration framework and no version table.
func (store *PostgresQuotaStore) Migrate(ctx context.Context) error {
	migration, err := billingQuotaMigrations.ReadFile("migrations/0002_billing_quota.sql")
	if err != nil {
		return err
	}
	if _, err = store.db.ExecContext(ctx, string(migration)); err != nil {
		return fmt.Errorf("migrate billing quota store: %w", err)
	}
	return nil
}

// SaveQuotas writes the batch in one transaction. Atomicity is the point: a
// charge decrements a user and its group together, so a crash between the two
// rows would leave the group ceiling holding money the user has already spent.
func (store *PostgresQuotaStore) SaveQuotas(ctx context.Context, records []billing.QuotaRecord) error {
	if len(records) == 0 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin billing quota write: %w", err)
	}
	defer transaction.Rollback()
	updatedAt := store.now().UTC()
	for _, record := range records {
		if record.Key == "" {
			return fmt.Errorf("billing quota record with empty %s key", record.Scope)
		}
		if _, err = transaction.ExecContext(ctx, `INSERT INTO billing_quotas
 (scope,principal_key,balance,submit_sm_count,provisioned_balance,provisioned_submit_sm_count,updated_at)
 VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(scope,principal_key) DO UPDATE SET
  balance=excluded.balance,
  submit_sm_count=excluded.submit_sm_count,
  provisioned_balance=excluded.provisioned_balance,
  provisioned_submit_sm_count=excluded.provisioned_submit_sm_count,
  updated_at=excluded.updated_at`,
			string(record.Scope), record.Key,
			nullFloat(record.Live.Balance), nullInt(record.Live.SubmitSmCount),
			nullFloat(record.Provisioned.Balance), nullInt(record.Provisioned.SubmitSmCount),
			updatedAt); err != nil {
			return fmt.Errorf("write %s quota %q: %w", record.Scope, record.Key, err)
		}
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit billing quota write: %w", err)
	}
	return nil
}

// LoadQuotas reads every row. The set is bounded by the number of provisioned
// accounts and is read once at boot, so it is deliberately not paginated.
func (store *PostgresQuotaStore) LoadQuotas(ctx context.Context) ([]billing.QuotaRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT scope,principal_key,balance,submit_sm_count,provisioned_balance,provisioned_submit_sm_count
 FROM billing_quotas ORDER BY scope,principal_key`)
	if err != nil {
		return nil, fmt.Errorf("read billing quotas: %w", err)
	}
	defer rows.Close()
	var records []billing.QuotaRecord
	for rows.Next() {
		var (
			scope                string
			key                  string
			balance              sql.NullFloat64
			count                sql.NullInt64
			provisionedBalance   sql.NullFloat64
			provisionedSubmitSMs sql.NullInt64
		)
		if err = rows.Scan(&scope, &key, &balance, &count, &provisionedBalance, &provisionedSubmitSMs); err != nil {
			return nil, fmt.Errorf("scan billing quota: %w", err)
		}
		records = append(records, billing.QuotaRecord{
			Scope:       billing.QuotaScope(scope),
			Key:         key,
			Live:        billing.Quota{Balance: optionalFloat(balance), SubmitSmCount: optionalInt(count)},
			Provisioned: billing.Quota{Balance: optionalFloat(provisionedBalance), SubmitSmCount: optionalInt(provisionedSubmitSMs)},
		})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read billing quotas: %w", err)
	}
	return records, nil
}

// PruneQuotas deletes rows whose user/group principal is absent from the fully
// replayed live directory. It must run only after config and persisted admin
// groups/users have been installed; running it earlier would mistake an admin
// principal that has not replayed yet for a deleted account.
func (store *PostgresQuotaStore) PruneQuotas(ctx context.Context, active []billing.QuotaKey) (int64, error) {
	users := make([]string, 0, len(active))
	groups := make([]string, 0, len(active))
	seen := make(map[string]struct{}, len(active))
	for _, principal := range active {
		if principal.Key == "" {
			return 0, fmt.Errorf("active billing quota principal has empty %s key", principal.Scope)
		}
		dedupeKey := string(principal.Scope) + "\x00" + principal.Key
		if _, duplicate := seen[dedupeKey]; duplicate {
			continue
		}
		seen[dedupeKey] = struct{}{}
		switch principal.Scope {
		case billing.QuotaScopeUser:
			users = append(users, principal.Key)
		case billing.QuotaScopeGroup:
			groups = append(groups, principal.Key)
		default:
			return 0, fmt.Errorf("active billing quota principal %q has unknown scope %q", principal.Key, principal.Scope)
		}
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM billing_quotas
 WHERE scope NOT IN ('user','group')
    OR (scope='user' AND NOT (principal_key = ANY($1::text[])))
    OR (scope='group' AND NOT (principal_key = ANY($2::text[])))`, users, groups)
	if err != nil {
		return 0, fmt.Errorf("prune billing quotas: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read pruned billing quota count: %w", err)
	}
	return deleted, nil
}

// nullFloat/nullInt map the legacy "unlimited" (nil) quota to SQL NULL
// explicitly rather than relying on driver pointer conversion.
func nullFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullInt(value *int) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func optionalFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func optionalInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

var _ billing.QuotaStore = (*PostgresQuotaStore)(nil)
var _ billing.QuotaPruner = (*PostgresQuotaStore)(nil)
