package adminweb

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/pumpitspace/synevyr/internal/app/admin"
)

// The JSON shapes here follow the Refine simple-rest data-provider contract:
// lists are plain arrays windowed by _start/_end with the full count in
// X-Total-Count; errors carry a "message" field the UI surfaces inline.

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}

// writeServiceError maps an admin service error to an HTTP status, mirroring
// the /admin JSON API so both surfaces speak the same failure language.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrConnectorNotFound),
		errors.Is(err, admin.ErrRouteNotFound),
		errors.Is(err, admin.ErrMORouteNotFound),
		errors.Is(err, admin.ErrInterceptorNotFound),
		errors.Is(err, admin.ErrSMPPsUserNotFound),
		errors.Is(err, admin.ErrUserNotFound),
		errors.Is(err, admin.ErrGroupNotFound),
		errors.Is(err, admin.ErrFilterNotFound),
		errors.Is(err, admin.ErrHTTPConnectorNotFound),
		errors.Is(err, admin.ErrProfileNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, admin.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, admin.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func decodeBody(r *http.Request, target any) error {
	return json.NewDecoder(r.Body).Decode(target)
}

// writeList windows items by the _start/_end query parameters and reports the
// full count in X-Total-Count. Sorting/filtering params are ignored — the
// datasets are small and already deterministically ordered.
func writeList[T any](w http.ResponseWriter, r *http.Request, items []T) {
	total := len(items)
	start, end := 0, total
	if v, err := strconv.Atoi(r.URL.Query().Get("_start")); err == nil {
		start = min(max(v, 0), total)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("_end")); err == nil {
		end = min(max(v, start), total)
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, items[start:end])
}
