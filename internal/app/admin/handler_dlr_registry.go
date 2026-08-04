package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/dlrgate"
)

// WithDLRRegistry enables /admin/dlr-registry, the machine-facing way to open an
// activation window for a destination.
//
// This is the endpoint an application calls when it legitimately expects traffic
// to a number: the per-user DLR gate reports its hit receipt for destinations
// with an open window and its miss receipt for the rest, so nothing else needs
// to know which numbers those are.
func WithDLRRegistry(registry *dlrgate.Registry) Option {
	return func(h *Handler) { h.dlrRegistry = registry }
}

func (h *Handler) registerDLRRegistryRoutes(mux *http.ServeMux) {
	if h.dlrRegistry == nil {
		return
	}
	mux.HandleFunc("/admin/dlr-registry", h.auth(h.dlrRegistryCollection))
	mux.HandleFunc("/admin/dlr-registry/", h.auth(h.dlrRegistryByMSISDN))
}

// dlrRegistryEntry is the wire form of one live entry. It is a flat DTO rather
// than dlrgate.Entry so the API shape does not move when the internal one does.
type dlrRegistryEntry struct {
	MSISDN           string `json:"msisdn"`
	AddedBy          string `json:"added_by,omitempty"`
	Owner            string `json:"owner,omitempty"`
	Note             string `json:"note,omitempty"`
	AddedAt          string `json:"added_at,omitempty"`
	ExpiresAt        string `json:"expires_at"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type dlrRegistryAddRequest struct {
	MSISDN     string `json:"msisdn"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	AddedBy    string `json:"added_by,omitempty"`
	Note       string `json:"note,omitempty"`
	// Owner scopes the window to one gated user. Omitted opens it for every
	// gated user, which is the operator's default: a window an operator opens
	// by hand is usually a test or an override, not one partner's traffic.
	Owner string `json:"owner,omitempty"`
}

func toDLRRegistryEntry(entry dlrgate.Entry, now time.Time) dlrRegistryEntry {
	wire := dlrRegistryEntry{
		MSISDN:    entry.MSISDN,
		AddedBy:   entry.AddedBy,
		Owner:     entry.Owner,
		Note:      entry.Note,
		ExpiresAt: entry.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if !entry.AddedAt.IsZero() {
		wire.AddedAt = entry.AddedAt.UTC().Format(time.RFC3339)
	}
	if remaining := entry.ExpiresAt.Sub(now); remaining > 0 {
		wire.ExpiresInSeconds = int64(remaining.Seconds())
	}
	return wire
}

// dlrRegistryCollection handles /admin/dlr-registry (GET list, POST add).
func (h *Handler) dlrRegistryCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		entries, err := h.dlrRegistry.List(r.Context(), limit)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		now := time.Now().UTC()
		wire := make([]dlrRegistryEntry, 0, len(entries))
		for _, entry := range entries {
			wire = append(wire, toDLRRegistryEntry(entry, now))
		}
		writeJSON(w, http.StatusOK, wire)
	case http.MethodPost:
		var request dlrRegistryAddRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(request.MSISDN) == "" {
			writeError(w, http.StatusBadRequest, "msisdn is required")
			return
		}
		addedBy := strings.TrimSpace(request.AddedBy)
		if addedBy == "" {
			// The caller is an API token, not a person. Naming the surface is
			// more useful in the console than an empty column.
			addedBy = "admin-api"
		}
		entry, err := h.dlrRegistry.Add(r.Context(), request.MSISDN, dlrgate.AddOptions{
			TTL:     time.Duration(request.TTLSeconds) * time.Second,
			AddedBy: addedBy,
			Note:    request.Note,
			Owner:   strings.TrimSpace(request.Owner),
		})
		if err != nil {
			if errors.Is(err, dlrgate.ErrInvalidMSISDN) {
				writeError(w, http.StatusBadRequest, "msisdn must be digits, optionally prefixed with '+'")
				return
			}
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, toDLRRegistryEntry(entry, time.Now().UTC()))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// dlrRegistryByMSISDN handles /admin/dlr-registry/<msisdn> (GET one, DELETE).
func (h *Handler) dlrRegistryByMSISDN(w http.ResponseWriter, r *http.Request) {
	msisdn := strings.TrimPrefix(r.URL.Path, "/admin/dlr-registry/")
	if msisdn == "" {
		writeError(w, http.StatusBadRequest, "msisdn is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		entry, err := h.dlrRegistry.Get(r.Context(), msisdn)
		switch {
		case errors.Is(err, dlrgate.ErrNotFound):
			writeError(w, http.StatusNotFound, "no open window for that destination")
			return
		case errors.Is(err, dlrgate.ErrInvalidMSISDN):
			writeError(w, http.StatusBadRequest, "msisdn must be digits, optionally prefixed with '+'")
			return
		case err != nil:
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, toDLRRegistryEntry(entry, time.Now().UTC()))
	case http.MethodDelete:
		if err := h.dlrRegistry.Remove(r.Context(), msisdn); err != nil {
			if errors.Is(err, dlrgate.ErrInvalidMSISDN) {
				writeError(w, http.StatusBadRequest, "msisdn must be digits, optionally prefixed with '+'")
				return
			}
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
