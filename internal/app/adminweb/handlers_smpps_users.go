package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
)

// smppsUserResource is the flat REST shape of one SMPPs bind account. The
// system_id is the identity. Password is write-only: never serialised back, and
// an empty value on update keeps the stored one.
type smppsUserResource struct {
	ID string `json:"id"`
	smppsserver.UserConfig
}

func toSMPPsUserResource(stored admin.StoredSMPPsUser) (smppsUserResource, error) {
	var cfg smppsserver.UserConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &cfg); err != nil {
		return smppsUserResource{}, fmt.Errorf("SMPPs user %q: stored spec is not valid JSON: %w", stored.SystemID, err)
	}
	cfg.SystemID = stored.SystemID
	cfg.Password = ""
	return smppsUserResource{ID: stored.SystemID, UserConfig: cfg}, nil
}

// storedSMPPsUser reads the persisted spec with its password intact, so an
// update that omits the password can preserve it.
func storedSMPPsUserConfig(stored admin.StoredSMPPsUser) (smppsserver.UserConfig, error) {
	var cfg smppsserver.UserConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &cfg); err != nil {
		return smppsserver.UserConfig{}, fmt.Errorf("SMPPs user %q: stored spec is not valid JSON: %w", stored.SystemID, err)
	}
	return cfg, nil
}

func (h *Handler) listSMPPsUsers(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.SMPPsUsers.ListUsers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resources := make([]smppsUserResource, 0, len(stored))
	for _, user := range stored {
		resource, err := toSMPPsUserResource(user)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resources = append(resources, resource)
	}
	writeList(w, r, resources)
}

func (h *Handler) getSMPPsUser(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.SMPPsUsers.GetUser(r.Context(), r.PathValue("systemID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toSMPPsUserResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

// putSMPPsUser applies one bind account and answers with the stored result.
func (h *Handler) putSMPPsUser(w http.ResponseWriter, r *http.Request, systemID string, cfg smppsserver.UserConfig, status int) {
	cfg.SystemID = systemID
	specJSON, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.SMPPsUsers.PutUser(r.Context(), systemID, string(specJSON)); err != nil {
		writeServiceError(w, err)
		return
	}
	stored, err := h.deps.SMPPsUsers.GetUser(r.Context(), systemID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toSMPPsUserResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, resource)
}

func (h *Handler) createSMPPsUser(w http.ResponseWriter, r *http.Request) {
	var res smppsUserResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if res.SystemID == "" {
		writeError(w, http.StatusBadRequest, "system_id is required")
		return
	}
	if res.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	h.putSMPPsUser(w, r, res.SystemID, res.UserConfig, http.StatusCreated)
}

func (h *Handler) updateSMPPsUser(w http.ResponseWriter, r *http.Request) {
	systemID := r.PathValue("systemID")
	stored, err := h.deps.SMPPsUsers.GetUser(r.Context(), systemID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	current, err := storedSMPPsUserConfig(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Start from stored state so a partial PATCH keeps omitted fields, then
	// restore the stored password when the body did not carry a new one.
	res := smppsUserResource{UserConfig: current}
	res.Password = ""
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if res.Password == "" {
		res.Password = current.Password
	}
	h.putSMPPsUser(w, r, systemID, res.UserConfig, http.StatusOK)
}

func (h *Handler) deleteSMPPsUser(w http.ResponseWriter, r *http.Request) {
	systemID := r.PathValue("systemID")
	stored, err := h.deps.SMPPsUsers.GetUser(r.Context(), systemID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toSMPPsUserResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.SMPPsUsers.DeleteUser(r.Context(), systemID); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}
