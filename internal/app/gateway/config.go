package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/dlrlookup"
	"github.com/pumpitspace/jasmin/internal/app/dlrthrower"
	"github.com/pumpitspace/jasmin/internal/app/modispatch"
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
	// PickleCodec selects the AMQP pickle encode/decode engine: "native"/"" (the
	// Go codec, no Python subprocess — the default) or "bridge" (the legacy
	// scripts/pickle_bridge.py subprocess, kept as an opt-in fallback).
	PickleCodec string `json:"pickle_codec,omitempty"`
	// AMQPDurableTopology declares every exchange/queue this process creates
	// durable (all publishes are already persistent), so queued submits survive
	// a broker restart. One switch for the whole process: durability must be
	// uniform per vhost — AMQP answers a redeclare with different durability
	// with PRECONDITION_FAILED (406). Leave false (the legacy default) when
	// sharing a vhost with the Python stack, e.g. the bridge-edge shadow.
	AMQPDurableTopology bool `json:"amqp_durable_topology,omitempty"`
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
	// SMPPServerLog configures the smpp.server.<id> logger (bind/unbind lines).
	// ApplyJasmin overlays it from the .cfg [smpp-server] log_* directives.
	SMPPServerLog ComponentLogConfig `json:"smpp_server_log,omitempty"`
	// The remaining named component loggers preserve the legacy file/level/
	// rotation split. They are top-level because several workers share the
	// outbound runtime and cannot safely open the same rotating file twice.
	RouterLog           ComponentLogConfig `json:"router_log,omitempty"`
	HTTPAPILog          ComponentLogConfig `json:"http_api_log,omitempty"`
	HTTPAccessLog       ComponentLogConfig `json:"http_access_log,omitempty"`
	DLRLog              ComponentLogConfig `json:"dlr_log,omitempty"`
	AMQPLog             ComponentLogConfig `json:"amqp_log,omitempty"`
	DLRThrowerLog       ComponentLogConfig `json:"dlr_thrower_log,omitempty"`
	DeliverSMThrowerLog ComponentLogConfig `json:"deliver_sm_thrower_log,omitempty"`
	// HTTPS, when present, serves the HTTP API over TLS instead of plaintext.
	HTTPS *HTTPSConfig `json:"https,omitempty"`
	// Admin, when present, runs the authenticated runtime provisioning API
	// (SQLite-backed connector CRUD, live-applied) mounted at /admin.
	Admin *AdminConfig `json:"admin,omitempty"`
	// HA, when present, fences the whole gateway active-passive through a
	// PostgreSQL session advisory lock. It prevents two processes from spending
	// the same in-memory quotas; it does not replicate the node-local admin DB.
	HA *HAConfig `json:"ha,omitempty"`
	// MORoutes, when present, runs the MO router dispatch in-process: MOs the
	// connectors ingest (deliver.sm.*) route to HTTP/SMPPS destinations via
	// deliver_sm_thrower.* (RouterPB.deliver_sm_callback semantics — default
	// route + connector-filtered static routes; content filters are plan 008
	// Step 6). Enable the deliver_sm_thrower worker to actually throw them.
	MORoutes []modispatch.RouteConfig `json:"mo_routes,omitempty"`
}

// HAConfig identifies one active-passive deployment. Gateways sharing the
// outbound PostgreSQL database and namespace contend for the same leader lock.
type HAConfig struct {
	Namespace string `json:"namespace"`
}

// AdminConfig configures the runtime provisioning plane. DBPath is the SQLite
// file (admin-created connectors persist here and survive restart). Token
// authenticates every admin request as a bearer token; it accepts the same
// env:/file:/literal: secret references as the other credentials and must be
// non-empty (the admin API is never unauthenticated).
type AdminConfig struct {
	DBPath string `json:"db_path"`
	Token  string `json:"token"`
	// WebListenAddress, when set, serves the browser management UI on its own
	// listener (separate from the public sendsms port). WebUsername/WebPassword
	// are the single admin login for that UI; WebPassword accepts the same
	// env:/file:/literal: secret references as the other credentials. All three
	// are required together — an empty WebListenAddress disables the UI entirely.
	WebListenAddress string `json:"web_listen_address,omitempty"`
	WebUsername      string `json:"web_username,omitempty"`
	WebPassword      string `json:"web_password,omitempty"`
	// AllowInterceptorEditing exposes interceptor CRUD through the admin plane.
	// Interceptor scripts are arbitrary Python executed on the gateway host, so
	// enabling this makes any admin session equivalent to shell access on this
	// machine. It defaults to false, and turning it on also starts the script
	// runner subprocess (a table added at runtime needs something to run it).
	AllowInterceptorEditing bool `json:"allow_interceptor_editing,omitempty"`
	// JCliListenAddress, when set, serves the jCli management console (a telnet
	// line protocol) on its own listener. JCliUsername/JCliPassword are its
	// login; the password accepts the same env:/file:/literal: secret refs as
	// the other credentials. Like the web UI this is a privilege boundary —
	// bind it internally. Empty disables the console entirely.
	JCliListenAddress string `json:"jcli_listen_address,omitempty"`
	JCliUsername      string `json:"jcli_username,omitempty"`
	JCliPassword      string `json:"jcli_password,omitempty"`
	// JCliIdleTimeoutSeconds closes an idle console session (0 disables).
	JCliIdleTimeoutSeconds float64 `json:"jcli_idle_timeout,omitempty"`
	// PBFacadeListenAddress, when set, serves the authenticated normalized JSON
	// seam used by a trusted Twisted PB compatibility process. It is a private
	// listener and is never mounted on the public sendsms API.
	PBFacadeListenAddress string `json:"pb_facade_listen_address,omitempty"`
	PBFacadeToken         string `json:"pb_facade_token,omitempty"`
}

