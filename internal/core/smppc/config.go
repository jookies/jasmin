package smppc

import (
	"errors"
	"fmt"
	"math"
	"time"
)

type BindType string

const (
	BindTransceiver BindType = "transceiver"
	BindTransmitter BindType = "transmitter"
	BindReceiver    BindType = "receiver"
)

type Config struct {
	CID        string   `json:"cid"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	SystemID   string   `json:"system_id"`
	Password   string   `json:"password"`
	SystemType string   `json:"system_type"`
	Bind       BindType `json:"bind"`

	// Address settings
	AddrTON      int    `json:"addr_ton"`
	AddrNPI      int    `json:"addr_npi"`
	AddressRange string `json:"address_range"`

	// Default TON/NPI for source and destination
	SrcTON int `json:"src_ton"`
	SrcNPI int `json:"src_npi"`
	DstTON int `json:"dst_ton"`
	DstNPI int `json:"dst_npi"`

	// Timeouts (seconds)
	TrxTimeout float64 `json:"trx_to"`
	ResTimeout float64 `json:"res_to"`
	PDUTimeout float64 `json:"pdu_to"`

	// Reconnection settings. Pointers preserve legacy default=true while still
	// allowing an explicit false value in JSON and programmatic configuration.
	ConLossRetry *bool   `json:"con_loss_retry,omitempty"`
	ConFailRetry *bool   `json:"con_fail_retry,omitempty"`
	ConLossDelay float64 `json:"con_loss_delay"`
	ConFailDelay float64 `json:"con_fail_delay"`

	// Transport security. Certificate verification is enabled by default.
	TLSEnabled            bool   `json:"tls_enabled"`
	TLSServerName         string `json:"tls_server_name,omitempty"`
	TLSCAFile             string `json:"tls_ca_file,omitempty"`
	TLSInsecureSkipVerify bool   `json:"tls_insecure_skip_verify,omitempty"`

	// Other
	Priority           int      `json:"priority"`
	LogLevel           string   `json:"log_level"`
	SubmitSMThroughput *float64 `json:"submit_sm_throughput,omitempty"`
	PrefetchCount      int      `json:"prefetch_count,omitempty"`
}

func (c *Config) Validate() error {
	if c.CID == "" {
		return errors.New("missing cid")
	}
	if c.Host == "" {
		return errors.New("missing host")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	if c.SystemID == "" {
		return errors.New("missing system_id")
	}
	switch c.Bind {
	case BindTransceiver:
	case "":
		c.Bind = BindTransceiver // Default
	default:
		return fmt.Errorf("unsupported bind type: %s (only transceiver is implemented)", c.Bind)
	}
	for name, value := range map[string]float64{
		"trx_to": c.TrxTimeout, "res_to": c.ResTimeout, "pdu_to": c.PDUTimeout,
		"con_loss_delay": c.ConLossDelay, "con_fail_delay": c.ConFailDelay,
	} {
		nanos := value * float64(time.Second)
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || math.IsInf(nanos, 0) || nanos >= float64(math.MaxInt64) {
			return fmt.Errorf("%s must be finite, non-negative and representable", name)
		}
	}

	// Defaults for timeouts
	if c.TrxTimeout == 0 {
		c.TrxTimeout = 30
	}
	if c.ResTimeout == 0 {
		c.ResTimeout = 60
	}
	if c.PDUTimeout == 0 {
		c.PDUTimeout = 30
	}
	if c.ConLossDelay == 0 {
		c.ConLossDelay = 10
	}
	if c.ConFailDelay == 0 {
		c.ConFailDelay = 10
	}
	if c.ConLossRetry == nil {
		value := true
		c.ConLossRetry = &value
	}
	if c.ConFailRetry == nil {
		value := true
		c.ConFailRetry = &value
	}
	if c.SubmitSMThroughput != nil {
		if err := validateThroughput(*c.SubmitSMThroughput); err != nil {
			return err
		}
	}
	if !c.TLSEnabled && (c.TLSServerName != "" || c.TLSCAFile != "" || c.TLSInsecureSkipVerify) {
		return errors.New("TLS options require tls_enabled")
	}
	if c.PrefetchCount < 0 || c.PrefetchCount > 65535 {
		return fmt.Errorf("prefetch_count must be between 1 and 65535")
	}
	if c.PrefetchCount == 0 {
		c.PrefetchCount = 1
	}

	return nil
}

func (c Config) EffectiveSubmitSMThroughput() float64 {
	if c.SubmitSMThroughput == nil {
		return DefaultSubmitSMThroughput
	}
	return *c.SubmitSMThroughput
}

func (c Config) ConnectionFailureRetryEnabled() bool {
	return c.ConFailRetry == nil || *c.ConFailRetry
}

func (c Config) ConnectionLossRetryEnabled() bool {
	return c.ConLossRetry == nil || *c.ConLossRetry
}

// Clone returns a config with no shared mutable pointer fields.
func (c Config) Clone() Config {
	clone := c
	if c.ConFailRetry != nil {
		value := *c.ConFailRetry
		clone.ConFailRetry = &value
	}
	if c.ConLossRetry != nil {
		value := *c.ConLossRetry
		clone.ConLossRetry = &value
	}
	if c.SubmitSMThroughput != nil {
		throughput := *c.SubmitSMThroughput
		clone.SubmitSMThroughput = &throughput
	}
	return clone
}
