package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (works with CGO_ENABLED=0)

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

var spoolBase = time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)

func newSQLiteMessageSpool(t *testing.T) *SQLiteMessageSpool {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewSQLiteMessageSpool(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = store.Init(context.Background()); err != nil {
		t.Fatalf("idempotent init: %v", err)
	}
	return store
}

func spoolMessage(id string, receivedAt time.Time) msgspool.Message {
	return msgspool.Message{
		MessageID: id, ConnectorID: "partner-a-term", UserID: "partner-a",
		SourceAddr: "NETFLIX", DestAddr: "380671234567",
		Text: "ПРОВЕРОЧНЫЙ КОД 63125", Raw: []byte{0x04, 0x1f, 0x04, 0x20},
		DataCoding: 8, Encoding: "ucs2", Parts: 2,
		Verdict: msgspool.Verdict{
			Accept: true, Stat: "DELIVRD", Err: "000", Reason: "activation window open",
		},
		ReceivedAt: receivedAt,
	}
}

func TestSQLiteMessageSpoolStoresAndMasksContent(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)
	message := spoolMessage("msg-1", spoolBase)
	written, err := store.Put(ctx, message, spoolBase.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if written.Sequence != 1 || written.DeliveryState != msgspool.DeliveryPending ||
		written.DeliveryAttempts != 0 {
		t.Fatalf("written=%+v", written)
	}

	masked, err := store.Get(ctx, "msg-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if masked.Text != "" || len(masked.Raw) != 0 || !masked.ContentRedacted {
		t.Fatalf("masked read returned content: text=%q raw=%v", masked.Text, masked.Raw)
	}
	if masked.DestAddr != "380671234567" || masked.Encoding != "ucs2" ||
		masked.DataCoding != 8 || masked.Parts != 2 || !masked.Verdict.Accept ||
		masked.Verdict.Stat != "DELIVRD" || masked.Verdict.Reason != "activation window open" {
		t.Fatalf("masked read lost metadata: %+v", masked)
	}
	if !masked.ReceivedAt.Equal(spoolBase) {
		t.Fatalf("received_at=%v", masked.ReceivedAt)
	}

	full, err := store.Get(ctx, "msg-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if full.Text != message.Text || string(full.Raw) != string(message.Raw) ||
		full.ContentRedacted {
		t.Fatalf("full read=%+v", full)
	}

	if _, err = store.Get(ctx, "absent", false); !errors.Is(err, msgspool.ErrNotFound) {
		t.Fatalf("missing row error=%v", err)
	}
	if _, err = store.Put(ctx, msgspool.Message{MessageID: "bad"}, spoolBase); !errors.Is(err, msgspool.ErrInvalidInput) {
		t.Fatalf("invalid message accepted: %v", err)
	}
}

// This is the correctness trap the plan calls out. Paging on received_at loses
// two kinds of row: one spooled late (reassembled or redelivered after a
// consumer's cursor passed its timestamp) and one whose delivery was retried
// after the consumer paged past it. Both must still be handed back.
func TestSQLiteMessageSpoolCursorReturnsLateAndRetriedRows(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)

	first, err := store.Put(ctx, spoolMessage("msg-fresh", spoolBase), spoolBase)
	if err != nil {
		t.Fatal(err)
	}
	// Spooled second, but received an hour earlier: a slow reassembly, or an
	// AMQP redelivery of a message the broker held.
	late, err := store.Put(ctx, spoolMessage("msg-late", spoolBase.Add(-time.Hour)),
		spoolBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !late.ReceivedAt.Before(first.ReceivedAt) {
		t.Fatal("the late row must carry the older timestamp for this test to mean anything")
	}
	if late.Sequence <= first.Sequence {
		t.Fatalf("sequence must follow write order: first=%d late=%d", first.Sequence, late.Sequence)
	}

	page, err := store.Search(ctx, msgspool.Query{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].MessageID != "msg-fresh" {
		t.Fatalf("first page=%v", spoolIDs(page))
	}
	cursor := page[0].Sequence

	// A received_at cursor would now be parked at spoolBase and would never
	// return msg-late, whose received_at is an hour earlier.
	page, err = store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].MessageID != "msg-late" {
		t.Fatalf("late row was skipped: %v", spoolIDs(page))
	}
	cursor = page[0].Sequence

	// A drained cursor stays drained: an unchanged row is not handed back.
	page, err = store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 {
		t.Fatalf("drained cursor returned %v", spoolIDs(page))
	}

	// A retried delivery re-surfaces the row the consumer already passed.
	if err = store.MarkAttemptFailed(ctx, "msg-fresh", spoolBase.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	page, err = store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].MessageID != "msg-fresh" || page[0].DeliveryAttempts != 1 {
		t.Fatalf("retried row was not returned: %+v", spoolIDs(page))
	}
	if page[0].Sequence <= cursor {
		t.Fatalf("mutation did not advance the cursor: %d <= %d", page[0].Sequence, cursor)
	}
	cursor = page[0].Sequence

	// So does the terminal transition, so a puller learns the message landed.
	if err = store.MarkDelivered(ctx, "msg-late", spoolBase.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	page, err = store.Search(ctx, msgspool.Query{AfterSequence: cursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].MessageID != "msg-late" ||
		page[0].DeliveryState != msgspool.DeliveryDelivered || page[0].DeliveredAt == nil {
		t.Fatalf("delivered row=%+v", page)
	}
	if err = store.MarkDelivered(ctx, "absent", spoolBase); !errors.Is(err, msgspool.ErrNotFound) {
		t.Fatalf("mutating a missing row: %v", err)
	}
}

// Re-spooling after a crash must not un-deliver a message or re-arm a receipt
// that already went out: either would give the partner or the app a duplicate.
func TestSQLiteMessageSpoolRePutPreservesLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)
	message := spoolMessage("msg-1", spoolBase)
	due := spoolBase.Add(5 * time.Second)
	message.ReceiptDueAt = &due
	first, err := store.Put(ctx, message, spoolBase)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDelivered(ctx, "msg-1", spoolBase.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueReceipts(ctx, "gateway-a", due, time.Minute, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%v err=%v", spoolIDs(claimed), err)
	}
	if err = store.MarkReceiptSent(ctx, "msg-1", "gateway-a", due); err != nil {
		t.Fatal(err)
	}

	again, err := store.Put(ctx, message, spoolBase.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if again.DeliveryState != msgspool.DeliveryDelivered || again.DeliveredAt == nil {
		t.Fatalf("re-spooling un-delivered the message: %+v", again)
	}
	if again.ReceiptSentAt == nil {
		t.Fatalf("re-spooling re-armed a receipt that was already sent: %+v", again)
	}
	if !again.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("created_at moved: %v -> %v", first.CreatedAt, again.CreatedAt)
	}
	if again.Sequence <= first.Sequence {
		t.Fatalf("re-spooling must advance the cursor: %d <= %d", again.Sequence, first.Sequence)
	}
	// And the already-sent receipt is never handed out again.
	claimed, err = store.ClaimDueReceipts(ctx, "gateway-a", due.Add(time.Hour), time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("a sent receipt was re-claimed: %v", spoolIDs(claimed))
	}
}

func TestSQLiteMessageSpoolDueForDelivery(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)
	soon, later := spoolBase.Add(time.Second), spoolBase.Add(time.Hour)

	scheduled := spoolMessage("msg-soon", spoolBase)
	scheduled.NextAttemptAt = &soon
	if _, err := store.Put(ctx, scheduled, spoolBase); err != nil {
		t.Fatal(err)
	}
	backedOff := spoolMessage("msg-later", spoolBase)
	backedOff.NextAttemptAt = &later
	if _, err := store.Put(ctx, backedOff, spoolBase); err != nil {
		t.Fatal(err)
	}
	// A pull-only row schedules no push and must never appear as due.
	if _, err := store.Put(ctx, spoolMessage("msg-pull-only", spoolBase), spoolBase); err != nil {
		t.Fatal(err)
	}

	due, err := store.DueForDelivery(ctx, soon, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].MessageID != "msg-soon" {
		t.Fatalf("due=%v", spoolIDs(due))
	}
	if due[0].Text == "" || len(due[0].Raw) == 0 {
		t.Fatal("the push path needs the content it is about to deliver")
	}

	if err = store.MarkDelivered(ctx, "msg-soon", soon); err != nil {
		t.Fatal(err)
	}
	if due, err = store.DueForDelivery(ctx, later, 10); err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].MessageID != "msg-later" {
		t.Fatalf("a delivered row stayed due: %v", spoolIDs(due))
	}

	if err = store.MarkDeadLettered(ctx, "msg-later"); err != nil {
		t.Fatal(err)
	}
	if due, err = store.DueForDelivery(ctx, later, 10); err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("a dead-lettered row stayed due: %v", spoolIDs(due))
	}
	// Replay puts it back in the queue rather than losing it.
	if err = store.MarkAttemptFailed(ctx, "msg-later", later); err != nil {
		t.Fatal(err)
	}
	if due, err = store.DueForDelivery(ctx, later, 10); err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].MessageID != "msg-later" {
		t.Fatalf("replay did not requeue: %v", spoolIDs(due))
	}
}

