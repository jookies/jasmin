// Package dlrlookup composes the legacy DLRLookup worker: the dlr.* AMQP
// subscription, the correlation engine over the compat Redis schema, and the
// legacy thrower-envelope publication.
package dlrlookup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	redis "github.com/redis/go-redis/v9"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

var ErrInvalidConfig = errors.New("dlrlookup: invalid configuration")

// Config mirrors the legacy [dlr] section knobs plus the transport endpoints.
type Config struct {
	AMQPURL  string `json:"amqp_url"`
	RedisURL string `json:"redis_url"`
	// PID names the worker; the legacy default is "main" and forms the
	// DLRLookup-<pid> queue and consumer tag.
	PID string `json:"pid,omitempty"`
	// MaxRetries is dlr_lookup_max_retries (default 2, counted as total attempts).
	MaxRetries int `json:"dlr_lookup_max_retries,omitempty"`
	// RetryDelaySeconds is dlr_lookup_retry_delay (default 10).
	RetryDelaySeconds float64 `json:"dlr_lookup_retry_delay,omitempty"`
	// SMPPReceiptOnSuccessSubmitSmResp mirrors smpp_receipt_on_success_submit_sm_resp.
	SMPPReceiptOnSuccessSubmitSmResp bool `json:"smpp_receipt_on_success_submit_sm_resp,omitempty"`
	// AMQPDurableTopology declares the lookup queue/exchange durable; must be
	// uniform per vhost (mismatched redeclare is an AMQP 406). Propagated from
	// the gateway's top-level flag when running in-process.
	AMQPDurableTopology bool `json:"amqp_durable_topology,omitempty"`
}

func (c *Config) applyDefaults() {
	if c.PID == "" {
		c.PID = "main"
	}
}

func ValidateConfig(config Config) error {
	if config.AMQPURL == "" {
		return fmt.Errorf("%w: empty amqp_url", ErrInvalidConfig)
	}
	if config.RedisURL == "" {
		return fmt.Errorf("%w: empty redis_url", ErrInvalidConfig)
	}
	if config.MaxRetries < 0 {
		return fmt.Errorf("%w: negative dlr_lookup_max_retries", ErrInvalidConfig)
	}
	if config.RetryDelaySeconds < 0 {
		return fmt.Errorf("%w: negative dlr_lookup_retry_delay", ErrInvalidConfig)
	}
	if _, err := redis.ParseURL(config.RedisURL); err != nil {
		return fmt.Errorf("%w: redis_url: %v", ErrInvalidConfig, err)
	}
	return nil
}

// redialDelay paces reconnection after a broker failure, the legacy AMQP
// client's reconnectOnConnectionLoss delay.
const redialDelay = 10 * time.Second

// switchablePublisher lets the long-lived correlator publish through the
// current AMQP connection: each redial swaps the inner publisher while the
// consumer's retrial state survives, like the legacy singleton whose broker
// channel reconnects underneath it.
type switchablePublisher struct {
	mu    sync.Mutex
	inner dlr.AMQPPublisher
}

func (p *switchablePublisher) swap(inner dlr.AMQPPublisher) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inner = inner
}

func (p *switchablePublisher) Publish(ctx context.Context, exchange, routingKey string, message amqpcompat.Envelope) error {
	p.mu.Lock()
	inner := p.inner
	p.mu.Unlock()
	if inner == nil {
		return errors.New("dlrlookup: no AMQP connection")
	}
	return inner.Publish(ctx, exchange, routingKey, message)
}

// session is one connected AMQP generation: the subscription stream plus the
// publisher bound to the same connection, and a cleanup for both.
type session struct {
	deliveries <-chan amqp.Delivery
	publisher  dlr.AMQPPublisher
	cleanup    func()
}

// Service runs the DLRLookup worker: a serial consume loop (the legacy
// dispatcher chains deliveries one at a time) with redial-on-failure.
type Service struct {
	cfg      Config
	redis    *redis.Client
	consumer *dlr.LookupConsumer
	switcher *switchablePublisher

	// connect is the AMQP session opener, injectable for tests.
	connect func(ctx context.Context) (*session, error)
	// pause waits between redials, injectable for tests.
	pause func(ctx context.Context, d time.Duration)

	// OnError, when set, observes per-cycle failures (connection loss,
	// handler errors); the loop keeps running regardless.
	OnError func(error)
}

