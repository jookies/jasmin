package msgspool

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// Pull-consumer errors.
var (
	// ErrConsumerNotFound reports an absent consumer id on the management path.
	ErrConsumerNotFound = errors.New("message spool consumer not found")
	// ErrConsumerExists reports a create against an id that is already taken.
	ErrConsumerExists = errors.New("message spool consumer already exists")
	// ErrUnauthorized reports a token that is unknown, malformed or revoked.
	//
	// It is deliberately one error for all three: telling a caller which of the
	// three it hit turns the endpoint into an oracle for valid consumer ids.
	ErrUnauthorized = errors.New("message spool consumer token rejected")
)

const (
	// ConsumerTokenPrefix marks a pull token in a log, a config file or a
	// secret scanner. It is part of the token, so it is part of what is hashed.
	ConsumerTokenPrefix = "synmsg_"
	// consumerTokenEntropyBytes is the random part of a token. 32 bytes is what
	// makes the unsalted digest below sound: brute-forcing 256 bits of entropy
	// is not a threat model, unlike brute-forcing a human-chosen password.
	consumerTokenEntropyBytes = 32
	// consumerTouchInterval throttles the last-used write. A consumer polling
	// once a second would otherwise generate 86 400 writes a day to record a
	// fact nobody reads at that resolution.
	consumerTouchInterval = time.Minute
)

// consumerIDPattern bounds a consumer id to what is safe in a URL path segment
// and readable in an audit row.
var consumerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Consumer is a stored, scoped, read-only pull credential.
//
// It carries no token and no digest. The plaintext exists once, at creation,
// and the digest never leaves the repository — so no management surface, log
// line or JSON projection can leak either by forgetting to redact a field that
// was never there.
type Consumer struct {
	// ID is the stable identity. It names the consumer in every audit row this
	// credential produces, which is the whole point of not sharing one token.
	ID string
	// Label is the human description ("smsget-api-gateway, production").
	Label string
	Scope Scope
	// Revoked stops the credential without deleting it, so the audit trail keeps
	// pointing at something that can still be looked up.
	Revoked   bool
	CreatedAt time.Time
	UpdatedAt time.Time
	// LastUsedAt is the newest successful authentication, throttled to
	// consumerTouchInterval. Nil means the credential has never been used, which
	// is the state that identifies a token issued and then forgotten.
	LastUsedAt *time.Time
}

// LogValue keeps a consumer out of a log line's reflection path. Nothing here
// is content, but the scope is a description of who may read OTP bodies and
// does not belong in an incidental %v.
func (consumer Consumer) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("consumer_id", consumer.ID),
		slog.Bool("revoked", consumer.Revoked),
		slog.String("scope", consumer.Scope.Describe()),
	)
}

// ConsumerSubject is the audit-row subject for a consumer. It is prefixed so a
// machine credential is never confused with a console session's username in the
// same trail.
func ConsumerSubject(id string) string { return "consumer:" + id }

// principal is the identity a consumer reads under.
//
// RoleRevealer is granted only when the scope includes text, which is what
// makes "this consumer's reads are recorded as reveals" a property of the
// stored scope rather than of the request.
func (consumer Consumer) principal() Principal {
	roles := []Role{RoleReader}
	if consumer.Scope.IncludeText {
		roles = append(roles, RoleRevealer)
	}
	return Principal{Subject: ConsumerSubject(consumer.ID), Roles: roles}
}

// ValidateConsumerID refuses an id that would be awkward in a URL or ambiguous
// in an audit row.
func ValidateConsumerID(id string) error {
	if !consumerIDPattern.MatchString(id) {
		return fmt.Errorf("%w: consumer id must match %s", ErrInvalidInput, consumerIDPattern)
	}
	return nil
}

// NewConsumerToken mints a credential: the plaintext to hand over once, and the
// digest to store.
func NewConsumerToken() (token string, digest []byte, err error) {
	raw := make([]byte, consumerTokenEntropyBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate message spool consumer token: %w", err)
	}
	token = ConsumerTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, ConsumerTokenDigest(token), nil
}

