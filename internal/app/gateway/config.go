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
	"github.com/pumpitspace/jasmin/internal/app/outbound"
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
