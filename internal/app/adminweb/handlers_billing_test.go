package adminweb

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
)

// stubCDRRepository is the minimum OperationsRepository a real cdr.Service can
// be built on, so these tests exercise the actual authorization and audit path
// the console will run against rather than a mock service.
type stubCDRRepository struct {
	record    cdr.Record
	recordErr error
	events    []cdr.Event
	records   []cdr.Record
	summaries []cdr.UsageSummary
	report    cdr.ReconciliationReport
	pruned    cdr.PruneResult
	pruneErr  error
	audits    []cdr.AccessAudit
}

func (s *stubCDRRepository) GetCDR(context.Context, string) (cdr.Record, error) {
	return s.record, s.recordErr
}

func (s *stubCDRRepository) ListCDREvents(context.Context, string) ([]cdr.Event, error) {
	return s.events, nil
}

func (s *stubCDRRepository) ExportCDRs(context.Context, cdr.ExportQuery) ([]cdr.Record, error) {
	return s.records, nil
}

func (s *stubCDRRepository) SummarizeCDRs(context.Context, cdr.SummaryQuery) ([]cdr.UsageSummary, error) {
	return s.summaries, nil
}

func (s *stubCDRRepository) PruneCDRs(context.Context, time.Time, int) (cdr.PruneResult, error) {
	return s.pruned, s.pruneErr
}

func (s *stubCDRRepository) ReconcileCDRs(context.Context, time.Time) (cdr.ReconciliationReport, error) {
	return s.report, nil
}

func (s *stubCDRRepository) AuditCDRAccess(_ context.Context, audit cdr.AccessAudit) error {
	s.audits = append(s.audits, audit)
	return nil
}

func newCDRService(t *testing.T, repository *stubCDRRepository, retentionDays int) *cdr.Service {
	t.Helper()
	policy := cdr.RetentionPolicy{}
	if retentionDays > 0 {
		policy = cdr.RetentionPolicy{Days: retentionDays, BatchSize: 100}
	}
	service, err := cdr.NewService(repository, policy, nil)
	if err != nil {
		t.Fatalf("new cdr service: %v", err)
	}
	return service
}

func floatPtr(value float64) *float64 { return &value }
func intPtr(value int) *int           { return &value }
func stringPtr(value string) *string  { return &value }

// The provisioned grant and the live balance are different numbers the moment a
// customer sends anything. The console showed only the grant, in a column named
// "balance"; this asserts both reach the browser and stay distinguishable.
func TestBillingAccountsSeparateGrantedFromRemaining(t *testing.T) {
	f := newWebFixture(t)
	f.do("POST", "/api/users",
		`{"username":"alice","password":"pw","balance":100,"submit_sm_count":500,"group_id":"retail"}`,
		http.StatusCreated, nil)
	f.do("POST", "/api/groups", `{"gid":"retail","balance":1000,"submit_sm_count":9000}`,
		http.StatusCreated, nil)
	f.rebuildHandler(func(deps *Deps) {
		deps.BalanceReader = balanceReaderStub{snapshot: core.BalanceSnapshot{
			Balance: stringPtr("3.42"), SMSCount: stringPtr("17"),
		}}
		deps.GroupQuota = func(gid string) (LiveQuota, bool) {
			if gid != "retail" {
				return LiveQuota{}, false
			}
			return LiveQuota{Balance: floatPtr(640.5), SubmitSMCount: intPtr(4000)}, true
		}
	})

	var accounts []accountResource
	f.do("GET", "/api/billing/accounts", "", http.StatusOK, &accounts)
	if len(accounts) != 1 {
		t.Fatalf("accounts=%+v", accounts)
	}
	account := accounts[0]
	if account.GrantedBalance == nil || *account.GrantedBalance != 100 {
		t.Fatalf("granted balance=%+v", account.GrantedBalance)
	}
	if account.RemainingBalance == nil || *account.RemainingBalance != 3.42 {
		t.Fatalf("remaining balance=%+v", account.RemainingBalance)
	}
	if account.GrantedSubmitSMCount == nil || *account.GrantedSubmitSMCount != 500 ||
		account.RemainingSubmitSMCount == nil || *account.RemainingSubmitSMCount != 17 {
		t.Fatalf("submit_sm_count granted=%v remaining=%v",
			account.GrantedSubmitSMCount, account.RemainingSubmitSMCount)
	}
	if account.BillingMode != "PREPAID" {
		t.Fatalf("billing mode=%q", account.BillingMode)
	}
	if account.GroupGrantedBalance == nil || *account.GroupGrantedBalance != 1000 ||
		account.GroupRemainingBalance == nil || *account.GroupRemainingBalance != 640.5 {
		t.Fatalf("group balance granted=%v remaining=%v",
			account.GroupGrantedBalance, account.GroupRemainingBalance)
	}
	if account.LiveError != "" {
		t.Fatalf("unexpected live error %q", account.LiveError)
	}
}

