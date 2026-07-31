package termination

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fakeWindowProbe is a RedisKeyProbe that counts lookups and can be made to block or
// fail. Counting is what proves the one-lookup-per-window property; a real
// client cannot report how often it was asked.
type fakeWindowProbe struct {
	mu    sync.Mutex
	calls int
	keys  []string

	value string
	err   error

	entered chan struct{} // closed on the first Get, so a test can join a lookup in flight
	release chan struct{} // when set, Get blocks until it is closed
	once    sync.Once
}

func (p *fakeWindowProbe) Get(_ context.Context, key string) *redis.StringCmd {
	p.mu.Lock()
	p.calls++
	p.keys = append(p.keys, key)
	value, err, release := p.value, p.err, p.release
	p.mu.Unlock()

	p.once.Do(func() {
		if p.entered != nil {
			close(p.entered)
		}
	})
	if release != nil {
		<-release
	}
	return redis.NewStringResult(value, err)
}

func (p *fakeWindowProbe) lookups() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeWindowProbe) key(t *testing.T, i int) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.keys) {
		t.Fatalf("lookup %d not performed, keys=%v", i, p.keys)
	}
	return p.keys[i]
}

// probeWindowOpen and probeWindowClosed are the two gate answers: GET returns a value, or
// GET returns redis.Nil.
func probeWindowOpen() *fakeWindowProbe   { return &fakeWindowProbe{value: "1"} }
func probeWindowClosed() *fakeWindowProbe { return &fakeWindowProbe{err: redis.Nil} }

// newWindowSource builds a redis-window source with a controllable clock, so cache TTL
// expiry is tested by advancing time rather than by sleeping.
func newWindowSource(t *testing.T, probe RedisKeyProbe, cfg VerdictConfig) (*redisWindowSource, func(time.Duration)) {
	t.Helper()
	cfg.Source = SourceRedisWindow
	built, err := NewVerdictSource(cfg, Dependencies{Redis: probe})
	if err != nil {
		t.Fatalf("build source: %v", err)
	}
	source, ok := built.(*redisWindowSource)
	if !ok {
		t.Fatalf("built %T, want *redisWindowSource", built)
	}
	clock := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	source.cache.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	return source, func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(d)
	}
}

func verdictMsg(to string) Message {
	return Message{MessageID: "019428c1", Connector: "partner-a-term", Partner: "partner-a", From: "NETFLIX", To: to}
}

func decideVerdict(t *testing.T, source VerdictSource, to string) Verdict {
	t.Helper()
	verdict, err := source.Decide(context.Background(), verdictMsg(to))
	if err != nil {
		t.Fatalf("decide %q: %v", to, err)
	}
	return verdict
}

func assertVerdictFields(t *testing.T, got Verdict, accept bool, stat, errCode, reason string, bypassed bool) {
	t.Helper()
	if got.Accept != accept || got.Stat != stat || got.Err != errCode {
		t.Fatalf("verdict=%+v want accept=%v stat=%s err=%s", got, accept, stat, errCode)
	}
	if got.Reason != reason {
		t.Fatalf("reason=%q want %q", got.Reason, reason)
	}
	if got.GateBypassed != bypassed {
		t.Fatalf("gate bypassed=%v want %v", got.GateBypassed, bypassed)
	}
	// dlvrd is derived, never stored: assert the pair a real SMSC would never
	// contradict (stat:REJECTD with dlvrd:001, or DELIVRD with a non-zero err).
	wantDelivered := 0
	if accept {
		wantDelivered = 1
	}
	if got.Delivered() != wantDelivered {
		t.Fatalf("dlvrd=%d want %d for stat=%s", got.Delivered(), wantDelivered, stat)
	}
}

