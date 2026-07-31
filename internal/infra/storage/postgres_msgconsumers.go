package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// PostgresMessageConsumers stores the scoped, read-only credentials for the
// message pull API.
//
// It shares the message spool's connection pool rather than opening its own:
// authenticating a poll and answering it are one request, and putting them on
// separate pools would let a consumer authenticate against a database its read
// cannot reach.
type PostgresMessageConsumers struct {
	db *sql.DB
}

// Consumers returns the pull-credential store backed by this spool's pool.
func (store *PostgresMessageSpool) Consumers() *PostgresMessageConsumers {
	return &PostgresMessageConsumers{db: store.db}
}

// NewPostgresMessageConsumers builds the store over an existing pool.
func NewPostgresMessageConsumers(db *sql.DB) (*PostgresMessageConsumers, error) {
	if db == nil {
		return nil, errors.New("nil PostgreSQL database")
	}
	return &PostgresMessageConsumers{db: db}, nil
}

const postgresConsumerColumns = `consumer_id,label,scope_json,revoked,created_at,updated_at,last_used_at`

func (store *PostgresMessageConsumers) CreateConsumer(
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
 VALUES($1,$2,$3,$4,false,$5,$6) ON CONFLICT(consumer_id) DO NOTHING`,
		consumer.ID, consumer.Label, tokenDigest, scope,
		consumer.CreatedAt.UTC(), consumer.UpdatedAt.UTC())
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

func (store *PostgresMessageConsumers) ListConsumers(ctx context.Context) ([]msgspool.Consumer, error) {
	rows, err := store.db.QueryContext(ctx,
		`SELECT `+postgresConsumerColumns+` FROM message_spool_consumers ORDER BY consumer_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	consumers := make([]msgspool.Consumer, 0, 8)
	for rows.Next() {
		consumer, scanErr := scanPostgresConsumer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		consumers = append(consumers, consumer)
	}
	return consumers, rows.Err()
}

func (store *PostgresMessageConsumers) GetConsumer(ctx context.Context, id string) (msgspool.Consumer, error) {
	consumer, err := scanPostgresConsumer(store.db.QueryRowContext(ctx,
		`SELECT `+postgresConsumerColumns+` FROM message_spool_consumers WHERE consumer_id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

func (store *PostgresMessageConsumers) UpdateConsumer(
	ctx context.Context,
	id, label string,
	scope msgspool.Scope,
	at time.Time,
) (msgspool.Consumer, error) {
	encoded, err := marshalConsumerScope(scope)
	if err != nil {
		return msgspool.Consumer{}, err
	}
	// The token is untouched on purpose: narrowing a scope must not force the
	// downstream application to redeploy a credential, or nobody will narrow one.
	consumer, err := scanPostgresConsumer(store.db.QueryRowContext(ctx,
		`UPDATE message_spool_consumers SET label=$2,scope_json=$3,updated_at=$4
 WHERE consumer_id=$1 RETURNING `+postgresConsumerColumns, id, label, encoded, at.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

func (store *PostgresMessageConsumers) SetConsumerRevoked(
	ctx context.Context,
	id string,
	revoked bool,
	at time.Time,
) (msgspool.Consumer, error) {
	consumer, err := scanPostgresConsumer(store.db.QueryRowContext(ctx,
		`UPDATE message_spool_consumers SET revoked=$2,updated_at=$3
 WHERE consumer_id=$1 RETURNING `+postgresConsumerColumns, id, revoked, at.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

func (store *PostgresMessageConsumers) DeleteConsumer(ctx context.Context, id string) error {
	result, err := store.db.ExecContext(ctx,
		`DELETE FROM message_spool_consumers WHERE consumer_id=$1`, id)
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

// ConsumerByTokenDigest resolves a presented credential through the unique
// index on the digest. A revoked row is returned rather than hidden, so the
// caller audits the attempt against a real consumer id instead of losing it as
// an anonymous rejection.
func (store *PostgresMessageConsumers) ConsumerByTokenDigest(
	ctx context.Context,
	tokenDigest []byte,
) (msgspool.Consumer, error) {
	if len(tokenDigest) != 32 {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	consumer, err := scanPostgresConsumer(store.db.QueryRowContext(ctx,
		`SELECT `+postgresConsumerColumns+` FROM message_spool_consumers WHERE token_sha256=$1`,
		tokenDigest))
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Consumer{}, msgspool.ErrConsumerNotFound
	}
	return consumer, err
}

// TouchConsumer records a successful authentication. It is best-effort by
// contract, so a missing row is not an error: the credential may have been
// deleted between the lookup and this write, which is not a reason to fail a
// read that was authorized.
func (store *PostgresMessageConsumers) TouchConsumer(ctx context.Context, id string, at time.Time) error {
	_, err := store.db.ExecContext(ctx,
		`UPDATE message_spool_consumers SET last_used_at=$2 WHERE consumer_id=$1`, id, at.UTC())
	return err
}

func scanPostgresConsumer(scanner interface{ Scan(...any) error }) (msgspool.Consumer, error) {
	var consumer msgspool.Consumer
	var scope string
	var lastUsed sql.NullTime
	if err := scanner.Scan(&consumer.ID, &consumer.Label, &scope, &consumer.Revoked,
		&consumer.CreatedAt, &consumer.UpdatedAt, &lastUsed); err != nil {
		return msgspool.Consumer{}, err
	}
	parsed, err := unmarshalConsumerScope(scope)
	if err != nil {
		return msgspool.Consumer{}, err
	}
	consumer.Scope = parsed
	consumer.CreatedAt = consumer.CreatedAt.UTC()
	consumer.UpdatedAt = consumer.UpdatedAt.UTC()
	consumer.LastUsedAt = nullTimeValue(lastUsed)
	return consumer, nil
}

// marshalConsumerScope encodes a scope for storage. It normalizes first so two
// scopes that mean the same thing are stored identically, which is what lets a
// stored row be compared with a submitted one without re-deriving the ordering.
func marshalConsumerScope(scope msgspool.Scope) (string, error) {
	encoded, err := json.Marshal(scope.Normalize())
	if err != nil {
		return "", fmt.Errorf("encode message spool consumer scope: %w", err)
	}
	return string(encoded), nil
}

// unmarshalConsumerScope decodes a stored scope.
//
// A row whose scope cannot be decoded is an error and never an empty scope: an
// empty Scope has no connectors, and while Scope.Validate would refuse it, a
// decode that quietly produced one would be a corrupt row silently changing
// what a credential is allowed to see.
func unmarshalConsumerScope(encoded string) (msgspool.Scope, error) {
	var scope msgspool.Scope
	decoder := json.NewDecoder(strings.NewReader(encoded))
	if err := decoder.Decode(&scope); err != nil {
		return msgspool.Scope{}, fmt.Errorf("decode message spool consumer scope: %w", err)
	}
	return scope.Normalize(), nil
}

var _ msgspool.ConsumerRepository = (*PostgresMessageConsumers)(nil)
