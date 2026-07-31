package restcompat

import "net/http"

// The scoped message pull endpoint rides this listener rather than getting one
// of its own.
//
// A new port would be a new thing to firewall, a new TLS terminator and a new
// address to publish to a partner, for a resource whose audience is exactly the
// audience the REST API already has. What it does *not* share is the
// credential: /secure/* authenticates a Jasmin user with Basic auth, and this
// path authenticates a stored, revocable, read-only consumer token with Bearer.
// The two never meet, which is why it is mounted beside /secure rather than
// inside it.

// MessagePullPath is the mounted path. It is exact-match: nothing below it is
// captured, so a future /messages/{id} is a deliberate addition rather than
// something this registration silently already answers.
const MessagePullPath = "/messages"

// WithMessagePull mounts the message pull endpoint on both REST views.
//
// resolve is called per request rather than the handler being captured once,
// because of a construction-order fact: the REST listener is built by the
// outbound runtime, and the message spool that backs this endpoint is built
// afterwards by the termination plane — it needs that runtime's publisher. A
// captured handler would therefore always be nil.
//
// A nil resolve leaves the path unregistered entirely, and a resolve that
// returns nil answers 404. Both are the same statement — this deployment does
// not serve pulled messages — which is deliberately distinct from an empty page
// meaning "no new messages".
func WithMessagePull(resolve func() http.Handler) Option {
	return func(options *handlerOptions) {
		options.messagePull = resolve
	}
}

// messages dispatches to the late-bound pull handler.
func (h *handler) messages(w http.ResponseWriter, request *http.Request) {
	var pull http.Handler
	if h.messagePull != nil {
		pull = h.messagePull()
	}
	if pull == nil {
		writeAPIError(w, http.StatusNotFound, "Resource not found",
			"This gateway does not serve pulled messages.", "")
		return
	}
	pull.ServeHTTP(w, request)
}
