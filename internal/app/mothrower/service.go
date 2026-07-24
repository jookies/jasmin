// Package mothrower composes the legacy deliverSmThrower worker: the
// deliver_sm_thrower.* AMQP subscription and routed MO HTTP delivery with the
// legacy retry lifecycle.
package mothrower

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/core/mo"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

var ErrInvalidConfig = errors.New("mothrower: invalid configuration")

// Config mirrors the legacy [deliversm-thrower] knobs plus the broker endpoint.
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

const redialDelay = 10 * time.Second

type session struct {
	deliveries <-chan amqp.Delivery
	cleanup    func()
}

// Service runs the deliverSmThrower worker: a serial consume loop with
// redial-on-failure; retry state and requeue timers survive redials.
type Service struct {
	cfg      Config
	consumer *mo.ThrowerConsumer

	connect func(ctx context.Context) (*session, error)
	pause   func(ctx context.Context, d time.Duration)

	// OnError, when set, observes per-cycle failures; the loop keeps running.
	OnError func(error)
}

// NewService wires the worker over the routed-content decoder (the trusted
// pickle bridge owned by the gateway runtime).
func NewService(config Config, decoder mo.RoutedDecoder) (*Service, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	if decoder == nil {
		return nil, fmt.Errorf("%w: nil routed decoder", ErrInvalidConfig)
	}
	timeout := time.Duration(config.HTTPTimeoutSeconds * float64(time.Second))
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	consumer, err := mo.NewThrowerConsumer(decoder, &http.Client{Timeout: timeout}, mo.ThrowerConsumerConfig{
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
				return errors.New("mothrower: delivery stream closed")
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
	subscription, err := amqpcompat.NewTopology(connection).OpenMOThrowerSubscription(ctx)
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