// TestRedisWindowReceiptFieldsMatchLegacy pins the two values the legacy
// DLR_RECEIPT_FIELDS table defines (fake_smsc.py:49-53).
func TestRedisWindowReceiptFieldsMatchLegacy(t *testing.T) {
	t.Run("window open is DELIVRD 000", func(t *testing.T) {
		probe := probeWindowOpen()
		source, _ := newWindowSource(t, probe, VerdictConfig{})
		assertVerdictFields(t, decideVerdict(t, source, "380671234567"), true, "DELIVRD", "000", ReasonWindowOpen, false)
		if got := probe.key(t, 0); got != "dlr:block:380671234567" {
			t.Fatalf("key=%q", got)
		}
	})
	t.Run("no window is REJECTD 008", func(t *testing.T) {
		source, _ := newWindowSource(t, probeWindowClosed(), VerdictConfig{})
		assertVerdictFields(t, decideVerdict(t, source, "380671234567"), false, "REJECTD", "008", ReasonNoWindow, false)
	})
	t.Run("leading plus and surrounding space normalize to the same key", func(t *testing.T) {
		probe := probeWindowOpen()
		source, _ := newWindowSource(t, probe, VerdictConfig{})
		decideVerdict(t, source, " +380671234567 ")
		if got := probe.key(t, 0); got != "dlr:block:380671234567" {
			t.Fatalf("key=%q", got)
		}
	})
	t.Run("key prefix is configurable", func(t *testing.T) {
		probe := probeWindowOpen()
		source, _ := newWindowSource(t, probe, VerdictConfig{KeyPrefix: "partner-b:block:"})
		decideVerdict(t, source, "380671234567")
		if got := probe.key(t, 0); got != "partner-b:block:380671234567" {
			t.Fatalf("key=%q", got)
		}
	})
}

// TestRedisWindowFailsOpen covers the parity behaviour that contradicts the
// obvious choice: a gate error accepts rather than rejects, and says so.
func TestRedisWindowFailsOpen(t *testing.T) {
	probe := &fakeWindowProbe{err: errors.New("dial tcp: connection refused")}
	source, _ := newWindowSource(t, probe, VerdictConfig{})

	first := decideVerdict(t, source, "380671234567")
	assertVerdictFields(t, first, true, "DELIVRD", "000", ReasonGateUnreachable, true)

	// A fail-open answer must not be cached: caching it would turn one blip
	// into a TTL-long stretch of blind accepts and hide the gate's recovery.
	second := decideVerdict(t, source, "380671234567")
	assertVerdictFields(t, second, true, "DELIVRD", "000", ReasonGateUnreachable, true)
	if probe.lookups() != 2 {
		t.Fatalf("lookups=%d want 2 (fail-open must not be cached)", probe.lookups())
	}

	// Once the gate answers again the real verdict is used and cached.
	probe.mu.Lock()
	probe.err = redis.Nil
	probe.mu.Unlock()
	assertVerdictFields(t, decideVerdict(t, source, "380671234567"), false, "REJECTD", "008", ReasonNoWindow, false)
	decideVerdict(t, source, "380671234567")
	if probe.lookups() != 3 {
		t.Fatalf("lookups=%d want 3 (recovered verdict must be cached)", probe.lookups())
	}
}

// TestRedisWindowInvalidDestinationSkipsGate is the dlr:block:undefined bug:
// garbage must be rejected on its own terms, never turned into a key.
func TestRedisWindowInvalidDestinationSkipsGate(t *testing.T) {
	for _, destination := range []string{"undefined", "", "+", "   ", "38067ABC", "+380-67-123-45-67", "380671234567890123456"} {
		t.Run(destination, func(t *testing.T) {
			probe := probeWindowOpen()
			source, _ := newWindowSource(t, probe, VerdictConfig{})
			assertVerdictFields(t, decideVerdict(t, source, destination), false, "REJECTD", "008", ReasonInvalidDestination, false)
			if probe.lookups() != 0 {
				t.Fatalf("lookups=%d want 0, keys=%v", probe.lookups(), probe.keys)
			}
		})
	}
}

