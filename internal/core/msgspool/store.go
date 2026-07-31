// Package msgspool is the message spool for the MT termination connector: the
// short-retention store of decoded message content that makes a failed
// downstream delivery recoverable and a partner dispute answerable.
//
// It is deliberately a spool and not an archive. Content lives for 24 h by
// default and is pruned in batches; the downstream application keeps its own
// table and remains the system of record. OTP bodies are the highest-value
// content this platform handles, so a second copy of them is a second breach
// surface and is bounded on purpose.
//
// Two properties are load-bearing and easy to lose:
//
//   - Every read that returns message text writes an audit row naming the actor.
//     The repository methods are reachable without that boundary, so management
//     surfaces and consumer APIs must go through Service, never the repository.
//   - Paging is by an allocated sequence, never by a timestamp. See Record.Sequence.
package msgspool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound     = errors.New("message spool row not found")
	ErrInvalidInput = errors.New("invalid message spool input")
	ErrForbidden    = errors.New("message spool access forbidden")
	ErrDisabled     = errors.New("message spool operation disabled")
	// ErrClaimLost reports that a leased receipt was completed by someone else,
	// or its lease expired and was taken over, before this worker finished.
	ErrClaimLost = errors.New("message spool receipt claim lost")
)

// CursorSchemaVersion is bumped when the opaque paging cursor changes shape, so
// a consumer replaying an old cursor is rejected rather than silently
// mis-positioned.
const CursorSchemaVersion = 1

// DeliveryState is the downstream push lifecycle. A pull-only connector leaves
// rows in DeliveryPending for their whole retention: nothing is pushing them,
// and pretending otherwise would make the DLQ depth metric meaningless.
type DeliveryState string

const (
	DeliveryPending      DeliveryState = "pending"
	DeliveryDelivered    DeliveryState = "delivered"
	DeliveryDeadLettered DeliveryState = "dlq"
)

func (state DeliveryState) Valid() bool {
	switch state {
	case DeliveryPending, DeliveryDelivered, DeliveryDeadLettered:
		return true
	default:
		return false
	}
}

// Verdict is what the partner was told, or will be told, about this message.
// It is stored with the content so a dispute can be answered from one row
// instead of a join against a decision trail that may already have rotated.
type Verdict struct {
	Accept bool
	// Stat is the SMPP receipt status (DELIVRD, REJECTD, UNDELIV, ...).
	Stat string
	// Err is the receipt's three-digit error field.
	Err string
	// Reason is the human-readable explanation for the decision trail.
	Reason string
	// GateBypassed records that the verdict source was unreachable and the
	// fail-open default was applied.
	GateBypassed bool
}

// Message is one row as handed to the spool.
//
// Almost always that is a fully assembled message with its parts already joined:
// the spool stores messages, not fragments. The exception is ReceiptOnly, which
// records the receipt one segment of a concatenated submit is owed without
// recording any of its content — see that field for why the two are different
// kinds of row rather than one kind with a missing body.
type Message struct {
	// MessageID is the gateway message id, the same value the receipt carries
	// and the idempotency key a downstream delivery uses. It is the primary key,
	// so re-spooling after a crash updates one row instead of creating a second.
	MessageID   string
	ConnectorID string
	// UserID is the submitting partner. The legacy single-tenant path had no
	// per-partner attribution at all; this is the column that adds it.
	UserID     string
	SourceAddr string
	// DestAddr is normalized to digits by the caller. The spool refuses an empty
	// destination rather than storing a row no activation window can match.
	DestAddr string
	Text     string
	// Raw is the pre-decode payload, all parts concatenated. It is the only
	// thing that settles a disagreement about what a message actually said.
	Raw []byte
	// DataCoding is the submitted data_coding value.
	DataCoding byte
	// Encoding names the codec that produced Text, which is not always what
	// DataCoding claimed.
	Encoding string
	Parts    int
	Verdict  Verdict
	// ReceivedAt is when the last part arrived. It is a search dimension and the
	// retention clock; it is never the paging cursor.
	ReceivedAt time.Time
	// NextAttemptAt schedules the first downstream push. Nil means no push is
	// scheduled: a pull-only or sink-less connector.
	NextAttemptAt *time.Time
	// ReceiptDueAt is when the synthesized receipt is owed to the partner
	// (submit time plus the connector's delay and jitter). It is persisted
	// rather than held in a goroutine timer so a restart inside the 5-7 s window
	// still emits the receipt. Nil means this message owes no receipt.
	ReceiptDueAt *time.Time
	// ReceiptOnly marks a row that exists to owe a receipt and nothing else: one
	// segment of a concatenated submit that has not completed its group.
	//
	// Each segment is its own submit_sm with its own message id and its own
	// registered_delivery flag, so each is owed its own receipt; the content,
	// however, belongs to the assembled message and is written once, on the row
	// of the segment that completes the group. A receipt-only row therefore
	// carries the verdict, the addresses and the receipt schedule, and never Text
	// or Raw.
	//
	// It is excluded from Search by default (see Query.IncludeReceiptOnly): a
	// consumer paging the spool must never be handed a fragment, and a row with
	// no content would arrive looking exactly like a blank SMS.
	ReceiptOnly bool
}