// An unreadable live balance must not render as zero: zero means "cannot send",
// unreadable means "we do not know", and an operator acts differently on each.
func TestBillingAccountsReportALiveReadFailureInsteadOfZero(t *testing.T) {
	f := newWebFixture(t)
	f.do("POST", "/api/users", `{"username":"bob","password":"pw","balance":10}`,
		http.StatusCreated, nil)
	f.rebuildHandler(func(deps *Deps) {
		deps.BalanceReader = balanceReaderStub{err: errors.New("directory unavailable")}
	})

	var accounts []accountResource
	f.do("GET", "/api/billing/accounts", "", http.StatusOK, &accounts)
	if len(accounts) != 1 {
		t.Fatalf("accounts=%+v", accounts)
	}
	if accounts[0].RemainingBalance != nil {
		t.Fatalf("remaining balance was fabricated: %+v", accounts[0].RemainingBalance)
	}
	if !strings.Contains(accounts[0].LiveError, "directory unavailable") {
		t.Fatalf("live error=%q", accounts[0].LiveError)
	}
}

func TestBillingModeDerivation(t *testing.T) {
	for name, testCase := range map[string]struct {
		balance *float64
		percent *int
		want    string
	}{
		"unlimited balance is never charged": {balance: nil, percent: intPtr(50), want: "UNLIMITED"},
		"no percentage means all up front":   {balance: floatPtr(10), percent: nil, want: "PREPAID"},
		"one hundred percent is prepaid":     {balance: floatPtr(10), percent: intPtr(100), want: "PREPAID"},
		"a partial percentage splits":        {balance: floatPtr(10), percent: intPtr(30), want: "SPLIT"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := billingModeFor(testCase.balance, testCase.percent); got != testCase.want {
				t.Fatalf("mode=%q want %q", got, testCase.want)
			}
		})
	}
}

func TestUserListCarriesTheLiveBalanceBesideTheGrant(t *testing.T) {
	f := newWebFixture(t)
	f.do("POST", "/api/users", `{"username":"carol","password":"pw","balance":80}`,
		http.StatusCreated, nil)
	f.rebuildHandler(func(deps *Deps) {
		deps.BalanceReader = balanceReaderStub{snapshot: core.BalanceSnapshot{
			Balance: stringPtr("12.5"),
		}}
	})
	var users []userResource
	f.do("GET", "/api/users", "", http.StatusOK, &users)
	if len(users) != 1 {
		t.Fatalf("users=%+v", users)
	}
	if users[0].Balance == nil || *users[0].Balance != 80 {
		t.Fatalf("granted balance=%v", users[0].Balance)
	}
	if users[0].LiveBalance == nil || *users[0].LiveBalance != 12.5 {
		t.Fatalf("live balance=%v", users[0].LiveBalance)
	}
}

func TestCDRRoutesAnswerServiceUnavailableWithoutACDRService(t *testing.T) {
	f := newWebFixture(t)
	for _, target := range []string{
		"/api/billing/cdrs",
		"/api/billing/cdrs/abc",
		"/api/billing/cdrs/abc/events",
		"/api/billing/export",
		"/api/billing/summary?from=2026-07-01T00:00:00Z&to=2026-07-02T00:00:00Z",
	} {
		f.do("GET", target, "", http.StatusServiceUnavailable, nil)
	}
	f.do("POST", "/api/billing/reconcile", "", http.StatusServiceUnavailable, nil)
}

