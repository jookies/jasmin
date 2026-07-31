package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// Message pull consumers on the admin plane.
//
// A consumer is a scoped, read-only credential for the cursor-based message
// pull API. It is deliberately not the admin token: the admin token creates
// users, moves balances and starts connectors, so handing it to a partner's
// application so it can read its own messages would make all of those reachable
// from that application's process — and would make revoking that application's
// access mean rotating the credential every operator tool uses.

// ErrMessageConsumerNotFound is returned when a consumer id is absent. It is
// distinct from the other not-found errors so a caller holding several kinds of
// identity can tell which store answered "no".
var ErrMessageConsumerNotFound = errors.New("admin: message consumer not found")

// MessageConsumerService is the admin-plane CRUD for pull credentials.
//
// It is a translation layer, not a second implementation: minting, hashing,
// scope validation and the audited read all live in msgspool, and this type
// exists to map that package's errors onto the admin plane's HTTP semantics and
// to project a view that structurally cannot carry a token.
type MessageConsumerService struct {
	consumers *msgspool.ConsumerService
}

// NewMessageConsumerService builds the admin surface over the spool's consumer
// service.
func NewMessageConsumerService(consumers *msgspool.ConsumerService) (*MessageConsumerService, error) {
	if consumers == nil {
		return nil, errors.New("admin: message consumer service is required")
	}
	return &MessageConsumerService{consumers: consumers}, nil
}

// MessageConsumerView is the read projection.
//
// There is no token field, and that is structural rather than a redaction rule
// someone has to remember: the plaintext exists once, inside CreateConsumer's
// return, and the digest never leaves the repository. A future read surface
// therefore cannot leak a credential by forgetting to mask a field, because
// there is no field.
type MessageConsumerView struct {
	Consumer msgspool.Consumer
	// Activity is what this credential has actually read. Zero-valued when the
	// caller did not ask for it or the token has never been used -- a token
	// issued and never used is the one worth noticing, and it must not be
	// indistinguishable from one whose activity simply was not loaded, which is
	// what ActivityLoaded reports.
	Activity       msgspool.SubjectActivity
	ActivityLoaded bool
}

// ListConsumersWithActivity is ListConsumers plus each credential's read
// history. It is a separate method because the join costs an aggregate over the
// audit table, and the callers that only need the credential list (jCli, the
// pull-side checks) should not pay for it.
func (s *MessageConsumerService) ListConsumersWithActivity(
	ctx context.Context,
	since time.Time,
) ([]MessageConsumerView, error) {
	views, err := s.ListConsumers(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.Consumer.ID)
	}
	activity, err := s.consumers.Activity(ctx, ids, since)
	if err != nil {
		return nil, translateConsumerError(err)
	}
	for index := range views {
		views[index].ActivityLoaded = true
		if entry, ok := activity[views[index].Consumer.ID]; ok {
			views[index].Activity = entry
		}
	}
	return views, nil
}

// ListConsumers returns every stored pull credential, ordered by id.
func (s *MessageConsumerService) ListConsumers(ctx context.Context) ([]MessageConsumerView, error) {
	consumers, err := s.consumers.ListConsumers(ctx)
	if err != nil {
		return nil, translateConsumerError(err)
	}
	views := make([]MessageConsumerView, 0, len(consumers))
	for _, consumer := range consumers {
		views = append(views, MessageConsumerView{Consumer: consumer})
	}
	return views, nil
}

// GetConsumer returns one stored pull credential.
func (s *MessageConsumerService) GetConsumer(ctx context.Context, id string) (MessageConsumerView, error) {
	consumer, err := s.consumers.GetConsumer(ctx, id)
	if err != nil {
		return MessageConsumerView{}, translateConsumerError(err)
	}
	return MessageConsumerView{Consumer: consumer}, nil
}

// CreateConsumer mints a credential and returns the plaintext token exactly
// once.
//
// The token is not recoverable afterwards by any path, including this one: the
// store holds a SHA-256 proof. Losing it means revoking the consumer and
// issuing another, which is the same trade the durable REST batch credentials
// make and for the same reason — a credential a management API can read back is
// a credential every operator with API access has.
func (s *MessageConsumerService) CreateConsumer(
	ctx context.Context,
	id string,
	label string,
	scope msgspool.Scope,
) (MessageConsumerView, string, error) {
	consumer, token, err := s.consumers.CreateConsumer(ctx, id, label, scope)
	if err != nil {
		return MessageConsumerView{}, "", translateConsumerError(err)
	}
	return MessageConsumerView{Consumer: consumer}, token, nil
}

// UpdateConsumer replaces a consumer's label and scope, leaving its token
// alone. Narrowing a scope must not require the downstream application to
// redeploy a credential, or nobody will narrow one.
func (s *MessageConsumerService) UpdateConsumer(
	ctx context.Context,
	id string,
	label string,
	scope msgspool.Scope,
) (MessageConsumerView, error) {
	consumer, err := s.consumers.UpdateConsumer(ctx, id, label, scope)
	if err != nil {
		return MessageConsumerView{}, translateConsumerError(err)
	}
	return MessageConsumerView{Consumer: consumer}, nil
}

// SetRevoked turns a credential off (or back on) without deleting it, so the
// access-audit rows it produced keep naming an id that can still be looked up.
func (s *MessageConsumerService) SetRevoked(ctx context.Context, id string, revoked bool) (MessageConsumerView, error) {
	consumer, err := s.consumers.SetConsumerRevoked(ctx, id, revoked)
	if err != nil {
		return MessageConsumerView{}, translateConsumerError(err)
	}
	return MessageConsumerView{Consumer: consumer}, nil
}

// DeleteConsumer forgets a credential entirely. Revoking is usually better.
func (s *MessageConsumerService) DeleteConsumer(ctx context.Context, id string) error {
	return translateConsumerError(s.consumers.DeleteConsumer(ctx, id))
}

// translateConsumerError maps msgspool's errors onto the admin plane's, so both
// management surfaces answer with the same status for the same cause.
func translateConsumerError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, msgspool.ErrConsumerNotFound):
		return fmt.Errorf("%w: %v", ErrMessageConsumerNotFound, err)
	case errors.Is(err, msgspool.ErrConsumerExists):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case errors.Is(err, msgspool.ErrInvalidInput):
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	default:
		return err
	}
}
