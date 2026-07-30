package adminweb

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "synevyr_admin_session"
	sessionMaxAge = 12 * time.Hour
)

// issueSession sets a signed session cookie for username. The value is
// base64(payload).base64(HMAC(key, payload)) with payload = "username|issuedUnix";
// the cookie is HttpOnly and SameSite=Strict (and Secure under TLS), so it is not
// readable by scripts and is not sent on cross-site requests.
func (h *Handler) issueSession(w http.ResponseWriter, username string, now time.Time) {
	payload := username + "|" + strconv.FormatInt(now.Unix(), 10)
	value := encodeSegment([]byte(payload)) + "." + encodeSegment(h.sign([]byte(payload)))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.deps.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionMaxAge.Seconds()),
	})
}

// verifySession returns the authenticated username if the request carries a valid,
// unexpired, correctly-signed session cookie.
func (h *Handler) verifySession(r *http.Request, now time.Time) (string, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	body, sig, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return "", false
	}
	payload, err := decodeSegment(body)
	if err != nil {
		return "", false
	}
	want, err := decodeSegment(sig)
	if err != nil {
		return "", false
	}
	if !hmac.Equal(want, h.sign(payload)) {
		return "", false
	}
	username, issued, ok := strings.Cut(string(payload), "|")
	if !ok {
		return "", false
	}
	issuedUnix, err := strconv.ParseInt(issued, 10, 64)
	if err != nil {
		return "", false
	}
	if now.Sub(time.Unix(issuedUnix, 0)) > sessionMaxAge {
		return "", false
	}
	return username, true
}

// clearSession expires the session cookie.
func (h *Handler) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.deps.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// sign is HMAC-SHA256 over data with the per-process signing key.
func (h *Handler) sign(data []byte) []byte {
	mac := hmac.New(sha256.New, h.signingKey)
	mac.Write(data)
	return mac.Sum(nil)
}

func encodeSegment(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decodeSegment(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
