package adminweb

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"time"
)

const healthProbeTimeout = 5 * time.Second

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// sessionPayload is what the SPA needs after authentication: who is logged in,
// the CSRF token to attach to mutations, and which optional capabilities this
// deployment enabled so the UI can hide what the server would refuse.
type sessionPayload struct {
	Username  string   `json:"username"`
	CSRFToken string   `json:"csrf_token"`
	Features  features `json:"features"`
}

type features struct {
	InterceptorEditing bool `json:"interceptor_editing"`
}

func (h *Handler) features() features {
	return features{InterceptorEditing: h.deps.Interceptors != nil}
}

// handleLogin authenticates the configured admin credential and issues the
// session cookie. Both username and password compare constant-time via their
// SHA-256 digests, so neither match nor length leaks through timing.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	userDigest := sha256.Sum256([]byte(req.Username))
	passDigest := sha256.Sum256([]byte(req.Password))
	userOK := subtle.ConstantTimeCompare(userDigest[:], h.usernameHash[:])
	passOK := subtle.ConstantTimeCompare(passDigest[:], h.passwordHash[:])
	if userOK&passOK != 1 {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	h.issueSession(w, req.Username, time.Now())
	writeJSON(w, http.StatusOK, sessionPayload{Username: req.Username, CSRFToken: h.csrfToken(req.Username), Features: h.features()})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	h.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleSession(w http.ResponseWriter, r *http.Request) {
	username := currentUser(r.Context())
	writeJSON(w, http.StatusOK, sessionPayload{Username: username, CSRFToken: h.csrfToken(username), Features: h.features()})
}

// handleHealth renders the shared readiness probe for the dashboard. Always
// 200 — the JSON status field carries health; the 503 convention belongs to
// the public /health endpoint.
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if h.deps.Health == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unavailable", "checks": map[string]string{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), healthProbeTimeout)
	defer cancel()
	status, checks := h.deps.Health(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "checks": checks})
}
