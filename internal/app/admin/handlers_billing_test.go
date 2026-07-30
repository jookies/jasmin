package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
)

// stubCDRRepository backs a real cdr.Service so these tests exercise the actual
// authorization and audit path rather than a mocked service.
type stubCDRRepository struct {
	record  cdr.Record
	events  []cdr.Event
	records []cdr.Record
	audits  []cdr.AccessAudit
	getIDs  []string
}

func (s *stubCDRRepository) GetCDR(_ context.Context, id string) (cdr.Record, error) {
	s.getIDs = append(s.getIDs, id)
	return s.record, nil
}

func (s *stubCDRRepository) ListCDREvents(_ context.Context, id string) ([]cdr.Event, error) {
	s.getIDs = append(s.getIDs, id)
	return s.events, nil
}

func (s *stubCDRRepository) ExportCDRs(context.Context, cdr.ExportQuery) ([]cdr.Record, error) {
	return s.records, nil
}

func (s *stubCDRRepository) SummarizeCDRs(context.Context, cdr.SummaryQuery) ([]cdr.UsageSummary, error) {
	return []cdr.UsageSummary{{
		UserID: "alice", Currency: "EUR", Parts: 2, Messages: 1,
		ChargedEarly: 0.5, ChargedLate: 0.25, QuotedLatePending: 0.25,
		FirstAdmittedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		LastAdmittedAt:  time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC),
	}}, nil
}

func (s *stubCDRRepository) PruneCDRs(context.Context, time.Time, int) (cdr.PruneResult, error) {
	return cdr.PruneResult{}, nil
}

func (s *stubCDRRepository) ReconcileCDRs(context.Context, time.Time) (cdr.ReconciliationReport, error) {
	return cdr.ReconciliationReport{}, nil
}

func (s *stubCDRRepository) AuditCDRAccess(_ context.Context, audit cdr.AccessAudit) error {
	s.audits = append(s.audits, audit)
	return nil
}

type stubBalanceReader struct {
	snapshot core.BalanceSnapshot
	err      error
}

func (s stubBalanceReader) Balance(context.Context, string) (core.BalanceSnapshot, error) {
	return s.snapshot, s.err
}

func testCDRRecord() cdr.Record {
	return cdr.Record{
		Admission: cdr.Admission{
			ID: "msg-1/000001", MessageID: "msg-1", PartNumber: 1, PartCount: 1,
			UserID: "alice", RouteID: "mt:0", ConnectorID: "smsc-a", Ingress: "httpapi",
			Rate: 1, Currency: "EUR", EarlyAmount: 0.5, LateAmount: 0.5,
			BillingMode: cdr.BillingSplit,
			OccurredAt:  time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
		},
		State: cdr.StateSMSCAccepted, ActualLateAmount: 0.5,
		BillingOutcome: cdr.BillingApplied,
	}
}

