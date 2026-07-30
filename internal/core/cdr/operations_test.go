package cdr

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeOperationsRepository records what the service asked for and what it
// audited, so the tests can assert the authorization boundary rather than the
// storage behaviour.
type fakeOperationsRepository struct {
	record    Record
	events    []Event
	summaries []UsageSummary
	exported  []Record

	audits       []AccessAudit
	auditErr     error
	lastExport   ExportQuery
	lastSummary  SummaryQuery
	eventsCalled int
}

func (f *fakeOperationsRepository) GetCDR(context.Context, string) (Record, error) {
	return f.record, nil
}

func (f *fakeOperationsRepository) ListCDREvents(context.Context, string) ([]Event, error) {
	f.eventsCalled++
	return f.events, nil
}

func (f *fakeOperationsRepository) ExportCDRs(_ context.Context, query ExportQuery) ([]Record, error) {
	f.lastExport = query
	return f.exported, nil
}

func (f *fakeOperationsRepository) SummarizeCDRs(_ context.Context, query SummaryQuery) ([]UsageSummary, error) {
	f.lastSummary = query
	return f.summaries, nil
}

func (f *fakeOperationsRepository) PruneCDRs(context.Context, time.Time, int) (PruneResult, error) {
	return PruneResult{}, nil
}

func (f *fakeOperationsRepository) ReconcileCDRs(context.Context, time.Time) (ReconciliationReport, error) {
	return ReconciliationReport{}, nil
}

func (f *fakeOperationsRepository) AuditCDRAccess(_ context.Context, audit AccessAudit) error {
	f.audits = append(f.audits, audit)
	return f.auditErr
}