func TestCDRSearchDetailAndEventsAreServedAndAudited(t *testing.T) {
	admitted := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	repository := &stubCDRRepository{
		record: cdr.Record{
			Admission: cdr.Admission{
				ID: "msg-1/000001", MessageID: "msg-1", PartNumber: 1, PartCount: 1,
				UserID: "alice", RouteID: "mt:0", ConnectorID: "smsc-a", Ingress: "http",
				Rate: 1, Currency: "EUR", EarlyAmount: 0.4, LateAmount: 0.6,
				BillingMode: cdr.BillingSplit, OccurredAt: admitted,
			},
			State: cdr.StateSMSCAccepted, ActualLateAmount: 0.6,
			BillingOutcome: cdr.BillingApplied, DeliveryState: cdr.DeliveryDelivered,
		},
		events: []cdr.Event{
			{Key: "cdr:msg-1/000001:admitted", Kind: cdr.EventAdmitted, State: cdr.StateAdmitted, OccurredAt: admitted},
			{Key: "cdr:msg-1/000001:attempt:1:result", Kind: cdr.EventSMSCAccepted, State: cdr.StateSMSCAccepted, OccurredAt: admitted.Add(time.Second)},
		},
	}
	repository.records = []cdr.Record{repository.record}
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) { deps.CDR = newCDRService(t, repository, 0) })

	var page struct {
		Records    []cdrResource `json:"records"`
		NextCursor string        `json:"next_cursor"`
	}
	f.do("GET", "/api/billing/cdrs?user=alice&limit=10", "", http.StatusOK, &page)
	if len(page.Records) != 1 || page.Records[0].ID != "msg-1/000001" {
		t.Fatalf("records=%+v", page.Records)
	}
	// Charged money is early plus what late billing actually applied.
	if page.Records[0].ChargedTotal != 1 {
		t.Fatalf("charged total=%v", page.Records[0].ChargedTotal)
	}
	if page.NextCursor != "" {
		t.Fatalf("a short page produced cursor %q", page.NextCursor)
	}

	var detail cdrResource
	f.do("GET", "/api/billing/cdrs/msg-1%2F000001", "", http.StatusOK, &detail)
	if detail.UserID != "alice" || detail.BillingOutcome != "APPLIED" {
		t.Fatalf("detail=%+v", detail)
	}

	var events []cdrEventResource
	f.do("GET", "/api/billing/cdrs/msg-1%2F000001/events", "", http.StatusOK, &events)
	if len(events) != 2 || events[1].Kind != "SMSC_ACCEPTED" {
		t.Fatalf("events=%+v", events)
	}

	// Every read is attributed to the logged-in operator, which is the only
	// per-actor trail the console has today.
	if len(repository.audits) != 3 {
		t.Fatalf("audits=%+v", repository.audits)
	}
	for _, audit := range repository.audits {
		if audit.Subject != "admin" || !audit.Allowed || audit.Action != cdr.ActionRead {
			t.Fatalf("audit=%+v", audit)
		}
	}
	// A CDR id is "<message-id>/<part>", so it contains a slash. The audit target
	// is the id the service actually received: if percent-encoding did not
	// survive routing, the detail and events routes would silently address the
	// wrong record.
	if repository.audits[1].Target != "msg-1/000001" || repository.audits[2].Target != "msg-1/000001" {
		t.Fatalf("path value did not survive routing: %+v", repository.audits)
	}
}

func TestCDRExportIsADownloadWithPagingHeaders(t *testing.T) {
	repository := &stubCDRRepository{records: []cdr.Record{{
		Admission: cdr.Admission{
			ID: "msg-2/000001", MessageID: "msg-2", PartNumber: 1, PartCount: 1,
			UserID: "alice", RouteID: "mt:0", ConnectorID: "smsc-a",
			Rate: 1, Currency: "EUR", EarlyAmount: 1, BillingMode: cdr.BillingPrepaid,
			OccurredAt: time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
		},
		State: cdr.StateSMSCAccepted,
	}}}
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) { deps.CDR = newCDRService(t, repository, 0) })

	rec := f.do("GET", "/api/billing/export?user=alice&format=csv", "", http.StatusOK, nil)
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "cdr-export.csv") {
		t.Fatalf("content-disposition=%q", got)
	}
	if got := rec.Header().Get("X-CDR-Record-Count"); got != "1" {
		t.Fatalf("record count header=%q", got)
	}
	if !strings.Contains(rec.Body.String(), "msg-2/000001") {
		t.Fatalf("body=%q", rec.Body.String())
	}
	// The export is audited as an export, not as a read.
	if len(repository.audits) != 1 || repository.audits[0].Action != cdr.ActionExport {
		t.Fatalf("audits=%+v", repository.audits)
	}

	rec = f.do("GET", "/api/billing/export?format=jsonl", "", http.StatusOK, nil)
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "cdr-export.jsonl") {
		t.Fatalf("jsonl content-disposition=%q", got)
	}
}

