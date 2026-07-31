package admin

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// TerminationManager is the slice of termination.Manager the admin plane
// drives, declared here at the consumer and mirroring ConnectorManager above:
// the manager already supports live add/update/remove/start/stop, so admin CRUD
// applies immediately without a gateway restart.
//
// The manager keeps its own reserved-cid set and refuses a config-owned cid
// itself. The service still guards, because the two lists are populated from
// different places and a service that trusted the manager would report a
// misleading error when they disagreed.
type TerminationManager interface {
	Add(cfg termination.ConnectorConfig) error
	Update(cfg termination.ConnectorConfig) error
	Remove(cid string) error
	Start(cid string) error
	Stop(cid string) error
	Status(cid string) (termination.ManagedStatus, error)
}

// redactedSecretMarker is whatever ConnectorConfig.Redacted() substitutes for a
// configured secret. It is derived from the contract rather than spelled here,
// so a change to the marker cannot silently turn it into an acceptable secret
// value: a console that round-trips a redacted read must never be able to
// overwrite the real credential with the marker it was shown.
var redactedSecretMarker = termination.ConnectorConfig{
	Delivery: termination.DeliveryConfig{Secret: "probe"},
}.Redacted().Delivery.Secret

// TerminationService applies termination connector CRUD through the manager and
// persists it. A mutex serialises mutations so the store and the manager never
// diverge under concurrent admin calls, mirroring Service.
type TerminationService struct {
	store    *Store
	manager  TerminationManager
	reserved map[string]struct{} // config-owned cids admin must not touch
	now      func() string
	mu       sync.Mutex
	applied  map[string]struct{}
}

