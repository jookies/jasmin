package smppc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
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

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewConnector(cfg Config) *Connector {
	return &Connector{
		cfg:    cfg.Clone(),
		status: StatusDisconnected,
	}
}

func (c *Connector) Config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Clone()
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

	if c.status != StatusDisconnected {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.status = StatusConnecting

	c.wg.Add(1)
	go c.loop(ctx)

	return nil
}

func (c *Connector) Stop() error {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()

	c.wg.Wait()
	c.setStatus(StatusDisconnected)
	return nil
}

func (c *Connector) loop(ctx context.Context) {
	defer c.wg.Done()

	for {
		err := c.connectAndBind(ctx)
		if err == nil {
			// Connected and Bound!
			// In this phase, we don't have a receiver loop yet, so we just stay bound
			// until connection is lost or ctx is cancelled.
			// Actually, we should wait for connection loss.
			// For now, let's just wait on ctx.Done.
			select {
			case <-ctx.Done():
				return
			}
		}

		// Reconnect logic
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(c.cfg.ConFailDelay * float64(time.Second))):
			c.setStatus(StatusConnecting)
		}
	}
}

func (c *Connector) connectAndBind(ctx context.Context) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port))
	if err != nil {
		return err
	}
	defer conn.Close()

	// Prepare BIND PDU
	pdu := smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandBindTransceiver,
			SequenceNumber: 1, // Fixed for now
		},
		Bind: &smppwire.BindBody{
			SystemID: []byte(c.cfg.SystemID),
			Password: []byte(c.cfg.Password),
		},
	}

	wire, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}

	if _, err := conn.Write(wire); err != nil {
		return err
	}

	c.setStatus(StatusBound)

	// Detect connection loss
	errChan := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := conn.Read(buf)
		if err == nil {
			// This shouldn't happen if we are just waiting,
			// unless server sends something unexpected.
			// For now, treat any data as "keep-alive" or ignore.
			// But EOF or error means connection lost.
		}
		errChan <- err
	}()

	select {
	case <-ctx.Done():
		c.setStatus(StatusDisconnected)
		return ctx.Err()
	case err := <-errChan:
		c.setStatus(StatusDisconnected)
		return err
	}
}
