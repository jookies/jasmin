package smppc

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrNotFound      = errors.New("connector not found")
	ErrAlreadyExists = errors.New("connector already exists")
)

type Manager struct {
	amqpURL    string
	connectors map[string]*Connector
	mu         sync.RWMutex
}

func NewManager(amqpURL string) *Manager {
	return &Manager{
		amqpURL:    amqpURL,
		connectors: make(map[string]*Connector),
	}
}

func (m *Manager) Add(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.connectors[cfg.CID]; ok {
		return ErrAlreadyExists
	}

	c, err := NewConnector(cfg, m.amqpURL)
	if err != nil {
		return err
	}
	m.connectors[cfg.CID] = c
	return nil
}

func (m *Manager) Remove(cid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.connectors[cid]; if !ok {
		return ErrNotFound
	}

	if c.Status() != StatusDisconnected {
		return fmt.Errorf("connector must be disconnected before removal")
	}

	delete(m.connectors, cid)
	return nil
}

func (m *Manager) Get(cid string) (*Connector, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.connectors[cid]; if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (m *Manager) List() []Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]Config, 0, len(m.connectors))
	for _, c := range m.connectors {
		list = append(list, c.Config())
	}
	return list
}