// ConsumerTokenDigest is the stored proof of a token, following the durable
// REST batch credentials: SHA-256 of the presented bytes, 32 bytes, never the
// secret itself.
//
// It is unsalted and uniterated on purpose, and that is safe here for a reason
// that does not hold for passwords: the input is 256 bits from crypto/rand, so
// there is no dictionary to precompute and no work factor that would change the
// outcome. Salting would also make the lookup a table scan instead of a unique
// index hit on a path a consumer polls every second.
func ConsumerTokenDigest(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ConsumerRepository is the durable side of the pull credentials. Both
// PostgreSQL and SQLite implement it identically.
//
// The digest is an argument and never a return value: it enters on create and
// on lookup, and there is no method that hands it back out.
type ConsumerRepository interface {
	CreateConsumer(ctx context.Context, consumer Consumer, tokenDigest []byte) error
	ListConsumers(ctx context.Context) ([]Consumer, error)
	GetConsumer(ctx context.Context, id string) (Consumer, error)
	UpdateConsumer(ctx context.Context, id, label string, scope Scope, at time.Time) (Consumer, error)
	SetConsumerRevoked(ctx context.Context, id string, revoked bool, at time.Time) (Consumer, error)
	DeleteConsumer(ctx context.Context, id string) error
	// ConsumerByTokenDigest resolves a presented credential. It returns
	// ErrConsumerNotFound for an unknown digest; whether the record is revoked is
	// reported in the Consumer, so the caller audits the attempt against a real
	// consumer id instead of losing it as an anonymous rejection.
	ConsumerByTokenDigest(ctx context.Context, tokenDigest []byte) (Consumer, error)
	// TouchConsumer records a successful authentication. It is best-effort by
	// contract: a failure must not deny a read that was already authorized.
	TouchConsumer(ctx context.Context, id string, at time.Time) error
}

// ConsumerService is the pull API's whole business logic: authenticate a token,
// compile its scope into a query, and read through the audited boundary.
//
// It holds *Service rather than a Repository so a consumer physically cannot
// reach an unaudited read. Every page this type returns has already written an
// access-audit row naming the consumer, the scope applied and the row count,
// and Service.audit is fail-closed — a page whose trail cannot be persisted is
// not returned.
type ConsumerService struct {
	spool     *Service
	consumers ConsumerRepository
	now       func() time.Time
}

// NewConsumerService builds the pull service.
func NewConsumerService(spool *Service, consumers ConsumerRepository, now func() time.Time) (*ConsumerService, error) {
	if spool == nil || consumers == nil {
		return nil, fmt.Errorf("%w: message spool service and consumer repository are required", ErrInvalidInput)
	}
	if now == nil {
		now = time.Now
	}
	return &ConsumerService{spool: spool, consumers: consumers, now: now}, nil
}

// Authenticate resolves a presented token to a live consumer.
//
// Every rejection writes an access-audit row before returning, so a token
// probing campaign is visible in the same trail as a legitimate read. A revoked
// credential is audited against its real id — that is the row that answers "did
// anyone keep using the token after we turned it off".
func (service *ConsumerService) Authenticate(ctx context.Context, presented string) (Consumer, error) {
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return Consumer{}, service.denyAuth(ctx, "", "empty token")
	}
	consumer, err := service.consumers.ConsumerByTokenDigest(ctx, ConsumerTokenDigest(presented))
	if err != nil {
		if errors.Is(err, ErrConsumerNotFound) {
			return Consumer{}, service.denyAuth(ctx, "", "unknown token")
		}
		return Consumer{}, err
	}
	if consumer.Revoked {
		return Consumer{}, service.denyAuth(ctx, consumer.ID, "revoked token")
	}
	service.touch(ctx, consumer)
	return consumer, nil
}

// denyAuth records the rejection and returns ErrUnauthorized. It mirrors
// Service.deny: if the audit row cannot be written, that failure is what the
// caller sees, because an unrecorded rejection is not one that happened.
func (service *ConsumerService) denyAuth(ctx context.Context, id, reason string) error {
	subject := ConsumerSubject(id)
	if id == "" {
		// An unknown token has no id to name. The token itself is never recorded:
		// a valid credential in an audit table is the same leak as a valid
		// credential in a log file.
		subject = ConsumerSubject("unknown")
	}
	if err := service.spool.audit(ctx, Principal{Subject: subject},
		ActionSearch, "pull-auth,reason="+reason, false, 0); err != nil {
		return err
	}
	return ErrUnauthorized
}