// A receipt claimed twice is a partner receiving two receipts for one message,
// so the claim is exclusive and only the holder of the lease can close it.
func TestSQLiteMessageSpoolReceiptClaimIsExclusive(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)
	due := spoolBase.Add(5 * time.Second)
	message := spoolMessage("msg-1", spoolBase)
	message.ReceiptDueAt = &due
	if _, err := store.Put(ctx, message, spoolBase); err != nil {
		t.Fatal(err)
	}
	// Nothing is owed before the delay elapses.
	claimed, err := store.ClaimDueReceipts(ctx, "gateway-a", spoolBase, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("receipt claimed before it was due: %v", spoolIDs(claimed))
	}

	claimed, err = store.ClaimDueReceipts(ctx, "gateway-a", due, 30*time.Second, 10)
	if err != nil || len(claimed) != 1 || claimed[0].MessageID != "msg-1" {
		t.Fatalf("claimed=%v err=%v", spoolIDs(claimed), err)
	}
	if claimed[0].Text != "" || len(claimed[0].Raw) != 0 || !claimed[0].ContentRedacted {
		t.Fatal("the receipt path must not carry message content")
	}
	if claimed[0].ReceiptLockOwner != "gateway-a" || claimed[0].ReceiptLockedUntil == nil {
		t.Fatalf("lease not recorded: %+v", claimed[0])
	}

	// A second process gets nothing while the lease holds.
	other, err := store.ClaimDueReceipts(ctx, "gateway-b", due.Add(time.Second), 30*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("a leased receipt was claimed twice: %v", spoolIDs(other))
	}

	// After the lease expires it is claimable again, so a dead process cannot
	// strand a receipt the partner is owed.
	other, err = store.ClaimDueReceipts(ctx, "gateway-b", due.Add(time.Minute), 30*time.Second, 10)
	if err != nil || len(other) != 1 {
		t.Fatalf("expired lease was not recovered: %v err=%v", spoolIDs(other), err)
	}

	// The process whose lease was taken over must not close the claim.
	if err = store.MarkReceiptSent(ctx, "msg-1", "gateway-a", due.Add(time.Minute)); !errors.Is(err, msgspool.ErrClaimLost) {
		t.Fatalf("stale owner closed the claim: %v", err)
	}
	if err = store.MarkReceiptSent(ctx, "msg-1", "gateway-b", due.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkReceiptSent(ctx, "msg-1", "gateway-b", due.Add(2*time.Minute)); !errors.Is(err, msgspool.ErrClaimLost) {
		t.Fatalf("a receipt was recorded as sent twice: %v", err)
	}

	stored, err := store.Get(ctx, "msg-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReceiptSentAt == nil || stored.ReceiptLockOwner != "" || stored.ReceiptLockedUntil != nil {
		t.Fatalf("lease not released: %+v", stored)
	}
}

