package rediscompat

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// LegacyMultipartTTLSeconds is the fixed MO multipart-assembly TTL (Q-007). Jasmin
// hard-codes 300 seconds for longDeliverSm keys regardless of connector config.
const LegacyMultipartTTLSeconds = 300

// ErrKeyNotFound reports that a key was missing or already expired at read time. It is
// the Go-visible form of Jasmin's missing/expired DLR/MO behavior (RD-004): the caller
// decides whether to ACK, reject, or log, exactly as the legacy consumers do.
var ErrKeyNotFound = errors.New("rediscompat: key not found")

// Client executes the fixture-proven Jasmin Redis operations against a live server,
// operating on the immutable projection types in this package (Key, HashRecord). It
// preserves the legacy hash-field and TTL contract for DLR correlation, queue-message
// mapping, and MO multipart assembly (RD-001..RD-003, quirks Q-006/Q-007).
//
// Deliberate improvement over legacy Jasmin: Jasmin issues HMSET and EXPIRE as two
// separate commands (managers/clients.py), so a crash between them leaves an immortal
// key. Client writes each record's fields and TTL in a single MULTI/EXEC transaction, so
// the observable end state is atomic. The stored fields and TTL are identical, so this is
// transparent to every reader.
type Client struct {
	rdb redis.Cmdable
}

// NewClient wraps a go-redis handle (a *redis.Client, *redis.ClusterClient, or any
// redis.Cmdable, which makes it testable against miniredis). It never owns the
// connection lifecycle.
func NewClient(rdb redis.Cmdable) *Client {
	return &Client{rdb: rdb}
}

// WriteHashRecord writes a DLR or queue-message correlation record: all of its hash
// fields plus its TTL, atomically. Integer fields are stored as their decimal string,
// matching how txredisapi serialized values in legacy Jasmin (numeric strings that
// readers reinterpret as ints; see Q-006). Use the state.go constructors
// (NewHTTPDLRRecord, NewSMPPSDLRRecord, NewQueueMessageCorrelation) to build the record.
func (c *Client) WriteHashRecord(ctx context.Context, record HashRecord) error {
	if record.Key().String() == "" || record.TTLSeconds() <= 0 {
		return fmt.Errorf("%w: empty or untimed record", ErrInvalidRecord)
	}
	fields := record.Fields()
	values := make(map[string]any, len(fields))
	for name, field := range fields {
		switch field.Kind() {
		case FieldString:
			s, _ := field.String()
			values[name] = s
		case FieldInteger:
			n, _ := field.Integer()
			values[name] = strconv.FormatInt(n, 10)
		default:
			return fmt.Errorf("%w: field %q has invalid kind", ErrInvalidRecord, name)
		}
	}
	key := record.Key().String()
	ttl := time.Duration(record.TTLSeconds()) * time.Second
	if _, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		if err := p.HSet(ctx, key, values).Err(); err != nil {
			return err
		}
		return p.Expire(ctx, key, ttl).Err()
	}); err != nil {
		return fmt.Errorf("rediscompat: write %s: %w", record.Key().Pattern(), err)
	}
	return nil
}

// ReadHash returns the raw stored hash fields for a key, or ErrKeyNotFound if the key is
// missing or expired (RD-004). Values are returned as the strings Redis holds; the caller
// reinterprets typed fields (e.g. via the record constructors, or the Q-006 SMSC-ID
// conversions for the correlation path). Only DLR and queue-message keys are accepted;
// legacy multipart parts are read through ReadLegacyMultipartParts.
func (c *Client) ReadHash(ctx context.Context, key Key) (map[string]string, error) {
	switch key.Kind() {
	case KeyDLR, KeyQueueMessage:
	default:
		return nil, fmt.Errorf("%w: ReadHash does not accept %s keys", ErrInvalidKey, key.Kind())
	}
	fields, err := c.rdb.HGetAll(ctx, key.String()).Result()
	if err != nil {
		return nil, fmt.Errorf("rediscompat: read %s: %w", key.Pattern(), err)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrKeyNotFound, key.Pattern())
	}
	return fields, nil
}

// Delete removes a key. It is the terminal-state deletion Jasmin performs on final DLR
// states (RD-001/RD-002). Deleting a missing key is not an error, which keeps redelivery
// idempotent (RD-005).
func (c *Client) Delete(ctx context.Context, key Key) error {
	if err := c.rdb.Del(ctx, key.String()).Err(); err != nil {
		return fmt.Errorf("rediscompat: delete %s: %w", key.Pattern(), err)
	}
	return nil
}

// WriteLegacyMultipartPart stores one MO segment under its segment-sequence field and
// (re)applies the fixed 300s TTL (Q-007), atomically. The payload is opaque: it is the
// Python-pickled part exactly as Jasmin stores it, and Go never decodes it — RD-003 keeps
// that partition Python-owned or bridged. The payload is validated for size only.
func (c *Client) WriteLegacyMultipartPart(ctx context.Context, key Key, segmentSequence uint32, payload []byte) error {
	if key.Kind() != KeyLegacyMultipart || key.String() == "" {
		return fmt.Errorf("%w: expected legacy multipart key", ErrInvalidRecord)
	}
	if segmentSequence == 0 {
		return fmt.Errorf("%w: zero segment sequence", ErrInvalidRecord)
	}
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty legacy multipart payload", ErrInvalidRecord)
	}
	if len(payload) > MaxLegacyMultipartPayload {
		return fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), MaxLegacyMultipartPayload)
	}
	field := strconv.FormatUint(uint64(segmentSequence), 10)
	if _, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		if err := p.HSet(ctx, key.String(), field, payload).Err(); err != nil {
			return err
		}
		return p.Expire(ctx, key.String(), LegacyMultipartTTLSeconds*time.Second).Err()
	}); err != nil {
		return fmt.Errorf("rediscompat: write %s: %w", key.Pattern(), err)
	}
	return nil
}

// ReadLegacyMultipartParts returns the opaque segment payloads for a longDeliverSm key,
// keyed by segment sequence, or ErrKeyNotFound if the key is missing or expired. The
// payloads are the raw pickled bytes; Go does not decode them (RD-003). Fields whose name
// is not a decimal segment sequence are ignored defensively.
func (c *Client) ReadLegacyMultipartParts(ctx context.Context, key Key) (map[uint32][]byte, error) {
	if key.Kind() != KeyLegacyMultipart {
		return nil, fmt.Errorf("%w: ReadLegacyMultipartParts requires a legacy multipart key", ErrInvalidKey)
	}
	fields, err := c.rdb.HGetAll(ctx, key.String()).Result()
	if err != nil {
		return nil, fmt.Errorf("rediscompat: read %s: %w", key.Pattern(), err)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrKeyNotFound, key.Pattern())
	}
	parts := make(map[uint32][]byte, len(fields))
	for name, value := range fields {
		sequence, convErr := strconv.ParseUint(name, 10, 32)
		if convErr != nil || sequence == 0 {
			continue // ignore unexpected fields rather than corrupting assembly
		}
		parts[uint32(sequence)] = []byte(value)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: %s (no segment fields)", ErrKeyNotFound, key.Pattern())
	}
	return parts, nil
}
