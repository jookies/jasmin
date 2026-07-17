package smppc

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type fakePacingClock struct {
	mu      sync.Mutex
	now     time.Time
	waits   []time.Duration
	waitErr error
}

func (c *fakePacingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakePacingClock) Wait(ctx context.Context, delay time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, delay)
	if c.waitErr != nil {
		return c.waitErr
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.now = c.now.Add(delay)
	return nil
}

func (c *fakePacingClock) advance(delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delay)
}

func (c *fakePacingClock) recordedWaits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func TestPacerSerializesConcurrentCallers(t *testing.T) {
	clock := &fakePacingClock{now: time.Unix(1, 0).UTC()}
	pacer, err := newPacer(1, clock)
	if err != nil {
		t.Fatal(err)
	}

	const callers = 4
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- pacer.Wait(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	waits := clock.recordedWaits()
	if len(waits) != callers {
		t.Fatalf("wait count = %d, want %d", len(waits), callers)
	}
	if waits[0] != 0 {
		t.Fatalf("first wait = %s, want 0", waits[0])
	}
	for i, wait := range waits[1:] {
		// The legacy timedelta.microseconds expression returns zero for an
		// exact whole-second remainder. The mutex still proves callers are
		// serialized; non-zero elapsed cases are fixture-covered separately.
		if wait != 0 {
			t.Fatalf("wait[%d] = %s, want legacy exact-timestamp wait 0", i+1, wait)
		}
	}
}

func TestPacerCancellationDoesNotAdvanceState(t *testing.T) {
	clock := &fakePacingClock{now: time.Unix(1, 0).UTC()}
	pacer, err := newPacer(2, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.advance(100 * time.Millisecond)
	clock.waitErr = context.Canceled
	if err := pacer.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	clock.waitErr = nil
	clock.advance(100 * time.Millisecond)
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waits := clock.recordedWaits()
	want := []time.Duration{0, 400 * time.Millisecond, 300 * time.Millisecond}
	if len(waits) != len(want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits[%d] = %s, want %s", i, waits[i], want[i])
		}
	}
}

func TestPacerCanceledWhileWaitingForAdmission(t *testing.T) {
	clock := &fakePacingClock{now: time.Unix(1, 0).UTC()}
	pacer, err := newPacer(2, clock)
	if err != nil {
		t.Fatal(err)
	}
	<-pacer.gate
	defer func() { pacer.gate <- struct{}{} }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := pacer.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled admission took %s, want <= 100ms", elapsed)
	}
}

func TestPacerPreCanceledFirstCallDoesNotAdvanceState(t *testing.T) {
	clock := &fakePacingClock{now: time.Unix(1, 0).UTC()}
	pacer, err := newPacer(2, clock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pacer.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waits := clock.recordedWaits()
	want := []time.Duration{0}
	if len(waits) != len(want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits[%d] = %s, want %s", i, waits[i], want[i])
		}
	}
}

func TestRealPacingClockHonorsPreCanceledZeroDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (realPacingClock{}).Wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestPacerUnlimitedDoesNotUseClock(t *testing.T) {
	clock := &fakePacingClock{now: time.Unix(1, 0).UTC()}
	pacer, err := newPacer(0, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if waits := clock.recordedWaits(); len(waits) != 0 {
		t.Fatalf("unlimited pacer waits = %v", waits)
	}
}

func TestPacerRejectsImpossibleNumericStates(t *testing.T) {
	for _, value := range []float64{
		math.NaN(), math.Inf(1), math.Inf(-1), math.SmallestNonzeroFloat64, 1e-20,
	} {
		if _, err := NewPacer(value); !errors.Is(err, ErrInvalidThroughput) {
			t.Fatalf("NewPacer(%v) error = %v, want ErrInvalidThroughput", value, err)
		}
	}
}

func TestPacerAcceptsLegacyLargeRepresentableInterval(t *testing.T) {
	const throughput = 1e-10
	if _, err := NewPacer(throughput); err != nil {
		t.Fatalf("NewPacer(%v) rejected a Python-timedelta-representable interval: %v", throughput, err)
	}
}

func TestPacerPythonTimedeltaOverflowBoundary(t *testing.T) {
	const overflowThroughput = 1.1574074074074074e-14
	if _, err := NewPacer(overflowThroughput); !errors.Is(err, ErrInvalidThroughput) {
		t.Fatalf("NewPacer(%v) error = %v, want ErrInvalidThroughput", overflowThroughput, err)
	}
	acceptedThroughput := math.Nextafter(overflowThroughput, math.Inf(1))
	if _, err := NewPacer(acceptedThroughput); err != nil {
		t.Fatalf("NewPacer(%v) rejected the largest adjacent representable interval: %v", acceptedThroughput, err)
	}
}

func TestLegacyWallUnixMicrosUsesNaiveWallClock(t *testing.T) {
	location := time.FixedZone("fixture-offset", -7*60*60)
	now := time.Date(2026, time.July, 16, 21, 30, 45, 123456000, location)
	want := time.Date(2026, time.July, 16, 21, 30, 45, 123456000, time.UTC).UnixMicro()
	got, err := legacyWallUnixMicros(now)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("legacyWallUnixMicros(%v) = %d, want naive wall micros %d", now, got, want)
	}
}

