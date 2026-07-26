package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/dlrlookup"
	"github.com/pumpitspace/jasmin/internal/app/dlrthrower"
	"github.com/pumpitspace/jasmin/internal/app/mothrower"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

var ErrInvalidConfig = errors.New("invalid gateway configuration")

const RoleHTTPAndSMPPc = "http+smppc"

type Config struct {
	Role                 string          `json:"role"`
	Outbound             outbound.Config `json:"outbound"`
	Connectors           []smppc.Config  `json:"connectors"`
	RequiredConnectorIDs []string        `json:"required_connector_ids"`
	BindTimeoutSeconds   float64         `json:"bind_timeout_seconds"`
	// DLRLookup, when present, runs the legacy DLRLookup worker in-process.
	// An empty amqp_url inherits the outbound broker.
	DLRLookup *dlrlookup.Config `json:"dlr_lookup,omitempty"`
	// DLRThrower, when present, runs the legacy DLRThrower worker in-process.
	// An empty amqp_url inherits the outbound broker.
	DLRThrower *dlrthrower.Config `json:"dlr_thrower,omitempty"`
	// MOThrower, when present, runs the legacy deliverSmThrower worker
	// in-process. An empty amqp_url inherits the outbound broker.
	MOThrower *mothrower.Config `json:"deliver_sm_thrower,omitempty"`
	// SMPPS, when present, runs the SMPPS server in-process: it binds ESMEs
	// and ingests their submit_sm into the shared MT pipeline.
	SMPPS *smppsserver.Config `json:"smpps,omitempty"`
	// SubmitAuditLog configures the jasmin-sm-listener SMS-MT audit line emitted
	// on each final submit_sm_resp. An empty level defaults to INFO; ApplyJasmin
	// overlays both fields from the .cfg [sm-listener] section.
	SubmitAuditLog SubmitAuditLogConfig `json:"submit_audit_log,omitempty"`
}

// SubmitAuditLogConfig is the jasmin-sm-listener audit-line logging config: the
// line renders at Level honouring Privacy (log_privacy). File/Rotate select the
// legacy log_file sink with TimedRotatingFileHandler rotation (midnight, W0..W6);
// an empty File keeps the stderr default.
type SubmitAuditLogConfig struct {
	Level   string `json:"level,omitempty"`
	Privacy bool   `json:"privacy,omitempty"`
	File    string `json:"file,omitempty"`
	Rotate  string `json:"rotate,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	if path == "" {
		return Config{}, fmt.Errorf("%w: empty config path", ErrInvalidConfig)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("%w: trailing JSON content", ErrInvalidConfig)
		}
		return Config{}, fmt.Errorf("decode trailing config content: %w", err)
	}
	if err := ValidateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func ValidateConfig(config Config) error {
	if config.Role != RoleHTTPAndSMPPc {
		return fmt.Errorf("%w: role must be %q", ErrInvalidConfig, RoleHTTPAndSMPPc)
	}
	if err := outbound.ValidateConfig(config.Outbound); err != nil {
		return fmt.Errorf("%w: outbound: %v", ErrInvalidConfig, err)
	}
	if config.DLRLookup != nil {
		lookup := *config.DLRLookup
		if lookup.AMQPURL == "" {
			lookup.AMQPURL = config.Outbound.AMQPURL
		}
		if err := dlrlookup.ValidateConfig(lookup); err != nil {
			return fmt.Errorf("%w: dlr_lookup: %v", ErrInvalidConfig, err)
		}
	}
	if config.DLRThrower != nil {
		thrower := *config.DLRThrower
		if thrower.AMQPURL == "" {
			thrower.AMQPURL = config.Outbound.AMQPURL
		}
		if err := dlrthrower.ValidateConfig(thrower); err != nil {
			return fmt.Errorf("%w: dlr_thrower: %v", ErrInvalidConfig, err)
		}
	}
	if config.MOThrower != nil {
		thrower := *config.MOThrower
		if thrower.AMQPURL == "" {
			thrower.AMQPURL = config.Outbound.AMQPURL
		}
		if err := mothrower.ValidateConfig(thrower); err != nil {
			return fmt.Errorf("%w: deliver_sm_thrower: %v", ErrInvalidConfig, err)
		}
	}
	if config.SMPPS != nil {
		if err := smppsserver.ValidateConfig(*config.SMPPS); err != nil {
			return fmt.Errorf("%w: smpps: %v", ErrInvalidConfig, err)
		}
	}
	if len(config.Connectors) == 0 {
		return fmt.Errorf("%w: at least one SMPPc connector is required", ErrInvalidConfig)
	}
	if math.IsNaN(config.BindTimeoutSeconds) || math.IsInf(config.BindTimeoutSeconds, 0) || config.BindTimeoutSeconds < 0 ||
		config.BindTimeoutSeconds*float64(time.Second) >= float64(math.MaxInt64) {
		return fmt.Errorf("%w: bind timeout must be finite, non-negative and representable", ErrInvalidConfig)
	}
	configured := make(map[string]struct{}, len(config.Connectors))
	for index, connector := range config.Connectors {
		if err := connector.Validate(); err != nil {
			return fmt.Errorf("%w: connector %d: %v", ErrInvalidConfig, index, err)
		}
		if _, exists := configured[connector.CID]; exists {
			return fmt.Errorf("%w: duplicate connector %q", ErrInvalidConfig, connector.CID)
		}
		configured[connector.CID] = struct{}{}
	}
	for _, route := range config.Outbound.Routes {
		for _, connectorID := range route.ConnectorCandidates() {
			if _, exists := configured[connectorID]; !exists {
				return fmt.Errorf("%w: route references missing connector %q", ErrInvalidConfig, connectorID)
			}
		}
	}
	required := config.RequiredConnectorIDs
	if len(required) == 0 {
		required = make([]string, 0, len(configured))
		for cid := range configured {
			required = append(required, cid)
		}
	}
	seen := make(map[string]struct{}, len(required))
	for _, cid := range required {
		if _, exists := configured[cid]; !exists {
			return fmt.Errorf("%w: required connector %q is not configured", ErrInvalidConfig, cid)
		}
		if _, duplicate := seen[cid]; duplicate {
			return fmt.Errorf("%w: duplicate required connector %q", ErrInvalidConfig, cid)
		}
		seen[cid] = struct{}{}
	}
	return nil
}

func (config Config) BindTimeout() time.Duration {
	if config.BindTimeoutSeconds == 0 {
		return 30 * time.Second
	}
	return time.Duration(config.BindTimeoutSeconds * float64(time.Second))
}

func (config Config) RequiredConnectors() []string {
	if len(config.RequiredConnectorIDs) > 0 {
		return append([]string(nil), config.RequiredConnectorIDs...)
	}
	ids := make([]string, 0, len(config.Connectors))
	for _, connector := range config.Connectors {
		ids = append(ids, connector.CID)
	}
	return ids
}
