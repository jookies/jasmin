package msgspool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// fakeRepository records what the service asked for and what it audited, so the
// tests assert the authorization boundary rather than the storage behaviour.
type fakeRepository struct {
	record  Record
	records []Record
	getErr  error

	audits   []AccessAudit
	auditErr error

	lastGetContent bool
	lastQuery      Query
	pruneOlderThan time.Time
	pruneBatch     int
	pruned         int64

	census    []ConnectorCensus
	censusNow time.Time
}

func (repository *fakeRepository) Census(_ context.Context, now time.Time) ([]ConnectorCensus, error) {
	repository.censusNow = now
	return repository.census, nil
}

func (repository *fakeRepository) Put(context.Context, Message, time.Time) (Record, error) {
	return repository.record, nil
}

func (repository *fakeRepository) Get(_ context.Context, _ string, includeContent bool) (Record, error) {
	repository.lastGetContent = includeContent
	if repository.getErr != nil {
		return Record{}, repository.getErr
	}
	record := repository.record
	if !includeContent {
		record.Text, record.Raw, record.ContentRedacted = "", nil, true
	}
	return record, nil
}

func (repository *fakeRepository) Search(_ context.Context, query Query) ([]Record, error) {
	repository.lastQuery = query
	return repository.records, nil
}

func (repository *fakeRepository) DueForDelivery(context.Context, time.Time, int) ([]Record, error) {
	return nil, nil
}

func (repository *fakeRepository) MarkDelivered(context.Context, string, time.Time) error { return nil }

func (repository *fakeRepository) MarkAttemptFailed(context.Context, string, time.Time) error {
	return nil
}

func (repository *fakeRepository) MarkDeadLettered(context.Context, string) error { return nil }

func (repository *fakeRepository) ClaimDueReceipts(
	context.Context, string, time.Time, time.Duration, int,
) ([]Record, error) {
	return nil, nil
}

func (repository *fakeRepository) MarkReceiptSent(context.Context, string, string, time.Time) error {
	return nil
}

func (repository *fakeRepository) Prune(_ context.Context, olderThan time.Time, batch int) (PruneResult, error) {
	repository.pruneOlderThan, repository.pruneBatch = olderThan, batch
	return PruneResult{Records: repository.pruned}, nil
}

func (repository *fakeRepository) AuditAccess(_ context.Context, audit AccessAudit) error {
	if repository.auditErr != nil {
		return repository.auditErr
	}
	repository.audits = append(repository.audits, audit)
	return nil
}

const (
	secretText = "PROVERKA-KOD-63125"
	secretRaw  = "raw-otp-bytes"
)

func sampleMessage() Message {
	return Message{
		MessageID: "019428c1", ConnectorID: "partner-a-term", UserID: "partner-a",
		SourceAddr: "NETFLIX", DestAddr: "380671234567", Text: secretText,
		Raw: []byte(secretRaw), DataCoding: 8, Encoding: "ucs2", Parts: 2,
		Verdict:    Verdict{Accept: true, Stat: "DELIVRD", Err: "000", Reason: "window open"},
		ReceivedAt: time.Date(2026, 7, 30, 18, 22, 41, 0, time.UTC),
	}
}

