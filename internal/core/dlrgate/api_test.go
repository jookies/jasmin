package dlrgate

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// stubKeys is the runtime user directory's key index.
type stubKeys map[string]struct{ username, digest string }

func (s stubKeys) ResolveDLRGateKey(keyID string) (string, string, bool) {
	key, ok := s[keyID]
	return key.username, key.digest, ok
}

type apiFixture struct {
	handler  *APIHandler
	registry *Registry
	keyID    string
	token    string
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	registry, err := NewRegistry(client, "")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	credential, err := NewCredential()
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	keys := stubKeys{credential.KeyID: {username: "demoesme", digest: credential.TokenSHA256}}
	handler := NewAPIHandler(registry, keys, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if handler == nil {
		t.Fatal("NewAPIHandler returned nil")
	}
	return &apiFixture{handler: handler, registry: registry, keyID: credential.KeyID, token: credential.Token}
}

func (f *apiFixture) do(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, APIPathPrefix+path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAPIOpensAndListsAWindow(t *testing.T) {
	f := newAPIFixture(t)

	response := f.do(t, http.MethodPost, f.keyID, f.token, `{"msisdn":"+380930242105","ttl_seconds":600,"note":"otp"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST code = %d, body %s", response.Code, response.Body.String())
	}
	var created apiEntry
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.MSISDN != "380930242105" {
		t.Fatalf("msisdn = %q, want the normalized digits", created.MSISDN)
	}
	if created.ExpiresInSeconds <= 0 || created.ExpiresInSeconds > 600 {
		t.Fatalf("expires_in_seconds = %d, want it inside the requested window", created.ExpiresInSeconds)
	}

	// The window must be owned, so it only ever counts for this user.
	entry, err := f.registry.Get(context.Background(), "380930242105")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.Owner != "demoesme" {
		t.Fatalf("owner = %q, want the authenticated user", entry.Owner)
	}

	response = f.do(t, http.MethodGet, f.keyID, f.token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET code = %d, body %s", response.Code, response.Body.String())
	}
	var listed []apiEntry
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].MSISDN != "380930242105" {
		t.Fatalf("list = %+v, want the one window", listed)
	}

	response = f.do(t, http.MethodDelete, f.keyID+"/380930242105", f.token, "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE code = %d, body %s", response.Code, response.Body.String())
	}
	if _, err := f.registry.Get(context.Background(), "380930242105"); err == nil {
		t.Fatal("the window is still open after DELETE")
	}
}

func TestAPIRejectsBadCredentials(t *testing.T) {
	f := newAPIFixture(t)
	body := `{"msisdn":"380930242105"}`

	cases := []struct {
		name  string
		path  string
		token string
	}{
		{"no token", f.keyID, ""},
		{"wrong token", f.keyID, TokenPrefix + "not-the-right-secret"},
		{"unknown key id", strings.Repeat("a", 32), f.token},
		{"malformed key id", "not-a-key", f.token},
		{"another user's token shape", f.keyID, "Bearer-less"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := f.do(t, http.MethodPost, testCase.path, testCase.token, body)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401 (body %s)", response.Code, response.Body.String())
			}
		})
	}
}

// TestAPICannotTouchAnotherUsersWindow is the isolation boundary: a gated user
// must not be able to close, or even discover, a window another user opened.
func TestAPICannotTouchAnotherUsersWindow(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()

	if _, err := f.registry.Add(ctx, "380930242105", AddOptions{
		TTL: time.Minute, AddedBy: "otherpartner", Owner: "otherpartner",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	response := f.do(t, http.MethodDelete, f.keyID+"/380930242105", f.token, "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("DELETE code = %d, want 404", response.Code)
	}
	// And it is still open.
	entry, err := f.registry.Get(ctx, "380930242105")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.Owner != "otherpartner" {
		t.Fatalf("owner = %q, want it untouched", entry.Owner)
	}

	response = f.do(t, http.MethodGet, f.keyID, f.token, "")
	var listed []apiEntry
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("list = %+v, want another user's window to be invisible", listed)
	}
}

func TestAPIRejectsBadInput(t *testing.T) {
	f := newAPIFixture(t)

	if response := f.do(t, http.MethodPost, f.keyID, f.token, `{"msisdn":"undefined"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric msisdn code = %d, want 400", response.Code)
	}
	if response := f.do(t, http.MethodPost, f.keyID, f.token, `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("missing msisdn code = %d, want 400", response.Code)
	}
	if response := f.do(t, http.MethodPost, f.keyID, f.token, `not json`); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed body code = %d, want 400", response.Code)
	}
	if response := f.do(t, http.MethodPut, f.keyID, f.token, `{}`); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT code = %d, want 405", response.Code)
	}
}

func TestAPIClampsTTL(t *testing.T) {
	f := newAPIFixture(t)
	response := f.do(t, http.MethodPost, f.keyID, f.token, `{"msisdn":"380930242105","ttl_seconds":86400}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("code = %d, body %s", response.Code, response.Body.String())
	}
	var created apiEntry
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ExpiresInSeconds > int64(MaxTTL/time.Second) {
		t.Fatalf("expires_in_seconds = %d, want it clamped to %s", created.ExpiresInSeconds, MaxTTL)
	}
}

func TestCredentialMintingAndDigest(t *testing.T) {
	first, err := NewCredential()
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	second, err := NewCredential()
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	if first.KeyID == second.KeyID || first.Token == second.Token {
		t.Fatal("two credentials collided")
	}
	if !ValidKeyID(first.KeyID) {
		t.Fatalf("minted key id %q is not accepted by the router", first.KeyID)
	}
	if !strings.HasPrefix(first.Token, TokenPrefix) {
		t.Fatalf("token %q lacks the recognisable prefix", first.Token)
	}
	if !TokenMatches(first.Token, first.TokenSHA256) {
		t.Fatal("a minted token does not match its own digest")
	}
	if TokenMatches(second.Token, first.TokenSHA256) {
		t.Fatal("a different token matched the digest")
	}
	// An empty stored digest must never authenticate, or clearing a credential
	// would leave the endpoint open to anyone presenting nothing.
	if TokenMatches("", "") || TokenMatches(first.Token, "") {
		t.Fatal("an empty digest authenticated")
	}
}
