package adminweb

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/dlrgate"
)

// dlrRegistryResource is one live activation-window entry.
type dlrRegistryResource struct {
	MSISDN  string `json:"msisdn"`
	AddedBy string `json:"added_by,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Note    string `json:"note,omitempty"`
	// AddedAt is empty for an entry written by a system that stores no
	// provenance — the legacy gateway writes an opaque marker, and such an entry
	// still gates traffic correctly.
	AddedAt   string `json:"added_at,omitempty"`
	ExpiresAt string `json:"expires_at"`
	// ExpiresInSeconds saves every client from doing clock arithmetic against a
	// server whose clock it cannot see. The list is polled, so this is the field
	// the console actually renders.
	ExpiresInSeconds int64 `json:"expires_in_seconds"`
}

func toDLRRegistryResource(entry dlrgate.Entry, now time.Time) dlrRegistryResource {
	resource := dlrRegistryResource{
		MSISDN:    entry.MSISDN,
		AddedBy:   entry.AddedBy,
		Owner:     entry.Owner,
		Note:      entry.Note,
		ExpiresAt: entry.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if !entry.AddedAt.IsZero() {
		resource.AddedAt = entry.AddedAt.UTC().Format(time.RFC3339)
	}
	if remaining := entry.ExpiresAt.Sub(now); remaining > 0 {
		resource.ExpiresInSeconds = int64(remaining.Seconds())
	}
	return resource
}

// dlrRegistryRequest adds one number.
type dlrRegistryRequest struct {
	MSISDN string `json:"msisdn"`
	// TTLSeconds is clamped to the registry maximum rather than rejected, so a
	// caller asking for longer gets the longest window the operator allows.
	// Zero takes the default.
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Note       string `json:"note,omitempty"`
	// Owner scopes the window to one gated user; empty opens it for all of them.
	Owner string `json:"owner,omitempty"`
}

// requireDLRRegistry answers 404 when this gateway has no Redis behind the DLR
// plane, matching how the other optional surfaces behave: a feature that cannot
// work leaves no trace rather than an empty list that invites using it.
func (h *Handler) requireDLRRegistry(w http.ResponseWriter) bool {
	if h.deps.DLRRegistry == nil {
		writeError(w, http.StatusNotFound, "the DLR registry is not enabled on this gateway")
		return false
	}
	return true
}

func (h *Handler) listDLRRegistry(w http.ResponseWriter, r *http.Request) {
	if !h.requireDLRRegistry(w) {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, _ = strconv.Atoi(raw)
	}
	entries, err := h.deps.DLRRegistry.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "the registry could not be read: "+err.Error())
		return
	}
	now := time.Now().UTC()
	resources := make([]dlrRegistryResource, 0, len(entries))
	for _, entry := range entries {
		resources = append(resources, toDLRRegistryResource(entry, now))
	}
	writeList(w, r, resources)
}

func (h *Handler) createDLRRegistryEntry(w http.ResponseWriter, r *http.Request) {
	if !h.requireDLRRegistry(w) {
		return
	}
	var request dlrRegistryRequest
	if err := decodeBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(request.MSISDN) == "" {
		writeError(w, http.StatusBadRequest, "msisdn is required")
		return
	}
	entry, err := h.deps.DLRRegistry.Add(r.Context(), request.MSISDN, dlrgate.AddOptions{
		TTL:     time.Duration(request.TTLSeconds) * time.Second,
		AddedBy: currentUser(r.Context()),
		Note:    request.Note,
		Owner:   strings.TrimSpace(request.Owner),
	})
	if err != nil {
		if errors.Is(err, dlrgate.ErrInvalidMSISDN) {
			writeError(w, http.StatusBadRequest, "msisdn must be digits, optionally prefixed with '+'")
			return
		}
		writeError(w, http.StatusBadGateway, "the registry could not be written: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toDLRRegistryResource(entry, time.Now().UTC()))
}

func (h *Handler) deleteDLRRegistryEntry(w http.ResponseWriter, r *http.Request) {
	if !h.requireDLRRegistry(w) {
		return
	}
	if err := h.deps.DLRRegistry.Remove(r.Context(), r.PathValue("msisdn")); err != nil {
		if errors.Is(err, dlrgate.ErrInvalidMSISDN) {
			writeError(w, http.StatusBadRequest, "msisdn must be digits, optionally prefixed with '+'")
			return
		}
		writeError(w, http.StatusBadGateway, "the registry could not be written: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