// NewTerminationService builds the termination connector admin service.
// reservedCIDs are the --config termination connector ids admin may not
// create/modify/delete (config owns them), the same rule the SMPP client
// connectors follow.
func NewTerminationService(store *Store, manager TerminationManager, reservedCIDs []string, now func() string) (*TerminationService, error) {
	if store == nil || manager == nil {
		return nil, errors.New("admin: termination store and manager are required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	reserved := make(map[string]struct{}, len(reservedCIDs))
	for _, cid := range reservedCIDs {
		reserved[cid] = struct{}{}
	}
	return &TerminationService{
		store:    store,
		manager:  manager,
		reserved: reserved,
		now:      now,
		applied:  make(map[string]struct{}),
	}, nil
}

// LoadAndApply re-applies every persisted termination connector into the
// manager at boot: Add, then Start when the connector was desired-started. A
// cid that now collides with a config connector is skipped with an error
// (config wins), exactly as Service.LoadAndApply does.
func (s *TerminationService) LoadAndApply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.ListTerminationConnectors(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for cid := range s.applied {
		// Stop before remove: a running consumer must let go of its queue before
		// its replacement claims it, and dropping the bookkeeping either way
		// keeps one transient failure from permanently shadowing the connector.
		if err := s.manager.Stop(cid); err != nil {
			errs = append(errs, fmt.Errorf("admin: stop previously applied termination connector %q: %w", cid, err))
		}
		if err := s.manager.Remove(cid); err != nil {
			errs = append(errs, fmt.Errorf("admin: remove previously applied termination connector %q: %w", cid, err))
		}
		delete(s.applied, cid)
	}
	for _, connector := range stored {
		cid := connector.Config.CID
		if _, reserved := s.reserved[cid]; reserved {
			errs = append(errs, fmt.Errorf("admin: stored termination connector %q collides with a config connector, skipped", cid))
			continue
		}
		if err := s.manager.Add(connector.Config); err != nil {
			errs = append(errs, fmt.Errorf("admin: re-add termination connector %q: %w", cid, err))
			continue
		}
		s.applied[cid] = struct{}{}
		if connector.DesiredStarted {
			if err := s.manager.Start(cid); err != nil {
				errs = append(errs, fmt.Errorf("admin: re-start termination connector %q: %w", cid, err))
			}
		}
	}
	return errors.Join(errs...)
}

// CreateConnector validates, applies (Add + optional Start), then persists a new
// termination connector. Validation runs before anything is touched, so a
// rejected config never reaches the manager; a persist failure rolls the live
// change back.
func (s *TerminationService) CreateConnector(ctx context.Context, config termination.ConnectorConfig, start bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(config.CID); err != nil {
		return err
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	// A create whose secret is the redaction marker is a console echoing back a
	// value it was shown for some other connector. There is no stored secret to
	// preserve here, so the only honest reading is "no secret", never the marker
	// itself as a signing key.
	if config.Delivery.Secret == redactedSecretMarker {
		config.Delivery.Secret = ""
	}
	// Persist the effective configuration rather than the sparse one. An
	// operator reading this connector back must see the 5s/2s the receipt will
	// actually be held for; the cost is that a later change to the defaults does
	// not reach connectors created before it, which is the right trade for
	// values that are parity constants.
	config = config.WithDefaults()
	if _, err := s.store.GetTerminationConnector(ctx, config.CID); err == nil {
		return fmt.Errorf("%w: termination connector %q already exists", ErrConflict, config.CID)
	} else if !errors.Is(err, ErrTerminationConnectorNotFound) {
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
	if err := s.store.UpsertTerminationConnector(ctx,
		StoredTerminationConnector{Config: config, DesiredStarted: start}, s.now()); err != nil {
		_ = s.manager.Stop(config.CID)
		_ = s.manager.Remove(config.CID) // roll back the live change on persist failure
		delete(s.applied, config.CID)
		return err
	}
	s.applied[config.CID] = struct{}{}
	return nil
}

// UpdateConnector replaces an existing termination connector's config live.
//
// The delivery secret is never taken from the caller unless the caller supplied
// a real one: an empty secret keeps the stored value, and the redaction marker
// keeps it too, because the marker is exactly what every read path returned. A
// UI that PATCHes back the object it rendered therefore cannot destroy the
// credential, and it cannot do so by mistake in any other client either — the
// rule lives here, not in the form.
//
// clearSecret is the one way to remove a configured secret. It is a separate
// argument rather than a magic value so "stop signing these deliveries" has to
// be asked for in words.
func (s *TerminationService) UpdateConnector(ctx context.Context, config termination.ConnectorConfig, clearSecret bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(config.CID); err != nil {
		return err
	}
	existing, err := s.store.GetTerminationConnector(ctx, config.CID)
	if err != nil {
		return err
	}
	config.Delivery.Secret = resolveDeliverySecret(config.Delivery.Secret, existing.Config.Delivery.Secret, clearSecret)
	if err := config.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	config = config.WithDefaults()

	if err := s.manager.Update(config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.UpsertTerminationConnector(ctx,
		StoredTerminationConnector{Config: config, DesiredStarted: existing.DesiredStarted}, s.now()); err != nil {
		return err
	}
	s.applied[config.CID] = struct{}{}
	return nil
}

// resolveDeliverySecret decides which secret is persisted. See UpdateConnector.
func resolveDeliverySecret(incoming, stored string, clear bool) string {
	if clear {
		return ""
	}
	if incoming == "" || incoming == redactedSecretMarker {
		return stored
	}
	return incoming
}

// DeleteConnector stops+removes a termination connector and forgets it.
func (s *TerminationService) DeleteConnector(ctx context.Context, cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(cid); err != nil {
		return err
	}
	if _, err := s.store.GetTerminationConnector(ctx, cid); err != nil {
		return err
	}
	if err := s.manager.Remove(cid); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.DeleteTerminationConnector(ctx, cid); err != nil {
		return err
	}
	delete(s.applied, cid)
	return nil
}

// SetStarted starts or stops a termination connector and records the desired
// state so a restart restores it.
func (s *TerminationService) SetStarted(ctx context.Context, cid string, start bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardCID(cid); err != nil {
		return err
	}
	if _, err := s.store.GetTerminationConnector(ctx, cid); err != nil {
		return err
	}
	action := s.manager.Stop
	if start {
		action = s.manager.Start
	}
	if err := action(cid); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.SetTerminationDesiredStarted(ctx, cid, start, s.now())
}

// TerminationConnectorView is the read projection: the persisted config with the
// delivery secret redacted, plus desired and live state.
//
// Config is always the redacted copy. HasSecret carries the one fact the marker
// was standing in for, so a console can render "signing configured" without
// parsing the marker string.
type TerminationConnectorView struct {
	Config         termination.ConnectorConfig
	DesiredStarted bool
	Observed       string
	HasSecret      bool
}

// ListConnectors returns every termination connector with its live state.
func (s *TerminationService) ListConnectors(ctx context.Context) ([]TerminationConnectorView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.ListTerminationConnectors(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]TerminationConnectorView, 0, len(stored))
	for _, connector := range stored {
		views = append(views, s.view(connector))
	}
	return views, nil
}

// GetConnector returns one termination connector with its live state.
func (s *TerminationService) GetConnector(ctx context.Context, cid string) (TerminationConnectorView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.GetTerminationConnector(ctx, cid)
	if err != nil {
		return TerminationConnectorView{}, err
	}
	return s.view(stored), nil
}

// view is the only place a stored termination connector becomes something a
// caller outside this package holds, which is why the redaction is here and not
// in each handler: a new read surface cannot forget it.
func (s *TerminationService) view(stored StoredTerminationConnector) TerminationConnectorView {
	observed := "UNKNOWN"
	if status, err := s.manager.Status(stored.Config.CID); err == nil {
		observed = string(status.Observed)
	}
	return TerminationConnectorView{
		Config:         stored.Config.Redacted(),
		DesiredStarted: stored.DesiredStarted,
		Observed:       observed,
		HasSecret:      stored.Config.HasSecret(),
	}
}

func (s *TerminationService) guardCID(cid string) error {
	if cid == "" {
		return fmt.Errorf("%w: empty cid", ErrInvalidRequest)
	}
	if _, reserved := s.reserved[cid]; reserved {
		return fmt.Errorf("%w: termination connector %q is config-owned and not admin-mutable", ErrConflict, cid)
	}
	return nil
}
