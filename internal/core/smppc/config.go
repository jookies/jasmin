package smppc

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/tlv"
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

	// Default submit_sm PDU params the connector applies to a front-door submit
	// when the submitter leaves them unset (legacy SMPPClientConfig defaults:
	// service_type/protocol_id/source_addr None, sm_default_msg_id 0).
	ServiceType          string `json:"service_type,omitempty"`
	ProtocolID           int    `json:"protocol_id,omitempty"`
	ReplaceIfPresentFlag int    `json:"replace_if_present_flag,omitempty"`
	SmDefaultMsgID       int    `json:"sm_default_msg_id,omitempty"`
	SourceAddr           string `json:"source_addr,omitempty"`

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
	// ReconnectBackoffMax and ReconnectBackoffJitter deliberately diverge from
	// Jasmin's fixed retry cadence. The first retry uses ConLossDelay or
	// ConFailDelay; later consecutive failures double up to this cap and apply
	// downward jitter. Defaults are 60 seconds and 20 percent.
	ReconnectBackoffMax    float64  `json:"reconnect_backoff_max,omitempty"`
	ReconnectBackoffJitter *float64 `json:"reconnect_backoff_jitter,omitempty"`

	// Transport security. Certificate verification is enabled by default.
	TLSEnabled            bool   `json:"tls_enabled"`
	TLSServerName         string `json:"tls_server_name,omitempty"`
	TLSCAFile             string `json:"tls_ca_file,omitempty"`
	TLSInsecureSkipVerify bool   `json:"tls_insecure_skip_verify,omitempty"`

	// EnquireLinkInterval is the legacy elink_interval (enquireLinkTimerSecs):
	// how often an idle bound session sends enquire_link. It is a distinct
	// timer from PDUTimeout, which legacy calls pdu_red_to (a PDU *read*
	// timer); the session used to conflate the two. Default 30, as in legacy.
	EnquireLinkInterval float64 `json:"elink_interval,omitempty"`

	// SessionInitTimeout is the legacy bind_to (sessionInitTimerSecs): how long
	// to wait for the bind response. Default 30.
	SessionInitTimeout float64 `json:"bind_to,omitempty"`

	// RequeueDelay is the legacy requeue_delay: seconds before a rejected
	// submit is retried. Default 120.
	RequeueDelay float64 `json:"requeue_delay,omitempty"`

	// DataCoding is the legacy coding: the connector's default data_coding for
	// a submit that does not carry one.
	DataCoding int `json:"data_coding,omitempty"`

	// ValidityPeriod is the legacy validity: the connector's default
	// validity_period. Empty means none, as in legacy.
	ValidityPeriod string `json:"validity_period,omitempty"`

	// Log* mirror the legacy per-connector logging directives. They are
	// provisioning state carried through admin/jCli and drive the named
	// smpp.client.<cid> lifecycle logger in the gateway runtime.
	LogFile    string `json:"log_file,omitempty"`
	LogRotate  string `json:"log_rotate,omitempty"`
	LogPrivacy bool   `json:"log_privacy,omitempty"`

	// Other
	Priority           int      `json:"priority"`
	LogLevel           string   `json:"log_level"`
	SubmitSMThroughput *float64 `json:"submit_sm_throughput,omitempty"`
	PrefetchCount      int      `json:"prefetch_count,omitempty"`
	// WindowSize bounds outstanding submit_sm PDUs independently of AMQP
	// delivery prefetch. Zero inherits PrefetchCount, preserving the previous
	// single-part concurrency while also bounding multipart chains.
	WindowSize int `json:"window_size,omitempty"`

	// DLRMsgIDBases is the legacy dlr_msg_id_bases: how a deliver_sm receipt's
	// SMSC id relates to the submit_sm_resp id base (0 same, 1 receipt decimal
	// vs resp hex, 2 receipt hex vs resp decimal) — code_dlr_msgid semantics.
	DLRMsgIDBases int `json:"dlr_msg_id_bases,omitempty"`

	// DLRExpiry is the legacy dlr_expiry: the TTL (seconds) of the submit-side
	// dlr:<msgid> callback record and the queue-msgid mapping. 0 → 86400.
	DLRExpiry int `json:"dlr_expiry,omitempty"`

	// AMQPDurableTopology declares this connector's submit queue (and the
	// messaging exchange) durable. Go-runtime knob, not a legacy field: the
	// legacy stack declares non-durable, and AMQP rejects mismatched
	// redeclares (406), so this must be uniform per broker vhost — the
	// gateway sets it from its top-level amqp_durable_topology flag.
	AMQPDurableTopology bool `json:"amqp_durable_topology,omitempty"`

	// CustomTLVs are the per-connector vendor TLV rules, the legacy smppcc
	// custom_tlvs config: declared wire type per tag, optional max encoded
	// byte length (null = unbounded), and required presence.
	CustomTLVs []CustomTLVRule `json:"custom_tlvs,omitempty"`
}

