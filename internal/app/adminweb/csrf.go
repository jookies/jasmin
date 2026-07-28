package adminweb

import (
	"crypto/hmac"
	"net/http"
)

const csrfHeader = "X-CSRF-Token"

// csrfToken derives a per-user CSRF token from the signing key: HMAC(key,
// "csrf|"+username). It is stateless (recomputable on every request from the
// verified session) and bound to the authenticated user, so a token minted for
// one session is not valid for another. The SPA receives it from /api/login
// and /api/session and sends it back in the X-CSRF-Token header on mutations.
func (h *Handler) csrfToken(username string) string {
	return encodeSegment(h.sign([]byte("csrf|" + username)))
}

// checkCSRF validates the X-CSRF-Token header against the token for the
// authenticated user. SameSite=Strict already blocks cross-site requests; this
// is defense-in-depth and constant-time.
func (h *Handler) checkCSRF(r *http.Request, username string) bool {
	submitted, err := decodeSegment(r.Header.Get(csrfHeader))
	if err != nil {
		return false
	}
	return hmac.Equal(submitted, h.sign([]byte("csrf|"+username)))
}