// Record is a stored spool row.
type Record struct {
	Message
	// Sequence is the paging cursor, allocated by the store in commit order and
	// re-allocated on every mutation.
	//
	// Paging on ReceivedAt or CreatedAt is the trap this column exists to avoid:
	// a message reassembled late, or a row re-spooled after an AMQP redelivery,
	// carries a timestamp a consumer's cursor has already passed, and would be
	// skipped forever. So would a row whose delivery was retried after the
	// consumer paged past it. A mutation-bumped sequence hands both back.
	Sequence         int64
	DeliveryState    DeliveryState
	DeliveryAttempts int
	DeliveredAt      *time.Time
	ReceiptSentAt    *time.Time
	// ReceiptLockOwner and ReceiptLockedUntil expose the current lease so an
	// operator can see which process owes a receipt and until when.
	ReceiptLockOwner   string
	ReceiptLockedUntil *time.Time
	CreatedAt          time.Time
	// ContentRedacted reports that Text and Raw were withheld by the read that
	// produced this record. It distinguishes an empty message from a masked one,
	// which a bare empty string cannot.
	ContentRedacted bool
}

// String redacts. Message content and raw bytes must never reach a log line,
// an error string or a %v of a struct that happens to contain a spool row.
//
// Note the one hole Go leaves open: %#v ignores Stringer and prints every
// field. Nothing may format a spool value with %#v.
func (message Message) String() string {
	return fmt.Sprintf(
		"msgspool.Message{id=%s connector=%s partner=%s from=%s to=%s dcs=%d encoding=%s parts=%d "+
			"text_len=%d raw_len=%d stat=%s}",
		message.MessageID, message.ConnectorID, message.UserID, message.SourceAddr,
		message.DestAddr, message.DataCoding, message.Encoding, message.Parts,
		len(message.Text), len(message.Raw), message.Verdict.Stat)
}

// LogValue keeps slog from reflecting over the struct and printing the content.
func (message Message) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("message_id", message.MessageID),
		slog.String("connector_id", message.ConnectorID),
		slog.String("user_id", message.UserID),
		slog.String("source_addr", message.SourceAddr),
		slog.String("dest_addr", message.DestAddr),
		slog.Int("data_coding", int(message.DataCoding)),
		slog.String("encoding", message.Encoding),
		slog.Int("parts", message.Parts),
		slog.Int("text_len", len(message.Text)),
		slog.Int("raw_len", len(message.Raw)),
		slog.String("verdict_stat", message.Verdict.Stat),
		slog.Bool("gate_bypassed", message.Verdict.GateBypassed),
	)
}

func (record Record) String() string {
	return fmt.Sprintf("msgspool.Record{seq=%d state=%s attempts=%d %s}",
		record.Sequence, record.DeliveryState, record.DeliveryAttempts, record.Message.String())
}

func (record Record) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("seq", record.Sequence),
		slog.String("delivery_state", string(record.DeliveryState)),
		slog.Int("delivery_attempts", record.DeliveryAttempts),
		slog.Any("message", record.Message),
	)
}

