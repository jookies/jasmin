package restcompat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// legacyProbe stands in for the HTTP front door the REST facade wraps.
type legacyProbe struct{ hits []string }

func (p *legacyProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.hits = append(p.hits, r.URL.Path)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("legacy"))
}

func messageProbe(marker string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(marker + " " + r.Header.Get("Authorization")))
	})
}

func serveMessages(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, MessagePullPath+"?after=abc", nil)
	request.Header.Set("Authorization", "Bearer synmsg_token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// The endpoint rides the existing REST listener — both views of it — rather
// than getting a port of its own.
func TestMessagePullIsMountedOnBothRESTViews(t *testing.T) {
	legacy := &legacyProbe{}
	handlers, err := NewHandlers(legacy, WithMessagePull(func() http.Handler {
		return messageProbe("pulled")
	}))
	if err != nil {
		t.Fatal(err)
	}
	for name, view := range map[string]http.Handler{
		"combined": handlers.Combined, "daemon": handlers.Daemon,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := serveMessages(t, view)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			// The Bearer credential reaches the handler untouched: this path does
			// not go through the Basic-auth gate /secure/* uses.
			if recorder.Body.String() != "pulled Bearer synmsg_token" {
				t.Fatalf("body=%q", recorder.Body.String())
			}
		})
	}
	if len(legacy.hits) != 0 {
		t.Fatalf("the pull path fell through to the legacy handler: %v", legacy.hits)
	}
}

// A deployment that spools no messages must answer 404, not an empty page: an
// empty page means "no new messages" on this API, and a consumer polling a
// gateway that will never have any would wait forever.
func TestMessagePullAnswers404WhenTheDeploymentHasNoSpool(t *testing.T) {
	handlers, err := NewHandlers(&legacyProbe{}, WithMessagePull(func() http.Handler { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	recorder := serveMessages(t, handlers.Daemon)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Resource not found") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

// Without the option the path is not registered at all, so nothing about the
// existing listener changes.
func TestMessagePullUnregisteredWithoutTheOption(t *testing.T) {
	legacy := &legacyProbe{}
	handlers, err := NewHandlers(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if recorder := serveMessages(t, handlers.Combined); recorder.Body.String() != "legacy" {
		t.Fatalf("combined body=%q", recorder.Body.String())
	}
	if len(legacy.hits) != 1 || legacy.hits[0] != MessagePullPath {
		t.Fatalf("legacy hits=%v", legacy.hits)
	}
	if recorder := serveMessages(t, handlers.Daemon); recorder.Code != http.StatusNotFound {
		t.Fatalf("daemon status=%d", recorder.Code)
	}
}

// The resolver is called per request, not captured once: the spool that backs
// the endpoint is built after this listener.
func TestMessagePullResolvesPerRequest(t *testing.T) {
	var installed http.Handler
	handlers, err := NewHandlers(&legacyProbe{}, WithMessagePull(func() http.Handler { return installed }))
	if err != nil {
		t.Fatal(err)
	}
	if recorder := serveMessages(t, handlers.Daemon); recorder.Code != http.StatusNotFound {
		t.Fatalf("before install status=%d", recorder.Code)
	}
	installed = messageProbe("late")
	recorder := serveMessages(t, handlers.Daemon)
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Body.String(), "late") {
		t.Fatalf("after install status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

// The pull path must not shadow, or be shadowed by, the resources this facade
// already serves.
func TestMessagePullDoesNotDisturbTheExistingResources(t *testing.T) {
	legacy := &legacyProbe{}
	handlers, err := NewHandlers(legacy, WithMessagePull(func() http.Handler {
		return messageProbe("pulled")
	}))
	if err != nil {
		t.Fatal(err)
	}
	// /secure/* still demands its Basic credential.
	request := httptest.NewRequest(http.MethodGet, "/secure/balance", nil)
	recorder := httptest.NewRecorder()
	handlers.Daemon.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("/secure/balance status=%d", recorder.Code)
	}
	// And a path merely prefixed by the pull path is not captured by it.
	request = httptest.NewRequest(http.MethodGet, MessagePullPath+"/019428c1", nil)
	recorder = httptest.NewRecorder()
	handlers.Combined.ServeHTTP(recorder, request)
	if recorder.Body.String() != "legacy" {
		t.Fatalf("sub-path body=%q; the exact-match mount captured it", recorder.Body.String())
	}
}
