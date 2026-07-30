package cdr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const ExportSchemaVersion = 1

type Role string

const (
	RoleReader   Role = "cdr_reader"
	RoleExporter Role = "cdr_exporter"
	RoleOperator Role = "cdr_operator"
)

type Action string

const (
	ActionRead      Action = "read"
	ActionExport    Action = "export"
	ActionPrune     Action = "prune"
	ActionReconcile Action = "reconcile"
)

type Principal struct {
	Subject string
	Roles   []Role
}

func (principal Principal) Allows(action Action) bool {
	if strings.TrimSpace(principal.Subject) == "" {
		return false
	}
	for _, role := range principal.Roles {
		if role == RoleOperator {
			return true
		}
		if action == ActionRead && role == RoleReader {
			return true
		}
		if action == ActionExport && role == RoleExporter {
			return true
		}
	}
	return false
}

type AccessAudit struct {
	Subject    string
	Action     Action
	Target     string
	Allowed    bool
	OccurredAt time.Time
}

type ExportFormat string

const (
	ExportJSONL ExportFormat = "jsonl"
	ExportCSV   ExportFormat = "csv"
)

type ExportQuery struct {
	After   time.Time
	AfterID string
	UserID  string
	// MessageID selects every part of one logical message. It answers the
	// operator's actual question — "what happened to the ID I handed the
	// customer?" — and rides the cdr_records(message_id, part_number) index.
	MessageID    string
	AdmittedFrom *time.Time
	AdmittedTo   *time.Time
	Limit        int
}

type ExportRequest struct {
	Cursor       string
	UserID       string
	MessageID    string
	AdmittedFrom *time.Time
	AdmittedTo   *time.Time
	Limit        int
	Format       ExportFormat
}

type ExportPage struct {
	SchemaVersion int
	ContentType   string
	Payload       []byte
	NextCursor    string
	RecordCount   int
}

type PruneResult struct {
	Records int64
	Events  int64
}

type ReconciliationIssue struct {
	Code  string
	Count int64
}

type ReconciliationReport struct {
	CheckedAt time.Time
	Issues    []ReconciliationIssue
}

func (report ReconciliationReport) Healthy() bool {
	for _, issue := range report.Issues {
		if issue.Count > 0 {
			return false
		}
	}
	return true
}

// SummaryQuery aggregates rated usage in the database. Both time bounds are
// required: an unbounded aggregate is a full-table scan disguised as a report,
// and every caller so far has a window in hand.
type SummaryQuery struct {
	UserID       string
	AdmittedFrom time.Time
	AdmittedTo   time.Time
}

// UsageSummary is one customer's rated usage over the queried window, grouped
// by currency so a mid-window currency change cannot silently add two different
// units into one total.
//
// The money split is deliberate. ChargedEarly was applied before admission and
// is never refunded when the SMSC later rejects the part. ChargedLate is what
// the idempotent late-billing ledger actually applied, not what was quoted;
// QuotedLatePending is the quoted late money whose outcome is still PENDING, so
// a statement can show committed and unsettled amounts apart instead of
// presenting an intent as revenue.
type UsageSummary struct {
	UserID            string
	Currency          string
	Parts             int64
	Messages          int64
	Accepted          int64
	Rejected          int64
	Delivered         int64
	Undelivered       int64
	DeliveryPending   int64
	ChargedEarly      float64
	ChargedLate       float64
	QuotedLatePending float64
	FirstAdmittedAt   time.Time
	LastAdmittedAt    time.Time
}

// ChargedTotal is the money actually taken from the customer in the window.
func (summary UsageSummary) ChargedTotal() float64 {
	return summary.ChargedEarly + summary.ChargedLate
}

type OperationsRepository interface {
	Repository
	ExportCDRs(context.Context, ExportQuery) ([]Record, error)
	SummarizeCDRs(context.Context, SummaryQuery) ([]UsageSummary, error)
	PruneCDRs(context.Context, time.Time, int) (PruneResult, error)
	ReconcileCDRs(context.Context, time.Time) (ReconciliationReport, error)
	AuditCDRAccess(context.Context, AccessAudit) error
}

type RetentionPolicy struct {
	Days      int
	BatchSize int
}

func (policy RetentionPolicy) Validate() error {
	if policy.Days < 0 || policy.BatchSize < 0 {
		return ErrInvalidInput
	}
	if policy.Days > 0 && policy.BatchSize == 0 {
		return ErrInvalidInput
	}
	return nil
}

type Service struct {
	repository OperationsRepository
	retention  RetentionPolicy
	now        func() time.Time
}