// ValidateMessage refuses a row the schema would reject anyway, so the caller
// gets a typed error instead of a driver-specific constraint violation.
func ValidateMessage(message Message) error {
	if strings.TrimSpace(message.MessageID) == "" ||
		strings.TrimSpace(message.ConnectorID) == "" ||
		strings.TrimSpace(message.DestAddr) == "" ||
		strings.TrimSpace(message.Verdict.Stat) == "" {
		return ErrInvalidInput
	}
	if message.Parts < 1 {
		return ErrInvalidInput
	}
	if message.ReceivedAt.IsZero() {
		return ErrInvalidInput
	}
	if message.ReceiptOnly {
		// The three invariants migration 0007 also states as CHECK constraints,
		// restated here because they are the ones a caller can get wrong by
		// forgetting a field rather than by typing a bad value, and because the
		// SQLite backend cannot add CHECKs to a spool file that predates the
		// column. A receipt-only row that carried a fragment's text would put a
		// second copy of an OTP in the store for nothing; one that were scheduled
		// for a push would walk a fragment through the retry budget and
		// dead-letter it; one with no receipt due would owe nothing and exist for
		// no reason.
		if message.Text != "" || len(message.Raw) > 0 {
			return ErrInvalidInput
		}
		if message.NextAttemptAt != nil {
			return ErrInvalidInput
		}
		if message.ReceiptDueAt == nil {
			return ErrInvalidInput
		}
	}
	return nil
}

type Role string

const (
	// RoleReader may see spool metadata: who sent what, when, to which number,
	// with which verdict and delivery state. It may not see the message.
	RoleReader Role = "message_reader"
	// RoleRevealer may see the decoded text and the raw bytes.
	RoleRevealer Role = "message_revealer"
	RoleOperator Role = "message_operator"
)

type Action string

const (
	ActionSearch Action = "search"
	ActionRead   Action = "read"
	// ActionReveal is the only action that returns message content, which is
	// what makes "every content read is audited" checkable in one place.
	ActionReveal Action = "reveal"
	ActionPrune  Action = "prune"
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
		switch action {
		case ActionSearch, ActionRead:
			if role == RoleReader || role == RoleRevealer {
				return true
			}
		case ActionReveal:
			if role == RoleRevealer {
				return true
			}
		}
	}
	return false
}

type AccessAudit struct {
	Subject string
	Action  Action
	Target  string
	Allowed bool
	// RowCount is how many rows the read actually returned. A reveal of one
	// message and a scripted sweep of ten thousand are the same Action; only
	// this column tells them apart afterwards.
	RowCount   int64
	OccurredAt time.Time
}

// Query is the repository-level search. Zero-valued filters mean "no filter".
type Query struct {
	// AfterSequence is the cursor. Rows are returned in sequence order.
	AfterSequence int64
	ConnectorID   string
	// ConnectorIDs restricts the read to a set of connectors. It is how a scoped
	// pull consumer's allow-list reaches the database, so it is compiled into
	// the predicate by both backends and never applied to fetched rows: a
	// consumer that paged across rows it may not see would receive short pages
	// indistinguishable from "no new messages", and the counts would still
	// describe traffic it is not entitled to know exists.
	//
	// Empty means "no filter", consistent with every other field here. Scope
	// carries the opposite default and refuses to compile an empty allow-list,
	// so the fail-open reading of this field is unreachable from the pull API;
	// see Scope.Connectors and ConsumerService.Page.
	ConnectorIDs  []string
	UserID        string
	DestAddr      string
	DeliveryState DeliveryState
	ReceivedFrom  *time.Time
	ReceivedTo    *time.Time
	// VerdictStat filters on the receipt status the partner was told
	// (DELIVRD, REJECTD, ...). It is the decision trail's primary axis: "show
	// me everything this partner was rejected on".
	VerdictStat string
	// GateBypassedOnly restricts the result to decisions taken with the
	// activation gate unreachable. It is the query an operator runs after a
	// Redis outage to find what was accepted blind, so it is compiled into the
	// predicate rather than filtered afterwards — post-filtering would page
	// through the whole spool to find a handful of rows and return pages that
	// look empty.
	GateBypassedOnly bool
	// IncludeReceiptOnly admits rows that exist only to owe a receipt — one
	// segment of a concatenated submit, carrying no content. See
	// Message.ReceiptOnly.
	//
	// It is the one filter here whose zero value is a restriction rather than
	// "no filter", and that inversion is deliberate: a caller that forgets this
	// field gets messages, and a caller that wants fragments has to say so. A
	// pull consumer handed a fragment would store it as the message, which is
	// worse than never receiving the message at all.
	//
	// Like every other restriction here it must be compiled into the predicate,
	// never applied to fetched rows: post-filtering leaks through the cursor and
	// through the row counts exactly as an unscoped read would.
	IncludeReceiptOnly bool
	// IncludeContent is compiled into the projection, not applied afterwards:
	// a masked read must not load the text into the process at all.
	IncludeContent bool
	Limit          int
}

