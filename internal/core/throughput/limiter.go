// Package throughput enforces Jasmin's per-user QoS submit ceiling — the
// `http_throughput` and `smpps_throughput` quotas of MtMessagingCredential.
//
// Legacy applies this as a *minimum spacing* between accepted submits rather
// than a token bucket: it keeps the wall-clock time of the user's last accepted
// submit per ingress, and rejects anything arriving sooner than 1/throughput
// seconds after it (jasmin/protocols/http/endpoints/send.py:294 and
// jasmin/protocols/smpp/factory.py:427). There is no burst allowance and no
// queueing — an over-rate submit is refused outright, so a client sending two
// messages back to back under a 1/s ceiling loses the second one even if it has
// been idle for an hour.
//
// The quirks below are Python's, preserved deliberately; see KNOWN_QUIRKS.md.
package throughput

import (
	"sync"
	"time"
)

// Limiter tracks the last accepted submit per key. The zero value is not
// usable; call New.
type Limiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// New builds an empty limiter. State is in-memory and per-process, matching
// legacy's CnxStatus, which is explicitly not persisted: a restart forgives
// every user's spacing.
func New() *Limiter {
	return &Limiter{last: make(map[string]time.Time)}
}

// Allow reports whether a submit for key at now is within quota submits per
// second, recording the acceptance when it is.
//
// quota is nil for "unset". Three Python behaviours are load bearing here:
//
//   - A quota of zero disables throttling rather than blocking everything.
//     Legacy guards with `if quota and quota >= 0`, and 0.0 is falsy, so an
//     operator who types 0 gets "unlimited", not "nothing gets through".
//   - A negative quota also disables it, via the same `>= 0` arm.
//   - The first submit always passes: legacy additionally requires
//     `qos_last_submit_sm_at != 0`, so there is nothing to space against until
//     one has been accepted.
//
// A rejected submit does not move the clock — legacy raises before its
// `qos_last_submit_sm_at = datetime.now()` assignment — so a client hammering
// the endpoint is measured against its last *accepted* message, not its last
// attempt. Otherwise a fast enough caller could starve itself indefinitely.
func (l *Limiter) Allow(key string, quota *float64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	previous, seen := l.last[key]
	throttled := quota != nil && *quota > 0 && seen
	if throttled {
		// Legacy computes the interval in whole microseconds
		// (timedelta(microseconds=(1/throughput)*1000000)), so match that
		// truncation rather than comparing in float seconds.
		interval := time.Duration(int64((1/(*quota))*1e6)) * time.Microsecond
		if now.Sub(previous) < interval {
			return false
		}
	}
	l.last[key] = now
	return true
}

// Forget drops a user's spacing state, so a deprovisioned username does not
// leak an entry for the process lifetime.
func (l *Limiter) Forget(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.last, key)
}
