package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/modispatch"
)

// moRouteResource is the flat REST shape of one MO route: the modispatch
// RouteConfig fields inline plus the Refine identity. As with MT routes the
// order is the identity, so it is fixed for an existing route.
type moRouteResource struct {
	ID        int    `json:"id"`
	ManagedBy string `json:"managed_by"`
	modispatch.RouteConfig
}

func toMORouteResource(stored admin.StoredSpec) (moRouteResource, error) {
	var cfg modispatch.RouteConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &cfg); err != nil {
		return moRouteResource{}, fmt.Errorf("MO route %d: stored spec is not valid JSON: %w", stored.Order, err)
	}
	cfg.Order = stored.Order
	return moRouteResource{ID: stored.Order, ManagedBy: "admin", RouteConfig: cfg}, nil
}

func moRouteFromConfig(config modispatch.RouteConfig) moRouteResource {
	return moRouteResource{ID: config.Order, ManagedBy: "config", RouteConfig: config}
}

func (h *Handler) configMORoute(order int) (moRouteResource, bool) {
	if h.deps.ConfigMORoutes == nil {
		return moRouteResource{}, false
	}
	for _, route := range h.deps.ConfigMORoutes() {
		if route.Order == order {
			return moRouteFromConfig(route), true
		}
	}
	return moRouteResource{}, false
}

func (h *Handler) listMORoutes(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.MORoutes.ListRoutes(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	configRoutes := []modispatch.RouteConfig{}
	if h.deps.ConfigMORoutes != nil {
		configRoutes = h.deps.ConfigMORoutes()
	}
	resources := make([]moRouteResource, 0, len(configRoutes)+len(stored))
	for _, route := range configRoutes {
		resources = append(resources, moRouteFromConfig(route))
	}
	for _, route := range stored {
		resource, err := toMORouteResource(route)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resources = append(resources, resource)
	}
	writeList(w, r, resources)
}

func (h *Handler) getMORoute(w http.ResponseWriter, r *http.Request) {
	order, err := strconv.Atoi(r.PathValue("order"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "route order must be an integer")
		return
	}
	stored, err := h.deps.MORoutes.GetRoute(r.Context(), order)
	if err != nil {
		if resource, ok := h.configMORoute(order); ok {
			writeJSON(w, http.StatusOK, resource)
			return
		}
		writeServiceError(w, err)
		return
	}
	resource, err := toMORouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

// putMORoute applies a decoded MO route at order and answers with the stored
// result; shared by create and update (PutRoute is an upsert).
func (h *Handler) putMORoute(w http.ResponseWriter, r *http.Request, order int, cfg modispatch.RouteConfig, status int) {
	cfg.Order = order
	specJSON, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.MORoutes.PutRoute(r.Context(), order, string(specJSON)); err != nil {
		writeServiceError(w, err)
		return
	}
	stored, err := h.deps.MORoutes.GetRoute(r.Context(), order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toMORouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, resource)
}

func (h *Handler) createMORoute(w http.ResponseWriter, r *http.Request) {
	var res moRouteResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putMORoute(w, r, res.Order, res.RouteConfig, http.StatusCreated)
}

func (h *Handler) updateMORoute(w http.ResponseWriter, r *http.Request) {
	order, err := strconv.Atoi(r.PathValue("order"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "route order must be an integer")
		return
	}
	stored, err := h.deps.MORoutes.GetRoute(r.Context(), order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	res, err := toMORouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putMORoute(w, r, order, res.RouteConfig, http.StatusOK)
}

func (h *Handler) deleteMORoute(w http.ResponseWriter, r *http.Request) {
	order, err := strconv.Atoi(r.PathValue("order"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "route order must be an integer")
		return
	}
	stored, err := h.deps.MORoutes.GetRoute(r.Context(), order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toMORouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.MORoutes.DeleteRoute(r.Context(), order); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}