type PruneResult struct {
	Records int64
}

// ConnectorCensus is one connector's spool population at one instant. It backs
// the operator gauges, so it counts rows and never returns any.
type ConnectorCensus struct {
	ConnectorID string
	// Rows is every row held for the connector, whatever its delivery state.
	Rows int64
	// DeadLettered is the rows that exhausted their delivery attempts.
	DeadLettered int64
	// ReceiptsOverdue is the rows whose receipt_due_at has passed with no
	// receipt_sent_at: partners who are waiting.
	ReceiptsOverdue int64
}

// Repository is the durable side of the spool. Both PostgreSQL and SQLite
// implement it identically; the SQLite backend exists for unit and local runs.
type Repository interface {
	// Put writes or refreshes one message. It is idempotent on MessageID and
	// must not reset the delivery or receipt lifecycle of a row that already
	// exists, so an AMQP redelivery cannot cause a second downstream push or a
	// second receipt.
	Put(ctx context.Context, message Message, createdAt time.Time) (Record, error)
	Get(ctx context.Context, messageID string, includeContent bool) (Record, error)
	Search(ctx context.Context, query Query) ([]Record, error)
	// DueForDelivery returns rows whose next push attempt is due. It returns
	// content, because the push carries it; it is a machine path and is
	// deliberately not exposed through Service's audited read surface.
	DueForDelivery(ctx context.Context, now time.Time, limit int) ([]Record, error)
	MarkDelivered(ctx context.Context, messageID string, at time.Time) error
	// MarkAttemptFailed counts the failed attempt and reschedules. It also moves
	// a dead-lettered row back to pending, which is what a console Replay does.
	MarkAttemptFailed(ctx context.Context, messageID string, nextAttemptAt time.Time) error
	MarkDeadLettered(ctx context.Context, messageID string) error
	// ClaimDueReceipts exclusively leases rows whose receipt is owed. Two
	// gateway processes must never claim the same row: the partner would receive
	// two receipts for one message.
	ClaimDueReceipts(ctx context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]Record, error)
	// MarkReceiptSent closes a claim. It fails with ErrClaimLost when the lease
	// was taken over or the receipt was already recorded as sent.
	MarkReceiptSent(ctx context.Context, messageID, owner string, at time.Time) error
	// Prune deletes rows older than olderThan, at most batch per call.
	Prune(ctx context.Context, olderThan time.Time, batch int) (PruneResult, error)
	// Census counts rows per connector for the operator gauges. It returns
	// counts only — never a row, never content — which is why it is the one
	// read on this interface with no audit obligation.
	Census(ctx context.Context, now time.Time) ([]ConnectorCensus, error)
	AuditAccess(ctx context.Context, audit AccessAudit) error
}

// RetentionPolicy bounds how long decoded content is kept. Window is a duration
// rather than a day count because the answer here is 24 h, and expressing that
// as "1 day" invites a prune that runs on date boundaries.
type RetentionPolicy struct {
	Window    time.Duration
	BatchSize int
}

const (
	DefaultRetentionWindow = 24 * time.Hour
	DefaultRetentionBatch  = 1000
)

func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{Window: DefaultRetentionWindow, BatchSize: DefaultRetentionBatch}
}

func (policy RetentionPolicy) Validate() error {
	if policy.Window < 0 || policy.BatchSize < 0 || policy.BatchSize > 10000 {
		return ErrInvalidInput
	}
	if policy.Window > 0 && policy.BatchSize == 0 {
		return ErrInvalidInput
	}
	return nil
}

// Service is the authorization-and-audit boundary. Nothing that can return
// message text is reachable through it without writing an audit row first.
type Service struct {
	repository Repository
	// retention is read per call rather than captured, so an operator changing
	// it at runtime takes effect on the next prune instead of the next restart.
	retention   RetentionPolicy
	retentionMu sync.RWMutex
	now         func() time.Time
}

func NewService(repository Repository, retention RetentionPolicy, now func() time.Time) (*Service, error) {
	if repository == nil || retention.Validate() != nil {
		return nil, ErrInvalidInput
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, retention: retention, now: now}, nil
}

func (service *Service) SetRetention(policy RetentionPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	service.retentionMu.Lock()
	service.retention = policy
	service.retentionMu.Unlock()
	return nil
}

