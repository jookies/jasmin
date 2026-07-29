package admin

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// ConnectorManager is the slice of smppc.Manager the admin plane drives. The
// manager already supports live add/remove/start/stop, so admin CRUD applies
// immediately without a gateway restart.
type ConnectorManager interface {
	Add(cfg smppc.Config) error
	Update(cfg smppc.Config) error
	Remove(cid string) error
	Start(cid string) error
	Stop(cid string) error
	Status(cid string) (smppc.ManagedStatus, error)
}

// Service applies admin connector CRUD through the manager and persists it.
// A mutex serialises mutations so the store and the manager never diverge
// under concurrent admin calls.
type Service struct {
	store    *Store
	manager  ConnectorManager
	reserved map[string]struct{} // config-owned cids admin must not touch
	now      func() string
	mu       sync.Mutex
	applied  map[string]struct{}
}

// NewService builds the admin service. reservedCIDs are the --config connector
// ids that admin may not create/modify/delete (config owns them).
func NewService(store *Store, manager ConnectorManager, reservedCIDs []string, now func() string) (*Service, error) {
	if store == nil || manager == nil {
		return nil, errors.New("admin: store and manager are required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	reserved := make(map[string]struct{}, len(reservedCIDs))
	for _, cid := range reservedCIDs {
		reserved[cid] = struct{}{}
	}
	return &Service{
		store:    store,
		manager:  manager,
		reserved: reserved,
		now:      now,
		applied:  make(map[string]struct{}),
	}, nil
}

// LoadAndApply re-applies every persisted admin connector into the manager at
// boot: Add, then Start when the connector was desired-started. A cid that now
// collides with a config connector is skipped with an error (config wins).
func (s *Service) LoadAndApply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.ListConnectors(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for cid := range s.applied {
		// Stop first: the manager refuses to remove a connector that is desired
		// or not yet disconnected (smppc.Manager.Remove), so a started admin
		// connector would otherwise never be reconciled — it would keep its old
		// config, or survive its own deletion, while stopped ones rebuilt fine.
		if err := s.manager.Stop(cid); err != nil {
			errs = append(errs, fmt.Errorf("admin: stop previously applied connector %q: %w", cid, err))
		}
		if err := s.manager.Remove(cid); err != nil {
			errs = append(errs, fmt.Errorf("admin: remove previously applied connector %q: %w", cid, err))
		}
		// Drop the bookkeeping either way. Retaining it makes the add loop below
		// skip the connector as "could not be refreshed", which turns a single
		// transient removal failure into a permanent one that only a process
		// restart clears. Re-adding a still-live cid fails loudly instead.
		delete(s.applied, cid)
	}
	for _, connector := range stored {
		if _, reserved := s.reserved[connector.Config.CID]; reserved {
			errs = append(errs, fmt.Errorf("admin: stored connector %q collides with a config connector, skipped", connector.Config.CID))
			continue
		}
		if _, stillApplied := s.applied[connector.Config.CID]; stillApplied {
			errs = append(errs, fmt.Errorf("admin: connector %q could not be refreshed", connector.Config.CID))
			continue
		}
		if err := s.manager.Add(connector.Config); err != nil {
			errs = append(errs, fmt.Errorf("admin: re-add %q: %w", connector.Config.CID, err))
			continue
		}
		s.applied[connector.Config.CID] = struct{}{}
		if connector.DesiredStarted {
			if err := s.manager.Start(connector.Config.CID); err != nil {
				errs = append(errs, fmt.Errorf("admin: re-start %q: %w", connector.Config.CID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// CreateConnector validates, applies (Add + optional Start), then persists a
// new admin connector. It applies to the manager first so a bad config is
// rejected before anything is written; a persist failure rolls the manager
// back.
func (s *Service) CreateConnector(ctx context.Context, config smppc.Config, start bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(config.CID); err != nil {
		return err
	}
	if _, err := s.store.GetConnector(ctx, config.CID); err == nil {
		return fmt.Errorf("%w: connector %q already exists", ErrConflict, config.CID)
	} else if !errors.Is(err, ErrConnectorNotFound) {
		return err
	}
	if err := s.manager.Add(config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if start {
		if err := s.manager.Start(config.CID); err != nil {
			_ = s.manager.Remove(config.CID)
			return fmt.Errorf("%w: start: %v", ErrInvalidRequest, err)
		}
	}
	if err := s.store.UpsertConnector(ctx, StoredConnector{Config: config, DesiredStarted: start}, s.now()); err != nil {
		_ = s.manager.Remove(config.CID) // roll back the live change on persist failure
		delete(s.applied, config.CID)
		return err
	}
	s.applied[config.CID] = struct{}{}
	return nil
}

// UpdateConnector replaces an existing admin connector's config live.
func (s *Service) UpdateConnector(ctx context.Context, config smppc.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(config.CID); err != nil {
		return err
	}
	existing, err := s.store.GetConnector(ctx, config.CID)
	if err != nil {
		return err
	}
	if err := s.manager.Update(config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.UpsertConnector(ctx, StoredConnector{Config: config, DesiredStarted: existing.DesiredStarted}, s.now()); err != nil {
		return err
	}
	s.applied[config.CID] = struct{}{}
	return nil
}

// DeleteConnector stops+removes an admin connector and forgets it.
func (s *Service) DeleteConnector(ctx context.Context, cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(cid); err != nil {
		return err
	}
	if _, err := s.store.GetConnector(ctx, cid); err != nil {
		return err
	}
	if err := s.manager.Remove(cid); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.DeleteConnector(ctx, cid); err != nil {
		return err
	}
	delete(s.applied, cid)
	return nil
}

// SetStarted starts or stops an admin connector and records the desired state
// so a restart restores it.
func (s *Service) SetStarted(ctx context.Context, cid string, start bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(cid); err != nil {
		return err
	}
	if _, err := s.store.GetConnector(ctx, cid); err != nil {
		return err
	}
	action := s.manager.Stop
	if start {
		action = s.manager.Start
	}
	if err := action(cid); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.SetDesiredStarted(ctx, cid, start, s.now())
}

// ConnectorView is the read projection: persisted config + live status.
type ConnectorView struct {
	Config         smppc.Config
	DesiredStarted bool
	Observed       string
}

// ListConnectors returns every admin connector with its live bind status.
func (s *Service) ListConnectors(ctx context.Context) ([]ConnectorView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.ListConnectors(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]ConnectorView, 0, len(stored))
	for _, connector := range stored {
		views = append(views, s.view(connector))
	}
	return views, nil
}

// GetConnector returns one admin connector with its live status.
func (s *Service) GetConnector(ctx context.Context, cid string) (ConnectorView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.GetConnector(ctx, cid)
	if err != nil {
		return ConnectorView{}, err
	}
	return s.view(stored), nil
}

func (s *Service) view(stored StoredConnector) ConnectorView {
	observed := "UNKNOWN"
	if status, err := s.manager.Status(stored.Config.CID); err == nil {
		observed = string(status.Observed)
	}
	return ConnectorView{Config: stored.Config, DesiredStarted: stored.DesiredStarted, Observed: observed}
}

func (s *Service) guardCID(cid string) error {
	if cid == "" {
		return fmt.Errorf("%w: empty cid", ErrInvalidRequest)
	}
	if _, reserved := s.reserved[cid]; reserved {
		return fmt.Errorf("%w: connector %q is config-owned and not admin-mutable", ErrConflict, cid)
	}
	return nil
}

var (
	// ErrInvalidRequest is a client error (bad config, unknown cid on the wire).
	ErrInvalidRequest = errors.New("admin: invalid request")
	// ErrConflict is a create over an existing / config-owned cid.
	ErrConflict = errors.New("admin: conflict")
)
