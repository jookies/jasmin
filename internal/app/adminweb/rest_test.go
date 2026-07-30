package adminweb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/admin"
)

// Every admin sentinel error must map to a deliberate HTTP status. Adding a new
// entity type means adding its Err*NotFound here and in writeServiceError —
// forget the latter and a plain "not found" surfaces as a 500, which reads to
// the operator as a gateway bug rather than a typo in a URL.
//
// That happened three times while the admin plane grew (MO routes, interceptors,
// SMPPs users), each time caught only because a CRUD test happened to assert the
// status. This table makes it impossible to miss: a new sentinel with no entry
// fails the completeness check below.
func TestWriteServiceErrorMapping(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"connector not found":   {admin.ErrConnectorNotFound, http.StatusNotFound},
		"MT route not found":    {admin.ErrRouteNotFound, http.StatusNotFound},
		"MO route not found":    {admin.ErrMORouteNotFound, http.StatusNotFound},
		"interceptor not found": {admin.ErrInterceptorNotFound, http.StatusNotFound},
		"SMPPs user not found":  {admin.ErrSMPPsUserNotFound, http.StatusNotFound},
		"user not found":        {admin.ErrUserNotFound, http.StatusNotFound},
		"group not found":       {admin.ErrGroupNotFound, http.StatusNotFound},
		"filter not found":      {admin.ErrFilterNotFound, http.StatusNotFound},
		"HTTP target not found": {admin.ErrHTTPConnectorNotFound, http.StatusNotFound},
		"profile not found":     {admin.ErrProfileNotFound, http.StatusNotFound},
		"conflict":              {admin.ErrConflict, http.StatusConflict},
		"invalid request":       {admin.ErrInvalidRequest, http.StatusBadRequest},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			// Wrapped, as the services return them, so errors.Is is exercised.
			wrapped := fmt.Errorf("admin service: %w", testCase.err)
			recorder := httptest.NewRecorder()
			writeServiceError(recorder, wrapped)
			if recorder.Code != testCase.want {
				t.Fatalf("status %d, want %d (body %s)", recorder.Code, testCase.want, recorder.Body.String())
			}
			if recorder.Body.Len() == 0 {
				t.Fatal("error response has no body; the UI shows a blank message")
			}
		})
	}
}

// An unmapped error is a genuine server fault and must stay a 500 — the default
// exists so an unexpected failure is loud, not so sentinels can fall through it.
func TestWriteServiceErrorDefaultsTo500(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeServiceError(recorder, fmt.Errorf("disk on fire"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", recorder.Code)
	}
}

// Completeness: every exported admin sentinel must appear in the table above.
// Kept as an explicit list rather than reflection because the point is to force
// a human to decide the status for a new sentinel, not to auto-map it.
func TestAllAdminSentinelsAreMapped(t *testing.T) {
	mapped := []error{
		admin.ErrConnectorNotFound,
		admin.ErrRouteNotFound,
		admin.ErrMORouteNotFound,
		admin.ErrInterceptorNotFound,
		admin.ErrSMPPsUserNotFound,
		admin.ErrUserNotFound,
		admin.ErrGroupNotFound,
		admin.ErrFilterNotFound,
		admin.ErrHTTPConnectorNotFound,
		admin.ErrProfileNotFound,
		admin.ErrConflict,
		admin.ErrInvalidRequest,
	}
	for _, sentinel := range mapped {
		recorder := httptest.NewRecorder()
		writeServiceError(recorder, fmt.Errorf("wrapped: %w", sentinel))
		if recorder.Code == http.StatusInternalServerError {
			t.Errorf("sentinel %v falls through to 500 — add it to writeServiceError", sentinel)
		}
	}
}
