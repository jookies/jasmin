package termination

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisKeyProbe is the one Redis operation the activation gate performs.
//
// It is declared here rather than added to internal/state/rediscompat because
// that package models Jasmin's own keyspace: its Client only accepts typed Key
// values (DLR, queue-message, legacy multipart) and exposes hash reads. The
// activation window is not a Jasmin key at all — it is written by a different
// system entirely — so widening rediscompat to carry a plain GET would blur the
// one thing that package is for. A *redis.Client satisfies this interface as it
// stands, so nothing has to be wrapped to use it.
type RedisKeyProbe interface {
	Get(ctx context.Context, key string) *redis.StringCmd
}

// sweepThreshold is the entry count above which a cache insert may evict expired
// entries. Below it the map is too small to be worth walking: at the plan's 200
// msg/s target with a 5 s TTL, a fully distinct destination stream holds ~1000
// entries, so the sweep is what keeps this from growing with uptime instead of
// with arrival rate.
const sweepThreshold = 1024

// redisWindowSource decides the receipt from the activation window in Redis.
//
// Parity contract with fake_smsc.py: the key is dlr:block:{digits} using the
// legacy msisdn normalization, presence means DELIVRD/000, absence means
// REJECTD/008, and any Redis failure fails open to DELIVRD rather than rejecting
// legitimate traffic on an infrastructure blip.
type redisWindowSource struct {
	client        RedisKeyProbe
	keyPrefix     string
	lookupTimeout time.Duration
	cache         *windowCache
}

func newRedisWindowSource(cfg VerdictConfig, client RedisKeyProbe) (VerdictSource, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: redis-window needs a redis client", ErrInvalidVerdictConfig)
	}
	return &redisWindowSource{
		client:        client,
		keyPrefix:     cfg.KeyPrefix,
		lookupTimeout: cfg.LookupTimeout,
		cache:         newWindowCache(cfg.CacheTTL, time.Now),
	}, nil
}

func (s *redisWindowSource) Name() string { return string(SourceRedisWindow) }

// Decide returns the receipt for msg.
//
// The error is non-nil only when no verdict may be used at all: a fail-open
// accept while the caller's context is already done would tell a partner
// DELIVRD for a message that is about to be redelivered and decided again.
// Every other outcome — including the gate being unreachable — is an answer, so
// callers get a Verdict and a nil error and must always emit a receipt.
func (s *redisWindowSource) Decide(ctx context.Context, msg Message) (Verdict, error) {
	digits, err := NormalizeDestination(msg.To)
	if err != nil {
		// Never build a key from garbage. The legacy path stripped only '+', so
		// a destination of the literal "undefined" became a lookup of
		// dlr:block:undefined — a key that cannot exist, making every such
		// message REJECTD with no record of why. Same outcome, stated reason.
		return rejectedVerdict(ReasonInvalidDestination), nil
	}
	return s.cache.decide(ctx, digits, s.lookup)
}

// lookup performs one gate read. It never returns an error: an unreachable gate
// is a fail-open accept, which is a verdict.
func (s *redisWindowSource) lookup(ctx context.Context, digits string) Verdict {
	// The lookup runs on a context detached from the caller's cancellation so a
	// result shared by the cache belongs to the gate, not to whichever message
	// happened to issue it — otherwise one caller's shutdown would hand its
	// cancellation to every other caller waiting on the same number. The
	// timeout still bounds it.
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.lookupTimeout)
	defer cancel()

	// GET, not EXISTS, matching `await rc_client.get(key) is not None`
	// (fake_smsc.py:228). It matters for a key of the wrong type: GET returns
	// WRONGTYPE, which is an error and therefore fails open, while EXISTS would
	// report the key as present and silently accept.
	err := s.client.Get(lookupCtx, s.keyPrefix+digits).Err()
	switch {
	case err == nil:
		return deliveredVerdict(ReasonWindowOpen)
	case errors.Is(err, redis.Nil):
		return rejectedVerdict(ReasonNoWindow)
	default:
		bypassed := deliveredVerdict(ReasonGateUnreachable)
		bypassed.GateBypassed = true
		return bypassed
	}
}