func newBillingHandler(t *testing.T, repository *stubCDRRepository, balance core.BalanceReader,
	configAccounts func() []ProvisionedAccount,
) (*Handler, *UserService) {
	t.Helper()
	service, _, store := newTestService(t)
	userService, err := NewUserService(store, newFakeUserProvisioner(2), func() string {
		return "2026-07-30T00:00:00Z"
	})
	if err != nil {
		t.Fatal(err)
	}
	var cdrService *cdr.Service
	if repository != nil {
		cdrService, err = cdr.NewService(repository, cdr.RetentionPolicy{}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHandler(service, nil, userService, "secret",
		WithBilling(cdrService, balance, configAccounts))
	if err != nil {
		t.Fatal(err)
	}
	return handler, userService
}

// Every new route sits behind the same bearer gate as the rest of the plane.
func TestBillingRoutesRequireTheAdminToken(t *testing.T) {
	repository := &stubCDRRepository{record: testCDRRecord()}
	handler, _ := newBillingHandler(t, repository,
		stubBalanceReader{snapshot: core.BalanceSnapshot{}}, nil)
	for _, path := range []string{
		"/admin/cdrs",
		"/admin/cdrs/msg-1/000001",
		"/admin/cdrs/msg-1/000001/events",
		"/admin/cdrs/export",
		"/admin/billing/summary?from=2026-07-01T00:00:00Z&to=2026-08-01T00:00:00Z",
		"/admin/billing/accounts",
	} {
		if rec := doAdmin(t, handler, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a token: status=%d want 401", path, rec.Code)
		}
	}
	if len(repository.audits) != 0 {
		t.Fatalf("an unauthenticated request reached the CDR service: %+v", repository.audits)
	}
}

// Without the option the routes must not exist. A deployment with no CDR
// repository answering an empty list would read as "this customer sent nothing".
func TestBillingRoutesAreAbsentWithoutTheOption(t *testing.T) {
	service, _, store := newTestService(t)
	userService, err := NewUserService(store, newFakeUserProvisioner(2), func() string {
		return "2026-07-30T00:00:00Z"
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, nil, userService, "secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/cdrs", "/admin/billing/summary", "/admin/billing/accounts"} {
		if rec := doAdmin(t, handler, http.MethodGet, path, "secret", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d want 404 when billing is not wired", path, rec.Code)
		}
	}
}

// A CDR id is "<message-id>/<part>" and therefore contains a slash. Prefix
// routing must carry it through intact in both raw and percent-encoded form,
// or detail and events silently address the wrong record.
func TestCDRDetailAndEventsAcceptASlashedID(t *testing.T) {
	repository := &stubCDRRepository{
		record: testCDRRecord(),
		events: []cdr.Event{{Key: "cdr:msg-1/000001:admitted", Kind: cdr.EventAdmitted}},
	}
	handler, _ := newBillingHandler(t, repository, nil, nil)

	for _, path := range []string{"/admin/cdrs/msg-1/000001", "/admin/cdrs/msg-1%2F000001"} {
		rec := doAdmin(t, handler, http.MethodGet, path, "secret", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		var payload cdrPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ID != "msg-1/000001" || payload.ChargedTotal != 1 {
			t.Fatalf("%s: payload=%+v", path, payload)
		}
	}
	rec := doAdmin(t, handler, http.MethodGet, "/admin/cdrs/msg-1/000001/events", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var events []cdrEventPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "ADMITTED" {
		t.Fatalf("events=%+v", events)
	}
	for _, id := range repository.getIDs {
		if id != "msg-1/000001" {
			t.Fatalf("service received id %q, not the full slashed id", id)
		}
	}
}

func TestAdminCDRReadsAreAuditedAsTheAPISurface(t *testing.T) {
	repository := &stubCDRRepository{record: testCDRRecord()}
	repository.records = []cdr.Record{repository.record}
	handler, _ := newBillingHandler(t, repository, nil, nil)

	doAdmin(t, handler, http.MethodGet, "/admin/cdrs?user=alice", "secret", nil)
	doAdmin(t, handler, http.MethodGet, "/admin/cdrs/export?format=csv", "secret", nil)
	if len(repository.audits) != 2 {
		t.Fatalf("audits=%+v", repository.audits)
	}
	// The admin plane has one shared token, so there is no per-caller identity
	// to record. Naming the surface beats inventing an actor.
	for _, audit := range repository.audits {
		if audit.Subject != "admin-api" || !audit.Allowed {
			t.Fatalf("audit=%+v", audit)
		}
	}
	if repository.audits[0].Action != cdr.ActionRead || repository.audits[1].Action != cdr.ActionExport {
		t.Fatalf("actions=%v/%v", repository.audits[0].Action, repository.audits[1].Action)
	}
}

func TestAdminExportPassesTheCoreBytesThroughWithPagingHeaders(t *testing.T) {
	repository := &stubCDRRepository{record: testCDRRecord()}
	repository.records = []cdr.Record{repository.record}
	handler, _ := newBillingHandler(t, repository, nil, nil)

	rec := doAdmin(t, handler, http.MethodGet, "/admin/cdrs/export?format=csv", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-CDR-Record-Count"); got != "1" {
		t.Fatalf("record count header=%q", got)
	}
	if got := rec.Header().Get("X-CDR-Schema-Version"); got == "" {
		t.Fatal("schema version header missing")
	}
	if !strings.Contains(rec.Body.String(), "msg-1/000001") {
		t.Fatalf("body=%q", rec.Body.String())
	}
	// Default format is JSONL, matching the core's own default.
	rec = doAdmin(t, handler, http.MethodGet, "/admin/cdrs/export", "secret", nil)
	if !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "{") {
		t.Fatalf("default format is not JSONL: %q", rec.Body.String())
	}
}

func TestAdminUsageSummaryRequiresABoundedWindow(t *testing.T) {
	handler, _ := newBillingHandler(t, &stubCDRRepository{}, nil, nil)

	if rec := doAdmin(t, handler, http.MethodGet, "/admin/billing/summary", "secret", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unbounded: status=%d want 400", rec.Code)
	}
	if rec := doAdmin(t, handler, http.MethodGet,
		"/admin/billing/summary?from=2026-07-02T00:00:00Z&to=2026-07-01T00:00:00Z", "secret", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("inverted: status=%d want 400", rec.Code)
	}
	rec := doAdmin(t, handler, http.MethodGet,
		"/admin/billing/summary?from=2026-07-01T00:00:00Z&to=2026-08-01T00:00:00Z", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0]["charged_total"] != 0.75 {
		t.Fatalf("payload=%+v", payload)
	}
	// Late money still quoted is reported apart from charged money.
	if payload[0]["quoted_late_pending"] != 0.25 {
		t.Fatalf("quoted_late_pending=%v", payload[0]["quoted_late_pending"])
	}
}

func TestAdminAccountsSeparateGrantedFromRemaining(t *testing.T) {
	granted := 100.0
	count := 500
	configAccounts := func() []ProvisionedAccount {
		return []ProvisionedAccount{{
			Username: "configured", ManagedBy: "config",
			Balance: &granted, SubmitSMCount: &count,
		}}
	}
	balance := "3.5"
	remaining := "17"
	handler, users := newBillingHandler(t, &stubCDRRepository{},
		stubBalanceReader{snapshot: core.BalanceSnapshot{Balance: &balance, SMSCount: &remaining}},
		configAccounts)
	if err := users.CreateUser(context.Background(), "alice",
		`{"username":"alice","balance":42,"submit_sm_count":7,"early_decrement_balance_percent":50}`); err != nil {
		t.Fatal(err)
	}

	rec := doAdmin(t, handler, http.MethodGet, "/admin/billing/accounts", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 {
		t.Fatalf("payload=%+v", payload)
	}
	config, admin := payload[0], payload[1]
	if config["managed_by"] != "config" || config["granted_balance"] != 100.0 {
		t.Fatalf("config account=%+v", config)
	}
	if admin["managed_by"] != "admin" || admin["granted_balance"] != 42.0 {
		t.Fatalf("admin account=%+v", admin)
	}
	// Granted comes from the provisioned spec, remaining from the live
	// directory. They are different numbers after any traffic.
	if admin["remaining_balance"] != 3.5 || admin["remaining_submit_sm_count"] != 17.0 {
		t.Fatalf("live values=%+v", admin)
	}
	if admin["billing_mode"] != "SPLIT" || config["billing_mode"] != "PREPAID" {
		t.Fatalf("modes: admin=%v config=%v", admin["billing_mode"], config["billing_mode"])
	}
}

// A live read failure must be reported, not rendered as zero: a consumer that
// saw 0 would suspend a customer who is merely unreadable.
func TestAdminAccountsReportALiveReadFailure(t *testing.T) {
	handler, _ := newBillingHandler(t, &stubCDRRepository{},
		stubBalanceReader{err: errors.New("directory unavailable")},
		func() []ProvisionedAccount {
			return []ProvisionedAccount{{Username: "alice", ManagedBy: "config"}}
		})
	rec := doAdmin(t, handler, http.MethodGet, "/admin/billing/accounts", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var payload []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, present := payload[0]["remaining_balance"]; present {
		t.Fatalf("a failed live read produced a balance: %+v", payload[0])
	}
	if !strings.Contains(payload[0]["live_error"].(string), "directory unavailable") {
		t.Fatalf("live_error=%v", payload[0]["live_error"])
	}
}
