package smppc

import "time"

type reconnectBackoff struct {
	attempt uint
	random  func() float64
}

func newReconnectBackoff(random func() float64) *reconnectBackoff {
	if random == nil {
		random = func() float64 { return 1 }
	}
	return &reconnectBackoff{random: random}
}

func (b *reconnectBackoff) next(base, maximum time.Duration, jitter float64) time.Duration {
	if base <= 0 {
		return 0
	}
	if maximum <= 0 {
		maximum = base
	}
	delay := base
	for remaining := b.attempt; remaining > 0 && delay < maximum; remaining-- {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	if b.attempt < ^uint(0) {
		b.attempt++
	}
	if jitter <= 0 {
		return delay
	}
	sample := b.random()
	if sample < 0 {
		sample = 0
	} else if sample > 1 {
		sample = 1
	}
	factor := 1 - jitter + jitter*sample
	return time.Duration(float64(delay) * factor)
}

func (b *reconnectBackoff) reset() {
	b.attempt = 0
}
