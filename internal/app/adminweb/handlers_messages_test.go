package adminweb

import (
	"context"
	"database/sql"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (works with CGO_ENABLED=0)

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
)

var messagesFixtureNow = time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)

// enableMessages wires the operator read path over a real SQLite spool and
// seeds one message, so the assertions run against the store's own
// authorization and redaction rather than a stub that could agree with the
// handler while the real one disagrees.
func enableMessages(f *webFixture) *msgspool.Service {
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
	spool, err := msgspool.NewService(spoolStore, msgspool.DefaultRetention(),
		func() time.Time { return messagesFixtureNow })
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err = spool.Put(context.Background(), msgspool.Message{
		MessageID:   "msg-1",
		ConnectorID: "partner-a-term",
		UserID:      "smppuser",
		SourceAddr:  "NETFLIX",
		DestAddr:    "380671234567",
		Text:        "code 63125",
		Raw:         []byte("code 63125"),
		Encoding:    "utf-8",
		Parts:       1,
		Verdict:     msgspool.Verdict{Accept: true, Stat: "DELIVRD"},
		ReceivedAt:  messagesFixtureNow,
	}); err != nil {
		f.t.Fatalf("seed spool: %v", err)
	}
	f.rebuildHandler(func(d *Deps) { d.Messages = spool })
	return spool
}

// A gateway that spools nothing must expose no trace of the resource: an empty
// list would read as "no traffic yet" and send an operator hunting for messages
// that were never stored here.
func TestMessagesAbsentWhenNotWired(t *testing.T) {
	f := newWebFixture(t)
	f.do("GET", "/api/messages", "", http.StatusNotFound, nil)
	f.do("GET", "/api/messages/msg-1", "", http.StatusNotFound, nil)
}

// The listing is metadata. Content must not ride along in a field the UI merely
// chose not to render — that is what keeps the reveal audit meaningful.
func TestMessagesListOmitsContent(t *testing.T) {
	f := newWebFixture(t)
	enableMessages(f)

	var page messagePageResource
	f.do("GET", "/api/messages", "", http.StatusOK, &page)
	if len(page.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(page.Messages))
	}
	got := page.Messages[0]
	if got.MessageID != "msg-1" || got.DestAddr != "380671234567" {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.VerdictStat != "DELIVRD" {
		t.Fatalf("want verdict DELIVRD, got %q", got.VerdictStat)
	}
	if got.Text != "" || got.RawHex != "" {
		t.Fatalf("listing leaked content: text=%q raw=%q", got.Text, got.RawHex)
	}
}

// Opening one message is the deliberate, audited act, and it is the only thing
// that returns content.
func TestMessageRevealReturnsContent(t *testing.T) {
	f := newWebFixture(t)
	enableMessages(f)

	var got messageResource
	f.do("GET", "/api/messages/msg-1", "", http.StatusOK, &got)
	if got.Text != "code 63125" {
		t.Fatalf("want revealed text, got %q", got.Text)
	}
	if want := hex.EncodeToString([]byte("code 63125")); got.RawHex != want {
		t.Fatalf("want raw hex %q, got %q", want, got.RawHex)
	}
}

// include_content=true is the explicit opt-in the reveal audit hangs on; without
// it the same query stays metadata-only.
func TestMessagesListIncludeContentOptIn(t *testing.T) {
	f := newWebFixture(t)
	enableMessages(f)

	var page messagePageResource
	f.do("GET", "/api/messages?include_content=true", "", http.StatusOK, &page)
	if len(page.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(page.Messages))
	}
	if page.Messages[0].Text != "code 63125" {
		t.Fatalf("want content with include_content=true, got %q", page.Messages[0].Text)
	}
}

func TestMessagesRejectBadPaging(t *testing.T) {
	f := newWebFixture(t)
	enableMessages(f)

	f.do("GET", "/api/messages?limit=abc", "", http.StatusBadRequest, nil)
	f.do("GET", "/api/messages?limit=0", "", http.StatusBadRequest, nil)
	f.do("GET", "/api/messages?limit=99999", "", http.StatusBadRequest, nil)
	f.do("GET", "/api/messages?received_from=yesterday", "", http.StatusBadRequest, nil)
}

func TestMessageRevealUnknownID(t *testing.T) {
	f := newWebFixture(t)
	enableMessages(f)

	f.do("GET", "/api/messages/nope", "", http.StatusNotFound, nil)
}

// The console lists newest first. An operator opening this page is nearly
// always asking about the message that just arrived, and making them page to
// the end to find it is the difference between the page being useful during an
// incident and not. The partner pull API keeps the opposite order on purpose.
func TestMessagesListIsNewestFirst(t *testing.T) {
	f := newWebFixture(t)
	spool := enableMessages(f)

	for _, id := range []string{"msg-2", "msg-3"} {
		if _, err := spool.Put(context.Background(), msgspool.Message{
			MessageID:   id,
			ConnectorID: "partner-a-term",
			UserID:      "smppuser",
			DestAddr:    "380671234567",
			Text:        "later " + id,
			Encoding:    "utf-8",
			Parts:       1,
			Verdict:     msgspool.Verdict{Accept: true, Stat: "DELIVRD"},
			ReceivedAt:  messagesFixtureNow,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	var page messagePageResource
	f.do("GET", "/api/messages", "", http.StatusOK, &page)
	if len(page.Messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(page.Messages))
	}
	// msg-1 was seeded first by enableMessages, so newest-first puts it last.
	if got := page.Messages[0].MessageID; got != "msg-3" {
		t.Errorf("want newest (msg-3) first, got %q", got)
	}
	if got := page.Messages[2].MessageID; got != "msg-1" {
		t.Errorf("want oldest (msg-1) last, got %q", got)
	}

	// The ascending order the pull API relies on stays reachable.
	var oldest messagePageResource
	f.do("GET", "/api/messages?oldest_first=true", "", http.StatusOK, &oldest)
	if got := oldest.Messages[0].MessageID; got != "msg-1" {
		t.Errorf("oldest_first=true should start at msg-1, got %q", got)
	}
}
