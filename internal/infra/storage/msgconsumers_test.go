package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (works with CGO_ENABLED=0)

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// The pull API's storage contract, exercised against both backends by the same
// test bodies.
//
// It is one suite rather than two because the properties being proved are
// security properties — a scope that is compiled into SQL, a cursor that hands
// back a mutated row, content that is masked in the projection — and a
// property enforced in PostgreSQL but not in SQLite is a leak that only shows
// up in production. SQLite runs unconditionally; PostgreSQL runs when
// TEST_POSTGRES_DSN is set, following this package's convention.

// pullBackend is one store pair plus a prefix that keeps a shared PostgreSQL
// database usable by concurrent runs.
type pullBackend struct {
	name      string
	spool     msgspool.Repository
	consumers msgspool.ConsumerRepository
	prefix    string
}

func pullBackends(t *testing.T) []pullBackend {
	t.Helper()
	backends := []pullBackend{newSQLitePullBackend(t)}
	if backend, ok := newPostgresPullBackend(t); ok {
		backends = append(backends, backend)
	}
	return backends
}

func newSQLitePullBackend(t *testing.T) pullBackend {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	spool, err := NewSQLiteMessageSpool(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = spool.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pullBackend{name: "sqlite", spool: spool, consumers: spool.Consumers()}
}

// newPostgresPullBackend opens the shared database and scopes every row this
// suite writes behind a unique prefix, so it can run beside anything else using
// the same instance and can clean up without touching another run's rows.
func newPostgresPullBackend(t *testing.T) (pullBackend, bool) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		return pullBackend{}, false
	}
	ctx := context.Background()
	spool, err := OpenPostgresMessageSpool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = spool.Close() })
	if err = spool.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = spool.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	prefix := "pull-test-" + time.Now().UTC().Format("20060102150405.000000000") + "-"
	t.Cleanup(func() {
		background := context.Background()
		_, _ = spool.db.ExecContext(background,
			`DELETE FROM message_spool WHERE message_id LIKE $1`, prefix+"%")
		_, _ = spool.db.ExecContext(background,
			`DELETE FROM message_spool_consumers WHERE consumer_id LIKE $1`, prefix+"%")
		_, _ = spool.db.ExecContext(background,
			`DELETE FROM message_spool_access_audit WHERE subject LIKE $1`, "consumer:"+prefix+"%")
	})
	return pullBackend{name: "postgres", spool: spool, consumers: spool.Consumers(), prefix: prefix}, true
}

