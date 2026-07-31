package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (works with CGO_ENABLED=0)

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
)

// newTestMessageConsumers builds the admin surface over a real SQLite spool.
// The store is real rather than faked because the properties worth asserting
// here — the token is stored hashed, a read never returns it — are properties
// of what was persisted.
func newTestMessageConsumers(t *testing.T) (*MessageConsumerService, *msgspool.ConsumerService) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	spoolStore, err := storage.NewSQLiteMessageSpool(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = spoolStore.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	spool, err := msgspool.NewService(spoolStore, msgspool.DefaultRetention(),
		func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	consumers, err := msgspool.NewConsumerService(spool, spoolStore.Consumers(),
		func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewMessageConsumerService(consumers)
	if err != nil {
		t.Fatal(err)
	}
	return service, consumers
}

func newMessageConsumerHandler(t *testing.T, token string) (*Handler, *MessageConsumerService) {
	t.Helper()
	service, _, _ := newTestService(t)
	consumers, _ := newTestMessageConsumers(t)
	handler, err := NewHandler(service, nil, nil, token, WithMessageConsumers(consumers))
	if err != nil {
		t.Fatal(err)
	}
	return handler, consumers
}

// Without the dependency the resource must not exist at all: "this gateway does
// not spool messages" and "it has no consumers" are different answers, and only
// the second invites creating one that could never work.
func TestMessageConsumersAbsentWhenNotWired(t *testing.T) {
	handler, _ := newTestHandler(t, "secret")
	for _, path := range []string{"/admin/message-consumers", "/admin/message-consumers/smsget"} {
		if rec := doAdmin(t, handler, http.MethodGet, path, "secret", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d want 404", path, rec.Code)
		}
	}
}

func TestMessageConsumerAdminCRUD(t *testing.T) {
	handler, _ := newMessageConsumerHandler(t, "secret")

	create := map[string]any{
		"id":    "smsget",
		"label": "smsget-api-gateway production",
		"scope": map[string]any{"connectors": []string{"partner-a-term"}, "include_text": true},
	}
	rec := doAdmin(t, handler, http.MethodPost, "/admin/message-consumers", "secret", create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	token, _ := created["token"].(string)
	if !strings.HasPrefix(token, msgspool.ConsumerTokenPrefix) {
		t.Fatalf("create response token=%q", token)
	}
	if notice, _ := created["token_notice"].(string); notice == "" {
		t.Fatal("no one-time notice accompanied the token")
	}
	if created["last_used_at"] != nil {
		t.Fatalf("last_used_at=%v on a fresh credential", created["last_used_at"])
	}

	// Every later read must be tokenless. This is the property that makes a
	// leaked admin transcript not a leaked message credential.
	for _, path := range []string{"/admin/message-consumers", "/admin/message-consumers/smsget"} {
		rec = doAdmin(t, handler, http.MethodGet, path, "secret", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), token) {
			t.Fatalf("%s returned the token: %s", path, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"token"`) {
			t.Fatalf("%s has a token field: %s", path, rec.Body.String())
		}
	}

	// Duplicate id conflicts rather than silently re-minting, which would
	// invalidate the credential the application is already using.
	if rec = doAdmin(t, handler, http.MethodPost, "/admin/message-consumers", "secret",
		create); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", rec.Code, rec.Body.String())
	}

	// An empty scope must be refused: it would be a credential that restricts
	// nothing at the one layer that restricts anything.
	wide := map[string]any{"id": "wide", "scope": map[string]any{"connectors": []string{}}}
	if rec = doAdmin(t, handler, http.MethodPost, "/admin/message-consumers", "secret",
		wide); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty scope status=%d body=%s", rec.Code, rec.Body.String())
	}

	update := map[string]any{
		"label": "narrowed",
		"scope": map[string]any{"connectors": []string{"partner-b-term"}, "include_text": false},
	}
	rec = doAdmin(t, handler, http.MethodPut, "/admin/message-consumers/smsget", "secret", update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated["label"] != "narrowed" {
		t.Fatalf("updated=%v", updated)
	}

	rec = doAdmin(t, handler, http.MethodPost, "/admin/message-consumers/smsget/revoke", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if revoked, _ := updated["revoked"].(bool); !revoked {
		t.Fatalf("revoked=%v", updated)
	}
	rec = doAdmin(t, handler, http.MethodPost, "/admin/message-consumers/smsget/unrevoke", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("unrevoke status=%d body=%s", rec.Code, rec.Body.String())
	}

	if rec = doAdmin(t, handler, http.MethodDelete, "/admin/message-consumers/smsget", "secret",
		nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = doAdmin(t, handler, http.MethodGet, "/admin/message-consumers/smsget", "secret",
		nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d", rec.Code)
	}
}

// The consumer surface is behind the same bearer token as the rest of /admin:
// creating a read credential is an administrative act.
func TestMessageConsumerRoutesRequireTheAdminToken(t *testing.T) {
	handler, _ := newMessageConsumerHandler(t, "secret")
	if rec := doAdmin(t, handler, http.MethodGet, "/admin/message-consumers", "",
		nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

// The credential the admin API mints is the credential the pull API accepts —
// checked end to end rather than assumed, because they are minted and verified
// in different packages.
func TestMessageConsumerTokenFromAdminAuthenticatesOnThePullAPI(t *testing.T) {
	service, consumers := newTestMessageConsumers(t)
	ctx := context.Background()
	_, token, err := service.CreateConsumer(ctx, "smsget", "",
		msgspool.Scope{Connectors: []string{"partner-a-term"}})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := consumers.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.ID != "smsget" {
		t.Fatalf("consumer=%+v", consumer)
	}
}