// HTTPSConfig terminates inbound TLS on the HTTP listener. File paths are
// checked at boot (ListenAndServeTLS), not at --check-config, so a config can
// be validated on machines without the certificates.
type HTTPSConfig struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// ComponentLogConfig is a component logger's level + rotating file sink, resolved
// from a section's log_level/log_file/log_rotate. Empty File keeps the stderr
// default; empty Level defaults to INFO.
type ComponentLogConfig struct {
	Level  string `json:"level,omitempty"`
	File   string `json:"file,omitempty"`
	Rotate string `json:"rotate,omitempty"`
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
	if err := resolveSecretRefs(&config); err != nil {
		return Config{}, err
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
	switch config.PickleCodec {
	case "", "bridge", "native":
	default:
		return fmt.Errorf("%w: pickle_codec must be \"native\" or \"bridge\"", ErrInvalidConfig)
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
	if config.HTTPS != nil && (config.HTTPS.CertFile == "" || config.HTTPS.KeyFile == "") {
		return fmt.Errorf("%w: https requires both cert_file and key_file", ErrInvalidConfig)
	}
	if config.HA != nil && config.HA.Namespace == "" {
		return fmt.Errorf("%w: ha requires a non-empty namespace", ErrInvalidConfig)
	}
	if config.Admin != nil {
		if config.Admin.DBPath == "" {
			return fmt.Errorf("%w: admin requires db_path", ErrInvalidConfig)
		}
		if config.Admin.Token == "" {
			return fmt.Errorf("%w: admin requires a non-empty token", ErrInvalidConfig)
		}
		if config.Admin.WebListenAddress != "" {
			if _, _, err := net.SplitHostPort(config.Admin.WebListenAddress); err != nil {
				return fmt.Errorf("%w: admin.web_listen_address %q is not host:port: %v", ErrInvalidConfig, config.Admin.WebListenAddress, err)
			}
			if config.Admin.WebUsername == "" || config.Admin.WebPassword == "" {
				return fmt.Errorf("%w: admin.web_listen_address requires web_username and web_password", ErrInvalidConfig)
			}
		}
		if config.Admin.JCliListenAddress != "" {
			if _, _, err := net.SplitHostPort(config.Admin.JCliListenAddress); err != nil {
				return fmt.Errorf("%w: admin.jcli_listen_address %q is not host:port: %v", ErrInvalidConfig, config.Admin.JCliListenAddress, err)
			}
			if config.Admin.JCliUsername == "" || config.Admin.JCliPassword == "" {
				return fmt.Errorf("%w: admin.jcli_listen_address requires jcli_username and jcli_password", ErrInvalidConfig)
			}
			if config.Admin.JCliIdleTimeoutSeconds < 0 {
				return fmt.Errorf("%w: admin.jcli_idle_timeout must not be negative", ErrInvalidConfig)
			}
		}
		if config.Admin.PBFacadeListenAddress != "" {
			if _, _, err := net.SplitHostPort(config.Admin.PBFacadeListenAddress); err != nil {
				return fmt.Errorf("%w: admin.pb_facade_listen_address %q is not host:port: %v",
					ErrInvalidConfig, config.Admin.PBFacadeListenAddress, err)
			}
			if config.Admin.PBFacadeToken == "" {
				return fmt.Errorf("%w: admin.pb_facade_listen_address requires pb_facade_token", ErrInvalidConfig)
			}
		} else if config.Admin.PBFacadeToken != "" {
			return fmt.Errorf("%w: admin.pb_facade_token requires pb_facade_listen_address", ErrInvalidConfig)
		}
	}
	if len(config.MORoutes) > 0 {
		moConfig := modispatch.Config{AMQPURL: config.Outbound.AMQPURL, Routes: config.MORoutes}
		if err := modispatch.ValidateConfig(moConfig); err != nil {
			return fmt.Errorf("%w: mo_routes: %v", ErrInvalidConfig, err)
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