// newPullService wires the audited boundary and the consumer service over one
// backend, at a fixed clock.
func newPullService(t *testing.T, backend pullBackend, now time.Time) *msgspool.ConsumerService {
	t.Helper()
	spool, err := msgspool.NewService(backend.spool, msgspool.DefaultRetention(),
		func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	service, err := msgspool.NewConsumerService(spool, backend.consumers, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// spoolMessageOn is spoolMessage with the connector and destination varied, so
// a test can write traffic for two partners.
func spoolMessageOn(id, connector, user string, receivedAt time.Time) msgspool.Message {
	message := spoolMessage(id, receivedAt)
	message.ConnectorID = connector
	message.UserID = user
	message.Text = "OTP for " + connector
	message.Raw = []byte(connector)
	return message
}

func TestMessageConsumerStoreLifecycle(t *testing.T) {
	for _, backend := range pullBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			service := newPullService(t, backend, spoolBase)
			id := backend.prefix + "smsget"

			consumer, token, err := service.CreateConsumer(ctx, id, "downstream app",
				msgspool.Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
			if err != nil {
				t.Fatal(err)
			}
			if token == "" || consumer.LastUsedAt != nil {
				t.Fatalf("consumer=%+v token=%q", consumer, token)
			}

			// The digest is the only stored form: the same token resolves, a
			// different one does not.
			resolved, err := service.Authenticate(ctx, token)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.ID != id || !resolved.Scope.IncludeText ||
				len(resolved.Scope.Connectors) != 1 {
				t.Fatalf("resolved=%+v", resolved)
			}
			if _, err = service.Authenticate(ctx, token+"x"); !errors.Is(err, msgspool.ErrUnauthorized) {
				t.Fatalf("wrong token error=%v", err)
			}
			// The plaintext is not in the row: a store that kept it would answer
			// this lookup by the token rather than by its hash.
			if _, err = backend.consumers.ConsumerByTokenDigest(ctx, []byte(token)); !errors.Is(
				err, msgspool.ErrConsumerNotFound) {
				t.Fatalf("a raw token resolved: %v", err)
			}

			// Authenticating recorded last-used.
			stored, err := service.GetConsumer(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if stored.LastUsedAt == nil || !stored.LastUsedAt.Equal(spoolBase) {
				t.Fatalf("last used=%v", stored.LastUsedAt)
			}

			if _, _, err = service.CreateConsumer(ctx, id, "again",
				msgspool.Scope{Connectors: []string{"partner-a-term"}}); !errors.Is(
				err, msgspool.ErrConsumerExists) {
				t.Fatalf("duplicate create error=%v", err)
			}

			updated, err := service.UpdateConsumer(ctx, id, "narrowed",
				msgspool.Scope{Connectors: []string{"partner-b-term"}})
			if err != nil {
				t.Fatal(err)
			}
			if updated.Label != "narrowed" || updated.Scope.IncludeText ||
				updated.Scope.Connectors[0] != "partner-b-term" {
				t.Fatalf("updated=%+v", updated)
			}
			// Narrowing did not invalidate the credential.
			if _, err = service.Authenticate(ctx, token); err != nil {
				t.Fatalf("token stopped working after an update: %v", err)
			}

			revoked, err := service.SetConsumerRevoked(ctx, id, true)
			if err != nil {
				t.Fatal(err)
			}
			if !revoked.Revoked {
				t.Fatalf("revoked=%+v", revoked)
			}
			if _, err = service.Authenticate(ctx, token); !errors.Is(err, msgspool.ErrUnauthorized) {
				t.Fatalf("revoked token error=%v", err)
			}

			listed, err := service.ListConsumers(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range listed {
				if entry.ID == id {
					found = true
				}
			}
			if !found {
				t.Fatalf("consumer missing from the list: %+v", listed)
			}

			if err = service.DeleteConsumer(ctx, id); err != nil {
				t.Fatal(err)
			}
			if err = service.DeleteConsumer(ctx, id); !errors.Is(err, msgspool.ErrConsumerNotFound) {
				t.Fatalf("second delete error=%v", err)
			}
			if _, err = service.GetConsumer(ctx, id); !errors.Is(err, msgspool.ErrConsumerNotFound) {
				t.Fatalf("get after delete error=%v", err)
			}
		})
	}
}

// The scope-leak proof, in the three ways a leak would show: in the page
// contents, in the cursor arithmetic across pages, and in the row counts the
// audit trail records.
func TestMessagePullNeverObservesAConnectorOutsideTheScope(t *testing.T) {
	for _, backend := range pullBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			service := newPullService(t, backend, spoolBase)

			// Interleaved, so a post-filtering implementation would produce short
			// pages instead of dense ones and would be visible here.
			const pairs = 5
			var visible []string
			for index := 0; index < pairs; index++ {
				a := fmt.Sprintf("%sa-%02d", backend.prefix, index)
				b := fmt.Sprintf("%sb-%02d", backend.prefix, index)
				if _, err := backend.spool.Put(ctx,
					spoolMessageOn(a, "partner-a-term", "partner-a", spoolBase), spoolBase); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.spool.Put(ctx,
					spoolMessageOn(b, "partner-b-term", "partner-b", spoolBase), spoolBase); err != nil {
					t.Fatal(err)
				}
				visible = append(visible, a)
			}

			consumer, token, err := service.CreateConsumer(ctx, backend.prefix+"a-only", "",
				msgspool.Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
			if err != nil {
				t.Fatal(err)
			}
			if consumer, err = service.Authenticate(ctx, token); err != nil {
				t.Fatal(err)
			}

			// Page with a limit that would straddle the interleaving. Every page
			// before the last must be full: that is what proves the restriction is
			// in the predicate. A post-filtered implementation would return one A
			// row per two-row page here.
			var seen []string
			cursor := ""
			pages := 0
			for {
				page, pageErr := service.Page(ctx, consumer,
					msgspool.PullRequest{Cursor: cursor, Limit: 2})
				if pageErr != nil {
					t.Fatal(pageErr)
				}
				if len(page.Records) == 0 {
					break
				}
				pages++
				if pages > 10 {
					t.Fatal("paging did not terminate")
				}
				for _, record := range page.Records {
					if record.ConnectorID != "partner-a-term" {
						t.Fatalf("out-of-scope row %s on connector %s",
							record.MessageID, record.ConnectorID)
					}
					if record.UserID != "partner-a" {
						t.Fatalf("out-of-scope partner %s", record.UserID)
					}
					seen = append(seen, record.MessageID)
				}
				cursor = page.NextCursor
			}
			if len(seen) != len(visible) {
				t.Fatalf("saw %d rows, want %d: %v", len(seen), len(visible), seen)
			}
			for index, id := range visible {
				if seen[index] != id {
					t.Fatalf("page contents=%v want %v", seen, visible)
				}
			}
			// pairs=5 rows at 2 per page: three pages, the first two full.
			if pages != 3 {
				t.Fatalf("pages=%d; a post-filtered scope would need more", pages)
			}

			// Counts: the audit trail must record only the rows this consumer was
			// entitled to, never the number of rows the cursor stepped over.
			audits := readAccessAudit(t, backend, msgspool.ConsumerSubject(backend.prefix+"a-only"))
			var total int64
			for _, audit := range audits {
				total += audit.RowCount
			}
			if total != int64(len(visible)) {
				t.Fatalf("audited row count=%d want %d", total, len(visible))
			}
		})
	}
}

// The exact case a "now - N seconds" window loses: a row mutated after the
// consumer paged past it. Its received_at is unchanged and already behind the
// consumer, so only a mutation-bumped sequence hands it back.
func TestMessagePullReturnsARowMutatedAfterTheConsumerPagedPastIt(t *testing.T) {
	for _, backend := range pullBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			service := newPullService(t, backend, spoolBase)

			first := backend.prefix + "retried"
			second := backend.prefix + "later"
			if _, err := backend.spool.Put(ctx,
				spoolMessageOn(first, "partner-a-term", "partner-a", spoolBase), spoolBase); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.spool.Put(ctx,
				spoolMessageOn(second, "partner-a-term", "partner-a", spoolBase.Add(time.Minute)),
				spoolBase); err != nil {
				t.Fatal(err)
			}

			_, token, err := service.CreateConsumer(ctx, backend.prefix+"puller", "",
				msgspool.Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := service.Authenticate(ctx, token)
			if err != nil {
				t.Fatal(err)
			}

			page, err := service.Page(ctx, consumer, msgspool.PullRequest{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 2 {
				t.Fatalf("first page=%d rows", len(page.Records))
			}
			cursor := page.NextCursor

			// Caught up: nothing new.
			if page, err = service.Page(ctx, consumer,
				msgspool.PullRequest{Cursor: cursor, Limit: 100}); err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 0 {
				t.Fatalf("caught-up poll returned %d rows", len(page.Records))
			}

			// A delivery retry on the *older* row, long after the consumer moved
			// past both its sequence and its timestamp.
			if err = backend.spool.MarkAttemptFailed(ctx, first, spoolBase.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}

			if page, err = service.Page(ctx, consumer,
				msgspool.PullRequest{Cursor: cursor, Limit: 100}); err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 1 || page.Records[0].MessageID != first {
				t.Fatalf("the retried row was not handed back: %+v", page.Records)
			}
			if page.Records[0].DeliveryAttempts != 1 {
				t.Fatalf("attempts=%d", page.Records[0].DeliveryAttempts)
			}
			// And its received_at is still behind where the consumer had reached,
			// which is precisely why a time window would have lost it.
			if !page.Records[0].ReceivedAt.Equal(spoolBase) {
				t.Fatalf("received_at=%v", page.Records[0].ReceivedAt)
			}
		})
	}
}

// A per-segment receipt row is not a message. A consumer handed one would store
// a fragment as the message, so the exclusion is part of the predicate.
func TestMessagePullNeverReturnsAReceiptOnlyRow(t *testing.T) {
	for _, backend := range pullBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			service := newPullService(t, backend, spoolBase)

			whole := spoolMessageOn(backend.prefix+"whole", "partner-a-term", "partner-a", spoolBase)
			if _, err := backend.spool.Put(ctx, whole, spoolBase); err != nil {
				t.Fatal(err)
			}
			due := spoolBase.Add(5 * time.Second)
			fragment := spoolMessageOn(backend.prefix+"segment", "partner-a-term", "partner-a", spoolBase)
			fragment.Text, fragment.Raw = "", nil
			fragment.ReceiptOnly, fragment.ReceiptDueAt = true, &due
			if _, err := backend.spool.Put(ctx, fragment, spoolBase); err != nil {
				t.Fatal(err)
			}

			_, token, err := service.CreateConsumer(ctx, backend.prefix+"puller2", "",
				msgspool.Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := service.Authenticate(ctx, token)
			if err != nil {
				t.Fatal(err)
			}
			page, err := service.Page(ctx, consumer, msgspool.PullRequest{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 1 || page.Records[0].MessageID != whole.MessageID {
				t.Fatalf("page=%+v", page.Records)
			}
		})
	}
}

// include_text:false must not load the content at all: the masking is in the
// SQL projection, so the record that reaches the process genuinely has none.
func TestMessagePullMasksContentInTheProjection(t *testing.T) {
	for _, backend := range pullBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			service := newPullService(t, backend, spoolBase)

			id := backend.prefix + "masked"
			if _, err := backend.spool.Put(ctx,
				spoolMessageOn(id, "partner-a-term", "partner-a", spoolBase), spoolBase); err != nil {
				t.Fatal(err)
			}
			_, token, err := service.CreateConsumer(ctx, backend.prefix+"metadata-only", "",
				msgspool.Scope{Connectors: []string{"partner-a-term"}})
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := service.Authenticate(ctx, token)
			if err != nil {
				t.Fatal(err)
			}
			page, err := service.Page(ctx, consumer, msgspool.PullRequest{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 1 {
				t.Fatalf("page=%d rows", len(page.Records))
			}
			record := page.Records[0]
			if record.Text != "" || len(record.Raw) != 0 || !record.ContentRedacted {
				t.Fatalf("content reached a metadata-only consumer: %+v", record)
			}
			// The metadata that makes the row useful is still there.
			if record.DestAddr == "" || record.Parts == 0 || record.Verdict.Stat == "" {
				t.Fatalf("metadata was dropped with the content: %+v", record)
			}
		})
	}
}

