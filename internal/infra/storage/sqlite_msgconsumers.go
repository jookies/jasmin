package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// sqliteMessageConsumerSchema mirrors migrations/0008_message_consumers.sql.
// Timestamps are integer nanoseconds, matching every other SQLite table here.
const sqliteMessageConsumerSchema = `
CREATE TABLE IF NOT EXISTS message_spool_consumers (
 consumer_id TEXT PRIMARY KEY,
 label TEXT NOT NULL DEFAULT '',
 token_sha256 BLOB NOT NULL UNIQUE CHECK(length(token_sha256)=32),
 scope_json TEXT NOT NULL,
 revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1)),
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, last_used_at INTEGER
);
CREATE INDEX IF NOT EXISTS message_spool_consumers_last_used
 ON message_spool_consumers(last_used_at);`

// SQLiteMessageConsumers is the unit and local-run pull-credential store. It is
// behaviourally identical to PostgresMessageConsumers, so a test that passes
// here exercises the contract production runs.
type SQLiteMessageConsumers struct{ db *sql.DB }

// Consumers returns the pull-credential store backed by this spool's handle.
func (store *SQLiteMessageSpool) Consumers() *SQLiteMessageConsumers {
	return &SQLiteMessageConsumers{db: store.db}
}

// NewSQLiteMessageConsumers builds the store over an existing handle.
func NewSQLiteMessageConsumers(db *sql.DB) (*SQLiteMessageConsumers, error) {
	if db == nil {
		return nil, errors.New("nil SQLite database")
	}
	return &SQLiteMessageConsumers{db: db}, nil
}

// Init applies the schema. SQLiteMessageSpool.Init already applies it too, so
// the local-run path needs one call; this exists for a store built directly.
func (store *SQLiteMessageConsumers) Init(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, sqliteMessageConsumerSchema)
	return err
}

const sqliteConsumerColumns = `consumer_id,label,scope_json,revoked,created_at,updated_at,last_used_at`

func (store *SQLiteMessageConsumers) CreateConsumer(
	ctx context.Context,
	consumer msgspool.Consumer,
	tokenDigest []byte,
) error {
	scope, err := marshalConsumerScope(consumer.Scope)
	if err != nil {
		return err
	}
	if len(tokenDigest) != 32 {
		return fmt.Errorf("%w: consumer token digest must be 32 bytes", msgspool.ErrInvalidInput)
	}
	result, err := store.db.ExecContext(ctx, `INSERT INTO message_spool_consumers(
 consumer_id,label,token_sha256,scope_json,revoked,created_at,updated_at)
 VALUES(?,?,?,?,0,?,?) ON CONFLICT(consumer_id) DO NOTHING`,
		consumer.ID, consumer.Label, tokenDigest, scope,
		nanos(consumer.CreatedAt), nanos(consumer.UpdatedAt))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return msgspool.ErrConsumerExists
	}
	return nil
}

func (store *SQLiteMessageConsumers) ListConsumers(ctx context.Context) ([]msgspool.Consumer, error) {
	rows, err := store.db.QueryContext(ctx,
		`SELECT `+sqliteConsumerColumns+` FROM message_spool_consumers ORDER BY consumer_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	consumers := make([]msgspool.Consumer, 0, 8)
	for rows.Next() {
		consumer, scanErr := scanSQLiteConsumer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		consumers = append(consumers, consumer)
	}
	return consumers, rows.Err()
}

func (store *SQLiteMessageConsumers) GetConsumer(ctx context.Context, id string) (msgspool.Consumer, error) {
	consumer, err := scanSQLiteConsumer(store.db.QueryRowContext(ctx,
		`SELECT `+sqliteConsumerColumns+` FROM message_spool_consumers WHERE consumer_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

func (store *SQLiteMessageConsumers) UpdateConsumer(
	ctx context.Context,
	id, label string,
	scope msgspool.Scope,
	at time.Time,
) (msgspool.Consumer, error) {
	encoded, err := marshalConsumerScope(scope)
	if err != nil {
		return msgspool.Consumer{}, err
	}
	consumer, err := scanSQLiteConsumer(store.db.QueryRowContext(ctx,
		`UPDATE message_spool_consumers SET label=?,scope_json=?,updated_at=?
 WHERE consumer_id=? RETURNING `+sqliteConsumerColumns, label, encoded, nanos(at), id))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

func (store *SQLiteMessageConsumers) SetConsumerRevoked(
	ctx context.Context,
	id string,
	revoked bool,
	at time.Time,
) (msgspool.Consumer, error) {
	consumer, err := scanSQLiteConsumer(store.db.QueryRowContext(ctx,
		`UPDATE message_spool_consumers SET revoked=?,updated_at=?
 WHERE consumer_id=? RETURNING `+sqliteConsumerColumns, revoked, nanos(at), id))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

func (store *SQLiteMessageConsumers) DeleteConsumer(ctx context.Context, id string) error {
	result, err := store.db.ExecContext(ctx,
		`DELETE FROM message_spool_consumers WHERE consumer_id=?`, id)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return msgspool.ErrConsumerNotFound
	}
	return nil
}

func (store *SQLiteMessageConsumers) ConsumerByTokenDigest(
	ctx context.Context,
	tokenDigest []byte,
) (msgspool.Consumer, error) {
	if len(tokenDigest) != 32 {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	consumer, err := scanSQLiteConsumer(store.db.QueryRowContext(ctx,
		`SELECT `+sqliteConsumerColumns+` FROM message_spool_consumers WHERE token_sha256=?`,
		tokenDigest))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

func (store *SQLiteMessageConsumers) TouchConsumer(ctx context.Context, id string, at time.Time) error {
	_, err := store.db.ExecContext(ctx,
		`UPDATE message_spool_consumers SET last_used_at=? WHERE consumer_id=?`, nanos(at), id)
	return err
}

func scanSQLiteConsumer(scanner interface{ Scan(...any) error }) (msgspool.Consumer, error) {
	var consumer msgspool.Consumer
	var scope string
	var created, updated int64
	var lastUsed sql.NullInt64
	if err := scanner.Scan(&consumer.ID, &consumer.Label, &scope, &consumer.Revoked,
		&created, &updated, &lastUsed); err != nil {
		return msgspool.Consumer{}, err
	}
	parsed, err := unmarshalConsumerScope(scope)
	if err != nil {
		return msgspool.Consumer{}, err
	}
	consumer.Scope = parsed
	consumer.CreatedAt = time.Unix(0, created).UTC()
	consumer.UpdatedAt = time.Unix(0, updated).UTC()
	consumer.LastUsedAt = timePtr(lastUsed)
	return consumer, nil
}

var _ msgspool.ConsumerRepository = (*SQLiteMessageConsumers)(nil)
