package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

func openTestPostgresMessageSpool(t *testing.T) (*PostgresMessageSpool, string) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	store, err := OpenPostgresMessageSpool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	// Every row this test writes is prefixed, so a shared database keeps its
	// other rows and the cleanup cannot delete anyone else's.
	prefix := "spool-test-" + time.Now().UTC().Format("20060102150405.000000000") + "-"
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(),
			`DELETE FROM message_spool WHERE message_id LIKE $1`, prefix+"%")
		_, _ = store.db.ExecContext(context.Background(),
			`DELETE FROM message_spool_access_audit WHERE target LIKE $1`, prefix+"%")
	})
	return store, prefix
}

func TestPostgresMessageSpoolStoresAndMasksContent(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()
	message := spoolMessage(prefix+"msg-1", spoolBase)
	written, err := store.Put(ctx, message, spoolBase)
	if err != nil {
		t.Fatal(err)
	}
	if written.DeliveryState != msgspool.DeliveryPending || written.DeliveryAttempts != 0 ||
		written.Sequence == 0 {
		t.Fatalf("written=%+v", written)
	}

	masked, err := store.Get(ctx, prefix+"msg-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if masked.Text != "" || len(masked.Raw) != 0 || !masked.ContentRedacted {
		t.Fatalf("masked read returned content: text=%q raw=%v", masked.Text, masked.Raw)
	}
	if masked.DestAddr != "380671234567" || masked.DataCoding != 8 || masked.Parts != 2 ||
		masked.Verdict.Stat != "DELIVRD" || !masked.ReceivedAt.Equal(spoolBase) {
		t.Fatalf("masked read lost metadata: %+v", masked)
	}

	full, err := store.Get(ctx, prefix+"msg-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if full.Text != message.Text || string(full.Raw) != string(message.Raw) {
		t.Fatalf("full read=%+v", full)
	}
	if _, err = store.Get(ctx, prefix+"absent", false); !errors.Is(err, msgspool.ErrNotFound) {
		t.Fatalf("missing row error=%v", err)
	}
}

// The PostgreSQL half of the correctness trap: paging on received_at loses a
// row spooled late and a row whose delivery was retried after the cursor passed.
func TestPostgresMessageSpoolCursorReturnsLateAndRetriedRows(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()

	first, err := store.Put(ctx, spoolMessage(prefix+"a-fresh", spoolBase), spoolBase)
	if err != nil {
		t.Fatal(err)
	}
	late, err := store.Put(ctx, spoolMessage(prefix+"b-late", spoolBase.Add(-time.Hour)),
		spoolBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !late.ReceivedAt.Before(first.ReceivedAt) || late.Sequence <= first.Sequence {
		t.Fatalf("first=%+v late=%+v", first, late)
	}

	cursor := first.Sequence - 1
	page, err := store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].MessageID != prefix+"a-fresh" {
		t.Fatalf("first page=%v", spoolIDs(page))
	}
	cursor = page[0].Sequence

	page, err = store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].MessageID != prefix+"b-late" {
		t.Fatalf("late row was skipped: %v", spoolIDs(page))
	}
	cursor = page[0].Sequence

	if page, err = store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 {
		t.Fatalf("drained cursor returned %v", spoolIDs(page))
	}

	if err = store.MarkAttemptFailed(ctx, prefix+"a-fresh", spoolBase.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	page, err = store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].MessageID != prefix+"a-fresh" ||
		page[0].DeliveryAttempts != 1 || page[0].Sequence <= cursor {
		t.Fatalf("retried row was not returned: %+v", page)
	}
	if err = store.MarkDelivered(ctx, prefix+"absent", spoolBase); !errors.Is(err, msgspool.ErrNotFound) {
		t.Fatalf("mutating a missing row: %v", err)
	}
}

// The cursor is safe to page with only because sequence order equals commit
// order. A bigserial would break exactly here: a writer holding the lower value
// can commit after one holding the higher, and a consumer past the higher value
// never sees the lower row again. The allocator's row lock is what prevents it,
// so this test holds that lock and proves a concurrent writer waits for it.
func TestPostgresMessageSpoolSequenceFollowsCommitOrder(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()

	holder, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	var held int64
	if err = holder.QueryRowContext(ctx, `UPDATE message_spool_sequence
 SET last_value=last_value+1 WHERE name='message_spool' RETURNING last_value`).Scan(&held); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		record msgspool.Record
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		record, putErr := store.Put(ctx, spoolMessage(prefix+"blocked", spoolBase), spoolBase)
		done <- outcome{record: record, err: putErr}
	}()

	select {
	case result := <-done:
		t.Fatalf("a concurrent write allocated seq %d without waiting for the holder (err=%v)",
			result.record.Sequence, result.err)
	case <-time.After(500 * time.Millisecond):
	}

	if err = holder.Commit(); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.record.Sequence != held+1 {
		t.Fatalf("blocked writer got seq %d, want %d", result.record.Sequence, held+1)
	}
}

