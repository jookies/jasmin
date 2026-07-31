package storage

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// TestSQLiteMessageSpoolCensus covers the three numbers behind the operator
// gauges: spool rows, dead-letter depth, and receipts owed but not sent.
func TestSQLiteMessageSpoolCensus(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)

	overdue := spoolBase.Add(-time.Minute)
	future := spoolBase.Add(time.Hour)

	// Two rows on one connector: one with a receipt already overdue, one whose
	// receipt is not due yet.
	late := spoolMessage("msg-late", spoolBase)
	late.ReceiptDueAt = &overdue
	if _, err := store.Put(ctx, late, spoolBase); err != nil {
		t.Fatal(err)
	}
	early := spoolMessage("msg-early", spoolBase)
	early.ReceiptDueAt = &future
	if _, err := store.Put(ctx, early, spoolBase); err != nil {
		t.Fatal(err)
	}
	// A third row on the same connector, dead-lettered.
	dead := spoolMessage("msg-dead", spoolBase)
	dead.ReceiptDueAt = &overdue
	if _, err := store.Put(ctx, dead, spoolBase); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeadLettered(ctx, "msg-dead"); err != nil {
		t.Fatal(err)
	}
	// A row on a second connector, so the census is proved to be per connector
	// rather than a single global count wearing a label.
	other := spoolMessage("msg-other", spoolBase)
	other.MessageID = "msg-other"
	other.ConnectorID = "partner-b-term"
	if _, err := store.Put(ctx, other, spoolBase); err != nil {
		t.Fatal(err)
	}

	census, err := store.Census(ctx, spoolBase)
	if err != nil {
		t.Fatal(err)
	}
	byConnector := map[string]msgspool.ConnectorCensus{}
	for _, entry := range census {
		byConnector[entry.ConnectorID] = entry
	}
	a := byConnector["partner-a-term"]
	if a.Rows != 3 {
		t.Errorf("partner-a rows = %d, want 3", a.Rows)
	}
	if a.DeadLettered != 1 {
		t.Errorf("partner-a dead-lettered = %d, want 1", a.DeadLettered)
	}
	// Two rows are past their receipt_due_at and unsent; the third is not due.
	if a.ReceiptsOverdue != 2 {
		t.Errorf("partner-a receipts overdue = %d, want 2", a.ReceiptsOverdue)
	}
	b := byConnector["partner-b-term"]
	if b.Rows != 1 || b.DeadLettered != 0 {
		t.Errorf("partner-b census = %+v, want 1 row and no dead letters", b)
	}

	// A sent receipt stops being owed. This is what makes the gauge fall, and a
	// gauge that only rises is the bug the census exists to avoid.
	claimed, err := store.ClaimDueReceipts(ctx, "owner-1", spoolBase, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) == 0 {
		t.Fatal("no receipts claimed")
	}
	if err := store.MarkReceiptSent(ctx, "msg-late", "owner-1", spoolBase); err != nil {
		t.Fatal(err)
	}
	census, err = store.Census(ctx, spoolBase)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range census {
		if entry.ConnectorID == "partner-a-term" && entry.ReceiptsOverdue != 1 {
			t.Errorf("after sending one receipt, overdue = %d, want 1", entry.ReceiptsOverdue)
		}
	}
}

// TestSQLiteMessageSpoolSearchDecisionTrailFilters covers the two filters the
// decision trail adds, including the one an operator runs after a Redis outage.
func TestSQLiteMessageSpoolSearchDecisionTrailFilters(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteMessageSpool(t)

	accepted := spoolMessage("msg-ok", spoolBase)
	if _, err := store.Put(ctx, accepted, spoolBase); err != nil {
		t.Fatal(err)
	}
	rejected := spoolMessage("msg-no", spoolBase)
	rejected.Verdict = msgspool.Verdict{
		Accept: false, Stat: "REJECTD", Err: "008", Reason: "no activation window",
	}
	if _, err := store.Put(ctx, rejected, spoolBase); err != nil {
		t.Fatal(err)
	}
	blind := spoolMessage("msg-blind", spoolBase)
	blind.Verdict = msgspool.Verdict{
		Accept: true, Stat: "DELIVRD", Err: "000",
		Reason: "activation gate unreachable, failed open", GateBypassed: true,
	}
	if _, err := store.Put(ctx, blind, spoolBase); err != nil {
		t.Fatal(err)
	}

	byStat, err := store.Search(ctx, msgspool.Query{VerdictStat: "REJECTD", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(byStat) != 1 || byStat[0].MessageID != "msg-no" {
		t.Fatalf("stat filter returned %d rows: %+v", len(byStat), byStat)
	}

	bypassed, err := store.Search(ctx, msgspool.Query{GateBypassedOnly: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(bypassed) != 1 || bypassed[0].MessageID != "msg-blind" {
		t.Fatalf("bypass filter returned %d rows: %+v", len(bypassed), bypassed)
	}

	// Unset filters must not narrow anything: a zero value means "no filter",
	// and a trail that silently returned only bypassed rows by default would
	// make a Redis outage look like the normal state of the world.
	all, err := store.Search(ctx, msgspool.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered search returned %d rows, want 3", len(all))
	}
}