func TestSQLiteMessageSpoolSearchFilters(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)
	first := spoolMessage("msg-1", spoolBase)
	if _, err := store.Put(ctx, first, spoolBase); err != nil {
		t.Fatal(err)
	}
	second := spoolMessage("msg-2", spoolBase.Add(time.Hour))
	second.DestAddr = "380507654321"
	second.ConnectorID = "partner-b-term"
	second.UserID = "partner-b"
	if _, err := store.Put(ctx, second, spoolBase.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	from := spoolBase.Add(30 * time.Minute)
	cases := map[string]struct {
		query msgspool.Query
		want  []string
	}{
		"by destination": {msgspool.Query{DestAddr: "380671234567", Limit: 10}, []string{"msg-1"}},
		"by connector":   {msgspool.Query{ConnectorID: "partner-b-term", Limit: 10}, []string{"msg-2"}},
		"by user":        {msgspool.Query{UserID: "partner-a", Limit: 10}, []string{"msg-1"}},
		"by state": {msgspool.Query{DeliveryState: msgspool.DeliveryPending, Limit: 10},
			[]string{"msg-1", "msg-2"}},
		"by time window": {msgspool.Query{ReceivedFrom: &from, Limit: 10}, []string{"msg-2"}},
		"unmatched":      {msgspool.Query{DestAddr: "000", Limit: 10}, nil},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			records, err := store.Search(ctx, testCase.query)
			if err != nil {
				t.Fatal(err)
			}
			got := spoolIDs(records)
			if len(got) != len(testCase.want) {
				t.Fatalf("got %v, want %v", got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("got %v, want %v", got, testCase.want)
				}
			}
			for _, record := range records {
				if record.Text != "" || len(record.Raw) != 0 {
					t.Fatal("a metadata search returned content")
				}
			}
		})
	}
	if _, err := store.Search(ctx, msgspool.Query{Limit: 0}); !errors.Is(err, msgspool.ErrInvalidInput) {
		t.Fatalf("unbounded search accepted: %v", err)
	}
}

