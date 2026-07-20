package smppc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type Status string

const (
	StatusDisconnected Status = "DISCONNECTED"
	StatusConnecting   Status = "CONNECTING"
	StatusBound        Status = "BOUND"
	StatusUnbinding    Status = "UNBINDING"
)

type AMQPProvider interface {
	Consume(ctx context.Context, amqpURL, cid string) (<-chan *amqpcompat.Delivery, error)
}

type defaultAMQPProvider struct{}

func (p *defaultAMQPProvider) Consume(ctx context.Context, amqpURL, cid string) (<-chan *amqpcompat.Delivery, error) {
	conn, err := amqp091.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	topology := amqpcompat.NewTopology(conn)
	// Connector queues in Jasmin are non-durable, non-exclusive, non-auto-delete.
	// Exchange is "messaging".
	err = topology.DeclareQueue(ctx, amqpcompat.ConnectorSubmitQueue(cid), "messaging", amqpcompat.ConnectorSubmitRoutingKey(cid))
	if err != nil {
		conn.Close()
		return nil, err
	}

	consumer, err := amqpcompat.NewConsumer(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	deliveries, err := consumer.Consume(ctx, amqpcompat.ConnectorSubmitQueue(cid))
	if err != nil {
		conn.Close()
		return nil, err
	}

	// We need to close the connection when the consumer is done.
	// Since amqpcompat.Consumer.Consume starts a goroutine that returns a channel,
	// we should wrap it to close the connection when the channel is closed.
	out := make(chan *amqpcompat.Delivery)
	go func() {
		defer conn.Close()
		defer consumer.Close()
		defer close(out)
		for d := range deliveries {
			select {
			case out <- d:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

type Connector struct {
	cfg       Config
	status    Status
	amqpURL   string
	amqp      AMQPProvider
	readiness *ReadinessPolicy
	mu        sync.RWMutex

	session *Session
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewConnector(cfg Config, amqpURL string) (*Connector, error) {
	readiness, err := NewReadinessPolicy(DefaultReadinessConfig())
	if err != nil {
		return nil, err
	}

	return &Connector{
		cfg:       cfg.Clone(),
		status:    StatusDisconnected,
		amqpURL:   amqpURL,
		amqp:      &defaultAMQPProvider{},
		readiness: readiness,
	}, nil
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

func (c *Connector) Session() *Session {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.session
}

func (c *Connector) SetStatus(s Status) {
	c.setStatus(s)
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
			session := c.Session()
			// Connected and Bound!
			if session != nil {
				sessionCtx, cancel := context.WithCancel(ctx)
				go session.Run(sessionCtx)
				c.runConsumer(sessionCtx, session)
				cancel()
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

func (c *Connector) RunConsumer(ctx context.Context, session *Session) {
	c.runConsumer(ctx, session)
}

func (c *Connector) runConsumer(ctx context.Context, session *Session) {
	c.mu.RLock()
	amqpURL := c.amqpURL
	cid := c.cfg.CID
	readiness := c.readiness
	c.mu.RUnlock()

	deliveries, err := c.amqp.Consume(ctx, amqpURL, cid)
	if err != nil {
		fmt.Printf("ERROR: [%s] Failed to start AMQP consumer: %v\n", cid, err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				return
			}

			// SC-005: Readiness check
			createdAt, _ := d.Envelope().Properties().CreatedAt()
			expiration, _ := d.Envelope().Properties().Expiration()
			var expPtr *time.Time
			if !expiration.IsZero() {
				expPtr = &expiration
			}

			decision, err := readiness.Decide(ReadinessInput{
				Now:        time.Now().UTC(),
				CreatedAt:  createdAt,
				Expiration: expPtr,
				Connected:  true, // If we are here, we are connected
				Bound:      c.Status() == StatusBound,
			})

			if err != nil {
				// Internal error, requeue for safety
				_ = d.Reject(true)
				continue
			}

			switch decision.Action {
			case ReadinessProceed:
				if session == nil {
					fmt.Printf("ERROR: [%s] ReadinessProceed with nil session\n", cid)
					_ = d.Reject(true)
					continue
				}
				err := session.Submit(ctx, d)
				if err != nil {
					_ = d.Reject(true)
				}
			case ReadinessRequeue:
				fmt.Printf("DEBUG: [%s] Requeue message %s (delay %v)\n", cid, d.Envelope().Properties().MessageID(), decision.RequeueDelay)
				_ = d.Reject(true)
			case ReadinessDiscard:
				fmt.Printf("DEBUG: [%s] Discard expired message %s\n", cid, d.Envelope().Properties().MessageID())
				_ = d.Reject(false)
			}
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

	// Setup Session
	retry, _ := NewErrorRetryPolicy(DefaultErrorRetryRules())
	session := NewSession(conn, c.cfg, retry, c.readiness, func(err error) {
		fmt.Printf("DEBUG: [%s] Session closed: %v\n", c.cfg.CID, err)
	})

	c.mu.Lock()
	c.session = session
	c.mu.Unlock()

	c.setStatus(StatusBound)

	// Wait for connection loss or context cancellation
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
