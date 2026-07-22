package submittransaction

import (
	"context"
	"time"
)

// Repository is the durable atomic boundary for submit intents, results and
// local side-effect events. Implementations must make Admit and CommitResult
// single database transactions and enforce all stable keys with unique indexes.
type Repository interface {
	Admit(context.Context, []LogicalPart, []OutboxEvent) error
	AggregateStatus(context.Context, string) (AggregateStatus, error)
	BeginAttempt(context.Context, string, time.Time) (SendAttempt, bool, error)
	MarkAttemptSent(context.Context, int64, time.Time) error
	MarkAttemptUnknownAfterSend(context.Context, int64, time.Time) error
	RecoverUnresolved(context.Context, time.Time) (int64, error)
	CommitResult(context.Context, ResultCommit) (bool, error)
	ClaimOutbox(context.Context, string, int, time.Time, time.Duration) ([]OutboxEvent, error)
	MarkOutboxDispatched(context.Context, string, string, time.Time) error
	ReleaseOutbox(context.Context, string, string, time.Time, error) error
}

// ProductionRepository is a marker implemented only by the PostgreSQL store.
// It prevents a local SQLite projection from being accidentally wired into a
// production runtime.
type ProductionRepository interface {
	Repository
	ProductionSubmitRepository()
}

func RequireProductionRepository(repository Repository) (ProductionRepository, error) {
	production, ok := repository.(ProductionRepository)
	if !ok || production == nil {
		return nil, ErrProductionRequiresPostgres
	}
	return production, nil
}