func TestSQLiteMessageSpoolPrunesInBatches(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)
	for index := 0; index < 5; index++ {
		id := "old-" + time.Duration(index).String()
		if _, err := store.Put(ctx, spoolMessage(id, spoolBase.Add(-48*time.Hour)), spoolBase); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Put(ctx, spoolMessage("recent", spoolBase), spoolBase); err != nil {
		t.Fatal(err)
	}
	cutoff := spoolBase.Add(-24 * time.Hour)

	result, err := store.Prune(ctx, cutoff, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Records != 2 {
		t.Fatalf("first batch pruned %d", result.Records)
	}
	total := result.Records
	for {
		result, err = store.Prune(ctx, cutoff, 2)
		if err != nil {
			t.Fatal(err)
		}
		total += result.Records
		if result.Records < 2 {
			break
		}
	}
	if total != 5 {
		t.Fatalf("pruned %d rows, want 5", total)
	}
	if _, err = store.Get(ctx, "recent", false); err != nil {
		t.Fatalf("a row inside the window was pruned: %v", err)
	}
	if _, err = store.Prune(ctx, time.Time{}, 10); !errors.Is(err, msgspool.ErrInvalidInput) {
		t.Fatalf("zero cutoff accepted: %v", err)
	}
	if _, err = store.Prune(ctx, cutoff, 0); !errors.Is(err, msgspool.ErrInvalidInput) {
		t.Fatalf("zero batch accepted: %v", err)
	}
}

// The service boundary over the real table: a reveal must leave a row naming
// the actor, and a metadata read must leave one too.
func TestSQLiteMessageSpoolServiceAuditsEveryRead(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)
	now := spoolBase.Add(time.Minute)
	service, err := msgspool.NewService(store, msgspool.DefaultRetention(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Put(ctx, spoolMessage("msg-1", spoolBase)); err != nil {
		t.Fatal(err)
	}
	revealer := msgspool.Principal{
		Subject: "console:bob", Roles: []msgspool.Role{msgspool.RoleRevealer},
	}
	if _, err = service.Get(ctx, revealer, "msg-1"); err != nil {
		t.Fatal(err)
	}
	revealed, err := service.Reveal(ctx, revealer, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if revealed.Text == "" {
		t.Fatal("reveal returned no content")
	}
	if _, err = service.Reveal(ctx, msgspool.Principal{Subject: "console:mallory"}, "msg-1"); err == nil {
		t.Fatal("an unauthorized reveal succeeded")
	}

	rows, err := store.db.QueryContext(ctx,
		`SELECT subject,action,allowed,row_count FROM message_spool_access_audit ORDER BY audit_id`)
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

func spoolIDs(records []msgspool.Record) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.MessageID)
	}
	return ids
}

// A redelivery after the receipt went out must not rewrite the verdict.
//
// The gate is a live activation window: by the time AMQP redelivers a message,
// the window may have closed, so re-deciding produces REJECTD for a message the
// partner was already told was DELIVRD. The spool is what answers a dispute
// about exactly that, so it has to keep what was sent rather than what would be
// decided now.
func TestSQLiteMessageSpoolFreezesVerdictAfterReceiptSent(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	due := now
	message := msgspool.Message{
		MessageID:    "msg-freeze",
		ConnectorID:  "partner-a-term",
		DestAddr:     "380671234567",
		Text:         "code 63125",
		Parts:        1,
		Verdict:      msgspool.Verdict{Accept: true, Stat: "DELIVRD", Err: "000"},
		ReceivedAt:   now,
		ReceiptDueAt: &due,
	}
	if _, err := store.Put(ctx, message, now); err != nil {
		t.Fatalf("put: %v", err)
	}
	claimed, err := store.ClaimDueReceipts(ctx, "gateway-1", now.Add(time.Second), time.Minute, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %d rows, err %v", len(claimed), err)
	}
	if err := store.MarkReceiptSent(ctx, "msg-freeze", "gateway-1", now.Add(2*time.Second)); err != nil {
		t.Fatalf("mark receipt sent: %v", err)
	}

	// The window has since closed; a redelivery re-decides and re-Puts.
	message.Verdict = msgspool.Verdict{Accept: false, Stat: "REJECTD", Err: "008", Reason: "no window"}
	if _, err := store.Put(ctx, message, now.Add(3*time.Second)); err != nil {
		t.Fatalf("re-put: %v", err)
	}

	record, err := store.Get(ctx, "msg-freeze", false)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if record.Verdict.Stat != "DELIVRD" || !record.Verdict.Accept {
		t.Errorf("verdict = %+v, want the DELIVRD the partner was actually told", record.Verdict)
	}
}
