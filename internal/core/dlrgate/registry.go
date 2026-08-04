package dlrgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// RedisRegistryClient is the Redis surface the registry needs.
//
// It is declared here rather than added to internal/state/rediscompat for the
// same reason termination.RedisKeyProbe is: rediscompat models Jasmin's own
// keyspace and only accepts its three typed Key families, while these keys
// belong to the activation window, which a different system also writes. A
// *redis.Client satisfies this as it stands.
type RedisRegistryClient interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	ZRem(ctx context.Context, key string, members ...any) *redis.IntCmd
	ZRemRangeByScore(ctx context.Context, key, min, max string) *redis.IntCmd
	ZRangeByScoreWithScores(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.ZSliceCmd
}

// Registry TTL bounds.
const (
	// MaxTTL is the longest a number may stay in the registry. The registry is
	// an activation window, not an allowlist: an entry that outlives the flow
	// that created it starts confirming traffic nobody asked for.
	MaxTTL = 15 * time.Minute
	// DefaultTTL is what an add with no explicit TTL gets.
	DefaultTTL = 15 * time.Minute
	// DefaultListLimit bounds a list read.
	DefaultListLimit = 500
)

// DefaultKeyPrefix is the activation window's namespace. It is the legacy
// gateway's own prefix (termination.DefaultActivationKeyPrefix), so entries
// written here are read by the termination plane's gate and by the legacy
// fake SMSC without either being told about this package.
const DefaultKeyPrefix = termination.DefaultActivationKeyPrefix

// indexSuffix names the sorted set that indexes live entries for listing.
//
// Redis expires the value keys on its own but has no way to expire a member of
// a sorted set, so the index is pruned by score on every read. It exists at all
// because listing by SCAN would walk the whole keyspace — which also holds the
// 24h dlr:<msgid> records — to find at most a few hundred registry keys.
const indexSuffix = "index"

var (
	// ErrInvalidMSISDN reports a destination that cannot be a subscriber number.
	ErrInvalidMSISDN = errors.New("dlrgate: invalid msisdn")
	// ErrNotFound reports a registry entry that is absent or already expired.
	ErrNotFound = errors.New("dlrgate: registry entry not found")
)