func newTestService(t *testing.T, repository *fakeRepository, now time.Time) *Service {
	t.Helper()
	service, err := NewService(repository, DefaultRetention(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// Message bodies are OTPs. A spool value must not print them, whatever the
// verb, and must not print them through slog either.
func TestFormattingNeverLeaksContent(t *testing.T) {
	message := sampleMessage()
	record := Record{
		Message: message, Sequence: 7, DeliveryState: DeliveryPending,
		DeliveryAttempts: 1, CreatedAt: message.ReceivedAt,
	}
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	logger.Info("spooled", "message", message, "record", record)

	rendered := []string{
		fmt.Sprintf("%v", message), fmt.Sprintf("%+v", message), fmt.Sprintf("%s", message),
		fmt.Sprintf("%v", &message), fmt.Sprintf("%v", record), fmt.Sprintf("%+v", record),
		fmt.Sprintf("%s", record), fmt.Sprintf("%v", &record),
		fmt.Sprintf("%v", []Record{record}),
		fmt.Sprintf("%v", struct{ Row Record }{record}),
		logged.String(),
	}
	for index, value := range rendered {
		if strings.Contains(value, secretText) || strings.Contains(value, secretRaw) {
			t.Fatalf("rendering %d leaked content: %s", index, value)
		}
		if !strings.Contains(value, "019428c1") {
			t.Fatalf("rendering %d lost the message id: %s", index, value)
		}
	}
	if !strings.Contains(fmt.Sprintf("%v", message), "text_len=18") {
		t.Fatalf("expected a redacted length instead of the text: %v", message)
	}
}

func TestValidateMessage(t *testing.T) {
	valid := sampleMessage()
	cases := map[string]func(*Message){
		"no message id":  func(m *Message) { m.MessageID = " " },
		"no connector":   func(m *Message) { m.ConnectorID = "" },
		"no destination": func(m *Message) { m.DestAddr = "" },
		"no verdict":     func(m *Message) { m.Verdict.Stat = "" },
		"zero parts":     func(m *Message) { m.Parts = 0 },
		"no received at": func(m *Message) { m.ReceivedAt = time.Time{} },
	}
	if err := ValidateMessage(valid); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			message := valid
			mutate(&message)
			if !errors.Is(ValidateMessage(message), ErrInvalidInput) {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestGetMasksContentAndAudits(t *testing.T) {
	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	repository := &fakeRepository{record: Record{Message: sampleMessage(), Sequence: 4}}
	service := newTestService(t, repository, now)
	principal := Principal{Subject: "console:alice", Roles: []Role{RoleReader}}

	record, err := service.Get(context.Background(), principal, "019428c1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Text != "" || record.Raw != nil || !record.ContentRedacted {
		t.Fatalf("metadata read returned content: %v", record)
	}
	if repository.lastGetContent {
		t.Fatal("metadata read asked the database for the message text")
	}
	if len(repository.audits) != 1 {
		t.Fatalf("audits=%+v", repository.audits)
	}
	audit := repository.audits[0]
	if audit.Subject != "console:alice" || audit.Action != ActionRead ||
		audit.Target != "019428c1" || !audit.Allowed || audit.RowCount != 1 ||
		!audit.OccurredAt.Equal(now) {
		t.Fatalf("audit=%+v", audit)
	}
}

func TestRevealRequiresRoleAndAuditsEveryContentRead(t *testing.T) {
	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	repository := &fakeRepository{record: Record{Message: sampleMessage(), Sequence: 4}}
	service := newTestService(t, repository, now)

	reader := Principal{Subject: "console:alice", Roles: []Role{RoleReader}}
	if _, err := service.Reveal(context.Background(), reader, "019428c1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader revealed content: %v", err)
	}
	if len(repository.audits) != 1 || repository.audits[0].Allowed ||
		repository.audits[0].Action != ActionReveal {
		t.Fatalf("denied reveal was not audited: %+v", repository.audits)
	}

	revealer := Principal{Subject: "console:bob", Roles: []Role{RoleRevealer}}
	record, err := service.Reveal(context.Background(), revealer, "019428c1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Text != secretText || string(record.Raw) != secretRaw {
		t.Fatalf("reveal withheld content: %v", record)
	}
	if len(repository.audits) != 2 {
		t.Fatalf("audits=%+v", repository.audits)
	}
	if audit := repository.audits[1]; audit.Subject != "console:bob" ||
		audit.Action != ActionReveal || !audit.Allowed || audit.RowCount != 1 {
		t.Fatalf("audit=%+v", audit)
	}
}

// An anonymous or role-less caller must not reach content, and the refusal must
// still be recorded: an unattributed attempt is the one worth seeing later.
func TestUnattributedPrincipalIsRefused(t *testing.T) {
	repository := &fakeRepository{record: Record{Message: sampleMessage()}}
	service := newTestService(t, repository, time.Now())
	for _, principal := range []Principal{
		{},
		{Subject: "", Roles: []Role{RoleOperator}},
		{Subject: "console:mallory"},
	} {
		if _, err := service.Get(context.Background(), principal, "019428c1"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("principal %+v was allowed: %v", principal, err)
		}
	}
	if len(repository.audits) != 3 {
		t.Fatalf("audits=%+v", repository.audits)
	}
}

// Access is fail-closed: if the trail cannot be written, the content is not
// handed over, even to an authorized caller.
func TestReadFailsClosedWhenAuditCannotBePersisted(t *testing.T) {
	repository := &fakeRepository{
		record:   Record{Message: sampleMessage()},
		auditErr: errors.New("audit sink down"),
	}
	service := newTestService(t, repository, time.Now())
	revealer := Principal{Subject: "console:bob", Roles: []Role{RoleRevealer}}
	record, err := service.Reveal(context.Background(), revealer, "019428c1")
	if err == nil || record.Text != "" {
		t.Fatalf("reveal survived an audit failure: record=%v err=%v", record, err)
	}
}

func TestSearchAuditsRowCountAndCompilesFilters(t *testing.T) {
	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	repository := &fakeRepository{records: []Record{
		{Message: sampleMessage(), Sequence: 11},
		{Message: sampleMessage(), Sequence: 12},
	}}
	service := newTestService(t, repository, now)
	principal := Principal{Subject: "consumer:app-1", Roles: []Role{RoleReader}}

	page, err := service.Search(context.Background(), principal, SearchRequest{
		ConnectorID: "partner-a-term", DestAddr: "380671234567", Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.lastQuery.ConnectorID != "partner-a-term" ||
		repository.lastQuery.DestAddr != "380671234567" ||
		repository.lastQuery.Limit != 50 || repository.lastQuery.IncludeContent {
		t.Fatalf("query=%+v", repository.lastQuery)
	}
	if page.NextCursor == "" {
		t.Fatal("a returned page must carry a cursor")
	}
	if len(repository.audits) != 1 || repository.audits[0].RowCount != 2 ||
		repository.audits[0].Action != ActionSearch {
		t.Fatalf("audits=%+v", repository.audits)
	}

	// The cursor must resume exactly after the last row handed out.
	if _, err = service.Search(context.Background(), principal, SearchRequest{
		Cursor: page.NextCursor, Limit: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if repository.lastQuery.AfterSequence != 12 {
		t.Fatalf("resumed at %d, want 12", repository.lastQuery.AfterSequence)
	}
}

// A paged sweep of message text is a reveal repeated, not a lesser act, so it
// needs the reveal role and is audited as one.
func TestSearchWithContentIsAuditedAsReveal(t *testing.T) {
	repository := &fakeRepository{records: []Record{{Message: sampleMessage(), Sequence: 3}}}
	service := newTestService(t, repository, time.Now())

	reader := Principal{Subject: "consumer:app-1", Roles: []Role{RoleReader}}
	if _, err := service.Search(context.Background(), reader, SearchRequest{
		IncludeContent: true, Limit: 10,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader swept content: %v", err)
	}
	revealer := Principal{Subject: "consumer:app-2", Roles: []Role{RoleRevealer}}
	if _, err := service.Search(context.Background(), revealer, SearchRequest{
		IncludeContent: true, Limit: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if len(repository.audits) != 2 ||
		repository.audits[0].Action != ActionReveal || repository.audits[0].Allowed ||
		repository.audits[1].Action != ActionReveal || repository.audits[1].RowCount != 1 {
		t.Fatalf("audits=%+v", repository.audits)
	}
	if !repository.lastQuery.IncludeContent {
		t.Fatal("content sweep did not reach the repository as a content query")
	}
}

func TestSearchRejectsBadInput(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository, time.Now())
	principal := Principal{Subject: "console:alice", Roles: []Role{RoleOperator}}
	cases := map[string]SearchRequest{
		"limit too large": {Limit: 1001},
		"negative limit":  {Limit: -1},
		"unknown state":   {DeliveryState: DeliveryState("sent"), Limit: 10},
		"broken cursor":   {Cursor: "!!!not-base64!!!", Limit: 10},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Search(context.Background(), principal, request); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("%s was accepted: %v", name, err)
			}
		})
	}
}

// The retention default is 24 h, and the prune shape matches cdr.Service.Prune
// so the existing maintenance loop can drive it: call until the count returned
// is below the batch size.
func TestPruneUsesTheRetentionWindow(t *testing.T) {
	now := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	repository := &fakeRepository{pruned: 1000}
	service := newTestService(t, repository, now)
	principal := Principal{Subject: "gateway-maintenance", Roles: []Role{RoleOperator}}

	if window := service.Retention().Window; window != 24*time.Hour {
		t.Fatalf("default retention window = %v", window)
	}
	result, err := service.Prune(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Records != 1000 {
		t.Fatalf("result=%+v", result)
	}
	if want := now.Add(-24 * time.Hour); !repository.pruneOlderThan.Equal(want) {
		t.Fatalf("cutoff=%v, want %v", repository.pruneOlderThan, want)
	}
	if repository.pruneBatch != DefaultRetentionBatch {
		t.Fatalf("batch=%d", repository.pruneBatch)
	}
	if len(repository.audits) != 1 || repository.audits[0].Action != ActionPrune ||
		repository.audits[0].RowCount != 1000 {
		t.Fatalf("audits=%+v", repository.audits)
	}

	if err = service.SetRetention(RetentionPolicy{Window: time.Hour, BatchSize: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Prune(context.Background(), principal); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-time.Hour); !repository.pruneOlderThan.Equal(want) {
		t.Fatalf("cutoff after SetRetention=%v, want %v", repository.pruneOlderThan, want)
	}
}

func TestPruneIsDisabledWithoutAWindow(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository, time.Now())
	if err := service.SetRetention(RetentionPolicy{}); err != nil {
		t.Fatal(err)
	}
	principal := Principal{Subject: "gateway-maintenance", Roles: []Role{RoleOperator}}
	if _, err := service.Prune(context.Background(), principal); !errors.Is(err, ErrDisabled) {
		t.Fatalf("prune ran without a retention window: %v", err)
	}
	if _, err := service.Prune(context.Background(), Principal{Subject: "console:alice",
		Roles: []Role{RoleRevealer}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a revealer pruned the spool: %v", err)
	}
}

func TestRetentionPolicyValidation(t *testing.T) {
	cases := map[string]RetentionPolicy{
		"negative window": {Window: -time.Hour, BatchSize: 10},
		"negative batch":  {Window: time.Hour, BatchSize: -1},
		"batch too large": {Window: time.Hour, BatchSize: 10001},
		"window no batch": {Window: time.Hour},
	}
	for name, policy := range cases {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(policy.Validate(), ErrInvalidInput) {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
	if err := (RetentionPolicy{}).Validate(); err != nil {
		t.Fatalf("a disabled policy must be valid: %v", err)
	}
}

func TestCursorRoundTripsAndRejectsGarbage(t *testing.T) {
	for _, sequence := range []int64{0, 1, 4096, 1 << 40} {
		decoded, err := decodeCursor(EncodeCursor(sequence))
		if err != nil || decoded != sequence {
			t.Fatalf("cursor %d round-tripped to %d (%v)", sequence, decoded, err)
		}
	}
	if decoded, err := decodeCursor(""); err != nil || decoded != 0 {
		t.Fatalf("empty cursor = %d (%v)", decoded, err)
	}
	for _, value := range []string{"@@@", "eyJ2Ijo5OTksInNlcSI6NX0"} {
		if _, err := decodeCursor(value); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("cursor %q was accepted: %v", value, err)
		}
	}
}

func TestNewServiceRejectsBadDependencies(t *testing.T) {
	if _, err := NewService(nil, DefaultRetention(), nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil repository accepted: %v", err)
	}
	if _, err := NewService(&fakeRepository{}, RetentionPolicy{Window: time.Hour}, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid retention accepted: %v", err)
	}
}
