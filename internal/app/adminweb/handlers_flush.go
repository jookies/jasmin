package adminweb

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/pumpitspace/synevyr/internal/app/admin"
)

func (h *Handler) flushRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.deps.Routes.ListRoutes(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var flushErrors []error
	deleted := 0
	for _, route := range routes {
		if err := h.deps.Routes.DeleteRoute(r.Context(), route.Order); err != nil {
			flushErrors = append(flushErrors, fmt.Errorf("route %d: %w", route.Order, err))
			continue
		}
		deleted++
	}
	writeFlushResult(w, deleted, flushErrors)
}

func (h *Handler) flushMORoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.deps.MORoutes.ListRoutes(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var flushErrors []error
	deleted := 0
	for _, route := range routes {
		if err := h.deps.MORoutes.DeleteRoute(r.Context(), route.Order); err != nil {
			flushErrors = append(flushErrors, fmt.Errorf("MO route %d: %w", route.Order, err))
			continue
		}
		deleted++
	}
	writeFlushResult(w, deleted, flushErrors)
}

func (h *Handler) flushInterceptors(w http.ResponseWriter, r *http.Request) {
	if h.deps.Interceptors == nil {
		writeError(w, http.StatusNotFound, "interceptor editing is disabled")
		return
	}
	direction := admin.InterceptorDirection(r.PathValue("direction"))
	interceptors, err := h.deps.Interceptors.ListInterceptors(r.Context(), direction)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var flushErrors []error
	deleted := 0
	for _, interceptor := range interceptors {
		if err := h.deps.Interceptors.DeleteInterceptor(r.Context(), direction, interceptor.Order); err != nil {
			flushErrors = append(flushErrors, fmt.Errorf("%s interceptor %d: %w", direction, interceptor.Order, err))
			continue
		}
		deleted++
	}
	writeFlushResult(w, deleted, flushErrors)
}

func writeFlushResult(w http.ResponseWriter, deleted int, flushErrors []error) {
	if err := errors.Join(flushErrors...); err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("deleted %d resources; some deletions failed: %v", deleted, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}
