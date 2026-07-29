package adminweb

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
)

type connectorStatsResource struct {
	CID      string           `json:"cid"`
	Counters map[string]int64 `json:"counters"`
}

type statsResource struct {
	StartedAt string                   `json:"started_at,omitempty"`
	HTTP      map[string]int64         `json:"http"`
	SMPPs     map[string]int64         `json:"smpps"`
	SMPPc     []connectorStatsResource `json:"smppc"`
}

func (h *Handler) handleStats(w http.ResponseWriter, _ *http.Request) {
	resource := statsResource{
		HTTP:  h.deps.HTTPStats.Snapshot(),
		SMPPs: h.deps.SMPPsStats.Snapshot(),
		SMPPc: []connectorStatsResource{},
	}
	if h.deps.StartedAt != nil {
		resource.StartedAt = h.deps.StartedAt().UTC().Format(time.RFC3339)
	}
	if h.deps.ConnectorIDs != nil {
		for _, cid := range h.deps.ConnectorIDs() {
			resource.SMPPc = append(resource.SMPPc, connectorStatsResource{
				CID:      cid,
				Counters: h.deps.SMPPcStats.Snapshot(cid),
			})
		}
	}
	writeJSON(w, http.StatusOK, resource)
}

func (h *Handler) handleMessageStatus(w http.ResponseWriter, r *http.Request) {
	if h.deps.Transactions == nil {
		writeError(w, http.StatusServiceUnavailable, "message status is unavailable")
		return
	}
	messageID := r.PathValue("messageID")
	status, err := h.deps.Transactions.AggregateStatus(r.Context(), messageID)
	if errors.Is(err, submittransaction.ErrPartNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("message %q was not found", messageID))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status.State = status.DerivedState()
	writeJSON(w, http.StatusOK, status)
}

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func profileName(r *http.Request) (string, error) {
	name := r.PathValue("profile")
	if !profileNamePattern.MatchString(name) {
		return "", fmt.Errorf("profile name must be 1–64 letters, numbers, dots, underscores or dashes")
	}
	return name, nil
}

func (h *Handler) saveProfile(w http.ResponseWriter, r *http.Request) {
	name, err := profileName(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.deps.Profiles.Save(r.Context(), name); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": name, "saved": true})
}

func (h *Handler) loadProfile(w http.ResponseWriter, r *http.Request) {
	name, err := profileName(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.deps.Profiles.Load(r.Context(), name); err != nil {
		writeServiceError(w, err)
		return
	}

	// Rebuild whole-table services first, then identity services in dependency
	// order (groups before users). Named filters/HTTP destinations are store-only.
	var applyErrors []error
	if err := h.deps.Groups.LoadAndApply(r.Context()); err != nil {
		applyErrors = append(applyErrors, fmt.Errorf("groups: %w", err))
	}
	if err := h.deps.Users.LoadAndApply(r.Context()); err != nil {
		applyErrors = append(applyErrors, fmt.Errorf("users: %w", err))
	}
	if err := h.deps.Routes.LoadAndApply(r.Context()); err != nil {
		applyErrors = append(applyErrors, fmt.Errorf("MT routes: %w", err))
	}
	if err := h.deps.MORoutes.LoadAndApply(r.Context()); err != nil {
		applyErrors = append(applyErrors, fmt.Errorf("MO routes: %w", err))
	}
	if h.deps.Interceptors != nil {
		if err := h.deps.Interceptors.LoadAndApply(r.Context()); err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("interceptors: %w", err))
		}
	}
	if err := h.deps.SMPPsUsers.LoadAndApply(r.Context()); err != nil {
		applyErrors = append(applyErrors, fmt.Errorf("SMPPs users: %w", err))
	}
	if err := h.deps.Connectors.LoadAndApply(r.Context()); err != nil {
		applyErrors = append(applyErrors, fmt.Errorf("connectors: %w", err))
	}
	if err := errors.Join(applyErrors...); err != nil {
		writeError(w, http.StatusInternalServerError, "profile restored in storage but some live services failed to reload: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": name, "loaded": true})
}
