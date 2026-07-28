package adminweb

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// bareHandler builds a Handler with fixed credentials and signing key, without
// services or routes — enough for the session/CSRF/middleware unit tests.
func bareHandler() *Handler {
	return &Handler{
		deps:         Deps{Username: "admin"},
		usernameHash: sha256.Sum256([]byte("admin")),
		passwordHash: sha256.Sum256([]byte("s3cret")),
		signingKey:   bytes.Repeat([]byte{0x42}, 32),
	}
}

func TestSessionRoundTrip(t *testing.T) {
	h := bareHandler()
	now := time.Unix(1_700_000_000, 0)
	rec := httptest.NewRecorder()
	h.issueSession(rec, "admin", now)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("issueSession set %d cookies", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie not HttpOnly/SameSite=Strict: %+v", cookie)
	}

	withCookie := func(c *http.Cookie) *http.Request {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(c)
		return req
	}

	if user, ok := h.verifySession(withCookie(cookie), now.Add(time.Hour)); !ok || user != "admin" {
		t.Fatalf("valid session: user=%q ok=%v", user, ok)
	}
	if _, ok := h.verifySession(withCookie(cookie), now.Add(sessionMaxAge+time.Minute)); ok {
		t.Fatal("expired session accepted")
	}
	tampered := *cookie
	tampered.Value = cookie.Value[:len(cookie.Value)-2] + "AA"
	if _, ok := h.verifySession(withCookie(&tampered), now.Add(time.Hour)); ok {
		t.Fatal("tampered session accepted")
	}
	other := bareHandler()
	other.signingKey = bytes.Repeat([]byte{0x99}, 32)
	if _, ok := other.verifySession(withCookie(cookie), now.Add(time.Hour)); ok {
		t.Fatal("session accepted under a different signing key")
	}
}

func TestCSRFHeader(t *testing.T) {
	h := bareHandler()
	withToken := func(token string) *http.Request {
		req := httptest.NewRequest("POST", "/x", nil)
		if token != "" {
			req.Header.Set(csrfHeader, token)
		}
		return req
	}
	if !h.checkCSRF(withToken(h.csrfToken("admin")), "admin") {
		t.Fatal("valid CSRF token rejected")
	}
	if h.checkCSRF(withToken(h.csrfToken("mallory")), "admin") {
		t.Fatal("cross-user CSRF token accepted")
	}
	if h.checkCSRF(withToken(""), "admin") {
		t.Fatal("missing CSRF token accepted")
	}
}

func TestHandleLogin(t *testing.T) {
	h := bareHandler()

	rec := httptest.NewRecorder()
	h.handleLogin(rec, httptest.NewRequest("POST", "/api/login",
		bytes.NewBufferString(`{"username":"admin","password":"wrong"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: code %d", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("cookie set on failed login")
	}

	rec2 := httptest.NewRecorder()
	h.handleLogin(rec2, httptest.NewRequest("POST", "/api/login",
		bytes.NewBufferString(`{"username":"admin","password":"s3cret"}`)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("good login: code %d body %s", rec2.Code, rec2.Body.String())
	}
	cookies := rec2.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie || cookies[0].Value == "" {
		t.Fatalf("good login did not set a session cookie: %+v", cookies)
	}
	if !bytes.Contains(rec2.Body.Bytes(), []byte("csrf_token")) {
		t.Fatalf("login response missing csrf_token: %s", rec2.Body.String())
	}
}

func TestRequireSession(t *testing.T) {
	h := bareHandler()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, currentUser(r.Context()))
	})
	guarded := h.requireSession(next)

	// Unauthenticated requests get 401 JSON — never a redirect (SPA owns nav).
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest("GET", "/api/session", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET: code %d", rec.Code)
	}

	issue := httptest.NewRecorder()
	h.issueSession(issue, "admin", time.Now())
	cookie := issue.Result().Cookies()[0]

	// Authenticated GET runs next with the user in context.
	rec2 := httptest.NewRecorder()
	authed := httptest.NewRequest("GET", "/api/session", nil)
	authed.AddCookie(cookie)
	guarded.ServeHTTP(rec2, authed)
	if rec2.Code != http.StatusOK || rec2.Body.String() != "admin" {
		t.Fatalf("auth GET: code %d body %q", rec2.Code, rec2.Body.String())
	}

	// Authenticated POST without the CSRF header is forbidden.
	rec3 := httptest.NewRecorder()
	post := httptest.NewRequest("POST", "/api/connectors", nil)
	post.AddCookie(cookie)
	guarded.ServeHTTP(rec3, post)
	if rec3.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF: code %d", rec3.Code)
	}

	// Authenticated POST with a valid CSRF header passes.
	rec4 := httptest.NewRecorder()
	postOK := httptest.NewRequest("POST", "/api/connectors", nil)
	postOK.AddCookie(cookie)
	postOK.Header.Set(csrfHeader, h.csrfToken("admin"))
	guarded.ServeHTTP(rec4, postOK)
	if rec4.Code != http.StatusOK {
		t.Fatalf("POST with CSRF: code %d", rec4.Code)
	}
}
