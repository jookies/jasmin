package termination

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrInvalidConnectorConfig reports a termination connector configuration that
// cannot be applied.
var ErrInvalidConnectorConfig = errors.New("termination: invalid connector configuration")

// Defaults matching the legacy fake SMSC (fake_smsc.py --dlr-delay 5,
// --dlr-jitter 2.0). They are parity values, not taste: a partner's monitoring
// can flag an instantly-returned receipt as synthetic.
const (
	DefaultReceiptDelay  = 5 * time.Second
	DefaultReceiptJitter = 2 * time.Second
)

// ConnectorConfig is one termination connector as persisted by the admin plane
// and as declared in the gateway configuration file.
//
// It is a separate shape from smppc.Config on purpose. An SMPP client connector
// is defined by a bind — host, port, credentials, throughput, window, reconnect —
// and none of that exists here: this connector's "upstream" is an HTTP endpoint
// and a Redis key. Reusing smppc.Config would produce a configuration where most
// fields are silently ignored, which is the failure mode this project treats as
// worse than a missing feature.
type ConnectorConfig struct {
	// CID is the connector id. It names the AMQP submit queue this connector
	// consumes and appears in every published DLR leg, so it is immutable once
	// created.
	CID string `json:"cid"`

	// Verdict selects and tunes the source that decides the receipt.
	Verdict VerdictConfig `json:"verdict"`

	// Delivery describes the downstream application push.
	Delivery DeliveryConfig `json:"delivery"`

	// ReceiptDelay and ReceiptJitter hold the receipt back after the verdict is
	// known. Zero takes the defaults above; a negative value is an error rather
	// than a silent zero, because "no delay" is a legitimate choice that should
	// have to be typed as such.
	ReceiptDelay  time.Duration `json:"receipt_delay,omitempty"`
	ReceiptJitter time.Duration `json:"receipt_jitter,omitempty"`

	// Stitch configures the plain-split reassembly heuristic, which joins
	// messages that arrive with no concatenation header at all. The zero value
	// disables it, and that is the right setting for every connector until a
	// specific partner is known to split messages this way: it can join two
	// unrelated messages that share a sender and a destination. See
	// StitchSettings.
	Stitch StitchSettings `json:"stitch,omitempty"`

	// SynchronousReject answers a rejected submit with an SMPP error status
	// instead of accepting it and sending a REJECTD receipt later. Off by
	// default: it is a visible behaviour change for the partner, whose stack may
	// treat a submit error as retryable, so it is enabled per connector after
	// agreeing it with them.
	SynchronousReject bool `json:"synchronous_reject,omitempty"`
}

// DeliveryFormatName is the wire shape of the downstream push, as configured.
type DeliveryFormatName string

const (
	// DeliveryFormatNameJSON is the documented contract: a signed JSON body.
	DeliveryFormatNameJSON DeliveryFormatName = "json"
	// DeliveryFormatNameLegacy is the inherited form-encoded shape requiring an
	// exact "ACK/Jasmin" response body. Offered for an application already
	// written against the MO callback contract.
	DeliveryFormatNameLegacy DeliveryFormatName = "legacy"
)

// DeliveryConfig is the downstream push half of a termination connector.
type DeliveryConfig struct {
	// Endpoint is the absolute http/https URL the decoded message is POSTed to.
	// Empty disables the push: the message is spooled and the receipt is still
	// emitted, which is the pull-only deployment.
	Endpoint string `json:"endpoint,omitempty"`
	// Format defaults to json.
	Format DeliveryFormatName `json:"format,omitempty"`
	// Secret keys the HMAC signature. Empty omits the signature header entirely
	// rather than signing with an empty key.
	//
	// It is a credential: whatever persists this must not return it in a read,
	// and nothing may log it.
	Secret string `json:"secret,omitempty"`
	// Timeout bounds one attempt. Zero takes the sink's default.
	Timeout time.Duration `json:"timeout,omitempty"`
	// MaxAttempts bounds retries before the message is dead-lettered. Zero takes
	// DefaultMaxDeliveryAttempts. It is never unlimited: a message retried
	// forever is a message nobody ever looks at.
	MaxAttempts int `json:"max_attempts,omitempty"`
	// Backoff is the first retry interval, doubling per attempt to BackoffCap.
	Backoff    time.Duration `json:"backoff,omitempty"`
	BackoffCap time.Duration `json:"backoff_cap,omitempty"`
}