func newTestService(t *testing.T, repository *fakeOperationsRepository) *Service {
	t.Helper()
	service, err := NewService(repository, RetentionPolicy{}, func() time.Time {
		return time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service
}

func TestEventsRequiresReadAuthorizationAndIsAudited(t *testing.T) {
	repository := &fakeOperationsRepository{events: []Event{{Key: "cdr:a:admitted", Kind: EventAdmitted}}}
	service := newTestService(t, repository)
	ctx := context.Background()

	// The raw repository method has neither authorization nor an audit trail, so
	// a surface that reached past the service would leak commercial history
	// silently. Refusal must still be recorded.
	if _, err := service.Events(ctx, Principal{Subject: "nobody"}, "a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized Events error=%v", err)
	}
	if repository.eventsCalled != 0 {
		t.Fatal("repository was reached without authorization")
	}
	if len(repository.audits) != 1 || repository.audits[0].Allowed {
		t.Fatalf("audits=%+v", repository.audits)
	}

	events, err := service.Events(ctx, Principal{Subject: "ops", Roles: []Role{RoleReader}}, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || repository.eventsCalled != 1 {
		t.Fatalf("events=%+v calls=%d", events, repository.eventsCalled)
	}
	last := repository.audits[len(repository.audits)-1]
	if !last.Allowed || last.Subject != "ops" || last.Action != ActionRead || last.Target != "a" {
		t.Fatalf("audit=%+v", last)
	}
}

func TestEventsRejectsAnEmptyIDBeforeAuditing(t *testing.T) {
	repository := &fakeOperationsRepository{}
	service := newTestService(t, repository)
	if _, err := service.Events(context.Background(), Principal{Subject: "ops", Roles: []Role{RoleOperator}}, "  "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error=%v", err)
	}
	if len(repository.audits) != 0 {
		t.Fatalf("audits=%+v", repository.audits)
	}
}

func TestSearchPagesAndCarriesFiltersThrough(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	repository := &fakeOperationsRepository{exported: []Record{
		{Admission: Admission{ID: "one", OccurredAt: from}},
		{Admission: Admission{ID: "two", OccurredAt: from.Add(time.Minute)}},
	}}
	service := newTestService(t, repository)
	page, err := service.Search(context.Background(),
		Principal{Subject: "ops", Roles: []Role{RoleReader}},
		SearchRequest{UserID: "alice", AdmittedFrom: &from, AdmittedTo: &to, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if repository.lastExport.UserID != "alice" || repository.lastExport.Limit != 2 {
		t.Fatalf("query=%+v", repository.lastExport)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records=%+v", page.Records)
	}
	// A full page must offer a cursor, or the caller silently sees a truncated
	// window and believes it is complete.
	if page.NextCursor == "" {
		t.Fatal("a full page produced no next cursor")
	}

	// A short page is the end of the window and must not offer one.
	repository.exported = repository.exported[:1]
	page, err = service.Search(context.Background(),
		Principal{Subject: "ops", Roles: []Role{RoleReader}}, SearchRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "" {
		t.Fatalf("short page produced cursor %q", page.NextCursor)
	}
}

func TestSearchRefusesAnExporterWithoutReadRights(t *testing.T) {
	repository := &fakeOperationsRepository{}
	service := newTestService(t, repository)
	if _, err := service.Search(context.Background(),
		Principal{Subject: "batch", Roles: []Role{RoleExporter}}, SearchRequest{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("error=%v", err)
	}
}

func TestSummarizeRequiresABoundedWindowAndNormalisesToUTC(t *testing.T) {
	repository := &fakeOperationsRepository{}
	service := newTestService(t, repository)
	principal := Principal{Subject: "ops", Roles: []Role{RoleReader}}
	ctx := context.Background()

	if _, err := service.Summarize(ctx, principal, SummaryQuery{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unbounded error=%v", err)
	}
	if len(repository.audits) != 0 {
		t.Fatal("an invalid query reached the audit trail")
	}

	zone := time.FixedZone("UTC+3", 3*60*60)
	from := time.Date(2026, 7, 1, 3, 0, 0, 0, zone)
	to := time.Date(2026, 7, 2, 3, 0, 0, 0, zone)
	if _, err := service.Summarize(ctx, principal, SummaryQuery{
		UserID: "alice", AdmittedFrom: from, AdmittedTo: to,
	}); err != nil {
		t.Fatal(err)
	}
	if repository.lastSummary.AdmittedFrom.Location() != time.UTC ||
		!repository.lastSummary.AdmittedFrom.Equal(from) {
		t.Fatalf("from=%v", repository.lastSummary.AdmittedFrom)
	}
	if len(repository.audits) != 1 || repository.audits[0].Action != ActionRead {
		t.Fatalf("audits=%+v", repository.audits)
	}
}

func TestReadsFailClosedWhenTheAuditTrailCannotBeWritten(t *testing.T) {
	repository := &fakeOperationsRepository{auditErr: errors.New("audit table is read-only")}
	service := newTestService(t, repository)
	principal := Principal{Subject: "ops", Roles: []Role{RoleOperator}}
	ctx := context.Background()
	if _, err := service.Events(ctx, principal, "a"); err == nil {
		t.Fatal("Events succeeded without an audit record")
	}
	if _, err := service.Search(ctx, principal, SearchRequest{}); err == nil {
		t.Fatal("Search succeeded without an audit record")
	}
	if _, err := service.Summarize(ctx, principal, SummaryQuery{
		AdmittedFrom: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		AdmittedTo:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	}); err == nil {
		t.Fatal("Summarize succeeded without an audit record")
	}
	if repository.eventsCalled != 0 {
		t.Fatal("the repository was read despite the audit failure")
	}
}

func TestUsageSummaryChargedTotalIsAppliedMoneyOnly(t *testing.T) {
	summary := UsageSummary{ChargedEarly: 1.5, ChargedLate: 0.5, QuotedLatePending: 3}
	if summary.ChargedTotal() != 2 {
		t.Fatalf("total=%v want 2 (pending late money is not revenue)", summary.ChargedTotal())
	}
}