func TestPacerFirstCallBeyondDurationRange(t *testing.T) {
	clock := &fakePacingClock{now: time.Date(2300, time.January, 1, 0, 0, 0, 0, time.UTC)}
	pacer, err := newPacer(9.99999999975e-11, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waits := clock.recordedWaits()
	if len(waits) != 1 || waits[0] != 0 {
		t.Fatalf("year-2300 first waits = %v, want [0s]", waits)
	}
}

func TestPacerUsesNaiveWallElapsedAcrossOffsetChange(t *testing.T) {
	before := time.FixedZone("before", -8*60*60)
	after := time.FixedZone("after", -7*60*60)
	clock := &fakePacingClock{now: time.Date(2026, time.March, 8, 3, 0, 0, 0, after)}
	pacer, err := newPacer(1/1800.25, clock)
	if err != nil {
		t.Fatal(err)
	}
	pacer.last = time.Date(2026, time.March, 8, 1, 59, 0, 0, before)
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waits := clock.recordedWaits()
	if len(waits) != 1 || waits[0] != 0 {
		t.Fatalf("offset-change waits = %v, want naive-wall [0s]", waits)
	}
}

func TestPacerRejectsClockOutsidePythonDatetimeRange(t *testing.T) {
	clock := &fakePacingClock{now: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)}
	pacer, err := newPacer(1, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := pacer.Wait(context.Background()); !errors.Is(err, ErrInvalidPacingTime) {
		t.Fatalf("error = %v, want ErrInvalidPacingTime", err)
	}
	if waits := clock.recordedWaits(); len(waits) != 0 {
		t.Fatalf("invalid clock waits = %v, want none", waits)
	}
}

func TestLegacyPacingDelayUsesPythonHalfEvenMicroseconds(t *testing.T) {
	tests := []struct {
		throughput float64
		elapsed    time.Duration
		want       time.Duration
	}{
		{throughput: 128, want: 7812 * time.Microsecond},
		{throughput: 400000, want: 2 * time.Microsecond},
		{throughput: 2000000, want: 0},
		{throughput: 1e-10, elapsed: time.Microsecond, want: 999999 * time.Microsecond},
		{throughput: 1e-10, elapsed: 3 * time.Microsecond, want: 999997 * time.Microsecond},
		{throughput: 9.99999999975e-11, elapsed: time.Microsecond, want: 249999 * time.Microsecond},
		{throughput: 1.0842021724855044e-13, want: 775808 * time.Microsecond},
		{throughput: 2000000, elapsed: -time.Microsecond, want: time.Microsecond},
	}
	for _, tc := range tests {
		if got := legacyPacingDelay(tc.throughput, tc.elapsed); got != tc.want {
			t.Errorf("legacyPacingDelay(%v, %s) = %s, want %s", tc.throughput, tc.elapsed, got, tc.want)
		}
	}
}

func TestPacerUsesLegacyMicrosecondClockResolution(t *testing.T) {
	clock := &fakePacingClock{now: time.Unix(1, 100).UTC()}
	pacer, err := newPacer(4, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.advance(100*time.Millisecond + 400*time.Nanosecond)
	if err := pacer.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waits := clock.recordedWaits()
	want := []time.Duration{0, 150 * time.Millisecond}
	if len(waits) != len(want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits[%d] = %s, want %s", i, waits[i], want[i])
		}
	}
}

func TestConfigThroughputDefaultExplicitUnlimitedAndValidation(t *testing.T) {
	cfg := Config{}
	if got := cfg.EffectiveSubmitSMThroughput(); got != 1 {
		t.Fatalf("default throughput = %v, want 1", got)
	}
	zero := 0.0
	cfg.SubmitSMThroughput = &zero
	if got := cfg.EffectiveSubmitSMThroughput(); got != 0 {
		t.Fatalf("explicit throughput = %v, want 0", got)
	}
	invalid := math.NaN()
	cfg.SubmitSMThroughput = &invalid
	cfg.CID, cfg.Host, cfg.Port, cfg.SystemID = "cid", "localhost", 2775, "user"
	if err := cfg.Validate(); !errors.Is(err, ErrInvalidThroughput) {
		t.Fatalf("Validate error = %v, want ErrInvalidThroughput", err)
	}
}

func FuzzLegacyPacingDelayNeverPanics(f *testing.F) {
	f.Add(uint16(1), int64(0))
	f.Add(uint16(2), int64(100_000_000))
	f.Fuzz(func(t *testing.T, rawThroughput uint16, elapsedNanos int64) {
		throughput := float64(rawThroughput)
		if throughput == 0 {
			throughput = 1
		}
		if elapsedNanos < 0 {
			elapsedNanos = 0
		}
		delay := legacyPacingDelay(throughput, time.Duration(elapsedNanos))
		if delay < 0 || delay >= time.Second {
			t.Fatalf("delay = %s, want [0,1s)", delay)
		}
	})
}

func FuzzLegacyPacingDelayFloatDomain(f *testing.F) {
	f.Add(math.Float64bits(1e-10), int64(time.Microsecond))
	f.Add(math.Float64bits(9.99999999975e-11), int64(time.Microsecond))
	f.Add(math.Float64bits(1.1574074074074075e-14), int64(0))
	f.Add(math.Float64bits(math.NaN()), int64(0))
	f.Fuzz(func(t *testing.T, throughputBits uint64, elapsedNanos int64) {
		throughput := math.Float64frombits(throughputBits)
		if validateThroughput(throughput) != nil {
			return
		}
		delay := legacyPacingDelay(throughput, time.Duration(elapsedNanos))
		if delay < 0 || delay >= time.Second {
			t.Fatalf("throughput=%v delay=%s, want [0,1s)", throughput, delay)
		}
		if throughput <= 0 && delay != 0 {
			t.Fatalf("throughput=%v delay=%s, want 0", throughput, delay)
		}
	})
}