// TestRedisWindowCacheIssuesOneLookupPerWindow is the correctness property: the
// verdict belongs to the number, so two messages to it inside one window get one
// lookup and identical answers.
func TestRedisWindowCacheIssuesOneLookupPerWindow(t *testing.T) {
	probe := probeWindowOpen()
	source, advance := newWindowSource(t, probe, VerdictConfig{})

	first := decideVerdict(t, source, "380671234567")
	second := decideVerdict(t, source, "+380671234567") // same number, different submitted form
	if first != second {
		t.Fatalf("verdicts differ inside one window: %+v vs %+v", first, second)
	}
	if probe.lookups() != 1 {
		t.Fatalf("lookups=%d want 1", probe.lookups())
	}

	// A different number is a different window.
	decideVerdict(t, source, "380509998877")
	if probe.lookups() != 2 {
		t.Fatalf("lookups=%d want 2 for a second number", probe.lookups())
	}

	// The cached answer survives to the last instant of the TTL and not beyond.
	advance(DefaultVerdictCacheTTL - time.Nanosecond)
	decideVerdict(t, source, "380671234567")
	if probe.lookups() != 2 {
		t.Fatalf("lookups=%d want 2 just before expiry", probe.lookups())
	}
	advance(time.Nanosecond)
	decideVerdict(t, source, "380671234567")
	if probe.lookups() != 3 {
		t.Fatalf("lookups=%d want 3 after expiry", probe.lookups())
	}

	// And the answer that comes back after expiry is the gate's current one.
	probe.mu.Lock()
	probe.err = redis.Nil
	probe.value = ""
	probe.mu.Unlock()
	advance(DefaultVerdictCacheTTL)
	assertVerdictFields(t, decideVerdict(t, source, "380671234567"), false, "REJECTD", "008", ReasonNoWindow, false)
}

// TestRedisWindowCacheCollapsesConcurrentLookups proves the property holds under
// the burst it exists for: 64 messages to one number arriving at once still ask
// the gate once. Run with -race.
func TestRedisWindowCacheCollapsesConcurrentLookups(t *testing.T) {
	probe := probeWindowOpen()
	probe.entered = make(chan struct{})
	probe.release = make(chan struct{})
	source, _ := newWindowSource(t, probe, VerdictConfig{})

	const callers = 64
	verdicts := make([]Verdict, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			verdict, err := source.Decide(context.Background(), verdictMsg("380671234567"))
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
				return
			}
			verdicts[i] = verdict
		}()
	}
	<-probe.entered // a lookup is in flight; the rest must join it, not start their own
	close(probe.release)
	wg.Wait()

	if probe.lookups() != 1 {
		t.Fatalf("lookups=%d want 1 for %d concurrent callers", probe.lookups(), callers)
	}
	for i, verdict := range verdicts {
		if verdict != verdicts[0] {
			t.Fatalf("caller %d verdict=%+v differs from %+v", i, verdict, verdicts[0])
		}
	}
	assertVerdictFields(t, verdicts[0], true, "DELIVRD", "000", ReasonWindowOpen, false)
}