func (service *Service) Retention() RetentionPolicy {
	service.retentionMu.RLock()
	defer service.retentionMu.RUnlock()
	return service.retention
}

// Put spools one assembled message. It is the connector's write path and is not
// audited: the actor is the gateway itself, and the row it wrote is the record.
func (service *Service) Put(ctx context.Context, message Message) (Record, error) {
	if err := ValidateMessage(message); err != nil {
		return Record{}, err
	}
	return service.repository.Put(ctx, message, service.now().UTC())
}

// Get returns one row's metadata with the content masked. It is audited because
// even "which number received a message at 03:12" is a disclosure.
func (service *Service) Get(ctx context.Context, principal Principal, messageID string) (Record, error) {
	if strings.TrimSpace(messageID) == "" {
		return Record{}, ErrInvalidInput
	}
	return service.read(ctx, principal, ActionRead, messageID, func() (Record, error) {
		return service.repository.Get(ctx, messageID, false)
	})
}

// Reveal returns the decoded text and the raw bytes. It requires RoleRevealer
// and always writes an audit row naming the actor; if that row cannot be
// persisted, the content is not returned.
func (service *Service) Reveal(ctx context.Context, principal Principal, messageID string) (Record, error) {
	if strings.TrimSpace(messageID) == "" {
		return Record{}, ErrInvalidInput
	}
	return service.read(ctx, principal, ActionReveal, messageID, func() (Record, error) {
		return service.repository.Get(ctx, messageID, true)
	})
}

func (service *Service) read(
	ctx context.Context,
	principal Principal,
	action Action,
	target string,
	load func() (Record, error),
) (Record, error) {
	if !principal.Allows(action) {
		return Record{}, service.deny(ctx, principal, action, target)
	}
	record, err := load()
	if err != nil {
		// The attempt is still recorded: an authorized read that failed is
		// operationally interesting and must not vanish from the trail.
		if auditErr := service.audit(ctx, principal, action, target, true, 0); auditErr != nil {
			return Record{}, auditErr
		}
		return Record{}, err
	}
	if auditErr := service.audit(ctx, principal, action, target, true, 1); auditErr != nil {
		return Record{}, auditErr
	}
	return record, nil
}

// SearchRequest pages the spool for a management surface or a scoped consumer.
type SearchRequest struct {
	Cursor      string
	ConnectorID string
	// ConnectorIDs is the scoped-consumer allow-list; see Query.ConnectorIDs.
	ConnectorIDs  []string
	UserID        string
	DestAddr      string
	DeliveryState DeliveryState
	ReceivedFrom  *time.Time
	ReceivedTo    *time.Time
	// VerdictStat and GateBypassedOnly are the decision trail's filters; see
	// Query for what each is for.
	VerdictStat      string
	GateBypassedOnly bool
	// IncludeReceiptOnly admits per-segment receipt rows; see
	// Query.IncludeReceiptOnly. False, the zero value, is the restriction.
	IncludeReceiptOnly bool
	// IncludeContent requires RoleRevealer and is audited as a reveal, because
	// a paged sweep of text is a reveal repeated, not a lesser act.
	IncludeContent bool
	Limit          int
}

type SearchPage struct {
	Records    []Record
	NextCursor string
}

func (service *Service) Search(ctx context.Context, principal Principal, request SearchRequest) (SearchPage, error) {
	if request.Limit == 0 {
		request.Limit = 100
	}
	if request.Limit < 1 || request.Limit > 1000 {
		return SearchPage{}, ErrInvalidInput
	}
	if request.DeliveryState != "" && !request.DeliveryState.Valid() {
		return SearchPage{}, ErrInvalidInput
	}
	action := ActionSearch
	if request.IncludeContent {
		action = ActionReveal
	}
	target := searchAuditTarget(request)
	if !principal.Allows(action) {
		return SearchPage{}, service.deny(ctx, principal, action, target)
	}
	after, err := decodeCursor(request.Cursor)
	if err != nil {
		return SearchPage{}, err
	}
	records, err := service.repository.Search(ctx, Query{
		AfterSequence: after, ConnectorID: request.ConnectorID,
		ConnectorIDs: request.ConnectorIDs, UserID: request.UserID,
		DestAddr: request.DestAddr, DeliveryState: request.DeliveryState,
		ReceivedFrom: request.ReceivedFrom, ReceivedTo: request.ReceivedTo,
		VerdictStat: request.VerdictStat, GateBypassedOnly: request.GateBypassedOnly,
		IncludeReceiptOnly: request.IncludeReceiptOnly,
		IncludeContent:     request.IncludeContent, Limit: request.Limit,
	})
	if err != nil {
		if auditErr := service.audit(ctx, principal, action, target, true, 0); auditErr != nil {
			return SearchPage{}, auditErr
		}
		return SearchPage{}, err
	}
	if auditErr := service.audit(ctx, principal, action, target, true, int64(len(records))); auditErr != nil {
		return SearchPage{}, auditErr
	}
	page := SearchPage{Records: records}
	// The cursor advances even on a short page, because a full page is not the
	// only reason a consumer keeps polling.
	if len(records) > 0 {
		page.NextCursor = EncodeCursor(records[len(records)-1].Sequence)
	}
	return page, nil
}

