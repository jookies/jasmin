package adminweb

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (works with CGO_ENABLED=0)

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
)

// enableMessageConsumers rebuilds the handler with the pull-credential surface
// wired, over a real SQLite spool.
func enableMessageConsumers(f *webFixture) *msgspool.ConsumerService {
	f.t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		f.t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	f.t.Cleanup(func() { _ = db.Close() })
	spoolStore, err := storage.NewSQLiteMessageSpool(db)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = spoolStore.Init(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	spool, err := msgspool.NewService(spoolStore, msgspool.DefaultRetention(),
		func() time.Time { return now })
	if err != nil {
		f.t.Fatal(err)
	}
	consumers, err := msgspool.NewConsumerService(spool, spoolStore.Consumers(),
		func() time.Time { return now })
	if err != nil {
		f.t.Fatal(err)
	}
	service, err := admin.NewMessageConsumerService(consumers)
	if err != nil {
		f.t.Fatal(err)
	}
	f.rebuildHandler(func(d *Deps) { d.MessageConsumers = service })
	return consumers
}

// Without the dependency the resource must not exist at all.
func TestMessageConsumersAbsentWhenNotWired(t *testing.T) {
	f := newWebFixture(t)
	f.do("GET", "/api/message-consumers", "", http.StatusNotFound, nil)
	f.do("POST", "/api/message-consumers",
		`{"id":"smsget","scope":{"connectors":["partner-a-term"]}}`, http.StatusNotFound, nil)
}

func TestMessageConsumerConsoleCRUD(t *testing.T) {
	f := newWebFixture(t)
	consumers := enableMessageConsumers(f)

	var created messageConsumerResource
	f.do("POST", "/api/message-consumers",
		`{"id":"smsget","label":"downstream","scope":{"connectors":["partner-a-term"],"include_text":true}}`,
		http.StatusCreated, &created)
	if created.ID != "smsget" || created.Token == "" || created.TokenNotice == "" {
		t.Fatalf("created=%+v", created)
	}
	if created.LastUsedAt != nil {
		t.Fatalf("last_used_at=%v on a fresh credential", *created.LastUsedAt)
	}
	token := created.Token

	// It is the credential the pull API accepts, and the console is the only
	// place it was ever shown.
	if _, err := consumers.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("console-minted token rejected by the pull API: %v", err)
	}

	var fetched messageConsumerResource
	f.do("GET", "/api/message-consumers/smsget", "", http.StatusOK, &fetched)
	if fetched.Token != "" || fetched.TokenNotice != "" {
		t.Fatalf("a read returned the token: %+v", fetched)
	}
	if !fetched.Scope.IncludeText || len(fetched.Scope.Connectors) != 1 {
		t.Fatalf("scope=%+v", fetched.Scope)
	}

	var listed []messageConsumerResource
	f.do("GET", "/api/message-consumers", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].Token != "" {
		t.Fatalf("list=%+v", listed)
	}

	// A PATCH that omits a field keeps it, and narrowing does not invalidate
	// the token — otherwise nobody would narrow one.
	var narrowed messageConsumerResource
	f.do("PATCH", "/api/message-consumers/smsget",
		`{"scope":{"connectors":["partner-b-term"],"include_text":false}}`, http.StatusOK, &narrowed)
	if narrowed.Label != "downstream" {
		t.Fatalf("PATCH dropped the label: %+v", narrowed)
	}
	if narrowed.Scope.IncludeText || narrowed.Scope.Connectors[0] != "partner-b-term" {
		t.Fatalf("scope=%+v", narrowed.Scope)
	}
	consumer, err := consumers.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("narrowing invalidated the token: %v", err)
	}
	if consumer.Scope.IncludeText {
		t.Fatal("the narrowed scope did not reach the pull API")
	}

	var revoked messageConsumerResource
	f.do("POST", "/api/message-consumers/smsget/revoke", "", http.StatusOK, &revoked)
	if !revoked.Revoked {
		t.Fatalf("revoked=%+v", revoked)
	}
	if _, err = consumers.Authenticate(context.Background(), token); err == nil {
		t.Fatal("a revoked credential still authenticated")
	}
	f.do("POST", "/api/message-consumers/smsget/unrevoke", "", http.StatusOK, &revoked)
	if revoked.Revoked {
		t.Fatalf("unrevoked=%+v", revoked)
	}

	f.do("DELETE", "/api/message-consumers/smsget", "", http.StatusOK, nil)
	f.do("GET", "/api/message-consumers/smsget", "", http.StatusNotFound, nil)
}

func TestMessageConsumerConsoleRefusesAnEmptyScope(t *testing.T) {
	f := newWebFixture(t)
	enableMessageConsumers(f)
	f.do("POST", "/api/message-consumers", `{"id":"wide","scope":{"connectors":[]}}`,
		http.StatusBadRequest, nil)
	f.do("POST", "/api/message-consumers", `{"id":"bad id!","scope":{"connectors":["a"]}}`,
		http.StatusBadRequest, nil)
}

// The console session is what authorizes minting a credential; an unauthorized
// request must not reach the service at all.
func TestMessageConsumerConsoleRequiresASession(t *testing.T) {
	f := newWebFixture(t)
	enableMessageConsumers(f)
	request, err := http.NewRequest("GET", "/api/message-consumers", nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, strings.TrimSpace(recorder.Body.String()))
	}
}

// Read activity comes from the access audit, so a token that has been used must
// be distinguishable from one that was issued and forgotten — that difference is
// the whole reason the column exists.
func TestMessageConsumerListReportsReadActivity(t *testing.T) {
	f := newWebFixture(t)
	consumers := enableMessageConsumers(f)

	var created messageConsumerResource
	f.do("POST", "/api/message-consumers",
		`{"id":"reader","scope":{"connectors":["partner-a-term"],"include_text":true}}`,
		http.StatusCreated, &created)
	if created.Token == "" {
		t.Fatal("create did not return a token")
	}

	// Before any read: present, but reporting nothing.
	var before []messageConsumerResource
	f.do("GET", "/api/message-consumers", "", http.StatusOK, &before)
	if len(before) != 1 {
		t.Fatalf("want 1 consumer, got %d", len(before))
	}
	if before[0].Reads == nil || *before[0].Reads != 0 {
		t.Fatalf("want zero reads before any pull, got %v", before[0].Reads)
	}
	if before[0].LastReadAt != nil {
		t.Fatalf("want no last-read before any pull, got %v", *before[0].LastReadAt)
	}

	// One pull through the real consumer service, which is what writes the audit
	// row. Driving it through the service rather than stubbing the audit is the
	// point: a handler that reported activity nobody recorded would pass a
	// stubbed test and lie in production.
	consumer, err := consumers.Authenticate(context.Background(), created.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if _, err = consumers.Page(context.Background(), consumer, msgspool.PullRequest{Limit: 10}); err != nil {
		t.Fatalf("page: %v", err)
	}

	var after []messageConsumerResource
	f.do("GET", "/api/message-consumers", "", http.StatusOK, &after)
	if len(after) != 1 {
		t.Fatalf("want 1 consumer, got %d", len(after))
	}
	if after[0].Reads == nil || *after[0].Reads == 0 {
		t.Fatalf("want the pull counted as a read, got %v", after[0].Reads)
	}
	if after[0].LastReadAt == nil {
		t.Fatal("want a last-read timestamp after a pull")
	}
}