// CustomTLVRule mirrors one legacy custom_tlvs dict {tag, type, length, required}.
type CustomTLVRule struct {
	Tag      int    `json:"tag"`
	Type     string `json:"type"`
	Length   *int   `json:"length"`
	Required bool   `json:"required"`
}

// ConnectorTLVRules projects the config rules into the typed pipeline's shape.
// Tags mask to uint16 exactly where the legacy rule map does (int(tag) & 0xFFFF).
func (c Config) ConnectorTLVRules() []tlv.ConnectorRule {
	if len(c.CustomTLVs) == 0 {
		return nil
	}
	rules := make([]tlv.ConnectorRule, 0, len(c.CustomTLVs))
	for _, rule := range c.CustomTLVs {
		converted := tlv.ConnectorRule{
			Tag:      uint16(rule.Tag & 0xFFFF),
			Type:     rule.Type,
			Required: rule.Required,
		}
		if rule.Length != nil {
			length := *rule.Length
			converted.Length = &length
		}
		rules = append(rules, converted)
	}
	return rules
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
		c.Bind = BindTransceiver // Default (legacy bindOperation default).
	default:
		return fmt.Errorf("unsupported bind type: %s (want transceiver, transmitter or receiver)", c.Bind)
	}
	for name, value := range map[string]float64{
		"trx_to": c.TrxTimeout, "res_to": c.ResTimeout, "pdu_to": c.PDUTimeout,
		"bind_to":        c.SessionInitTimeout,
		"con_loss_delay": c.ConLossDelay, "con_fail_delay": c.ConFailDelay,
		"reconnect_backoff_max": c.ReconnectBackoffMax,
	} {
		nanos := value * float64(time.Second)
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || math.IsInf(nanos, 0) || nanos >= float64(math.MaxInt64) {
			return fmt.Errorf("%s must be finite, non-negative and representable", name)
		}
	}

	// Defaults for timeouts. These are the frozen SMPPClientConfig values
	// (jasmin/protocols/smpp/configs.py), verified against the oracle: a
	// connector provisioned with only host/port/credentials must behave like a
	// legacy one, including on the wire.
	if c.TrxTimeout == 0 {
		c.TrxTimeout = 300 // inactivityTimerSecs
	}
	if c.ResTimeout == 0 {
		c.ResTimeout = 120 // responseTimerSecs
	}
	if c.PDUTimeout == 0 {
		c.PDUTimeout = 10 // pduReadTimerSecs
	}
	if c.EnquireLinkInterval == 0 {
		c.EnquireLinkInterval = 30
	}
	if c.SessionInitTimeout == 0 {
		c.SessionInitTimeout = 30
	}
	if c.RequeueDelay == 0 {
		c.RequeueDelay = 120
	}
	// Address defaults are wire-visible: legacy submits carry source
	// NATIONAL/ISDN and destination INTERNATIONAL/ISDN unless the connector
	// says otherwise. Defaulting them to 0 (UNKNOWN) put different bytes on the
	// wire than the Python gateway for any connector that did not set them.
	if c.SrcTON == 0 {
		c.SrcTON = 2 // NATIONAL
	}
	if c.SrcNPI == 0 {
		c.SrcNPI = 1 // ISDN
	}
	if c.DstTON == 0 {
		c.DstTON = 1 // INTERNATIONAL
	}
	if c.DstNPI == 0 {
		c.DstNPI = 1 // ISDN
	}
	if c.ConLossDelay == 0 {
		c.ConLossDelay = 10
	}
	if c.ConFailDelay == 0 {
		c.ConFailDelay = 10
	}
	if c.ReconnectBackoffMax == 0 {
		c.ReconnectBackoffMax = 60
	}
	if c.ReconnectBackoffJitter == nil {
		value := 0.2
		c.ReconnectBackoffJitter = &value
	} else if math.IsNaN(*c.ReconnectBackoffJitter) ||
		math.IsInf(*c.ReconnectBackoffJitter, 0) ||
		*c.ReconnectBackoffJitter < 0 ||
		*c.ReconnectBackoffJitter >= 1 {
		return errors.New("reconnect_backoff_jitter must be finite and between 0 (inclusive) and 1 (exclusive)")
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
	if c.TLSInsecureSkipVerify {
		return errors.New("SMPP TLS certificate verification cannot be disabled")
	}
	if c.PrefetchCount < 0 || c.PrefetchCount > 65535 {
		return fmt.Errorf("prefetch_count must be between 1 and 65535")
	}
	if c.WindowSize < 0 || c.WindowSize > 65535 {
		return fmt.Errorf("window_size must be between 1 and 65535")
	}
	if c.DLRMsgIDBases < 0 || c.DLRMsgIDBases > 2 {
		return fmt.Errorf("dlr_msg_id_bases must be 0, 1 or 2")
	}
	if c.DLRExpiry < 0 {
		return fmt.Errorf("dlr_expiry must be non-negative")
	}
	if c.PrefetchCount == 0 {
		c.PrefetchCount = 1
	}
	if c.WindowSize == 0 {
		c.WindowSize = c.PrefetchCount
	}
	// Legacy SMPPClientConfig TON/NPI defaults: source NATIONAL/ISDN, dest
	// INTERNATIONAL/ISDN. An explicit 0 (UNKNOWN) is not distinguishable from
	// unset here (docs/plans/003); this matches the common operator case.
	if c.SrcTON == 0 {
		c.SrcTON = 2 // AddrTon.NATIONAL
	}
	if c.SrcNPI == 0 {
		c.SrcNPI = 1 // AddrNpi.ISDN
	}
	if c.DstTON == 0 {
		c.DstTON = 1 // AddrTon.INTERNATIONAL
	}
	if c.DstNPI == 0 {
		c.DstNPI = 1 // AddrNpi.ISDN
	}
	// Validate the resolved PDU-default bytes are SMPP-wire-encodable. These
	// fields carry raw SMPP WIRE values (not the 1-indexed smpp.pdu enum
	// ordinals: NATIONAL is wire 2, enum 3), and the bridge decodes each as one
	// wire byte at submit time. Rejecting an out-of-range value here fails the
	// config at load instead of poisoning every submit at encode time (and
	// avoids the silent uint8() truncation in PDUDefaults, e.g. 300 -> 44).
	if !validAddrTON(c.SrcTON) {
		return fmt.Errorf("src_ton %d is not a valid SMPP address TON (wire 0-6)", c.SrcTON)
	}
	if !validAddrNPI(c.SrcNPI) {
		return fmt.Errorf("src_npi %d is not a valid SMPP address NPI (wire 0,1,3,4,6,8,9,10,14,18)", c.SrcNPI)
	}
	if !validAddrTON(c.DstTON) {
		return fmt.Errorf("dst_ton %d is not a valid SMPP address TON (wire 0-6)", c.DstTON)
	}
	if !validAddrNPI(c.DstNPI) {
		return fmt.Errorf("dst_npi %d is not a valid SMPP address NPI (wire 0,1,3,4,6,8,9,10,14,18)", c.DstNPI)
	}
	if c.ProtocolID < 0 || c.ProtocolID > 255 {
		return fmt.Errorf("protocol_id %d is outside the 0-255 byte range", c.ProtocolID)
	}
	if c.SmDefaultMsgID < 0 || c.SmDefaultMsgID > 255 {
		return fmt.Errorf("sm_default_msg_id %d is outside the 0-255 byte range", c.SmDefaultMsgID)
	}
	if c.ReplaceIfPresentFlag < 0 || c.ReplaceIfPresentFlag > 1 {
		return fmt.Errorf("replace_if_present_flag %d must be 0 (do not replace) or 1 (replace)", c.ReplaceIfPresentFlag)
	}

	// Mirrors the legacy SMPPClientConfig custom_tlvs validation: int tag,
	// known type name, positive-or-null max length, boolean required.
	for index, rule := range c.CustomTLVs {
		switch rule.Type {
		case tlv.TypeInt1, tlv.TypeInt2, tlv.TypeInt4, tlv.TypeInt8,
			tlv.TypeOctetString, tlv.TypeCOctetString:
		default:
			return fmt.Errorf("custom_tlvs[%d]: type %q is not a valid TLV type", index, rule.Type)
		}
		if rule.Length != nil && *rule.Length <= 0 {
			return fmt.Errorf("custom_tlvs[%d]: length must be a positive max byte count or omitted", index)
		}
	}

	return nil
}

