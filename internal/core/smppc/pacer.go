package smppc

import (
	"context"
	"errors"
	"math"
	"time"
)

const DefaultSubmitSMThroughput = 1.0

var ErrInvalidThroughput = errors.New("submit_sm_throughput must be finite and representable")
var ErrInvalidPacingTime = errors.New("pacing clock must be within Python datetime range")

func validateThroughput(throughput float64) error {
	if math.IsNaN(throughput) || math.IsInf(throughput, 0) {
		return ErrInvalidThroughput
	}
	if throughput > 0 {
		intervalMicros := math.RoundToEven((1 / throughput) * float64(time.Second/time.Microsecond))
		const pythonTimedeltaMicrosUpperBound = 86_400_000_000_000_000_000.0
		if math.IsInf(intervalMicros, 0) || intervalMicros >= pythonTimedeltaMicrosUpperBound {
			return ErrInvalidThroughput
		}
	}
	return nil
}

type pacingClock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type realPacingClock struct{}

func (realPacingClock) Now() time.Time { return time.Now() }

func (realPacingClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Pacer serializes submit attempts and preserves the legacy listener's pacing
// decision, including its timedelta.microseconds behavior for intervals over a
// second. A throughput less than or equal to zero disables pacing.
type Pacer struct {
	throughput float64
	clock      pacingClock

	gate chan struct{}
	last time.Time
}

func NewPacer(throughput float64) (*Pacer, error) {
	return newPacer(throughput, realPacingClock{})
}

func newPacer(throughput float64, clock pacingClock) (*Pacer, error) {
	if err := validateThroughput(throughput); err != nil {
		return nil, err
	}
	if clock == nil {
		return nil, errors.New("pacing clock is required")
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Pacer{throughput: throughput, clock: clock, gate: gate}, nil
}

func (p *Pacer) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("pacing context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.throughput <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.gate:
	}
	defer func() { p.gate <- struct{}{} }()
	// Python datetime has microsecond resolution; discard Go's nanosecond tail
	// before calculating or storing timestamps so pacing stays fixture exact.
	now := p.clock.Now().Truncate(time.Microsecond)
	nowMicros, err := legacyWallUnixMicros(now)
	if err != nil {
		return err
	}
	first := p.last.IsZero()
	var elapsedMicros int64
	if first {
		// The legacy callback replaces an absent timestamp with a naive local
		// 1970-01-01 sentinel before applying the ordinary pacing comparison.
		// Ordinary rates therefore appear unpaced on the first call, while a
		// huge valid interval can retain a sub-second wait component.
		elapsedMicros = nowMicros
	} else {
		lastMicros, err := legacyWallUnixMicros(p.last)
		if err != nil {
			return err
		}
		elapsedMicros = nowMicros - lastMicros
	}
	delay := legacyPacingDelayMicros(p.throughput, elapsedMicros)
	if err := p.clock.Wait(ctx, delay); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	completedAt := p.clock.Now().Truncate(time.Microsecond)
	if _, err := legacyWallUnixMicros(completedAt); err != nil {
		return err
	}
	// The pacing admission linearizes at the cursor write. Recheck immediately
	// before it so a timer/cancellation tie cannot consume a slot.
	if err := ctx.Err(); err != nil {
		return err
	}
	p.last = completedAt
	return nil
}

func legacyWallUnixMicros(now time.Time) (int64, error) {
	// Python subtracts two naive datetimes, so timezone and DST offsets do not
	// participate. Rebuild the sampled wall-clock fields in UTC. Microseconds
	// cover Python's complete year 1..9999 range without time.Duration's
	// approximately 292-year nanosecond saturation.
	if now.Year() < 1 || now.Year() > 9999 {
		return 0, ErrInvalidPacingTime
	}
	wallNow := time.Date(
		now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC,
	)
	return wallNow.UnixMicro(), nil
}

func legacyPacingDelay(throughput float64, elapsed time.Duration) time.Duration {
	return legacyPacingDelayMicros(throughput, int64(elapsed/time.Microsecond))
}

func legacyPacingDelayMicros(throughput float64, elapsedMicros int64) time.Duration {
	if throughput <= 0 {
		return 0
	}
	intervalMicros := math.RoundToEven((1 / throughput) * float64(time.Second/time.Microsecond))
	const maxInt64Exclusive = 9223372036854775808.0 // 2^63; float64(math.MaxInt64) rounds here.
	if elapsedMicros >= 0 && intervalMicros < maxInt64Exclusive &&
		elapsedMicros >= int64(intervalMicros) {
		return 0
	}
	// Python first rounds the interval into an integer-microsecond timedelta,
	// then subtracts elapsed time exactly, and finally reads the sub-second
	// .microseconds component. Reduce both integer operands before subtraction
	// so a huge interval's float64 ULP cannot erase a small elapsed value.
	componentMicros := math.Mod(intervalMicros, 1_000_000) - float64(elapsedMicros%1_000_000)
	componentMicros = math.Mod(componentMicros, 1_000_000)
	if componentMicros < 0 {
		componentMicros += 1_000_000
	}
	return time.Duration(componentMicros) * time.Microsecond
}
