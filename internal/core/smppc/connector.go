package smppc

import (
	"sync"
)

type Status string

const (
	StatusDisconnected Status = "DISCONNECTED"
	StatusConnecting   Status = "CONNECTING"
	StatusBound        Status = "BOUND"
	StatusUnbinding    Status = "UNBINDING"
)

type Connector struct {
	cfg    Config
	status Status
	mu     sync.RWMutex
}

func NewConnector(cfg Config) *Connector {
	return &Connector{
		cfg:    cfg,
		status: StatusDisconnected,
	}
}

func (c *Connector) Config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *Connector) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Connector) setStatus(s Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = s
}

func (c *Connector) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// For Phase 2.17, just transition to CONNECTING
	if c.status != StatusDisconnected {
		return nil
	}
	c.status = StatusConnecting
	return nil
}

func (c *Connector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// For Phase 2.17, just transition to DISCONNECTED
	c.status = StatusDisconnected
	return nil
}
