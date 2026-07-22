package smppc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrNotFound      = errors.New("connector not found")
	ErrAlreadyExists = errors.New("connector already exists")
)

type ConnectorFactory func(Config, string) (*Connector, error)

type managedConnector struct {
	connector *Connector
	desired   bool
}

type ManagedStatus struct {
	CID      string
	Desired  bool
	Observed Status
	Config   Config
}

type ManagerStats struct {
	Total        int
	Desired      int
	Disconnected int
	Connecting   int
	Bound        int
	Unbinding    int
}

type Manager struct {
	amqpURL    string
	factory    ConnectorFactory
	connectors map[string]*managedConnector
	mu         sync.RWMutex
	opMu       sync.Mutex
	operations map[string]*sync.Mutex
}

func NewManager(amqpURL string) *Manager {
	return NewManagerWithFactory(amqpURL, NewConnector)
}

func NewManagerWithFactory(amqpURL string, factory ConnectorFactory) *Manager {
	if factory == nil {
		factory = NewConnector
	}
	return &Manager{amqpURL: amqpURL, factory: factory, connectors: make(map[string]*managedConnector), operations: make(map[string]*sync.Mutex)}
}

// operationLock serializes every lifecycle transition for one CID without
// holding the manager map lock across network startup/shutdown.
func (m *Manager) operationLock(cid string) func() {
	m.opMu.Lock()
	lock := m.operations[cid]
	if lock == nil {
		lock = &sync.Mutex{}
		m.operations[cid] = lock
	}
	m.opMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (m *Manager) Add(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	connector, err := m.factory(cfg.Clone(), m.amqpURL)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.connectors[cfg.CID]; ok {
		return ErrAlreadyExists
	}
	m.connectors[cfg.CID] = &managedConnector{connector: connector}
	return nil
}

func (m *Manager) Remove(cid string) error {
	m.mu.Lock()
	entry, ok := m.connectors[cid]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if entry.desired || entry.connector.Status() != StatusDisconnected {
		m.mu.Unlock()
		return fmt.Errorf("connector must be stopped before removal")
	}
	delete(m.connectors, cid)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Get(cid string) (*Connector, error) {
	m.mu.RLock()
	entry, ok := m.connectors[cid]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return entry.connector, nil
}

func (m *Manager) List() []Config {
	m.mu.RLock()
	list := make([]Config, 0, len(m.connectors))
	for _, entry := range m.connectors {
		list = append(list, entry.connector.Config())
	}
	m.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].CID < list[j].CID })
	return list
}

func (m *Manager) Start(cid string) error {
	unlock := m.operationLock(cid)
	defer unlock()
	return m.start(cid)
}

func (m *Manager) start(cid string) error {
	m.mu.Lock()
	entry, ok := m.connectors[cid]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	entry.desired = true
	connector := entry.connector
	m.mu.Unlock()
	if err := connector.Start(); err != nil {
		m.mu.Lock()
		if current := m.connectors[cid]; current == entry {
			current.desired = false
		}
		m.mu.Unlock()
		return err
	}
	return nil
}

func (m *Manager) Stop(cid string) error {
	unlock := m.operationLock(cid)
	defer unlock()
	return m.stop(cid)
}

func (m *Manager) stop(cid string) error {
	m.mu.Lock()
	entry, ok := m.connectors[cid]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	entry.desired = false
	connector := entry.connector
	m.mu.Unlock()
	// Connector.Stop may block on network/session shutdown. Never hold manager.mu.
	return connector.Stop()
}

func (m *Manager) Update(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	unlock := m.operationLock(cfg.CID)
	defer unlock()
	m.mu.RLock()
	old, ok := m.connectors[cfg.CID]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	// Build first so an invalid replacement cannot disturb the current connector.
	replacement, err := m.factory(cfg.Clone(), m.amqpURL)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.connectors[cfg.CID] != old {
		m.mu.Unlock()
		return errors.New("connector changed concurrently")
	}
	desired := old.desired
	m.mu.Unlock()
	if err := old.connector.Stop(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.connectors[cfg.CID] != old {
		m.mu.Unlock()
		return errors.New("connector changed concurrently")
	}
	m.connectors[cfg.CID] = &managedConnector{connector: replacement, desired: desired}
	m.mu.Unlock()
	if desired {
		return replacement.Start()
	}
	return nil
}

func (m *Manager) Status(cid string) (ManagedStatus, error) {
	m.mu.RLock()
	entry, ok := m.connectors[cid]
	if !ok {
		m.mu.RUnlock()
		return ManagedStatus{}, ErrNotFound
	}
	desired, connector := entry.desired, entry.connector
	m.mu.RUnlock()
	return ManagedStatus{CID: cid, Desired: desired, Observed: connector.Status(), Config: connector.Config()}, nil
}

func (m *Manager) Stats() ManagerStats {
	m.mu.RLock()
	entries := make([]*managedConnector, 0, len(m.connectors))
	for _, entry := range m.connectors {
		entries = append(entries, entry)
	}
	m.mu.RUnlock()
	stats := ManagerStats{Total: len(entries)}
	for _, entry := range entries {
		if entry.desired {
			stats.Desired++
		}
		switch entry.connector.Status() {
		case StatusDisconnected:
			stats.Disconnected++
		case StatusConnecting:
			stats.Connecting++
		case StatusBound:
			stats.Bound++
		case StatusUnbinding:
			stats.Unbinding++
		}
	}
	return stats
}

func (m *Manager) Reconcile() error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.connectors))
	for cid := range m.connectors {
		ids = append(ids, cid)
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	var errs []error
	for _, cid := range ids {
		unlock := m.operationLock(cid)
		status, err := m.Status(cid)
		if err != nil {
			errs = append(errs, err)
			unlock()
			continue
		}
		if status.Desired && status.Observed == StatusDisconnected {
			if err := m.start(cid); err != nil {
				errs = append(errs, fmt.Errorf("start %s: %w", cid, err))
			}
		} else if !status.Desired && status.Observed != StatusDisconnected {
			if err := m.stop(cid); err != nil {
				errs = append(errs, fmt.Errorf("stop %s: %w", cid, err))
			}
		}
		unlock()
	}
	return errors.Join(errs...)
}

func (m *Manager) StartAll() error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.connectors))
	for cid := range m.connectors {
		ids = append(ids, cid)
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	var errs []error
	for _, cid := range ids {
		if err := m.Start(cid); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", cid, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) StopAll() error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.connectors))
	for cid := range m.connectors {
		ids = append(ids, cid)
	}
	m.mu.RUnlock()
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var errs []error
	for _, cid := range ids {
		if err := m.Stop(cid); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", cid, err))
		}
	}
	return errors.Join(errs...)
}
