package smppc

import "sync"

// submitChain tracks the parts of one legacy multipart submit (a nextPdu chain).
// The N parts share one durable attempt/partKey/delivery and are sent as N wire
// PDUs; the message is settled exactly once — when the last part's response
// arrives, or when any part times out / the connection drops. Every settling
// path (handleResponse, handleTimeout, cleanupSession) routes through this guard
// so the shared delivery is never settled twice, mirroring Jasmin's
// LongSubmitSmTransaction closing once all OutboundTransactions close.
type submitChain struct {
	mu        sync.Mutex
	remaining int
	settled   bool
}

// arrive records one part's response. It returns final=true when this is the
// last outstanding part (this caller must finalize: Commit + settle once), or
// dead=true when the chain was already settled by a timeout/cleanup (this
// response must be ignored). A non-final, non-dead arrival just waits for more.
func (c *submitChain) arrive() (final bool, dead bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.settled {
		return false, true
	}
	if c.remaining > 0 {
		c.remaining--
	}
	if c.remaining == 0 {
		c.settled = true
		return true, false
	}
	return false, false
}

// finalize claims the chain for a failure path (timeout / connection loss).
// It returns true to exactly one caller; later callers get false and must not
// re-settle the shared delivery.
func (c *submitChain) finalize() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.settled {
		return false
	}
	c.settled = true
	return true
}
