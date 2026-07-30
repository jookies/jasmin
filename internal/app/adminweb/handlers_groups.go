package adminweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

type groupResource struct {
	ID            string   `json:"id"`
	GID           string   `json:"gid"`
	Number        int64    `json:"number"`
	Balance       *float64 `json:"balance"`
	SubmitSMCount *int     `json:"submit_sm_count"`
	Disabled      bool     `json:"disabled"`
	ManagedBy     string   `json:"managed_by"`
}

func groupFromConfig(config outbound.GroupConfig, number int64, managedBy string) groupResource {
	return groupResource{
		ID:            config.GID,
		GID:           config.GID,
		Number:        number,
		Balance:       config.Balance,
		SubmitSMCount: config.SubmitSMCount,
		Disabled:      config.Disabled,
		ManagedBy:     managedBy,
	}
}

func toGroupResource(stored admin.StoredGroup) (groupResource, error) {
	var config outbound.GroupConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &config); err != nil {
		return groupResource{}, fmt.Errorf("group %q: stored spec is not valid JSON: %w", stored.GID, err)
	}
	config.GID = stored.GID
	return groupFromConfig(config, stored.Number, "admin"), nil
}

func (h *Handler) configGroup(gid string) (groupResource, bool) {
	if h.deps.ConfigGroups == nil {
		return groupResource{}, false
	}
	for index, group := range h.deps.ConfigGroups() {
		if group.GID == gid {
			return groupFromConfig(group, int64(index+1), "config"), true
		}
	}
	return groupResource{}, false
}

// groupResources merges config-owned and admin-owned groups. The billing views
// read through the same helper so two pages can never disagree about a shared
// ceiling.
func (h *Handler) groupResources(ctx context.Context) ([]groupResource, error) {
	stored, err := h.deps.Groups.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	configGroups := []outbound.GroupConfig{}
	if h.deps.ConfigGroups != nil {
		configGroups = h.deps.ConfigGroups()
	}
	resources := make([]groupResource, 0, len(configGroups)+len(stored))
	for index, group := range configGroups {
		resources = append(resources, groupFromConfig(group, int64(index+1), "config"))
	}
	for _, group := range stored {
		resource, convertErr := toGroupResource(group)
		if convertErr != nil {
			return nil, convertErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	resources, err := h.groupResources(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeList(w, r, resources)
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	gid := r.PathValue("gid")
	stored, err := h.deps.Groups.GetGroup(r.Context(), gid)
	if err != nil {
		if resource, ok := h.configGroup(gid); ok {
			writeJSON(w, http.StatusOK, resource)
			return
		}
		writeServiceError(w, err)
		return
	}
	resource, err := toGroupResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (h *Handler) putGroup(w http.ResponseWriter, r *http.Request, gid string, resource groupResource, status int) {
	config := outbound.GroupConfig{
		GID:           gid,
		Balance:       resource.Balance,
		SubmitSMCount: resource.SubmitSMCount,
		Disabled:      resource.Disabled,
	}
	spec, err := json.Marshal(config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Groups.CreateGroup(r.Context(), gid, string(spec)); err != nil {
		writeServiceError(w, err)
		return
	}
	stored, err := h.deps.Groups.GetGroup(r.Context(), gid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	result, err := toGroupResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, result)
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var resource groupResource
	if err := decodeBody(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if resource.GID == "" {
		writeError(w, http.StatusBadRequest, "gid is required")
		return
	}
	h.putGroup(w, r, resource.GID, resource, http.StatusCreated)
}

func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request) {
	gid := r.PathValue("gid")
	stored, err := h.deps.Groups.GetGroup(r.Context(), gid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toGroupResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := decodeBody(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resource.GID = gid
	h.putGroup(w, r, gid, resource, http.StatusOK)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	gid := r.PathValue("gid")
	stored, err := h.deps.Groups.GetGroup(r.Context(), gid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toGroupResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Groups.DeleteGroup(r.Context(), gid); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}
