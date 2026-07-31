package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// The receipt-only row is the storage half of "receipts belong to segments,
// content belongs to the message". Each segment of a concatenated submit is its
// own submit_sm and is owed its own receipt, so the spool holds a row per
// segment; only one of those rows is a message.
//
// Every test here runs against both backends. A restriction enforced in
// PostgreSQL and not in SQLite is a leak that only appears in production, and
// the SQLite store is what the unit suite and every local run exercise.
func forEachSpoolBackend(t *testing.T, run func(t *testing.T, store msgspool.Repository, prefix string)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		run(t, newSQLiteMessageSpool(t), "")
	})
	t.Run("postgres", func(t *testing.T) {
		store, prefix := openTestPostgresMessageSpool(t)
		run(t, store, prefix)
	})
}

// receiptOnlyMessage is what termination.Spool.RecordReceiptOnly writes: the
// verdict, the addresses and when the receipt is owed, and no content at all.
func receiptOnlyMessage(id string, receivedAt, receiptDueAt time.Time) msgspool.Message {
	message := spoolMessage(id, receivedAt)
	message.Text, message.Raw = "", nil
	message.Encoding, message.Parts = "", 1
	message.NextAttemptAt = nil
	due := receiptDueAt
	message.ReceiptDueAt = &due
	message.ReceiptOnly = true
	return message
}

// deliverableMessage is what termination.Spool.Record writes for an assembled
// message: content, a scheduled push and a receipt.
func deliverableMessage(id string, receivedAt, dueAt time.Time) msgspool.Message {
	message := spoolMessage(id, receivedAt)
	attempt := dueAt
	receipt := dueAt
	message.NextAttemptAt = &attempt
	message.ReceiptDueAt = &receipt
	return message
}

// A pull consumer paging the spool must never be handed a fragment: a row with
// no text arrives looking exactly like a blank SMS, and an application that
// stored it would record an empty message against a real message id. The
// exclusion is the default — a caller that forgets the filter gets messages.
func TestSpoolSearchExcludesReceiptOnlyRowsByDefault(t *testing.T) {
	forEachSpoolBackend(t, func(t *testing.T, store msgspool.Repository, prefix string) {
		ctx := context.Background()
		held := prefix + "queue-msg-1"
		assembled := prefix + "queue-msg-3"

		if _, err := store.Put(ctx, receiptOnlyMessage(held, spoolBase, spoolBase.Add(6*time.Second)), spoolBase); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Put(ctx, deliverableMessage(assembled, spoolBase, spoolBase), spoolBase); err != nil {
			t.Fatal(err)
		}

		// The shape the pull API issues: a scoped connector allow-list, a cursor,
		// content included, and no request for receipt obligations.
		pull := msgspool.Query{
			ConnectorIDs: []string{"partner-a-term"},
			// A shared PostgreSQL database may hold other tests' rows, so the read
			// is narrowed the same way the cleanup is.
			UserID:         "partner-a",
			IncludeContent: true,
			Limit:          100,
		}
		found, err := store.Search(ctx, pull)
		if err != nil {
			t.Fatal(err)
		}
		ours := filterSpoolIDs(found, held, assembled)
		if len(ours) != 1 {
			t.Fatalf("a pull-shaped read returned %d rows, want only the assembled message", len(ours))
		}
		if ours[0].MessageID != assembled {
			t.Fatalf("returned %q, want the assembled message %q", ours[0].MessageID, assembled)
		}
		if ours[0].Text == "" {
			t.Error("the assembled message came back without its text")
		}

		// An operator answering "why did this partner receive three receipts" can
		// ask for them, and only then.
		pull.IncludeReceiptOnly = true
		found, err = store.Search(ctx, pull)
		if err != nil {
			t.Fatal(err)
		}
		if ours = filterSpoolIDs(found, held, assembled); len(ours) != 2 {
			t.Fatalf("an explicit read returned %d rows, want both", len(ours))
		}
	})
}