func NewService(repository OperationsRepository, retention RetentionPolicy, now func() time.Time) (*Service, error) {
	if repository == nil || retention.Validate() != nil {
		return nil, ErrInvalidInput
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, retention: retention, now: now}, nil
}

func (service *Service) Get(ctx context.Context, principal Principal, id string) (Record, error) {
	if strings.TrimSpace(id) == "" {
		return Record{}, ErrInvalidInput
	}
	if err := service.authorize(ctx, principal, ActionRead, id); err != nil {
		return Record{}, err
	}
	return service.repository.GetCDR(ctx, id)
}

// Events returns one record's immutable audit trail through the same
// authorization and access-audit boundary as Get. The raw repository method is
// reachable without either, so management surfaces must call this one.
func (service *Service) Events(ctx context.Context, principal Principal, id string) ([]Event, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalidInput
	}
	if err := service.authorize(ctx, principal, ActionRead, id); err != nil {
		return nil, err
	}
	return service.repository.ListCDREvents(ctx, id)
}

// SearchRequest pages records for a management surface. It carries the same
// filters as ExportRequest without a format: the caller wants records, not an
// encoded file.
type SearchRequest struct {
	Cursor       string
	UserID       string
	MessageID    string
	AdmittedFrom *time.Time
	AdmittedTo   *time.Time
	Limit        int
}

type SearchPage struct {
	Records    []Record
	NextCursor string
}

// Search returns a page of records through the read authorization. Export
// remains the byte-producing path; this one exists so a console does not have to
// decode an export to render a table.
func (service *Service) Search(ctx context.Context, principal Principal, request SearchRequest) (SearchPage, error) {
	if request.Limit == 0 {
		request.Limit = 100
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return SearchPage{}, ErrInvalidInput
	}
	if err := service.authorize(ctx, principal, ActionRead, searchAuditTarget(request)); err != nil {
		return SearchPage{}, err
	}
	after, afterID, err := decodeCursor(request.Cursor)
	if err != nil {
		return SearchPage{}, err
	}
	records, err := service.repository.ExportCDRs(ctx, ExportQuery{
		After: after, AfterID: afterID, UserID: request.UserID,
		MessageID:    request.MessageID,
		AdmittedFrom: request.AdmittedFrom, AdmittedTo: request.AdmittedTo,
		Limit: request.Limit,
	})
	if err != nil {
		return SearchPage{}, err
	}
	page := SearchPage{Records: records}
	if len(records) == request.Limit {
		last := records[len(records)-1]
		page.NextCursor = encodeCursor(last.OccurredAt, last.ID)
	}
	return page, nil
}

// Summarize aggregates rated usage over a required time window. It is a read,
// not an export: it returns money totals rather than the underlying records, and
// is audited as such.
func (service *Service) Summarize(ctx context.Context, principal Principal, query SummaryQuery) ([]UsageSummary, error) {
	if query.AdmittedFrom.IsZero() || query.AdmittedTo.IsZero() ||
		!query.AdmittedTo.After(query.AdmittedFrom) {
		return nil, ErrInvalidInput
	}
	if err := service.authorize(ctx, principal, ActionRead, summaryAuditTarget(query)); err != nil {
		return nil, err
	}
	query.AdmittedFrom = query.AdmittedFrom.UTC()
	query.AdmittedTo = query.AdmittedTo.UTC()
	return service.repository.SummarizeCDRs(ctx, query)
}

func (service *Service) Export(ctx context.Context, principal Principal, request ExportRequest) (ExportPage, error) {
	if err := service.authorize(ctx, principal, ActionExport, exportAuditTarget(request)); err != nil {
		return ExportPage{}, err
	}
	if request.Limit == 0 {
		request.Limit = 1000
	}
	if request.Limit < 1 || request.Limit > 10000 {
		return ExportPage{}, ErrInvalidInput
	}
	if request.Format == "" {
		request.Format = ExportJSONL
	}
	if request.Format != ExportJSONL && request.Format != ExportCSV {
		return ExportPage{}, ErrInvalidInput
	}
	after, afterID, err := decodeCursor(request.Cursor)
	if err != nil {
		return ExportPage{}, err
	}
	records, err := service.repository.ExportCDRs(ctx, ExportQuery{
		After: after, AfterID: afterID, UserID: request.UserID,
		MessageID:    request.MessageID,
		AdmittedFrom: request.AdmittedFrom, AdmittedTo: request.AdmittedTo,
		Limit: request.Limit,
	})
	if err != nil {
		return ExportPage{}, err
	}
	payload, contentType, err := encodeExport(records, request.Format)
	if err != nil {
		return ExportPage{}, err
	}
	page := ExportPage{
		SchemaVersion: ExportSchemaVersion, ContentType: contentType,
		Payload: payload, RecordCount: len(records),
	}
	if len(records) == request.Limit {
		last := records[len(records)-1]
		page.NextCursor = encodeCursor(last.OccurredAt, last.ID)
	}
	return page, nil
}

