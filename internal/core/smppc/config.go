package smppc

import (
	"errors"
	"fmt"
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

	// Reconnection settings
	ConLossRetry  bool    `json:"con_loss_retry"`
	ConLossDelay  float64 `json:"con_loss_delay"`
	ConFailDelay  float64 `json:"con_fail_delay"`
	ReconnectLoss bool    `json:"reconnect_on_connection_loss"`

	// Other
	Priority           int      `json:"priority"`
	LogLevel           string   `json:"log_level"`
	SubmitSMThroughput *float64 `json:"submit_sm_throughput,omitempty"`
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
	case BindTransceiver, BindTransmitter, BindReceiver:
	case "":
		c.Bind = BindTransceiver // Default
	default:
		return fmt.Errorf("invalid bind type: %s", c.Bind)
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
	if c.SubmitSMThroughput != nil {
		if err := validateThroughput(*c.SubmitSMThroughput); err != nil {
			return err
		}
	}

	return nil
}

func (c Config) EffectiveSubmitSMThroughput() float64 {
	if c.SubmitSMThroughput == nil {
		return DefaultSubmitSMThroughput
	}
	return *c.SubmitSMThroughput
}

// Clone returns a config with no shared mutable pointer fields.
func (c Config) Clone() Config {
	clone := c
	if c.SubmitSMThroughput != nil {
		throughput := *c.SubmitSMThroughput
		clone.SubmitSMThroughput = &throughput
	}
	return clone
}
