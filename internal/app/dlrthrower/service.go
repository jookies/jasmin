// Package dlrthrower composes the legacy DLRThrower worker: the dlr_thrower.*
// AMQP subscription and the HTTP DLR callback delivery with the legacy retry
// lifecycle. SMPPS receipt delivery activates when a session sink exists.
package dlrthrower

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

var ErrInvalidConfig = errors.New("dlrthrower: invalid configuration")

// Config mirrors the legacy [dlr-thrower] knobs plus the broker endpoint.
type Config struct {
	AMQPURL string `json:"amqp_url"`
	// HTTPTimeoutSeconds is http_timeout (default 30).
	HTTPTimeoutSeconds float64 `json:"http_timeout,omitempty"`
	// RetryDelaySeconds is retry_delay (default 30).
	RetryDelaySeconds float64 `json:"retry_delay,omitempty"`
	// MaxRetries is max_retries (default 3); attempts retry while count <= MaxRetries.
	MaxRetries int `json:"max_retries,omitempty"`
}

func ValidateConfig(config Config) error {
	if config.AMQPURL == "" {
		return fmt.Errorf("%w: empty amqp_url", ErrInvalidConfig)
	}
	if config.HTTPTimeoutSeconds < 0 || config.RetryDelaySeconds < 0 || config.MaxRetries < 0 {
		return fmt.Errorf("%w: negative timeout, delay, or retry cap", ErrInvalidConfig)
	}
	return nil
}

// redialDelay paces reconnection after a broker failure, like the lookup worker.
const redialDelay = 10 * time.Second

type session struct {
	deliveries <-chan amqp.Delivery
	cleanup    func()
}

// Service runs the DLRThrower worker: a serial consume loop (the legacy
// thrower chains deliveries one at a time) with redial-on-failure. Retry
// state and requeue timers survive redials.
type Service struct {
	cfg      Config
	consumer *dlr.ThrowerConsumer

	connect func(ctx context.Context) (*session, error)
	pause   func(ctx context.Context, d time.Duration)

	// OnError, when set, observes per-cycle failures; the loop keeps running.
	OnError func(error)
}

// Option configures optional service dependencies.
type Option func(*options)

type options struct {
	smppsSink dlr.SMPPSReceiptSink
}

// WithSMPPSReceiptSink wires the smpps receipt delivery path: dlr_thrower.smpps
// forwards push a deliver_sm receipt down the sender's bound session. Without
// it, smpps forwards fail into the retry path (a deployment with no SMPPS
// access), the prior behavior.
func WithSMPPSReceiptSink(sink dlr.SMPPSReceiptSink) Option {
	return func(o *options) { o.smppsSink = sink }
}

func NewService(config Config, opts ...Option) (*Service, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	var settings options
	for _, opt := range opts {
		opt(&settings)
	}
	timeout := time.Duration(config.HTTPTimeoutSeconds * float64(time.Second))
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	consumer, err := dlr.NewThrowerConsumer(&http.Client{Timeout: timeout}, settings.smppsSink, dlr.ThrowerConsumerConfig{
		MaxRetries: config.MaxRetries,
		RetryDelay: time.Duration(config.RetryDelaySeconds * float64(time.Second)),
	})
	if err != nil {
		return nil, err
	}
	service := &Service{cfg: config, consumer: consumer}
	service.connect = service.dialSession
	service.pause = func(ctx context.Context, d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
	return service, nil
}

// Run consumes until ctx is done, redialing after failures.
func (s *Service) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.runSession(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && s.OnError != nil {
			s.OnError(err)
		}
		s.pause(ctx, redialDelay)
	}
}

func (s *Service) runSession(ctx context.Context) error {
	connected, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer connected.cleanup()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case raw, ok := <-connected.deliveries:
			if !ok {
				return errors.New("dlrthrower: delivery stream closed")
			}
			delivery, err := amqpcompat.NewDelivery(raw)
			if err != nil {
				_ = raw.Reject(false)
				if s.OnError != nil {
					s.OnError(err)
				}
				continue
			}
			if err := s.consumer.Handle(ctx, delivery); err != nil && s.OnError != nil {
				s.OnError(err)
			}
		}
	}
}

func (s *Service) dialSession(ctx context.Context) (*session, error) {
	connection, err := amqp.Dial(s.cfg.AMQPURL)
	if err != nil {
		return nil, fmt.Errorf("connect RabbitMQ: %w", err)
	}
	subscription, err := amqpcompat.NewTopology(connection).OpenDLRThrowerSubscription(ctx)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &session{
		deliveries: subscription.Deliveries,
		cleanup: func() {
			_ = subscription.Close()
			_ = connection.Close()
		},
	}, nil
}