// PDUDefaults is the connector's default submit_sm PDU parameters, applied to a
// front-door submit when the submitter leaves them unset. Call after Validate so
// the TON/NPI defaults are resolved.
// validAddrTON reports whether v is a valid SMPP address TON wire byte (0-6) —
// the range the bridge's AddrTonEncoder can encode.
func validAddrTON(v int) bool { return v >= 0 && v <= 6 }

// validAddrNPI reports whether v is a valid SMPP address NPI wire byte — the
// non-contiguous set the bridge's AddrNpiEncoder can encode.
func validAddrNPI(v int) bool {
	switch v {
	case 0, 1, 3, 4, 6, 8, 9, 10, 14, 18:
		return true
	default:
		return false
	}
}

type PDUDefaults struct {
	SourceAddrTON        uint8
	SourceAddrNPI        uint8
	DestAddrTON          uint8
	DestAddrNPI          uint8
	ServiceType          string
	ProtocolID           uint8
	ReplaceIfPresentFlag uint8
	SmDefaultMsgID       uint8
	SourceAddr           string
}

// PDUDefaults resolves the connector's default submit_sm PDU parameters.
func (c Config) PDUDefaults() PDUDefaults {
	return PDUDefaults{
		SourceAddrTON:        uint8(c.SrcTON),
		SourceAddrNPI:        uint8(c.SrcNPI),
		DestAddrTON:          uint8(c.DstTON),
		DestAddrNPI:          uint8(c.DstNPI),
		ServiceType:          c.ServiceType,
		ProtocolID:           uint8(c.ProtocolID),
		ReplaceIfPresentFlag: uint8(c.ReplaceIfPresentFlag),
		SmDefaultMsgID:       uint8(c.SmDefaultMsgID),
		SourceAddr:           c.SourceAddr,
	}
}