// TestRedisWindowCancelledCaller covers the one case where there is no usable
// verdict: the caller is going away, so a fabricated accept would receipt a
// message that is about to be redelivered and decided again.
func TestRedisWindowCancelledCaller(t *testing.T) {
	t.Run("fail-open is withheld from a cancelled caller", func(t *testing.T) {
		source, _ := newWindowSource(t, &fakeWindowProbe{err: errors.New("connection refused")}, VerdictConfig{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := source.Decide(ctx, verdictMsg("380671234567")); !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v want context.Canceled", err)
		}
	})
	t.Run("a real answer is still returned to a cancelled caller", func(t *testing.T) {
		// The lookup is detached from the caller's cancellation, so a gate that
		// answers gives a usable verdict even mid-shutdown.
		source, _ := newWindowSource(t, probeWindowOpen(), VerdictConfig{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		verdict, err := source.Decide(ctx, verdictMsg("380671234567"))
		if err != nil {
			t.Fatalf("decide: %v", err)
		}
		assertVerdictFields(t, verdict, true, "DELIVRD", "000", ReasonWindowOpen, false)
	})
	t.Run("a waiter abandons a lookup it does not own", func(t *testing.T) {
		probe := probeWindowOpen()
		probe.entered = make(chan struct{})
		probe.release = make(chan struct{})
		source, _ := newWindowSource(t, probe, VerdictConfig{})

		leaderDone := make(chan struct{})
		go func() {
			defer close(leaderDone)
			if _, err := source.Decide(context.Background(), verdictMsg("380671234567")); err != nil {
				t.Errorf("leader: %v", err)
			}
		}()
		<-probe.entered

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := source.Decide(ctx, verdictMsg("380671234567")); !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter err=%v want context.Canceled", err)
		}
		close(probe.release)
		<-leaderDone
		if probe.lookups() != 1 {
			t.Fatalf("lookups=%d want 1", probe.lookups())
		}
	})
}

// TestRedisWindowAgainstRealClient proves a *redis.Client satisfies RedisKeyProbe
// with no adapter, and that the key form is what a live gateway writes.
func TestRedisWindowAgainstRealClient(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	source, err := NewVerdictSource(VerdictConfig{Source: SourceRedisWindow}, Dependencies{Redis: rdb})
	if err != nil {
		t.Fatalf("build source: %v", err)
	}
	assertVerdictFields(t, decideVerdict(t, source, "380671234567"), false, "REJECTD", "008", ReasonNoWindow, false)

	if err := server.Set("dlr:block:380509998877", "1"); err != nil {
		t.Fatalf("seed window: %v", err)
	}
	assertVerdictFields(t, decideVerdict(t, source, "+380509998877"), true, "DELIVRD", "000", ReasonWindowOpen, false)

	// A key of the wrong type is a Redis error, so it fails open — EXISTS would
	// have called it present and accepted silently.
	server.HSet("dlr:block:380671110000", "field", "value")
	assertVerdictFields(t, decideVerdict(t, source, "380671110000"), true, "DELIVRD", "000", ReasonGateUnreachable, true)
}

func TestStaticSourceAcceptsEverything(t *testing.T) {
	source, err := NewVerdictSource(VerdictConfig{Source: SourceStatic}, Dependencies{})
	if err != nil {
		t.Fatalf("build source: %v", err)
	}
	if source.Name() != "static" {
		t.Fatalf("name=%q", source.Name())
	}
	for _, destination := range []string{"380671234567", "undefined", ""} {
		assertVerdictFields(t, decideVerdict(t, source, destination), true, "DELIVRD", "000", ReasonStaticAccept, false)
	}
}

func TestNewVerdictSource(t *testing.T) {
	t.Run("defaults are applied", func(t *testing.T) {
		source, _ := newWindowSource(t, probeWindowClosed(), VerdictConfig{})
		if source.keyPrefix != DefaultActivationKeyPrefix {
			t.Fatalf("prefix=%q", source.keyPrefix)
		}
		if source.lookupTimeout != DefaultGateLookupTimeout {
			t.Fatalf("timeout=%v", source.lookupTimeout)
		}
		if source.cache.ttl != DefaultVerdictCacheTTL {
			t.Fatalf("ttl=%v", source.cache.ttl)
		}
		if source.Name() != "redis-window" {
			t.Fatalf("name=%q", source.Name())
		}
	})
	t.Run("errors", func(t *testing.T) {
		cases := []struct {
			name string
			cfg  VerdictConfig
			deps Dependencies
			want error
		}{
			{"empty source", VerdictConfig{}, Dependencies{}, ErrInvalidVerdictConfig},
			{"unknown source", VerdictConfig{Source: "redis"}, Dependencies{}, ErrInvalidVerdictConfig},
			{"redis-window without a client", VerdictConfig{Source: SourceRedisWindow}, Dependencies{}, ErrInvalidVerdictConfig},
			{"negative cache ttl", VerdictConfig{Source: SourceStatic, CacheTTL: -time.Second}, Dependencies{}, ErrInvalidVerdictConfig},
			{"negative lookup timeout", VerdictConfig{Source: SourceStatic, LookupTimeout: -time.Second}, Dependencies{}, ErrInvalidVerdictConfig},
			{"http-gate", VerdictConfig{Source: SourceHTTPGate}, Dependencies{}, ErrSourceNotImplemented},
			{"http-inline", VerdictConfig{Source: SourceHTTPInline}, Dependencies{}, ErrSourceNotImplemented},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				source, err := NewVerdictSource(tc.cfg, tc.deps)
				if !errors.Is(err, tc.want) {
					t.Fatalf("err=%v want %v", err, tc.want)
				}
				if source != nil {
					t.Fatalf("source=%v want nil", source)
				}
			})
		}
	})
}
