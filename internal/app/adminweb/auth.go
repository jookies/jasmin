package adminweb

import (
	"context"
	"net/http"
	"time"
)

// requireSession gates the authenticated API: it verifies the session cookie
// and, for state-changing methods, the CSRF header. Failures are JSON (401 /
// 403) — the SPA owns navigation, so the server never redirects. On success
// the username is placed in the request context.
func (h *Handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, ok := h.verifySession(r, time.Now())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !h.checkCSRF(r, username) {
				writeError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, username)))
	})
}