// windowCache reuses one number's verdict for a short TTL and collapses
// concurrent lookups of the same number into one.
//
// It is a correctness mechanism, not a speed one: the activation window is a
// property of the destination, so two messages arriving on the same number
// inside one window must receive the same receipt. Deciding each message
// independently makes that a race against the window's own expiry.
type windowCache struct {
	ttl time.Duration
	now func() time.Time

	mu        sync.Mutex
	entries   map[string]*windowEntry
	lastSweep time.Time
}

// windowEntry is one number's decision, possibly still in flight. ready is
// closed exactly once, when verdict is final; waiters read verdict only after
// observing the close, which is what publishes the write to them.
type windowEntry struct {
	ready   chan struct{}
	verdict Verdict
	expires time.Time
}

func newWindowCache(ttl time.Duration, now func() time.Time) *windowCache {
	return &windowCache{ttl: ttl, now: now, entries: map[string]*windowEntry{}}
}

// lookupFunc decides one key. It cannot fail: see redisWindowSource.lookup.
type lookupFunc func(ctx context.Context, key string) Verdict

func (c *windowCache) decide(ctx context.Context, key string, lookup lookupFunc) (Verdict, error) {
	entry, leader := c.acquire(key)
	if leader {
		c.resolve(key, entry, lookup(ctx, key))
	} else {
		select {
		case <-entry.ready:
		case <-ctx.Done():
			// Someone else owns this lookup and this caller is going away.
			// Abandoning the wait is safe: the leader still finishes and caches.
			return Verdict{}, ctx.Err()
		}
	}
	verdict := entry.verdict
	if verdict.GateBypassed && ctx.Err() != nil {
		// The gate failed and this caller is already cancelled, so the message
		// is heading back to the queue. Fabricating an accept for it would
		// deliver a receipt for a message that will be decided a second time.
		return Verdict{}, ctx.Err()
	}
	return verdict, nil
}

// acquire returns the entry for key and whether this caller must perform the
// lookup. A fresh cached entry comes back with ready already closed, so the
// caller's wait returns immediately.
func (c *windowCache) acquire(key string) (*windowEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		select {
		case <-existing.ready:
			if c.now().Before(existing.expires) {
				return existing, false
			}
			// Expired: replaced below by a fresh lookup.
		default:
			// In flight: join it instead of issuing a second lookup.
			return existing, false
		}
	}
	c.sweepLocked()
	entry := &windowEntry{ready: make(chan struct{})}
	c.entries[key] = entry
	return entry, true
}

func (c *windowCache) resolve(key string, entry *windowEntry, verdict Verdict) {
	c.mu.Lock()
	entry.verdict = verdict
	entry.expires = c.now().Add(c.ttl)
	if verdict.GateBypassed {
		// A fail-open answer is the absence of an answer. Caching it would turn
		// one Redis blip into TTL seconds of blind accepts and would hide the
		// gate's recovery, so it is dropped as soon as it is handed out. The
		// callers already waiting on this entry still get it: sharing one
		// in-flight failure is deduplication, not caching.
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	close(entry.ready)
}

// sweepLocked drops expired entries. It runs at most once per TTL and only once
// the map is big enough to matter, so a hot cache does not walk itself on every
// insert. In-flight entries are never swept: their owner is still writing them.
func (c *windowCache) sweepLocked() {
	if len(c.entries) < sweepThreshold {
		return
	}
	now := c.now()
	if now.Sub(c.lastSweep) < c.ttl {
		return
	}
	c.lastSweep = now
	for key, entry := range c.entries {
		select {
		case <-entry.ready:
			if !now.Before(entry.expires) {
				delete(c.entries, key)
			}
		default:
		}
	}
}