type serviceOptions struct {
	cdrRecorder cdr.FinalDLRRecorder
	now         func() time.Time
	logger      *slog.Logger
}

type Option func(*serviceOptions)

// WithLogger names the logger the correlator records receipt overrides on. A
// DLR registry gate is the one thing that makes the receipt a partner is sent
// differ from the status the CDR records, so it is logged wherever it happens.
func WithLogger(logger *slog.Logger) Option {
	return func(options *serviceOptions) { options.logger = logger }
}

// WithFinalDLRRecorder wires the durable commercial receipt projection into
// the in-process DLR worker.
func WithFinalDLRRecorder(recorder cdr.FinalDLRRecorder, now func() time.Time) Option {
	return func(options *serviceOptions) {
		options.cdrRecorder = recorder
		options.now = now
	}
}

func NewService(config Config, optionFunctions ...Option) (*Service, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	config.applyDefaults()
	options, err := redis.ParseURL(config.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("%w: redis_url: %v", ErrInvalidConfig, err)
	}
	client := redis.NewClient(options)
	var serviceSettings serviceOptions
	for _, option := range optionFunctions {
		if option != nil {
			option(&serviceSettings)
		}
	}

	switcher := &switchablePublisher{}
	forwardPublisher, err := dlr.NewForwardPublisher(switcher)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	correlatorOptions := make([]dlr.CorrelatorOption, 0, 2)
	if serviceSettings.logger != nil {
		correlatorOptions = append(correlatorOptions, dlr.WithLogger(serviceSettings.logger))
	}
	if serviceSettings.cdrRecorder != nil {
		correlatorOptions = append(correlatorOptions,
			dlr.WithFinalDLRRecorder(serviceSettings.cdrRecorder, serviceSettings.now))
	}
	correlator := dlr.NewCorrelator(
		rediscompat.NewClient(client), forwardPublisher,
		dlr.Config{SMPPReceiptOnSuccessSubmitSmResp: config.SMPPReceiptOnSuccessSubmitSmResp},
		correlatorOptions...,
	)
	consumer, err := dlr.NewLookupConsumer(correlator, dlr.LookupConsumerConfig{
		MaxRetries: config.MaxRetries,
		RetryDelay: time.Duration(config.RetryDelaySeconds * float64(time.Second)),
	})
	if err != nil {
		_ = client.Close()
		return nil, err
	}

	service := &Service{cfg: config, redis: client, consumer: consumer, switcher: switcher}
	service.connect = service.dialSession
	service.pause = func(ctx context.Context, d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
	return service, nil
}

// Run consumes until ctx is done, redialing after failures. The per-msgid
// retrial state survives redials, like the legacy singleton.
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
	s.switcher.swap(connected.publisher)
	defer s.switcher.swap(nil)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case raw, ok := <-connected.deliveries:
			if !ok {
				return errors.New("dlrlookup: delivery stream closed")
			}
			delivery, err := amqpcompat.NewDelivery(raw)
			if err != nil {
				// The dlr.* binding can carry keys outside the compatibility
				// scope; the legacy dispatcher rejects unknown keys.
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
	subscription, err := amqpcompat.NewTopology(connection, s.cfg.AMQPDurableTopology).OpenDLRLookupSubscription(ctx, s.cfg.PID)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	publisher, err := amqpcompat.NewPublisher(connection)
	if err != nil {
		_ = subscription.Close()
		_ = connection.Close()
		return nil, err
	}
	return &session{
		deliveries: subscription.Deliveries,
		publisher:  publisher,
		cleanup: func() {
			_ = publisher.Close()
			_ = subscription.Close()
			_ = connection.Close()
		},
	}, nil
}

// Close releases the Redis client. Stop Run via its context first.
func (s *Service) Close() error {
	if s == nil || s.redis == nil {
		return nil
	}
	return s.redis.Close()
}