func TestPostgresMessageSpoolRePutPreservesLifecycle(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()
	due := spoolBase.Add(5 * time.Second)
	message := spoolMessage(prefix+"msg-1", spoolBase)
	message.ReceiptDueAt = &due
	first, err := store.Put(ctx, message, spoolBase)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDelivered(ctx, prefix+"msg-1", spoolBase.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueReceipts(ctx, prefix+"gateway-a", due, time.Minute, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%v err=%v", spoolIDs(claimed), err)
	}
	if err = store.MarkReceiptSent(ctx, prefix+"msg-1", prefix+"gateway-a", due); err != nil {
		t.Fatal(err)
	}

	again, err := store.Put(ctx, message, spoolBase.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if again.DeliveryState != msgspool.DeliveryDelivered || again.DeliveredAt == nil ||
		again.ReceiptSentAt == nil {
		t.Fatalf("re-spooling reset the lifecycle: %+v", again)
	}
	if !again.CreatedAt.Equal(first.CreatedAt) || again.Sequence <= first.Sequence {
		t.Fatalf("first=%+v again=%+v", first, again)
	}
}

func TestPostgresMessageSpoolReceiptClaimIsExclusive(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()
	due := spoolBase.Add(5 * time.Second)
	message := spoolMessage(prefix+"msg-1", spoolBase)
	message.ReceiptDueAt = &due
	if _, err := store.Put(ctx, message, spoolBase); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueReceipts(ctx, prefix+"a", spoolBase, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("receipt claimed before it was due: %v", spoolIDs(claimed))
	}

	claimed, err = store.ClaimDueReceipts(ctx, prefix+"a", due, 30*time.Second, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%v err=%v", spoolIDs(claimed), err)
	}
	if claimed[0].Text != "" || len(claimed[0].Raw) != 0 || !claimed[0].ContentRedacted {
		t.Fatal("the receipt path must not carry message content")
	}
	other, err := store.ClaimDueReceipts(ctx, prefix+"b", due.Add(time.Second), 30*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("a leased receipt was claimed twice: %v", spoolIDs(other))
	}
	other, err = store.ClaimDueReceipts(ctx, prefix+"b", due.Add(time.Minute), 30*time.Second, 10)
	if err != nil || len(other) != 1 {
		t.Fatalf("expired lease was not recovered: %v err=%v", spoolIDs(other), err)
	}
	if err = store.MarkReceiptSent(ctx, prefix+"msg-1", prefix+"a", due); !errors.Is(err, msgspool.ErrClaimLost) {
		t.Fatalf("stale owner closed the claim: %v", err)
	}
	if err = store.MarkReceiptSent(ctx, prefix+"msg-1", prefix+"b", due); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkReceiptSent(ctx, prefix+"msg-1", prefix+"b", due); !errors.Is(err, msgspool.ErrClaimLost) {
		t.Fatalf("a receipt was recorded as sent twice: %v", err)
	}
	stored, err := store.Get(ctx, prefix+"msg-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReceiptSentAt == nil || stored.ReceiptLockOwner != "" || stored.ReceiptLockedUntil != nil {
		t.Fatalf("lease not released: %+v", stored)
	}
}

// Sequential claims prove the lease predicate; only contention proves the
// claim itself is exclusive. Every owed receipt must go to exactly one worker,
// because a receipt handed to two workers is a partner receiving two receipts
// for one message.
func TestPostgresMessageSpoolConcurrentClaimsAreDisjoint(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()
	due := spoolBase.Add(5 * time.Second)
	const messages = 24
	for index := 0; index < messages; index++ {
		message := spoolMessage(prefix+"concurrent-"+time.Duration(index).String(), spoolBase)
		message.ReceiptDueAt = &due
		if _, err := store.Put(ctx, message, spoolBase); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 6
	results := make(chan []msgspool.Record, workers)
	failures := make(chan error, workers)
	start := make(chan struct{})
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			<-start
			claimed, err := store.ClaimDueReceipts(ctx,
				prefix+"worker-"+time.Duration(worker).String(), due, time.Minute, 1000)
			if err != nil {
				failures <- err
				return
			}
			results <- claimed
		}(worker)
	}
	close(start)

	seen := make(map[string]string, messages)
	total := 0
	for worker := 0; worker < workers; worker++ {
		select {
		case err := <-failures:
			t.Fatal(err)
		case claimed := <-results:
			for _, record := range claimed {
				if owner, duplicate := seen[record.MessageID]; duplicate {
					t.Fatalf("%s was claimed by %s and %s", record.MessageID,
						owner, record.ReceiptLockOwner)
				}
				seen[record.MessageID] = record.ReceiptLockOwner
				total++
			}
		}
	}
	if total != messages {
		t.Fatalf("claimed %d of %d owed receipts", total, messages)
	}
}

func TestPostgresMessageSpoolDueForDeliveryAndPrune(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()
	soon := spoolBase.Add(time.Second)
	scheduled := spoolMessage(prefix+"due", spoolBase)
	scheduled.NextAttemptAt = &soon
	if _, err := store.Put(ctx, scheduled, spoolBase); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, spoolMessage(prefix+"pull-only", spoolBase), spoolBase); err != nil {
		t.Fatal(err)
	}
	due, err := store.DueForDelivery(ctx, soon, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, record := range due {
		if record.MessageID == prefix+"due" {
			found++
			if record.Text == "" || len(record.Raw) == 0 {
				t.Fatal("the push path needs the content it is about to deliver")
			}
		}
		if record.MessageID == prefix+"pull-only" {
			t.Fatal("an unscheduled row was reported as due")
		}
	}
	if found != 1 {
		t.Fatalf("due rows=%v", spoolIDs(due))
	}

	old := spoolBase.Add(-48 * time.Hour)
	for _, id := range []string{prefix + "old-1", prefix + "old-2", prefix + "old-3"} {
		if _, err = store.Put(ctx, spoolMessage(id, old), spoolBase); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := spoolBase.Add(-24 * time.Hour)
	result, err := store.Prune(ctx, cutoff, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Records != 2 {
		t.Fatalf("first batch pruned %d, want 2", result.Records)
	}
	if result, err = store.Prune(ctx, cutoff, 2); err != nil {
		t.Fatal(err)
	}
	if result.Records != 1 {
		t.Fatalf("second batch pruned %d, want 1", result.Records)
	}
	if _, err = store.Get(ctx, prefix+"due", false); err != nil {
		t.Fatalf("a row inside the window was pruned: %v", err)
	}
	if _, err = store.Prune(ctx, time.Time{}, 10); !errors.Is(err, msgspool.ErrInvalidInput) {
		t.Fatalf("zero cutoff accepted: %v", err)
	}
}

func TestPostgresMessageSpoolServiceAuditsEveryRead(t *testing.T) {
	store, prefix := openTestPostgresMessageSpool(t)
	ctx := context.Background()
	now := spoolBase.Add(time.Minute)
	service, err := msgspool.NewService(store, msgspool.DefaultRetention(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Put(ctx, spoolMessage(prefix+"msg-1", spoolBase)); err != nil {
		t.Fatal(err)
	}
	revealer := msgspool.Principal{
		Subject: "console:bob", Roles: []msgspool.Role{msgspool.RoleRevealer},
	}
	if _, err = service.Get(ctx, revealer, prefix+"msg-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Reveal(ctx, revealer, prefix+"msg-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Reveal(ctx, msgspool.Principal{Subject: "console:mallory"},
		prefix+"msg-1"); !errors.Is(err, msgspool.ErrForbidden) {
		t.Fatalf("an unauthorized reveal succeeded: %v", err)
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT subject,action,allowed,row_count FROM message_spool_access_audit
 WHERE target=$1 ORDER BY audit_id`, prefix+"msg-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type entry struct {
		subject  string
		action   string
		allowed  bool
		rowCount int64
	}
	var entries []entry
	for rows.Next() {
		var value entry
		if err = rows.Scan(&value.subject, &value.action, &value.allowed, &value.rowCount); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []entry{
		{"console:bob", "read", true, 1},
		{"console:bob", "reveal", true, 1},
		{"console:mallory", "reveal", false, 0},
	}
	if len(entries) != len(want) {
		t.Fatalf("audit rows=%+v", entries)
	}
	for index := range want {
		if entries[index] != want[index] {
			t.Fatalf("audit row %d = %+v, want %+v", index, entries[index], want[index])
		}
	}
}
