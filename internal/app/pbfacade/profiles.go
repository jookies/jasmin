package pbfacade

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const rollbackProfile = "__pb_facade_live_rollback__"

// SnapshotStore is the durable admin profile store.
type SnapshotStore interface {
	Save(context.Context, string) error
	Load(context.Context, string) error
}

type scopedSnapshotStore interface {
	SaveScope(context.Context, string, string) error
	LoadScope(context.Context, string, string) error
}

// LiveReconciler re-applies one restored admin entity family.
type LiveReconciler interface {
	LoadAndApply(context.Context) error
}

// RuntimeProfiles makes profile restoration a live operation. The underlying
// SQLite replacement is transactional; this coordinator serializes callers,
// replays all services in dependency order, and restores the pre-load snapshot
// if any service rejects the new profile.
type RuntimeProfiles struct {
	store       SnapshotStore
	reconcilers []LiveReconciler

	mu        sync.Mutex
	persisted bool
}

func NewRuntimeProfiles(store SnapshotStore, reconcilers ...LiveReconciler) (*RuntimeProfiles, error) {
	if store == nil {
		return nil, errors.New("pb facade: profile store is required")
	}
	for index, reconciler := range reconcilers {
		if reconciler == nil {
			return nil, fmt.Errorf("pb facade: nil profile reconciler %d", index)
		}
	}
	return &RuntimeProfiles{
		store:       store,
		reconcilers: append([]LiveReconciler(nil), reconcilers...),
	}, nil
}

func (p *RuntimeProfiles) Save(ctx context.Context, profile string) error {
	return p.SaveScope(ctx, profile, "all")
}

func (p *RuntimeProfiles) SaveScope(ctx context.Context, profile, scope string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := validateProfileName(profile); err != nil {
		return err
	}
	if err := p.saveStore(ctx, profile, scope); err != nil {
		return err
	}
	p.persisted = true
	return nil
}

func (p *RuntimeProfiles) Load(ctx context.Context, profile string) error {
	return p.LoadScope(ctx, profile, "all")
}

func (p *RuntimeProfiles) LoadScope(ctx context.Context, profile, scope string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := validateProfileName(profile); err != nil {
		return err
	}
	// Capture the currently effective row set before replacing it. The internal
	// name is deliberately unavailable to PB clients.
	if err := p.saveStore(ctx, rollbackProfile, scope); err != nil {
		return fmt.Errorf("pb facade: capture live profile: %w", err)
	}
	if err := p.loadStore(ctx, profile, scope); err != nil {
		return err
	}
	if err := p.reconcile(ctx); err != nil {
		applyErr := err
		rollbackErr := p.loadStore(ctx, rollbackProfile, scope)
		if rollbackErr == nil {
			rollbackErr = p.reconcile(ctx)
		}
		p.persisted = false
		return errors.Join(
			fmt.Errorf("pb facade: apply profile %q: %w", profile, applyErr),
			wrapRollbackError(rollbackErr),
		)
	}
	p.persisted = true
	return nil
}

func (p *RuntimeProfiles) saveStore(ctx context.Context, profile, scope string) error {
	if store, ok := p.store.(scopedSnapshotStore); ok {
		return store.SaveScope(ctx, profile, scope)
	}
	return p.store.Save(ctx, profile)
}

func (p *RuntimeProfiles) loadStore(ctx context.Context, profile, scope string) error {
	if store, ok := p.store.(scopedSnapshotStore); ok {
		return store.LoadScope(ctx, profile, scope)
	}
	return p.store.Load(ctx, profile)
}

func (p *RuntimeProfiles) reconcile(ctx context.Context) error {
	for index, reconciler := range p.reconcilers {
		if err := reconciler.LoadAndApply(ctx); err != nil {
			return fmt.Errorf("reconciler %d: %w", index, err)
		}
	}
	return nil
}

func wrapRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("pb facade: restore previous live profile: %w", err)
}

func (p *RuntimeProfiles) IsPersisted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persisted
}

func (p *RuntimeProfiles) MarkDirty() {
	p.mu.Lock()
	p.persisted = false
	p.mu.Unlock()
}

func validateProfileName(profile string) error {
	if profile == "" {
		return errors.New("pb facade: empty profile")
	}
	if profile == rollbackProfile {
		return errors.New("pb facade: reserved profile")
	}
	return nil
}
