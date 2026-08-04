package dlrgate

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// APIPathPrefix is where the per-user registry API is mounted on the public
// front door. Every path under it is /dlr-registry/<key-id>[/<msisdn>].
const APIPathPrefix = "/dlr-registry/"

// maxRequestBytes bounds a request body. The documents here are three short
// fields; anything larger is a mistake or an attack, and neither deserves the
// allocation.
const maxRequestBytes = 4 << 10

// KeyResolver maps a public key id to the gated user that owns it and the
// stored digest of its token. The second return is false for an unknown id.
//
// It is satisfied by the runtime user directory, so a credential rotated or
// revoked through the admin plane takes effect on the next request rather than
// at the next restart.
type KeyResolver interface {
	ResolveDLRGateKey(keyID string) (username string, tokenSHA256 string, ok bool)
}

// APIHandler serves the per-user registry API.
//
// Authentication is a public key id in the path plus a secret bearer token, and
// BOTH must resolve to the same user. The id alone identifies whose endpoint is
// being called — which is what makes an access log useful — while the token is
// the only thing that authorizes it.
type APIHandler struct {
	registry *Registry
	keys     KeyResolver
	logger   *slog.Logger
}

// NewAPIHandler builds the handler. A nil registry or resolver yields nil, and
// the caller then does not mount the route at all.
func NewAPIHandler(registry *Registry, keys KeyResolver, logger *slog.Logger) *APIHandler {
	if registry == nil || keys == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &APIHandler{registry: registry, keys: keys, logger: logger}
}

type apiEntry struct {
	MSISDN           string `json:"msisdn"`
	Note             string `json:"note,omitempty"`
	ExpiresAt        string `json:"expires_at"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type apiAddRequest struct {
	MSISDN     string `json:"msisdn"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Note       string `json:"note,omitempty"`
}

func toAPIEntry(entry Entry, now time.Time) apiEntry {
	wire := apiEntry{
		MSISDN:    entry.MSISDN,
		Note:      entry.Note,
		ExpiresAt: entry.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if remaining := entry.ExpiresAt.Sub(now); remaining > 0 {
		wire.ExpiresInSeconds = int64(remaining.Seconds())
	}
	return wire
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeAPIJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, APIPathPrefix)
	keyID, msisdn, _ := strings.Cut(rest, "/")

	username, ok := h.authenticate(r, keyID)
	if !ok {
		// One message for a bad id, a bad token and a disabled gate alike:
		// distinguishing them would let a caller probe which key ids exist.
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeAPIError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch {
	case r.Method == http.MethodPost && msisdn == "":
		h.add(w, r, username)
	case r.Method == http.MethodGet && msisdn == "":
		h.list(w, r, username)
	case r.Method == http.MethodDelete && msisdn != "":
		h.remove(w, r, username, msisdn)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed for this path")
	}
}

// authenticate resolves the key id and constant-time compares the bearer token.
func (h *APIHandler) authenticate(r *http.Request, keyID string) (string, bool) {
	if !ValidKeyID(keyID) {
		return "", false
	}
	username, digest, known := h.keys.ResolveDLRGateKey(keyID)
	if !known {
		return "", false
	}
	header := r.Header.Get("Authorization")
	presented, found := strings.CutPrefix(header, "Bearer ")
	if !found || !TokenMatches(strings.TrimSpace(presented), digest) {
		h.logger.Warn("DLR registry API rejected a credential",
			slog.String("key_id", keyID), slog.String("user", username))
		return "", false
	}
	return username, true
}

func (h *APIHandler) add(w http.ResponseWriter, r *http.Request, username string) {
	var request apiAddRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(request.MSISDN) == "" {
		writeAPIError(w, http.StatusBadRequest, "msisdn is required")
		return
	}
	entry, err := h.registry.Add(r.Context(), request.MSISDN, AddOptions{
		TTL:     time.Duration(request.TTLSeconds) * time.Second,
		AddedBy: username,
		Note:    request.Note,
		Owner:   username,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidMSISDN) {
			writeAPIError(w, http.StatusBadRequest, "msisdn must be digits, optionally prefixed with '+'")
			return
		}
		h.logger.Error("DLR registry API could not open a window",
			slog.String("user", username), slog.String("error", err.Error()))
		writeAPIError(w, http.StatusBadGateway, "the registry is unavailable")
		return
	}
	h.logger.Info("DLR registry window opened",
		slog.String("user", username),
		slog.String("destination", entry.MSISDN),
		slog.Time("expires_at", entry.ExpiresAt))
	writeAPIJSON(w, http.StatusCreated, toAPIEntry(entry, time.Now().UTC()))
}

func (h *APIHandler) list(w http.ResponseWriter, r *http.Request, username string) {
	entries, err := h.registry.ListOwned(r.Context(), username, 0)
	if err != nil {
		h.logger.Error("DLR registry API could not list windows",
			slog.String("user", username), slog.String("error", err.Error()))
		writeAPIError(w, http.StatusBadGateway, "the registry is unavailable")
		return
	}
	now := time.Now().UTC()
	wire := make([]apiEntry, 0, len(entries))
	for _, entry := range entries {
		wire = append(wire, toAPIEntry(entry, now))
	}
	writeAPIJSON(w, http.StatusOK, wire)
}

func (h *APIHandler) remove(w http.ResponseWriter, r *http.Request, username, msisdn string) {
	err := h.registry.RemoveOwned(r.Context(), msisdn, username)
	switch {
	case errors.Is(err, ErrNotFound):
		// Either no window is open or it belongs to someone else. The caller is
		// told the same thing in both cases: whose window it is, is not theirs
		// to learn.
		writeAPIError(w, http.StatusNotFound, "no window of yours is open for that destination")
	case errors.Is(err, ErrInvalidMSISDN):
		writeAPIError(w, http.StatusBadRequest, "msisdn must be digits, optionally prefixed with '+'")
	case err != nil:
		h.logger.Error("DLR registry API could not close a window",
			slog.String("user", username), slog.String("error", err.Error()))
		writeAPIError(w, http.StatusBadGateway, "the registry is unavailable")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
