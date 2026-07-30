package adminweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

// routeResource is the flat REST shape of one MT route: the outbound
// RouteConfig fields inline plus the Refine identity. A route's order is its
// identity (matching RouteService), so id == order and changing the order of
// an existing route is not supported through the UI (delete + recreate).
type routeResource struct {
	ID        int    `json:"id"`
	ManagedBy string `json:"managed_by"`
	outbound.RouteConfig
}

func toRouteResource(stored admin.StoredRoute) (routeResource, error) {
	var cfg outbound.RouteConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &cfg); err != nil {
		return routeResource{}, fmt.Errorf("route %d: stored spec is not valid JSON: %w", stored.Order, err)
	}
	cfg.Order = stored.Order
	return routeResource{ID: stored.Order, ManagedBy: "admin", RouteConfig: cfg}, nil
}

func routeFromConfig(config outbound.RouteConfig) routeResource {
	return routeResource{ID: config.Order, ManagedBy: "config", RouteConfig: config}
}

func (h *Handler) configRoute(order int) (routeResource, bool) {
	if h.deps.ConfigRoutes == nil {
		return routeResource{}, false
	}
	for _, route := range h.deps.ConfigRoutes() {
		if route.Order == order {
			return routeFromConfig(route), true
		}
	}
	return routeResource{}, false
}

func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.Routes.ListRoutes(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	configRoutes := []outbound.RouteConfig{}
	if h.deps.ConfigRoutes != nil {
		configRoutes = h.deps.ConfigRoutes()
	}
	resources := make([]routeResource, 0, len(configRoutes)+len(stored))
	for _, route := range configRoutes {
		resources = append(resources, routeFromConfig(route))
	}
	for _, route := range stored {
		resource, err := toRouteResource(route)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resources = append(resources, resource)
	}
	writeList(w, r, resources)
}

func routeOrder(r *http.Request) (int, error) {
	return strconv.Atoi(r.PathValue("order"))
}

func (h *Handler) getRoute(w http.ResponseWriter, r *http.Request) {
	order, err := routeOrder(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "route order must be an integer")
		return
	}
	stored, err := h.deps.Routes.GetRoute(r.Context(), order)
	if err != nil {
		if resource, ok := h.configRoute(order); ok {
			writeJSON(w, http.StatusOK, resource)
			return
		}
		writeServiceError(w, err)
		return
	}
	resource, err := toRouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

// putRoute applies a decoded route at the given order and answers with the
// stored result; shared by create and update (PutRoute is an upsert).
func (h *Handler) putRoute(w http.ResponseWriter, r *http.Request, order int, cfg outbound.RouteConfig, status int) {
	cfg.Order = order
	specJSON, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Routes.PutRoute(r.Context(), order, string(specJSON)); err != nil {
		writeServiceError(w, err)
		return
	}
	stored, err := h.deps.Routes.GetRoute(r.Context(), order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toRouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, resource)
}

func (h *Handler) createRoute(w http.ResponseWriter, r *http.Request) {
	var res routeResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putRoute(w, r, res.Order, res.RouteConfig, http.StatusCreated)
}

func (h *Handler) updateRoute(w http.ResponseWriter, r *http.Request) {
	order, err := routeOrder(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "route order must be an integer")
		return
	}
	stored, err := h.deps.Routes.GetRoute(r.Context(), order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	res, err := toRouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	h.putRoute(w, r, order, res.RouteConfig, http.StatusOK)
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	order, err := routeOrder(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "route order must be an integer")
		return
	}
	stored, err := h.deps.Routes.GetRoute(r.Context(), order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toRouteResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Routes.DeleteRoute(r.Context(), order); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}
