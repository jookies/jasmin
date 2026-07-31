package msgspool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeConsumers is an in-memory ConsumerRepository. It records what was asked
// so the tests assert the credential boundary rather than storage behaviour;
// the SQL behaviour is proven against both real backends in
// internal/infra/storage.
type fakeConsumers struct {
	byID      map[string]Consumer
	byDigest  map[string]string // hex-ish key -> consumer id
	touched   []string
	lookupErr error
}

func newFakeConsumers() *fakeConsumers {
	return &fakeConsumers{byID: map[string]Consumer{}, byDigest: map[string]string{}}
}

func (f *fakeConsumers) CreateConsumer(_ context.Context, consumer Consumer, digest []byte) error {
	if _, exists := f.byID[consumer.ID]; exists {
		return ErrConsumerExists
	}
	f.byID[consumer.ID] = consumer
	f.byDigest[string(digest)] = consumer.ID
	return nil
}

func (f *fakeConsumers) ListConsumers(context.Context) ([]Consumer, error) {
	consumers := make([]Consumer, 0, len(f.byID))
	for _, consumer := range f.byID {
		consumers = append(consumers, consumer)
	}
	return consumers, nil
}

func (f *fakeConsumers) GetConsumer(_ context.Context, id string) (Consumer, error) {
	consumer, ok := f.byID[id]
	if !ok {
		return Consumer{}, ErrConsumerNotFound
	}
	return consumer, nil
}

func (f *fakeConsumers) UpdateConsumer(
	_ context.Context, id, label string, scope Scope, at time.Time,
) (Consumer, error) {
	consumer, ok := f.byID[id]
	if !ok {
		return Consumer{}, ErrConsumerNotFound
	}
	consumer.Label, consumer.Scope, consumer.UpdatedAt = label, scope, at
	f.byID[id] = consumer
	return consumer, nil
}

func (f *fakeConsumers) SetConsumerRevoked(
	_ context.Context, id string, revoked bool, at time.Time,
) (Consumer, error) {
	consumer, ok := f.byID[id]
	if !ok {
		return Consumer{}, ErrConsumerNotFound
	}
	consumer.Revoked, consumer.UpdatedAt = revoked, at
	f.byID[id] = consumer
	return consumer, nil
}

func (f *fakeConsumers) DeleteConsumer(_ context.Context, id string) error {
	if _, ok := f.byID[id]; !ok {
		return ErrConsumerNotFound
	}
	delete(f.byID, id)
	return nil
}

func (f *fakeConsumers) ConsumerByTokenDigest(_ context.Context, digest []byte) (Consumer, error) {
	if f.lookupErr != nil {
		return Consumer{}, f.lookupErr
	}
	id, ok := f.byDigest[string(digest)]
	if !ok {
		return Consumer{}, ErrConsumerNotFound
	}
	return f.byID[id], nil
}

func (f *fakeConsumers) TouchConsumer(_ context.Context, id string, at time.Time) error {
	f.touched = append(f.touched, id)
	consumer := f.byID[id]
	stamp := at
	consumer.LastUsedAt = &stamp
	f.byID[id] = consumer
	return nil
}

var _ ConsumerRepository = (*fakeConsumers)(nil)