// The cursor has to stay dense over what the consumer can see. A receipt-only
// row that advanced the sequence without being returned would give a consumer a
// page shorter than it asked for, which it cannot distinguish from "no new
// messages" — so the exclusion belongs in the predicate, and the proof is that
// paging one row at a time across a spool full of fragments still walks every
// message.
func TestSpoolCursorPagingSkipsReceiptOnlyRowsWithoutLosingMessages(t *testing.T) {
	forEachSpoolBackend(t, func(t *testing.T, store msgspool.Repository, prefix string) {
		ctx := context.Background()
		var wantIDs []string
		// Three long messages, each two held segments then the assembled row.
		for group := 1; group <= 3; group++ {
			for segment := 1; segment <= 2; segment++ {
				id := prefix + "held-" + string(rune('0'+group)) + "-" + string(rune('0'+segment))
				if _, err := store.Put(ctx,
					receiptOnlyMessage(id, spoolBase, spoolBase.Add(6*time.Second)), spoolBase); err != nil {
					t.Fatal(err)
				}
			}
			id := prefix + "message-" + string(rune('0'+group))
			if _, err := store.Put(ctx, deliverableMessage(id, spoolBase, spoolBase), spoolBase); err != nil {
				t.Fatal(err)
			}
			wantIDs = append(wantIDs, id)
		}

		var walked []string
		cursor := int64(0)
		for range 10 {
			page, err := store.Search(ctx, msgspool.Query{
				AfterSequence: cursor,
				ConnectorIDs:  []string{"partner-a-term"},
				UserID:        "partner-a",
				Limit:         1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			for _, record := range page {
				if record.ReceiptOnly {
					t.Fatalf("paging handed out receipt obligation %s", record.MessageID)
				}
				walked = append(walked, record.MessageID)
				cursor = record.Sequence
			}
		}
		if got := filterSpoolNames(walked, wantIDs); len(got) != len(wantIDs) {
			t.Fatalf("paged %d messages %v, want all %d", len(got), got, len(wantIDs))
		}
	})
}

// A receipt obligation is not pushed anywhere — there is nothing to push — but
// it is still claimed by the receipt runner, which is the entire reason it
// exists.
func TestSpoolReceiptOnlyRowIsReceiptedButNeverDelivered(t *testing.T) {
	forEachSpoolBackend(t, func(t *testing.T, store msgspool.Repository, prefix string) {
		ctx := context.Background()
		held := prefix + "queue-msg-1"
		due := spoolBase.Add(6 * time.Second)
		if _, err := store.Put(ctx, receiptOnlyMessage(held, spoolBase, due), spoolBase); err != nil {
			t.Fatal(err)
		}

		pending, err := store.DueForDelivery(ctx, due.Add(time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range pending {
			if record.MessageID == held {
				t.Fatal("a receipt obligation was scheduled for a downstream push")
			}
		}

		claimed, err := store.ClaimDueReceipts(ctx, "owner-1", due.Add(time.Second), 30*time.Second, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(filterSpoolIDs(claimed, held)) != 1 {
			t.Fatalf("claimed %d of our rows, want the held segment's receipt", len(filterSpoolIDs(claimed, held)))
		}
		if err := store.MarkReceiptSent(ctx, held, "owner-1", due.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	})
}

// The redelivery that would otherwise destroy a message.
//
// A worker that commits the assembled row and then fails to ack sees the
// completing segment redelivered; the assembler has already dropped the joined
// parts, so it arrives looking incomplete and is written as a receipt
// obligation under the id the assembled row already holds. That write must
// narrow nothing.
func TestSpoolReceiptOnlyPutCannotBlankAnAssembledMessage(t *testing.T) {
	forEachSpoolBackend(t, func(t *testing.T, store msgspool.Repository, prefix string) {
		ctx := context.Background()
		id := prefix + "queue-msg-3"
		assembled := deliverableMessage(id, spoolBase, spoolBase)
		if _, err := store.Put(ctx, assembled, spoolBase); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Put(ctx,
			receiptOnlyMessage(id, spoolBase, spoolBase.Add(6*time.Second)), spoolBase); err != nil {
			t.Fatal(err)
		}

		after, err := store.Get(ctx, id, true)
		if err != nil {
			t.Fatal(err)
		}
		if after.ReceiptOnly {
			t.Error("a redelivered segment demoted an assembled message to a receipt obligation")
		}
		if after.Text != assembled.Text || len(after.Raw) != len(assembled.Raw) {
			t.Errorf("content after redelivery = text %q raw_len %d, want it untouched",
				after.Text, len(after.Raw))
		}
		if after.Parts != assembled.Parts || after.Encoding != assembled.Encoding {
			t.Errorf("parts/encoding after redelivery = %d/%q, want %d/%q",
				after.Parts, after.Encoding, assembled.Parts, assembled.Encoding)
		}
		if after.NextAttemptAt == nil {
			t.Error("the message stopped being scheduled for delivery")
		}
	})
}

// The other direction, which happens when the process dies between dropping the
// joined parts and committing the assembled row: the redelivered segment writes
// a receipt obligation first and the assembled message lands on top of it. The
// row has to become deliverable, including arming the push that its receipt-only
// form deliberately had none of — an upsert that left next_attempt_at alone
// would spool a message nothing would ever deliver.
func TestSpoolReceiptOnlyRowUpgradesToAnAssembledMessage(t *testing.T) {
	forEachSpoolBackend(t, func(t *testing.T, store msgspool.Repository, prefix string) {
		ctx := context.Background()
		id := prefix + "queue-msg-3"
		receiptDue := spoolBase.Add(6 * time.Second)
		if _, err := store.Put(ctx, receiptOnlyMessage(id, spoolBase, receiptDue), spoolBase); err != nil {
			t.Fatal(err)
		}
		assembled := deliverableMessage(id, spoolBase, spoolBase.Add(time.Second))
		if _, err := store.Put(ctx, assembled, spoolBase); err != nil {
			t.Fatal(err)
		}

		after, err := store.Get(ctx, id, true)
		if err != nil {
			t.Fatal(err)
		}
		if after.ReceiptOnly {
			t.Fatal("the row is still a receipt obligation after the message arrived")
		}
		if after.Text != assembled.Text || after.Parts != assembled.Parts {
			t.Errorf("content = text %q parts %d, want the assembled message", after.Text, after.Parts)
		}
		if after.NextAttemptAt == nil {
			t.Fatal("the assembled message was never scheduled for delivery")
		}
		// The receipt keeps the schedule it was first given: re-arming it would
		// move a receipt the partner is already waiting for.
		if after.ReceiptDueAt == nil || !after.ReceiptDueAt.Equal(receiptDue.UTC()) {
			t.Errorf("receipt due at %v, want the original %v", after.ReceiptDueAt, receiptDue.UTC())
		}
	})
}

// The invariants are refused before they reach a driver, so a caller gets a
// typed error rather than a backend-specific constraint violation — and so the
// SQLite spool file that predates the column, which cannot carry the CHECKs, is
// still protected.
func TestSpoolRefusesAnIllFormedReceiptOnlyRow(t *testing.T) {
	due := spoolBase.Add(6 * time.Second)
	cases := map[string]func(msgspool.Message) msgspool.Message{
		"carries text": func(m msgspool.Message) msgspool.Message {
			m.Text = "half a code"
			return m
		},
		"carries raw bytes": func(m msgspool.Message) msgspool.Message {
			m.Raw = []byte{0x04, 0x1f}
			return m
		},
		"is scheduled for a push": func(m msgspool.Message) msgspool.Message {
			at := spoolBase
			m.NextAttemptAt = &at
			return m
		},
		"owes no receipt": func(m msgspool.Message) msgspool.Message {
			m.ReceiptDueAt = nil
			return m
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			message := mutate(receiptOnlyMessage("msg-1", spoolBase, due))
			if err := msgspool.ValidateMessage(message); !errors.Is(err, msgspool.ErrInvalidInput) {
				t.Fatalf("ValidateMessage = %v, want ErrInvalidInput", err)
			}
			ctx := context.Background()
			if _, err := newSQLiteMessageSpool(t).Put(ctx, message, spoolBase); !errors.Is(err, msgspool.ErrInvalidInput) {
				t.Fatalf("Put = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// filterSpoolIDs keeps only the rows this test wrote. A shared PostgreSQL
// database holds other runs' rows, and asserting on a total would make the test
// pass or fail depending on who else was running.
func filterSpoolIDs(records []msgspool.Record, ids ...string) []msgspool.Record {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var kept []msgspool.Record
	for _, record := range records {
		if wanted[record.MessageID] {
			kept = append(kept, record)
		}
	}
	return kept
}

func filterSpoolNames(walked []string, wanted []string) []string {
	want := map[string]bool{}
	for _, id := range wanted {
		want[id] = true
	}
	var kept []string
	for _, id := range walked {
		if want[id] {
			kept = append(kept, id)
		}
	}
	return kept
}