// Entry is one live registry record.
type Entry struct {
	// MSISDN is the normalized key form: digits only, no leading '+'.
	MSISDN string `json:"msisdn"`
	// AddedBy identifies whoever wrote the entry (an admin username, or the
	// label an API caller supplied). Free text; it is provenance, not identity.
	AddedBy string `json:"added_by,omitempty"`
	// Note is optional free text carried for the operator's benefit.
	Note string `json:"note,omitempty"`
	// Owner is the gated user whose token opened this window, or empty for a
	// window opened by an operator (or by another system writing the keyspace
	// directly). An owned window counts as a hit only for its owner; an
	// unowned one counts for every gated user, which is what keeps entries
	// written by the legacy gateway working unchanged.
	Owner string `json:"owner,omitempty"`
	// AddedAt and ExpiresAt are UTC.
	AddedAt   time.Time `json:"added_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// entryValue is what is stored under the value key.
//
// It is a JSON *string* value, never a hash. This is load-bearing: the
// termination plane's gate reads these keys with GET and treats a WRONGTYPE
// error as a fail-open DELIVRD (termination/verdict_redis.go:90-104), so
// storing a hash here would make every gated message silently succeed. Both
// that gate and the legacy fake_smsc.py test only for presence, so the body is
// free for us to use as metadata.
type entryValue struct {
	AddedBy string `json:"added_by,omitempty"`
	Note    string `json:"note,omitempty"`
	Owner   string `json:"owner,omitempty"`
	AddedAt int64  `json:"added_at"`
}

// Registry reads and writes the activation-window keyspace.
type Registry struct {
	client    RedisRegistryClient
	keyPrefix string
	now       func() time.Time
}

// NewRegistry builds a registry over client. An empty prefix means
// DefaultKeyPrefix.
func NewRegistry(client RedisRegistryClient, keyPrefix string) (*Registry, error) {
	if client == nil {
		return nil, errors.New("dlrgate: registry needs a redis client")
	}
	if keyPrefix == "" {
		keyPrefix = DefaultKeyPrefix
	}
	return &Registry{client: client, keyPrefix: keyPrefix, now: time.Now}, nil
}

// KeyPrefix reports the namespace this registry reads and writes.
func (r *Registry) KeyPrefix() string { return r.keyPrefix }

func (r *Registry) valueKey(digits string) string { return r.keyPrefix + digits }

func (r *Registry) indexKey() string { return r.keyPrefix + indexSuffix }

// ownerIndexKey is the per-owner index. A user listing their own windows must
// not have to page through everyone else's: at production volume the global
// index holds every number in a 15-minute window across all traffic, which is
// far more than any one partner opened.
func (r *Registry) ownerIndexKey(owner string) string {
	return r.keyPrefix + indexSuffix + ":" + owner
}

// Normalize converts a destination into the registry key form, which is the
// same form the termination gate builds its lookups from.
func Normalize(msisdn string) (string, error) {
	digits, err := termination.NormalizeDestination(msisdn)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidMSISDN, msisdn)
	}
	return digits, nil
}

// AddOptions carries everything about a new window except the number itself.
type AddOptions struct {
	// TTL is how long the window stays open. Zero means DefaultTTL; anything
	// above MaxTTL is clamped rather than rejected, so a caller asking for an
	// hour gets the longest window the operator allows instead of an error it
	// may not handle.
	TTL time.Duration
	// AddedBy is provenance for the console: an admin username, an API label.
	AddedBy string
	// Note is optional free text.
	Note string
	// Owner scopes the window to one gated user. Empty opens it for every gated
	// user, which is what the operator-facing admin API does by default.
	Owner string
}

// Add puts msisdn in the registry, replacing any existing entry.
func (r *Registry) Add(ctx context.Context, msisdn string, options AddOptions) (Entry, error) {
	digits, err := Normalize(msisdn)
	if err != nil {
		return Entry{}, err
	}
	ttl := options.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if ttl > MaxTTL {
		ttl = MaxTTL
	}
	now := r.now().UTC()
	expires := now.Add(ttl)

	payload, err := json.Marshal(entryValue{
		AddedBy: strings.TrimSpace(options.AddedBy),
		Note:    strings.TrimSpace(options.Note),
		Owner:   strings.TrimSpace(options.Owner),
		AddedAt: now.Unix(),
	})
	if err != nil {
		return Entry{}, fmt.Errorf("dlrgate: encode entry: %w", err)
	}
	if err := r.client.Set(ctx, r.valueKey(digits), string(payload), ttl).Err(); err != nil {
		return Entry{}, fmt.Errorf("dlrgate: write entry: %w", err)
	}
	// The indexes are written second and are best-effort on read (an entry whose
	// value key is gone is filtered out), so a failure here costs visibility in
	// the console, never a wrong verdict.
	member := redis.Z{Score: float64(expires.Unix()), Member: digits}
	if err := r.client.ZAdd(ctx, r.indexKey(), member).Err(); err != nil {
		return Entry{}, fmt.Errorf("dlrgate: index entry: %w", err)
	}
	if owner := strings.TrimSpace(options.Owner); owner != "" {
		if err := r.client.ZAdd(ctx, r.ownerIndexKey(owner), member).Err(); err != nil {
			return Entry{}, fmt.Errorf("dlrgate: index entry for owner: %w", err)
		}
	}
	return Entry{
		MSISDN:    digits,
		AddedBy:   strings.TrimSpace(options.AddedBy),
		Note:      strings.TrimSpace(options.Note),
		Owner:     strings.TrimSpace(options.Owner),
		AddedAt:   now,
		ExpiresAt: expires,
	}, nil
}

// Remove drops msisdn from the registry regardless of who opened it. It is the
// operator's unrestricted delete; RemoveOwned is the one a user's own token
// gets. Removing an absent entry is not an error: the caller asked for it to be
// gone and it is.
func (r *Registry) Remove(ctx context.Context, msisdn string) error {
	return r.remove(ctx, msisdn, "", false)
}

// RemoveOwned drops msisdn only if owner opened it. A user's token must not be
// able to close a window another user or the operator opened, so a mismatch is
// ErrNotFound rather than a silent success — reporting "removed" for something
// still open would be worse than refusing.
func (r *Registry) RemoveOwned(ctx context.Context, msisdn, owner string) error {
	return r.remove(ctx, msisdn, owner, true)
}

func (r *Registry) remove(ctx context.Context, msisdn, owner string, requireOwner bool) error {
	digits, err := Normalize(msisdn)
	if err != nil {
		return err
	}
	// Read first: the entry names the owner index the member has to leave, and
	// it is what an ownership check is decided on.
	existing, readErr := r.Get(ctx, msisdn)
	switch {
	case errors.Is(readErr, ErrNotFound):
		if requireOwner {
			return ErrNotFound
		}
	case readErr != nil:
		return readErr
	case requireOwner && existing.Owner != owner:
		return ErrNotFound
	}

	if err := r.client.Del(ctx, r.valueKey(digits)).Err(); err != nil {
		return fmt.Errorf("dlrgate: delete entry: %w", err)
	}
	if err := r.client.ZRem(ctx, r.indexKey(), digits).Err(); err != nil {
		return fmt.Errorf("dlrgate: deindex entry: %w", err)
	}
	if existing.Owner != "" {
		if err := r.client.ZRem(ctx, r.ownerIndexKey(existing.Owner), digits).Err(); err != nil {
			return fmt.Errorf("dlrgate: deindex entry for owner: %w", err)
		}
	}
	return nil
}

// Get returns one live entry.
func (r *Registry) Get(ctx context.Context, msisdn string) (Entry, error) {
	digits, err := Normalize(msisdn)
	if err != nil {
		return Entry{}, err
	}
	raw, err := r.client.Get(ctx, r.valueKey(digits)).Result()
	if errors.Is(err, redis.Nil) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("dlrgate: read entry: %w", err)
	}
	return r.decode(digits, raw, time.Time{}), nil
}

// List returns every live entry, soonest to expire first. It is the operator
// view; ListOwned is the one a user's own token gets.
//
// Entries written directly into the keyspace by another system are in the
// keyspace but not in this index, so they are gated correctly and simply do not
// appear here. Surfacing them would mean a keyspace SCAN; the console says what
// the list covers instead.
func (r *Registry) List(ctx context.Context, limit int) ([]Entry, error) {
	return r.list(ctx, r.indexKey(), limit)
}

// ListOwned returns the live entries owner opened.
func (r *Registry) ListOwned(ctx context.Context, owner string, limit int) ([]Entry, error) {
	if strings.TrimSpace(owner) == "" {
		return []Entry{}, nil
	}
	return r.list(ctx, r.ownerIndexKey(owner), limit)
}

func (r *Registry) list(ctx context.Context, indexKey string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > DefaultListLimit {
		limit = DefaultListLimit
	}
	now := r.now().UTC()
	cutoff := fmt.Sprintf("%d", now.Unix())

	// Prune first so an index that has accumulated expired members does not
	// consume the result window with entries that are already gone.
	if err := r.client.ZRemRangeByScore(ctx, indexKey, "-inf", "("+cutoff).Err(); err != nil {
		return nil, fmt.Errorf("dlrgate: prune index: %w", err)
	}
	members, err := r.client.ZRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
		Min:   cutoff,
		Max:   "+inf",
		Count: int64(limit),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("dlrgate: read index: %w", err)
	}
	if len(members) == 0 {
		return []Entry{}, nil
	}

	keys := make([]string, 0, len(members))
	for _, member := range members {
		digits, _ := member.Member.(string)
		keys = append(keys, r.valueKey(digits))
	}
	values, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("dlrgate: read entries: %w", err)
	}

	entries := make([]Entry, 0, len(members))
	for index, member := range members {
		digits, _ := member.Member.(string)
		if index >= len(values) || values[index] == nil {
			// Indexed but the value key is gone: Redis expired it early or it was
			// deleted outside this API. It is not live, so it is not listed.
			continue
		}
		raw, _ := values[index].(string)
		entries = append(entries, r.decode(digits, raw, time.Unix(int64(member.Score), 0).UTC()))
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ExpiresAt.Before(entries[j].ExpiresAt)
	})
	return entries, nil
}

// decode turns a stored value into an Entry. A value this package did not write
// (the legacy gateway writes an opaque marker) still yields a usable entry with
// empty provenance rather than an error — presence is what the gate acts on.
func (r *Registry) decode(digits, raw string, expires time.Time) Entry {
	entry := Entry{MSISDN: digits, ExpiresAt: expires}
	var value entryValue
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		entry.AddedBy = value.AddedBy
		entry.Note = value.Note
		entry.Owner = value.Owner
		if value.AddedAt > 0 {
			entry.AddedAt = time.Unix(value.AddedAt, 0).UTC()
		}
	}
	return entry
}

// Window reports whether msisdn currently has an open window, and which user
// owns it. An empty owner with present=true is a window open to every gated
// user.
//
// It reads with GET rather than EXISTS deliberately, matching the termination
// gate: on a key of the wrong type GET returns an error, which the caller fails
// open on, while EXISTS would report the key as present and silently accept.
func (r *Registry) Window(ctx context.Context, digits string) (owner string, present bool, err error) {
	raw, getErr := r.client.Get(ctx, r.valueKey(digits)).Result()
	switch {
	case getErr == nil:
		return r.decode(digits, raw, time.Time{}).Owner, true, nil
	case errors.Is(getErr, redis.Nil):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("dlrgate: probe entry: %w", getErr)
	}
}