func newTestConsumerService(
	t *testing.T, repository *fakeRepository, consumers *fakeConsumers, now time.Time,
) *ConsumerService {
	t.Helper()
	spool := newTestService(t, repository, now)
	service, err := NewConsumerService(spool, consumers, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewConsumerTokenIsPrefixedAndHashedNotStored(t *testing.T) {
	token, digest, err := NewConsumerToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, ConsumerTokenPrefix) {
		t.Fatalf("token %q has no prefix", token)
	}
	if len(digest) != 32 {
		t.Fatalf("digest length=%d", len(digest))
	}
	// The stored proof must be derivable from the presented token and nothing
	// else — that is what makes the lookup a single indexed hit.
	if string(ConsumerTokenDigest(token)) != string(digest) {
		t.Fatal("digest is not reproducible from the token")
	}
	// And the digest must not be the token: a store that kept the secret would
	// pass every other test here.
	if strings.Contains(string(digest), token) {
		t.Fatal("digest contains the token")
	}
	other, _, err := NewConsumerToken()
	if err != nil {
		t.Fatal(err)
	}
	if other == token {
		t.Fatal("two mints produced the same token")
	}
}

func TestCreateConsumerReturnsTheTokenOnceAndValidatesInput(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	consumers := newFakeConsumers()
	service := newTestConsumerService(t, &fakeRepository{}, consumers, now)
	ctx := context.Background()

	consumer, token, err := service.CreateConsumer(ctx, "smsget", "downstream app",
		Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || consumer.ID != "smsget" {
		t.Fatalf("consumer=%+v token=%q", consumer, token)
	}
	// Nothing on the read path hands the token back.
	stored, err := service.GetConsumer(ctx, "smsget")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Label+stored.ID, token) {
		t.Fatal("a read surface returned the token")
	}

	if _, _, err = service.CreateConsumer(ctx, "smsget", "again",
		Scope{Connectors: []string{"partner-a-term"}}); !errors.Is(err, ErrConsumerExists) {
		t.Fatalf("duplicate create error=%v", err)
	}
	if _, _, err = service.CreateConsumer(ctx, "bad id!", "",
		Scope{Connectors: []string{"partner-a-term"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad id error=%v", err)
	}
	// An empty scope must never become a credential: it would be stored, and
	// the only thing between it and the whole spool is this check.
	if _, _, err = service.CreateConsumer(ctx, "wide", "", Scope{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty scope error=%v", err)
	}
}

func TestAuthenticateRejectsUnknownAndRevokedTokensAndAuditsBoth(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	consumers := newFakeConsumers()
	service := newTestConsumerService(t, repository, consumers, now)
	ctx := context.Background()

	_, token, err := service.CreateConsumer(ctx, "smsget", "",
		Scope{Connectors: []string{"partner-a-term"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.Authenticate(ctx, "synmsg_not-a-real-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown token error=%v", err)
	}
	if _, err = service.Authenticate(ctx, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty token error=%v", err)
	}
	if len(repository.audits) != 2 {
		t.Fatalf("rejections audited=%d want 2", len(repository.audits))
	}
	for _, audit := range repository.audits {
		if audit.Allowed || audit.Subject != ConsumerSubject("unknown") {
			t.Fatalf("audit=%+v", audit)
		}
		// A valid credential in an audit table is the same leak as one in a log.
		if strings.Contains(audit.Target, token) {
			t.Fatal("the audit row recorded the presented token")
		}
	}

	if _, err = service.SetConsumerRevoked(ctx, "smsget", true); err != nil {
		t.Fatal(err)
	}
	repository.audits = nil
	if _, err = service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token error=%v", err)
	}
	if len(repository.audits) != 1 {
		t.Fatalf("revoked rejection audits=%d", len(repository.audits))
	}
	// A revoked credential is audited against its real id: that is the row that
	// answers "did anyone keep using the token after we turned it off".
	if repository.audits[0].Subject != ConsumerSubject("smsget") ||
		!strings.Contains(repository.audits[0].Target, "revoked") {
		t.Fatalf("audit=%+v", repository.audits[0])
	}

	if _, err = service.SetConsumerRevoked(ctx, "smsget", false); err != nil {
		t.Fatal(err)
	}
	consumer, err := service.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.ID != "smsget" {
		t.Fatalf("authenticated=%+v", consumer)
	}
	if len(consumers.touched) != 1 {
		t.Fatalf("touches=%v", consumers.touched)
	}
	// Throttled: a consumer polling every second must not write once a second.
	if _, err = service.Authenticate(ctx, token); err != nil {
		t.Fatal(err)
	}
	if len(consumers.touched) != 1 {
		t.Fatalf("last-used write was not throttled: %v", consumers.touched)
	}
}

// A rejection whose trail cannot be persisted is not a rejection that happened.
func TestAuthenticateIsFailClosedOnTheAuditWrite(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	repository := &fakeRepository{auditErr: errors.New("audit store down")}
	service := newTestConsumerService(t, repository, newFakeConsumers(), now)
	_, err := service.Authenticate(context.Background(), "synmsg_whatever")
	if err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error=%v want the audit failure", err)
	}
}

func TestPageCompilesTheScopeIntoTheQueryAndAuditsTheRead(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	repository := &fakeRepository{records: []Record{{
		Message: sampleMessage(), Sequence: 7, DeliveryState: DeliveryPending,
	}}}
	consumers := newFakeConsumers()
	service := newTestConsumerService(t, repository, consumers, now)
	ctx := context.Background()

	consumer, _, err := service.CreateConsumer(ctx, "smsget", "",
		Scope{Connectors: []string{"partner-a-term", "partner-b-term"}, IncludeText: true})
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.Page(ctx, consumer, PullRequest{Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.NextCursor != EncodeCursor(7) {
		t.Fatalf("page=%+v", page)
	}
	// The restriction reached the repository as a query field, not as a filter
	// applied to what came back.
	query := repository.lastQuery
	if len(query.ConnectorIDs) != 2 || query.ConnectorIDs[0] != "partner-a-term" {
		t.Fatalf("query=%+v", query)
	}
	if query.IncludeReceiptOnly {
		t.Fatal("the pull query admitted receipt-only rows")
	}
	if !query.IncludeContent || query.Limit != 25 {
		t.Fatalf("query=%+v", query)
	}

	if len(repository.audits) != 1 {
		t.Fatalf("audits=%d want 1", len(repository.audits))
	}
	audit := repository.audits[0]
	if audit.Subject != ConsumerSubject("smsget") || audit.RowCount != 1 || !audit.Allowed {
		t.Fatalf("audit=%+v", audit)
	}
	// The scope that was enforced has to be recoverable from the audit row
	// alone: the consumer record may since have been narrowed or deleted.
	if !strings.Contains(audit.Target, "partner-a-term|partner-b-term") ||
		!strings.Contains(audit.Target, "content=true") {
		t.Fatalf("audit target=%q", audit.Target)
	}
	// Reading content is recorded as a reveal, not as a lesser act.
	if audit.Action != ActionReveal {
		t.Fatalf("audit action=%s", audit.Action)
	}
}

// The read is fail-closed on its own trail: if the audit row cannot be
// persisted, no content is returned.
func TestPageIsFailClosedOnTheAuditWrite(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	repository := &fakeRepository{records: []Record{{Message: sampleMessage(), Sequence: 1}}}
	consumers := newFakeConsumers()
	service := newTestConsumerService(t, repository, consumers, now)
	ctx := context.Background()
	consumer, _, err := service.CreateConsumer(ctx, "smsget", "",
		Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
	if err != nil {
		t.Fatal(err)
	}
	repository.auditErr = errors.New("audit store down")
	page, err := service.Page(ctx, consumer, PullRequest{})
	if err == nil {
		t.Fatal("a read whose trail could not be written returned a page")
	}
	if len(page.Records) != 0 {
		t.Fatalf("content escaped a failed audit: %d records", len(page.Records))
	}
}

// Page is the second lock on the same door: even handed a consumer whose scope
// is empty or admits fragments, it refuses rather than reading.
func TestPageRefusesAConsumerWhoseScopeWouldNotRestrict(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	repository := &fakeRepository{records: []Record{{Message: sampleMessage(), Sequence: 1}}}
	service := newTestConsumerService(t, repository, newFakeConsumers(), now)
	ctx := context.Background()

	if _, err := service.Page(ctx, Consumer{ID: "wide"}, PullRequest{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty-scope page error=%v", err)
	}
	if repository.lastQuery.Limit != 0 {
		t.Fatal("an empty scope reached the repository")
	}
	revoked := Consumer{ID: "gone", Revoked: true, Scope: Scope{Connectors: []string{"partner-a-term"}}}
	if _, err := service.Page(ctx, revoked, PullRequest{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked page error=%v", err)
	}
}

func TestUpdateConsumerKeepsTheTokenAndRefusesAnEmptyScope(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	consumers := newFakeConsumers()
	service := newTestConsumerService(t, &fakeRepository{}, consumers, now)
	ctx := context.Background()

	_, token, err := service.CreateConsumer(ctx, "smsget", "before",
		Scope{Connectors: []string{"partner-a-term", "partner-b-term"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.UpdateConsumer(ctx, "smsget", "after", Scope{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty scope update error=%v", err)
	}
	updated, err := service.UpdateConsumer(ctx, "smsget", "after",
		Scope{Connectors: []string{"partner-a-term"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != "after" || len(updated.Scope.Connectors) != 1 {
		t.Fatalf("updated=%+v", updated)
	}
	// Narrowing a scope must not invalidate the credential, or nobody narrows.
	consumer, err := service.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("narrowing the scope invalidated the token: %v", err)
	}
	if len(consumer.Scope.Connectors) != 1 || consumer.Scope.Connectors[0] != "partner-a-term" {
		t.Fatalf("authenticated scope=%+v", consumer.Scope)
	}
}

func TestConsumerLogValueOmitsNothingSecretButNamesTheScope(t *testing.T) {
	consumer := Consumer{ID: "smsget", Revoked: true,
		Scope: Scope{Connectors: []string{"partner-a-term"}, IncludeText: true}}
	rendered := consumer.LogValue().String()
	if !strings.Contains(rendered, "smsget") || !strings.Contains(rendered, "partner-a-term") {
		t.Fatalf("LogValue=%s", rendered)
	}
}