// touch refreshes last_used_at, throttled and best-effort. A failure here must
// not turn an authorized read into an error: this column is operational
// housekeeping, and the authoritative record of the read is the audit row.
func (service *ConsumerService) touch(ctx context.Context, consumer Consumer) {
	now := service.now().UTC()
	if consumer.LastUsedAt != nil && now.Sub(*consumer.LastUsedAt) < consumerTouchInterval {
		return
	}
	_ = service.consumers.TouchConsumer(ctx, consumer.ID, now)
}

// Page returns one page of spooled messages for a consumer.
//
// The scope is compiled into the query; nothing is dropped afterwards. The
// returned page's NextCursor is the sequence of the last row the consumer may
// see, so paging is dense over its own traffic and carries no information about
// anyone else's.
func (service *ConsumerService) Page(
	ctx context.Context,
	consumer Consumer,
	request PullRequest,
) (SearchPage, error) {
	if consumer.Revoked {
		return SearchPage{}, ErrUnauthorized
	}
	search, err := consumer.Scope.Compile(request)
	if err != nil {
		return SearchPage{}, err
	}
	// The second lock on the same door. Query treats an empty ConnectorIDs as
	// "no filter", which for a scoped consumer would mean the entire spool; the
	// only thing standing between that and a caller is Scope.Validate. This
	// check makes the fail-open path unreachable even if a future scope field
	// changes how Compile builds the list.
	if len(search.ConnectorIDs) == 0 {
		return SearchPage{}, ErrForbidden
	}
	// Same reasoning, for the other restriction Compile is responsible for: a
	// pull consumer must never be handed a per-segment receipt row, which it
	// would store as the message.
	if search.IncludeReceiptOnly {
		return SearchPage{}, ErrForbidden
	}
	return service.spool.Search(ctx, consumer.principal(), search)
}

// CreateConsumer mints a credential and stores its digest.
//
// The plaintext token is returned exactly once, here. There is no path that
// reads it back: the repository holds a hash, and recovering from a lost token
// is revoking the consumer and issuing another.
func (service *ConsumerService) CreateConsumer(
	ctx context.Context,
	id string,
	label string,
	scope Scope,
) (Consumer, string, error) {
	id = strings.TrimSpace(id)
	if err := ValidateConsumerID(id); err != nil {
		return Consumer{}, "", err
	}
	scope = scope.Normalize()
	if err := scope.Validate(); err != nil {
		return Consumer{}, "", err
	}
	token, digest, err := NewConsumerToken()
	if err != nil {
		return Consumer{}, "", err
	}
	now := service.now().UTC()
	consumer := Consumer{
		ID:        id,
		Label:     strings.TrimSpace(label),
		Scope:     scope,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err = service.consumers.CreateConsumer(ctx, consumer, digest); err != nil {
		return Consumer{}, "", err
	}
	return consumer, token, nil
}

// ListConsumers returns every stored consumer, ordered by id.
func (service *ConsumerService) ListConsumers(ctx context.Context) ([]Consumer, error) {
	return service.consumers.ListConsumers(ctx)
}

// GetConsumer returns one stored consumer.
func (service *ConsumerService) GetConsumer(ctx context.Context, id string) (Consumer, error) {
	return service.consumers.GetConsumer(ctx, id)
}

// UpdateConsumer replaces a consumer's label and scope. The token is untouched:
// narrowing a scope must not require the downstream application to redeploy a
// credential, or nobody will narrow one.
func (service *ConsumerService) UpdateConsumer(
	ctx context.Context,
	id string,
	label string,
	scope Scope,
) (Consumer, error) {
	scope = scope.Normalize()
	if err := scope.Validate(); err != nil {
		return Consumer{}, err
	}
	return service.consumers.UpdateConsumer(ctx, id, strings.TrimSpace(label), scope, service.now().UTC())
}

// SetConsumerRevoked turns a credential off (or back on) without deleting it.
func (service *ConsumerService) SetConsumerRevoked(ctx context.Context, id string, revoked bool) (Consumer, error) {
	return service.consumers.SetConsumerRevoked(ctx, id, revoked, service.now().UTC())
}

// DeleteConsumer forgets a credential entirely. Revoking is usually the better
// answer: the audit rows this consumer produced keep naming an id that can then
// still be looked up.
func (service *ConsumerService) DeleteConsumer(ctx context.Context, id string) error {
	return service.consumers.DeleteConsumer(ctx, id)
}