// Validate reports whether the configuration can be applied, filling nothing in:
// callers that want defaults use WithDefaults, so validation stays a pure
// predicate an admin API can run before persisting.
func (c ConnectorConfig) Validate() error {
	if strings.TrimSpace(c.CID) == "" {
		return fmt.Errorf("%w: empty cid", ErrInvalidConnectorConfig)
	}
	if c.ReceiptDelay < 0 || c.ReceiptJitter < 0 {
		return fmt.Errorf("%w: receipt delay and jitter cannot be negative", ErrInvalidConnectorConfig)
	}
	switch c.Verdict.Source {
	case SourceRedisWindow, SourceStatic:
	case "":
		// An empty source must not default to "accept everything": a config path
		// that loses the field would start delivering receipts for numbers with
		// no activation window.
		return fmt.Errorf("%w: verdict source is required", ErrInvalidConnectorConfig)
	default:
		return fmt.Errorf("%w: unsupported verdict source %q", ErrInvalidConnectorConfig, c.Verdict.Source)
	}
	if c.Verdict.CacheTTL < 0 || c.Verdict.LookupTimeout < 0 {
		return fmt.Errorf("%w: verdict cache ttl and lookup timeout cannot be negative", ErrInvalidConnectorConfig)
	}
	if err := c.Stitch.Validate(); err != nil {
		return err
	}
	return c.Delivery.validate()
}

func (d DeliveryConfig) validate() error {
	switch d.Format {
	case "", DeliveryFormatNameJSON, DeliveryFormatNameLegacy:
	default:
		return fmt.Errorf("%w: unsupported delivery format %q", ErrInvalidConnectorConfig, d.Format)
	}
	if d.Timeout < 0 || d.Backoff < 0 || d.BackoffCap < 0 || d.MaxAttempts < 0 {
		return fmt.Errorf("%w: delivery timings cannot be negative", ErrInvalidConnectorConfig)
	}
	if d.Endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(d.Endpoint)
	if err != nil {
		return fmt.Errorf("%w: delivery endpoint: %v", ErrInvalidConnectorConfig, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: delivery endpoint must be http or https", ErrInvalidConnectorConfig)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: delivery endpoint has no host", ErrInvalidConnectorConfig)
	}
	return nil
}

// WithDefaults returns the configuration with unset timings filled in. It does
// not default the verdict source: see Validate.
func (c ConnectorConfig) WithDefaults() ConnectorConfig {
	if c.ReceiptDelay == 0 {
		c.ReceiptDelay = DefaultReceiptDelay
	}
	if c.ReceiptJitter == 0 {
		c.ReceiptJitter = DefaultReceiptJitter
	}
	if c.Delivery.Format == "" {
		c.Delivery.Format = DeliveryFormatNameJSON
	}
	if c.Delivery.MaxAttempts == 0 {
		c.Delivery.MaxAttempts = DefaultMaxDeliveryAttempts
	}
	if c.Delivery.Backoff == 0 {
		c.Delivery.Backoff = DefaultDeliveryBackoff
	}
	if c.Delivery.BackoffCap == 0 {
		c.Delivery.BackoffCap = DefaultDeliveryBackoffCap
	}
	return c
}

// Redacted returns a copy safe to return from a read API or write to a log: the
// delivery secret is replaced with a presence marker.
//
// Returning the secret from a list endpoint is how a credential ends up in a
// browser cache, a screenshot and a support ticket. The marker is kept so an
// operator can still see whether signing is configured.
func (c ConnectorConfig) Redacted() ConnectorConfig {
	if c.Delivery.Secret != "" {
		c.Delivery.Secret = "***"
	}
	return c
}

// HasSecret reports whether a signing secret is configured, for a UI that shows
// the fact without the value.
func (c ConnectorConfig) HasSecret() bool { return c.Delivery.Secret != "" }