// Prune enforces the retention window. Its shape matches cdr.Service.Prune so
// the existing maintenance loop can drive it: call it until the returned count
// is below the configured batch size.
func (service *Service) Prune(ctx context.Context, principal Principal) (PruneResult, error) {
	retention := service.Retention()
	if !principal.Allows(ActionPrune) {
		return PruneResult{}, service.deny(ctx, principal, ActionPrune, "retention")
	}
	if retention.Window == 0 {
		return PruneResult{}, ErrDisabled
	}
	cutoff := service.now().UTC().Add(-retention.Window)
	result, err := service.repository.Prune(ctx, cutoff, retention.BatchSize)
	if auditErr := service.audit(ctx, principal, ActionPrune, "retention", true, result.Records); auditErr != nil {
		return PruneResult{}, auditErr
	}
	return result, err
}

func (service *Service) deny(ctx context.Context, principal Principal, action Action, target string) error {
	if err := service.audit(ctx, principal, action, target, false, 0); err != nil {
		return err
	}
	return ErrForbidden
}

// audit is fail-closed: a read whose trail cannot be persisted does not happen.
func (service *Service) audit(
	ctx context.Context,
	principal Principal,
	action Action,
	target string,
	allowed bool,
	rowCount int64,
) error {
	return service.repository.AuditAccess(ctx, AccessAudit{
		Subject: principal.Subject, Action: action, Target: target,
		Allowed: allowed, RowCount: rowCount, OccurredAt: service.now().UTC(),
	})
}

type cursorV1 struct {
	Version  int   `json:"v"`
	Sequence int64 `json:"seq"`
}

// EncodeCursor renders a paging position opaque. It carries a sequence and
// nothing else: a cursor that carried a timestamp would invite a caller to
// reconstruct it from a clock and reintroduce the skipped-row bug.
func EncodeCursor(sequence int64) string {
	raw, _ := json.Marshal(cursorV1{Version: CursorSchemaVersion, Sequence: sequence})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, ErrInvalidInput
	}
	var cursor cursorV1
	if err = json.Unmarshal(raw, &cursor); err != nil ||
		cursor.Version != CursorSchemaVersion || cursor.Sequence < 0 {
		return 0, ErrInvalidInput
	}
	return cursor.Sequence, nil
}

// searchAuditTarget renders the filter set that was actually enforced. The
// connector allow-list is spelled out rather than summarised: after the fact,
// "which rows could this read have returned" has to be answerable from the
// audit row alone, because the consumer record it came from may since have been
// narrowed, widened or deleted.
func searchAuditTarget(request SearchRequest) string {
	return fmt.Sprintf(
		"search,connector=%s,connectors=[%s],user=%s,dest=%s,state=%s,stat=%s,bypassed=%t,"+
			"receipt_only=%t,content=%t",
		request.ConnectorID, strings.Join(request.ConnectorIDs, "|"), request.UserID,
		request.DestAddr, request.DeliveryState, request.VerdictStat, request.GateBypassedOnly,
		request.IncludeReceiptOnly, request.IncludeContent)
}

// Census counts spool rows per connector for the operator gauges.
//
// It takes no principal and writes no audit row, unlike every other read here.
// That is a deliberate exception with a checkable boundary: it returns counts,
// never rows and never content, and its caller is the gateway's own metrics
// loop. Requiring a principal would mean inventing a synthetic actor and writing
// an audit row every fifteen seconds for the rest of the process's life, which
// would bury the audit trail the reveal path depends on.
func (service *Service) Census(ctx context.Context) ([]ConnectorCensus, error) {
	return service.repository.Census(ctx, service.now().UTC())
}