func TestUsageSummaryRequiresABoundedWindow(t *testing.T) {
	repository := &stubCDRRepository{summaries: []cdr.UsageSummary{{
		UserID: "alice", Currency: "EUR", Parts: 3, Messages: 2,
		ChargedEarly: 1.8, ChargedLate: 0.6, QuotedLatePending: 0.6,
		FirstAdmittedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		LastAdmittedAt:  time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC),
	}}}
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) { deps.CDR = newCDRService(t, repository, 0) })

	f.do("GET", "/api/billing/summary", "", http.StatusBadRequest, nil)
	f.do("GET", "/api/billing/summary?from=2026-07-02T00:00:00Z&to=2026-07-01T00:00:00Z", "",
		http.StatusBadRequest, nil)

	var summaries []usageSummaryResource
	f.do("GET", "/api/billing/summary?from=2026-07-01T00:00:00Z&to=2026-08-01T00:00:00Z", "",
		http.StatusOK, &summaries)
	if len(summaries) != 1 {
		t.Fatalf("summaries=%+v", summaries)
	}
	if summaries[0].ChargedTotal != 2.4 {
		t.Fatalf("charged total=%v want 2.4", summaries[0].ChargedTotal)
	}
	if summaries[0].QuotedLatePending != 0.6 {
		t.Fatalf("quoted late pending=%v", summaries[0].QuotedLatePending)
	}
}

func TestBillingSettingsAreReadOnlyAndFlagThePlaceholderCurrency(t *testing.T) {
	f := newWebFixture(t)

	var payload struct {
		Settings              BillingSettings `json:"settings"`
		CurrencyIsPlaceholder bool            `json:"currency_is_placeholder"`
		RetentionEnabled      bool            `json:"retention_enabled"`
		CDRAvailable          bool            `json:"cdr_available"`
		Editable              bool            `json:"editable"`
	}
	// Without a configured provider the console must still say what it is
	// running on, and XXX must be flagged rather than rendered as money.
	f.do("GET", "/api/billing/settings", "", http.StatusOK, &payload)
	if payload.Settings.Currency != "XXX" || !payload.CurrencyIsPlaceholder {
		t.Fatalf("payload=%+v", payload)
	}
	if payload.Editable || payload.CDRAvailable || payload.RetentionEnabled {
		t.Fatalf("payload=%+v", payload)
	}

	f.rebuildHandler(func(deps *Deps) {
		deps.CDR = newCDRService(t, &stubCDRRepository{}, 30)
		deps.BillingSettings = func() BillingSettings {
			return BillingSettings{
				Currency: "EUR", RetentionDays: 30, RetentionBatchSize: 500,
				MaintenanceIntervalSeconds: 3600, QuotaPersistIntervalSeconds: 10,
			}
		}
	})
	f.do("GET", "/api/billing/settings", "", http.StatusOK, &payload)
	if payload.Settings.Currency != "EUR" || payload.CurrencyIsPlaceholder {
		t.Fatalf("payload=%+v", payload)
	}
	if !payload.RetentionEnabled || !payload.CDRAvailable {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestPruneRequiresConfirmationAndReportsDisabledRetention(t *testing.T) {
	repository := &stubCDRRepository{pruned: cdr.PruneResult{Records: 12, Events: 30}}
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) { deps.CDR = newCDRService(t, repository, 0) })

	// No confirmation: refused before the service is reached.
	f.do("POST", "/api/billing/prune", `{}`, http.StatusBadRequest, nil)
	if len(repository.audits) != 0 {
		t.Fatalf("an unconfirmed prune reached the service: %+v", repository.audits)
	}
	// Confirmed, but retention is disabled in this deployment.
	f.do("POST", "/api/billing/prune", `{"confirm":"PRUNE"}`, http.StatusConflict, nil)

	f.rebuildHandler(func(deps *Deps) { deps.CDR = newCDRService(t, repository, 30) })
	var result struct {
		Records int64 `json:"records"`
		Events  int64 `json:"events"`
	}
	f.do("POST", "/api/billing/prune", `{"confirm":"PRUNE"}`, http.StatusOK, &result)
	if result.Records != 12 || result.Events != 30 {
		t.Fatalf("result=%+v", result)
	}
}

func TestReconcileReportsIssueCounts(t *testing.T) {
	repository := &stubCDRRepository{report: cdr.ReconciliationReport{
		CheckedAt: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		Issues:    []cdr.ReconciliationIssue{{Code: "CDR_WITHOUT_SUBMIT_PART", Count: 2}},
	}}
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) { deps.CDR = newCDRService(t, repository, 0) })

	var payload struct {
		Healthy bool `json:"healthy"`
		Issues  []struct {
			Code  string `json:"code"`
			Count int64  `json:"count"`
		} `json:"issues"`
	}
	f.do("POST", "/api/billing/reconcile", "", http.StatusOK, &payload)
	if payload.Healthy {
		t.Fatal("a report with a non-zero issue count was reported healthy")
	}
	if len(payload.Issues) != 1 || payload.Issues[0].Count != 2 {
		t.Fatalf("issues=%+v", payload.Issues)
	}
}