// CanSubmit reports whether this bind role may send submit_sm (transmitter or
// transceiver). A receiver bind cannot submit — the SMSC rejects it — so such a
// connector is excluded from MT routing and never consumes the submit queue.
func (c Config) CanSubmit() bool {
	return c.Bind == BindTransceiver || c.Bind == BindTransmitter
}

// CanReceive reports whether this bind role receives deliver_sm (MO/DLR):
// receiver or transceiver. A transmitter bind gets no deliver_sm.
func (c Config) CanReceive() bool {
	return c.Bind == BindTransceiver || c.Bind == BindReceiver
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

func (c Config) EffectiveReconnectBackoffMax() float64 {
	if c.ReconnectBackoffMax <= 0 {
		return 60
	}
	return c.ReconnectBackoffMax
}

func (c Config) EffectiveReconnectBackoffJitter() float64 {
	if c.ReconnectBackoffJitter == nil {
		return 0.2
	}
	return *c.ReconnectBackoffJitter
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
	if c.ReconnectBackoffJitter != nil {
		jitter := *c.ReconnectBackoffJitter
		clone.ReconnectBackoffJitter = &jitter
	}
	if c.CustomTLVs != nil {
		clone.CustomTLVs = make([]CustomTLVRule, len(c.CustomTLVs))
		for index, rule := range c.CustomTLVs {
			cloned := rule
			if rule.Length != nil {
				length := *rule.Length
				cloned.Length = &length
			}
			clone.CustomTLVs[index] = cloned
		}
	}
	return clone
}
