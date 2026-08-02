package adminweb

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

// interceptorResource is the flat REST shape of one interceptor. The identity
// is (direction, order); Refine needs a single scalar id, so it is the string
// "<direction>:<order>".
type interceptorResource struct {
	ID        string `json:"id"`
	Direction string `json:"direction"`
	outbound.InterceptorConfig
}

func interceptorID(direction admin.InterceptorDirection, order int) string {
	return fmt.Sprintf("%s:%d", direction, order)
}

// parseInterceptorID splits the composite id back into its parts.
func parseInterceptorID(id string) (admin.InterceptorDirection, int, error) {
	rawDirection, rawOrder, ok := strings.Cut(id, ":")
	if !ok {
		return "", 0, fmt.Errorf("interceptor id must be \"<direction>:<order>\", got %q", id)
	}
	direction := admin.InterceptorDirection(rawDirection)
	if direction != admin.InterceptMT && direction != admin.InterceptMO {
		return "", 0, fmt.Errorf("interceptor direction must be \"mt\" or \"mo\", got %q", rawDirection)
	}
	order, err := strconv.Atoi(rawOrder)
	if err != nil {
		return "", 0, fmt.Errorf("interceptor order must be an integer, got %q", rawOrder)
	}
	return direction, order, nil
}

func toInterceptorResource(direction admin.InterceptorDirection, stored admin.StoredSpec) (interceptorResource, error) {
	var cfg outbound.InterceptorConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &cfg); err != nil {
		return interceptorResource{}, fmt.Errorf("interceptor %s:%d: stored spec is not valid JSON: %w", direction, stored.Order, err)
	}
	cfg.Order = stored.Order
	return interceptorResource{
		ID:                interceptorID(direction, stored.Order),
		Direction:         string(direction),
		InterceptorConfig: cfg,
	}, nil
}

// requireInterceptors answers 404 when interceptor editing is disabled, so a
// deployment that never opted in exposes no trace of the capability.
func (h *Handler) requireInterceptors(w http.ResponseWriter) bool {
	if h.deps.Interceptors == nil {
		writeError(w, http.StatusNotFound, "interceptor editing is disabled (admin.allow_interceptor_editing)")
		return false
	}
	return true
}

// auditInterceptorChange records every mutation with the authenticated user.
// Interceptor scripts execute on this host, so these entries are the audit
// trail for what is effectively remote code execution.
func auditInterceptorChange(r *http.Request, action string, direction admin.InterceptorDirection, order int) {
	slog.Default().Warn("adminweb: interceptor "+action,
		"user", currentUser(r.Context()),
		"direction", string(direction),
		"order", order,
		"remote_addr", r.RemoteAddr)
}

func (h *Handler) listInterceptors(w http.ResponseWriter, r *http.Request) {
	if !h.requireInterceptors(w) {
		return
	}
	resources, err := h.collectInterceptors(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeList(w, r, resources)
}

func (h *Handler) getInterceptor(w http.ResponseWriter, r *http.Request) {
	if !h.requireInterceptors(w) {
		return
	}
	direction, order, err := parseInterceptorID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stored, err := h.deps.Interceptors.GetInterceptor(r.Context(), direction, order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toInterceptorResource(direction, stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

// putInterceptor applies one interceptor and answers with the stored result.
func (h *Handler) putInterceptor(w http.ResponseWriter, r *http.Request, direction admin.InterceptorDirection, order int, cfg outbound.InterceptorConfig, status int) {
	cfg.Order = order
	specJSON, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Interceptors.PutInterceptor(r.Context(), direction, order, string(specJSON)); err != nil {
		writeServiceError(w, err)
		return
	}
	auditInterceptorChange(r, "applied", direction, order)
	stored, err := h.deps.Interceptors.GetInterceptor(r.Context(), direction, order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toInterceptorResource(direction, stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, resource)
}

func (h *Handler) createInterceptor(w http.ResponseWriter, r *http.Request) {
	if !h.requireInterceptors(w) {
		return
	}
	var res interceptorResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	direction := admin.InterceptorDirection(res.Direction)
	if direction != admin.InterceptMT && direction != admin.InterceptMO {
		writeError(w, http.StatusBadRequest, "direction must be \"mt\" or \"mo\"")
		return
	}
	if strings.TrimSpace(res.PyCode) == "" {
		writeError(w, http.StatusBadRequest, "py_code is required")
		return
	}
	h.putInterceptor(w, r, direction, res.Order, res.InterceptorConfig, http.StatusCreated)
}

func (h *Handler) updateInterceptor(w http.ResponseWriter, r *http.Request) {
	if !h.requireInterceptors(w) {
		return
	}
	direction, order, err := parseInterceptorID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stored, err := h.deps.Interceptors.GetInterceptor(r.Context(), direction, order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	res, err := toInterceptorResource(direction, stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Direction and order are the identity; the path wins.
	h.putInterceptor(w, r, direction, order, res.InterceptorConfig, http.StatusOK)
}

func (h *Handler) deleteInterceptor(w http.ResponseWriter, r *http.Request) {
	if !h.requireInterceptors(w) {
		return
	}
	direction, order, err := parseInterceptorID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stored, err := h.deps.Interceptors.GetInterceptor(r.Context(), direction, order)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toInterceptorResource(direction, stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Interceptors.DeleteInterceptor(r.Context(), direction, order); err != nil {
		writeServiceError(w, err)
		return
	}
	auditInterceptorChange(r, "deleted", direction, order)
	writeJSON(w, http.StatusOK, resource)
}
