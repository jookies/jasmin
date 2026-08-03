package storage

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
)

// seedSummaryCDR inserts one admitted part and then forces the outcome columns
// the aggregate reads. The lifecycle itself is covered elsewhere; this fixture
// exists to build a known mix of states cheaply.
func seedSummaryCDR(t *testing.T, db *sql.DB, repository *SQLiteSubmitTransactionRepository,
	admission cdr.Admission, state cdr.State, deliveryState cdr.DeliveryState,
	outcome cdr.BillingOutcome, actualLate float64,
) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertSQLiteCDRAdmission(ctx, tx, admission); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE cdr_records SET state=?,delivery_state=?,billing_outcome=?,actual_late_amount=? WHERE cdr_id=?`,
		string(state), nullableDeliveryState(deliveryState), string(outcome), actualLate, admission.ID,
	); err != nil {
		t.Fatal(err)
	}
	_ = repository
}

func nullableDeliveryState(value cdr.DeliveryState) any {
	if value == cdr.DeliveryNone {
		return nil
	}
	return string(value)
}

func newSummaryFixture(t *testing.T) (*sql.DB, *SQLiteSubmitTransactionRepository) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repository, _ := NewSQLiteSubmitTransactionRepository(db)
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, repository
}

func admissionAt(id, messageID, user string, part, count int, at time.Time, early, late float64) cdr.Admission {
	return cdr.Admission{
		ID: id, MessageID: messageID, PartNumber: part, PartCount: count,
		UserID: user, RouteID: "mt:0", ConnectorID: "smsc-a", Ingress: "http",
		Rate: early + late, Currency: "EUR", EarlyAmount: early, LateAmount: late,
		BillingMode: cdr.ModeForAmounts(early, late), OccurredAt: at,
	}
}

func TestSummarizeCDRsAggregatesChargedMoneyAndOutcomes(t *testing.T) {
	db, repository := newSummaryFixture(t)
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	// alice: a two-part split-billed message, one part settled late, one still
	// pending, plus a rejected single part whose early money was still taken.
	seedSummaryCDR(t, db, repository,
		admissionAt("m1/000001", "m1", "alice", 1, 2, base, 0.4, 0.6),
		cdr.StateSMSCAccepted, cdr.DeliveryDelivered, cdr.BillingApplied, 0.6)
	seedSummaryCDR(t, db, repository,
		admissionAt("m1/000002", "m1", "alice", 2, 2, base.Add(time.Second), 0.4, 0.6),
		cdr.StateSMSCAccepted, cdr.DeliveryNone, cdr.BillingPending, 0)
	seedSummaryCDR(t, db, repository,
		admissionAt("m2/000001", "m2", "alice", 1, 1, base.Add(2*time.Second), 1, 0),
		cdr.StateSMSCRejected, cdr.DeliveryNone, cdr.BillingNotApplicable, 0)
	// bob is a different customer in the same window.
	seedSummaryCDR(t, db, repository,
		admissionAt("m3/000001", "m3", "bob", 1, 1, base.Add(3*time.Second), 2, 0),
		cdr.StateSMSCAccepted, cdr.DeliveryUndeliverable, cdr.BillingNotApplicable, 0)
	// Outside the window: must not appear in any total.
	seedSummaryCDR(t, db, repository,
		admissionAt("m4/000001", "m4", "alice", 1, 1, base.AddDate(0, 0, 5), 9, 0),
		cdr.StateSMSCAccepted, cdr.DeliveryDelivered, cdr.BillingNotApplicable, 0)

	summaries, err := repository.SummarizeCDRs(context.Background(), cdr.SummaryQuery{
		AdmittedFrom: base.Add(-time.Hour), AdmittedTo: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries=%+v", summaries)
	}
	alice, bob := summaries[0], summaries[1]
	if alice.UserID != "alice" || bob.UserID != "bob" {
		t.Fatalf("unexpected order: %+v", summaries)
	}
	if alice.Parts != 3 || alice.Messages != 2 {
		t.Fatalf("alice parts=%d messages=%d", alice.Parts, alice.Messages)
	}
	if alice.Accepted != 2 || alice.Rejected != 1 {
		t.Fatalf("alice accepted=%d rejected=%d", alice.Accepted, alice.Rejected)
	}
	// The SMSC-rejected part has no delivery state and never will: nothing
	// accepted it, so no receipt is coming. It used to be counted as "delivery
	// pending", which read as a receipt merely running late.
	if alice.Delivered != 1 || alice.DeliveryPending != 1 ||
		alice.Undelivered != 0 || alice.NoReceiptExpected != 1 {
		t.Fatalf("alice delivery delivered=%d pending=%d undelivered=%d no-receipt=%d",
			alice.Delivered, alice.DeliveryPending, alice.Undelivered, alice.NoReceiptExpected)
	}
	// Early money is taken on every part, including the SMSC-rejected one.
	if alice.ChargedEarly != 1.8 {
		t.Fatalf("alice charged early=%v want 1.8", alice.ChargedEarly)
	}
	// Only the settled late charge counts as charged; the pending one is quoted.
	if alice.ChargedLate != 0.6 || alice.QuotedLatePending != 0.6 {
		t.Fatalf("alice late charged=%v pending=%v", alice.ChargedLate, alice.QuotedLatePending)
	}
	if alice.ChargedTotal() != 2.4 {
		t.Fatalf("alice charged total=%v want 2.4", alice.ChargedTotal())
	}
	if alice.Currency != "EUR" {
		t.Fatalf("alice currency=%q", alice.Currency)
	}
	if !alice.FirstAdmittedAt.Equal(base) || !alice.LastAdmittedAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("alice window first=%v last=%v", alice.FirstAdmittedAt, alice.LastAdmittedAt)
	}
	if bob.Parts != 1 || bob.Undelivered != 1 || bob.ChargedTotal() != 2 {
		t.Fatalf("bob=%+v", bob)
	}
}

// The aggregate must agree with the records it aggregates: an operator who
// exports the same window and adds the column up has to get the same number, or
// one of the two views is a lie.
func TestSummarizeCDRsMatchesTheExportedRecordsForTheSameWindow(t *testing.T) {
	db, repository := newSummaryFixture(t)
	base := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	for index, amounts := range [][2]float64{{0.5, 0}, {0.25, 0.25}, {1, 0}, {0, 0}} {
		id := "x" + string(rune('a'+index))
		outcome := cdr.BillingNotApplicable
		actualLate := 0.0
		if amounts[1] > 0 {
			outcome, actualLate = cdr.BillingApplied, amounts[1]
		}
		seedSummaryCDR(t, db, repository,
			admissionAt(id+"/000001", id, "carol", 1, 1,
				base.Add(time.Duration(index)*time.Minute), amounts[0], amounts[1]),
			cdr.StateSMSCAccepted, cdr.DeliveryDelivered, outcome, actualLate)
	}
	ctx := context.Background()
	from, to := base.Add(-time.Hour), base.Add(time.Hour)
	records, err := repository.ExportCDRs(ctx, cdr.ExportQuery{
		AdmittedFrom: &from, AdmittedTo: &to, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var expected float64
	for _, record := range records {
		expected += record.EarlyAmount + record.ActualLateAmount
	}
	summaries, err := repository.SummarizeCDRs(ctx, cdr.SummaryQuery{
		AdmittedFrom: from, AdmittedTo: to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries=%+v", summaries)
	}
	if summaries[0].ChargedTotal() != expected {
		t.Fatalf("summary total=%v export total=%v", summaries[0].ChargedTotal(), expected)
	}
	if summaries[0].Parts != int64(len(records)) {
		t.Fatalf("summary parts=%d exported=%d", summaries[0].Parts, len(records))
	}
}

func TestSummarizeCDRsRejectsAnUnboundedWindow(t *testing.T) {
	_, repository := newSummaryFixture(t)
	for name, query := range map[string]cdr.SummaryQuery{
		"no bounds": {},
		"no start":  {AdmittedTo: time.Now()},
		"inverted": {
			AdmittedFrom: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			AdmittedTo:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := repository.SummarizeCDRs(context.Background(), query); err == nil {
				t.Fatal("expected an error for an unbounded or inverted window")
			}
		})
	}
}

// TestSummarizeCDRsAccountsForEveryPart is the regression for the statement that
// could not be reconciled: Accepted counted only SMSC_ACCEPTED, so a gateway
// terminating its own traffic reported thousands delivered and zero accepted,
// and five of the seven states landed in no submission bucket at all.
//
// Both groups of counters must partition Parts exactly, whatever mix of states
// the window contains.
func TestSummarizeCDRsAccountsForEveryPart(t *testing.T) {
	db, repository := newSummaryFixture(t)
	base := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	// One part in every state a record can hold, so nothing can hide in a gap.
	states := []struct {
		state    cdr.State
		delivery cdr.DeliveryState
	}{
		{cdr.StateSMSCAccepted, cdr.DeliveryDelivered},
		{cdr.StateSMSCAccepted, cdr.DeliveryNone},
		{cdr.StateTerminatedLocally, cdr.DeliveryDelivered},
		{cdr.StateTerminatedLocally, cdr.DeliveryNone},
		{cdr.StateSMSCRejected, cdr.DeliveryNone},
		{cdr.StateTerminalTimeout, cdr.DeliveryNone},
		{cdr.StateAdmitted, cdr.DeliveryNone},
		{cdr.StateRetryPending, cdr.DeliveryNone},
		{cdr.StateUnknownAfterSend, cdr.DeliveryNone},
		{cdr.StateSMSCAccepted, cdr.DeliveryExpired},
	}
	for index, seed := range states {
		id := fmt.Sprintf("p%02d/000001", index)
		seedSummaryCDR(t, db, repository,
			admissionAt(id, fmt.Sprintf("p%02d", index), "carol", 1, 1,
				base.Add(time.Duration(index)*time.Second), 1, 0),
			seed.state, seed.delivery, cdr.BillingNotApplicable, 0)
	}

	summaries, err := repository.SummarizeCDRs(context.Background(), cdr.SummaryQuery{
		AdmittedFrom: base.Add(-time.Hour), AdmittedTo: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %+v", summaries)
	}
	carol := summaries[0]
	if carol.Parts != int64(len(states)) {
		t.Fatalf("parts = %d, want %d", carol.Parts, len(states))
	}

	submission := carol.Accepted + carol.TerminatedLocally + carol.Rejected +
		carol.Failed + carol.InFlight
	if submission != carol.Parts {
		t.Errorf("submission counters sum to %d, want Parts=%d "+
			"(accepted=%d terminated=%d rejected=%d failed=%d in-flight=%d)",
			submission, carol.Parts, carol.Accepted, carol.TerminatedLocally,
			carol.Rejected, carol.Failed, carol.InFlight)
	}
	delivery := carol.Delivered + carol.Undelivered +
		carol.DeliveryPending + carol.NoReceiptExpected
	if delivery != carol.Parts {
		t.Errorf("delivery counters sum to %d, want Parts=%d "+
			"(delivered=%d undelivered=%d pending=%d no-receipt=%d)",
			delivery, carol.Parts, carol.Delivered, carol.Undelivered,
			carol.DeliveryPending, carol.NoReceiptExpected)
	}

	// The specific contradiction from the reported console screenshot: a
	// terminating gateway must never report deliveries it never accepted.
	if carol.Delivered > carol.Accepted+carol.TerminatedLocally {
		t.Errorf("delivered=%d exceeds accepted+terminated=%d",
			carol.Delivered, carol.Accepted+carol.TerminatedLocally)
	}
	if carol.TerminatedLocally != 2 {
		t.Errorf("terminated locally = %d, want 2", carol.TerminatedLocally)
	}
	// Rejected, timed-out and in-flight parts are owed no receipt.
	if carol.NoReceiptExpected != 5 {
		t.Errorf("no receipt expected = %d, want 5", carol.NoReceiptExpected)
	}
}