func (service *Service) Prune(ctx context.Context, principal Principal) (PruneResult, error) {
	if err := service.authorize(ctx, principal, ActionPrune, "retention"); err != nil {
		return PruneResult{}, err
	}
	if service.retention.Days == 0 {
		return PruneResult{}, ErrDisabled
	}
	cutoff := service.now().UTC().AddDate(0, 0, -service.retention.Days)
	return service.repository.PruneCDRs(ctx, cutoff, service.retention.BatchSize)
}

func (service *Service) Reconcile(ctx context.Context, principal Principal) (ReconciliationReport, error) {
	if err := service.authorize(ctx, principal, ActionReconcile, "commercial-ledger"); err != nil {
		return ReconciliationReport{}, err
	}
	return service.repository.ReconcileCDRs(ctx, service.now().UTC())
}

func (service *Service) authorize(ctx context.Context, principal Principal, action Action, target string) error {
	allowed := principal.Allows(action)
	audit := AccessAudit{
		Subject: principal.Subject, Action: action, Target: target,
		Allowed: allowed, OccurredAt: service.now().UTC(),
	}
	if err := service.repository.AuditCDRAccess(ctx, audit); err != nil {
		// Access is fail-closed when its audit trail cannot be persisted.
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

type cursorV1 struct {
	Version    int    `json:"v"`
	AdmittedAt string `json:"at"`
	ID         string `json:"id"`
}

func encodeCursor(admittedAt time.Time, id string) string {
	raw, _ := json.Marshal(cursorV1{
		Version: ExportSchemaVersion, AdmittedAt: admittedAt.UTC().Format(time.RFC3339Nano), ID: id,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(value string) (time.Time, string, error) {
	if value == "" {
		return time.Time{}, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", ErrInvalidInput
	}
	var cursor cursorV1
	if err = json.Unmarshal(raw, &cursor); err != nil ||
		cursor.Version != ExportSchemaVersion || cursor.ID == "" {
		return time.Time{}, "", ErrInvalidInput
	}
	at, err := time.Parse(time.RFC3339Nano, cursor.AdmittedAt)
	if err != nil {
		return time.Time{}, "", ErrInvalidInput
	}
	return at.UTC(), cursor.ID, nil
}

type exportRecordV1 struct {
	SchemaVersion      int            `json:"schema_version"`
	ID                 string         `json:"cdr_id"`
	MessageID          string         `json:"message_id"`
	PartNumber         int            `json:"part_number"`
	PartCount          int            `json:"part_count"`
	UserID             string         `json:"user_id"`
	GroupID            string         `json:"group_id"`
	RouteID            string         `json:"route_id"`
	ConnectorID        string         `json:"connector_id"`
	Ingress            string         `json:"ingress"`
	BillID             string         `json:"bill_id"`
	Rate               float64        `json:"rate"`
	Currency           string         `json:"currency"`
	EarlyAmount        float64        `json:"early_amount"`
	LateAmount         float64        `json:"late_amount"`
	ActualLateAmount   float64        `json:"actual_late_amount"`
	BillingMode        BillingMode    `json:"billing_mode"`
	BillingOutcome     BillingOutcome `json:"billing_outcome"`
	SubmissionState    State          `json:"submission_state"`
	SMPPStatus         string         `json:"smpp_status"`
	SMSCMessageID      string         `json:"smsc_message_id"`
	DeliveryState      DeliveryState  `json:"delivery_state,omitempty"`
	DeliveryStatus     string         `json:"delivery_status,omitempty"`
	DeliveryError      string         `json:"delivery_error,omitempty"`
	AdmittedAt         time.Time      `json:"admitted_at"`
	SubmissionTerminal *time.Time     `json:"submission_terminal_at,omitempty"`
	DeliveryDoneAt     *time.Time     `json:"delivery_done_at,omitempty"`
	DeliveryReceivedAt *time.Time     `json:"delivery_received_at,omitempty"`
	LateBillingAt      *time.Time     `json:"late_billing_at,omitempty"`
}

func projectExport(record Record) exportRecordV1 {
	return exportRecordV1{
		SchemaVersion: ExportSchemaVersion, ID: record.ID, MessageID: record.MessageID,
		PartNumber: record.PartNumber, PartCount: record.PartCount,
		UserID: record.UserID, GroupID: record.GroupID, RouteID: record.RouteID,
		ConnectorID: record.ConnectorID, Ingress: record.Ingress, BillID: record.BillID,
		Rate: record.Rate, Currency: record.Currency, EarlyAmount: record.EarlyAmount,
		LateAmount: record.LateAmount, ActualLateAmount: record.ActualLateAmount,
		BillingMode: record.BillingMode, BillingOutcome: record.BillingOutcome,
		SubmissionState: record.State, SMPPStatus: record.SMPPStatus,
		SMSCMessageID: record.SMSCMessageID, DeliveryState: record.DeliveryState,
		DeliveryStatus: record.DeliveryStatus, DeliveryError: record.DeliveryError,
		AdmittedAt: record.OccurredAt, SubmissionTerminal: record.TerminalAt,
		DeliveryDoneAt: record.DeliveryDoneAt, DeliveryReceivedAt: record.DeliveryReceivedAt,
		LateBillingAt: record.LateBillingAt,
	}
}

func encodeExport(records []Record, format ExportFormat) ([]byte, string, error) {
	var output bytes.Buffer
	switch format {
	case ExportJSONL:
		encoder := json.NewEncoder(&output)
		encoder.SetEscapeHTML(false)
		for _, record := range records {
			if err := encoder.Encode(projectExport(record)); err != nil {
				return nil, "", err
			}
		}
		return output.Bytes(), "application/x-ndjson; charset=utf-8", nil
	case ExportCSV:
		writer := csv.NewWriter(&output)
		header := []string{
			"schema_version", "cdr_id", "message_id", "part_number", "part_count",
			"user_id", "group_id", "route_id", "connector_id", "ingress", "bill_id",
			"rate", "currency", "early_amount", "late_amount", "actual_late_amount",
			"billing_mode", "billing_outcome", "submission_state", "smpp_status",
			"smsc_message_id", "delivery_state", "delivery_status", "delivery_error",
			"admitted_at", "submission_terminal_at", "delivery_done_at",
			"delivery_received_at", "late_billing_at",
		}
		if err := writer.Write(header); err != nil {
			return nil, "", err
		}
		for _, record := range records {
			row := projectExport(record)
			if err := writer.Write([]string{
				strconv.Itoa(row.SchemaVersion), row.ID, row.MessageID,
				strconv.Itoa(row.PartNumber), strconv.Itoa(row.PartCount),
				row.UserID, row.GroupID, row.RouteID, row.ConnectorID, row.Ingress, row.BillID,
				strconv.FormatFloat(row.Rate, 'g', -1, 64), row.Currency,
				strconv.FormatFloat(row.EarlyAmount, 'g', -1, 64),
				strconv.FormatFloat(row.LateAmount, 'g', -1, 64),
				strconv.FormatFloat(row.ActualLateAmount, 'g', -1, 64),
				string(row.BillingMode), string(row.BillingOutcome), string(row.SubmissionState),
				row.SMPPStatus, row.SMSCMessageID, string(row.DeliveryState),
				row.DeliveryStatus, row.DeliveryError, formatTime(row.AdmittedAt),
				formatTimePtr(row.SubmissionTerminal), formatTimePtr(row.DeliveryDoneAt),
				formatTimePtr(row.DeliveryReceivedAt), formatTimePtr(row.LateBillingAt),
			}); err != nil {
				return nil, "", err
			}
		}
		writer.Flush()
		return output.Bytes(), "text/csv; charset=utf-8", writer.Error()
	default:
		return nil, "", ErrInvalidInput
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatTime(*value)
}

func exportAuditTarget(request ExportRequest) string {
	return fmt.Sprintf("format=%s,user=%s,message=%s", request.Format, request.UserID, request.MessageID)
}

func searchAuditTarget(request SearchRequest) string {
	return fmt.Sprintf("search,user=%s,message=%s,from=%s,to=%s", request.UserID, request.MessageID,
		formatTimePtr(request.AdmittedFrom), formatTimePtr(request.AdmittedTo))
}

func summaryAuditTarget(query SummaryQuery) string {
	return fmt.Sprintf("summary,user=%s,from=%s,to=%s", query.UserID,
		formatTime(query.AdmittedFrom), formatTime(query.AdmittedTo))
}