// Every read writes an audit row naming the consumer, the scope and the count —
// on both backends, through the real audit table.
func TestMessagePullWritesAnAuditRowPerRead(t *testing.T) {
	for _, backend := range pullBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			service := newPullService(t, backend, spoolBase)

			for index := 0; index < 3; index++ {
				id := fmt.Sprintf("%saudited-%d", backend.prefix, index)
				if _, err := backend.spool.Put(ctx,
					spoolMessageOn(id, "partner-a-term", "partner-a", spoolBase), spoolBase); err != nil {
					t.Fatal(err)
				}
			}
			consumerID := backend.prefix + "audited"
			_, token, err := service.CreateConsumer(ctx, consumerID, "",
				msgspool.Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := service.Authenticate(ctx, token)
			if err != nil {
				t.Fatal(err)
			}

			before := len(readAccessAudit(t, backend, msgspool.ConsumerSubject(consumerID)))
			page, err := service.Page(ctx, consumer, msgspool.PullRequest{Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Records) != 2 {
				t.Fatalf("page=%d rows", len(page.Records))
			}
			audits := readAccessAudit(t, backend, msgspool.ConsumerSubject(consumerID))
			if len(audits) != before+1 {
				t.Fatalf("audits=%d want %d", len(audits), before+1)
			}
			latest := audits[len(audits)-1]
			if latest.RowCount != 2 || !latest.Allowed {
				t.Fatalf("audit=%+v", latest)
			}
			// A read that returns content is a reveal, not a search.
			if latest.Action != msgspool.ActionReveal {
				t.Fatalf("audit action=%s", latest.Action)
			}
			if !containsAll(latest.Target, "partner-a-term", "content=true") {
				t.Fatalf("audit target=%q", latest.Target)
			}
		})
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

// readAccessAudit reads the audit rows a subject produced, oldest first. The
// repository interface deliberately has no reader — nothing in production needs
// one — so the tests go to the table directly.
func readAccessAudit(t *testing.T, backend pullBackend, subject string) []msgspool.AccessAudit {
	t.Helper()
	var rows *sql.Rows
	var err error
	switch store := backend.spool.(type) {
	case *SQLiteMessageSpool:
		rows, err = store.db.QueryContext(context.Background(),
			`SELECT subject,action,target,allowed,row_count FROM message_spool_access_audit
 WHERE subject=? ORDER BY audit_id`, subject)
	case *PostgresMessageSpool:
		rows, err = store.db.QueryContext(context.Background(),
			`SELECT subject,action,target,allowed,row_count FROM message_spool_access_audit
 WHERE subject=$1 ORDER BY audit_id`, subject)
	default:
		t.Fatalf("unknown backend %T", backend.spool)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var audits []msgspool.AccessAudit
	for rows.Next() {
		var audit msgspool.AccessAudit
		if err = rows.Scan(&audit.Subject, &audit.Action, &audit.Target,
			&audit.Allowed, &audit.RowCount); err != nil {
			t.Fatal(err)
		}
		audits = append(audits, audit)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return audits
}
