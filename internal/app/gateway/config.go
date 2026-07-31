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
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/dlrlookup"
	"github.com/pumpitspace/synevyr/internal/app/dlrthrower"
	"github.com/pumpitspace/synevyr/internal/app/modispatch"
	"github.com/pumpitspace/synevyr/internal/app/mothrower"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/termination"
	"github.com/pumpitspace/synevyr/internal/transport/restcompat"
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
	// REST configures the durable /secure API batch worker and, when
	// listen_address is set, its historical standalone JSON listener.
	REST restcompat.Config `json:"rest_api,omitempty"`
	// Admin, when present, runs the authenticated runtime provisioning API
	// (SQLite-backed connector CRUD, live-applied) mounted at /admin.
	Admin *AdminConfig `json:"admin,omitempty"`
	// HA, when present, fences the whole gateway active-passive through a
	// PostgreSQL session advisory lock and moves admin state into a shared,
	// namespace-isolated PostgreSQL control plane.
	HA *HAConfig `json:"ha,omitempty"`
	// PublicHostname is the host name partners use to reach this deployment's
	// customer-facing listeners (sendsms, REST, SMPPS). Nothing dials it; it
	// exists because every listener binds 0.0.0.0 and the process therefore
	// cannot know its own reachable address, so any generated integration
	// instruction would otherwise have to print a bind address as if a partner
	// could connect to it. Host only — each ingress keeps its own configured
	// port. Empty leaves the console showing an explicit placeholder.
	PublicHostname string `json:"public_hostname,omitempty"`
	// MORoutes, when present, runs the MO router dispatch in-process: MOs the
	// connectors ingest (deliver.sm.*) route to HTTP/SMPPS destinations via
	// deliver_sm_thrower.* (RouterPB.deliver_sm_callback semantics — default
	// route + connector-filtered static routes; content filters are plan 008
	// Step 6). Enable the deliver_sm_thrower worker to actually throw them.
	MORoutes []modispatch.RouteConfig `json:"mo_routes,omitempty"`
	// TerminationConnectors, when present, runs MT termination connectors: MT
	// routed to one of them stops on this platform instead of going to an
	// upstream SMSC. See docs/plans/021-mt-termination-connector.md.
	TerminationConnectors *TerminationConfig `json:"termination_connectors,omitempty"`
}

// TerminationConfig is the termination-connector section: the config-owned
// connectors plus the process-level spool and runner settings they share.
//
// The runners are per process, not per connector, because they work off the
// shared spool: one delivery loop and one receipt loop drain rows written by
// every termination connector this gateway runs.
type TerminationConfig struct {
	// Connectors are config-owned. The admin plane may start and stop them but
	// not edit or delete them, exactly as for config-declared SMPP client
	// connectors — a deletion that the next restart undoes is worse than a
	// refusal.
	//
	// Durations in a connector are JSON numbers of NANOSECONDS
	// (encoding/json's time.Duration), because this is the same shape the admin
	// plane persists. 5000000000 is five seconds. Leave them out to take the
	// 5 s / 2 s defaults, which is what production parity wants anyway.
	Connectors []termination.ConnectorConfig `json:"connectors"`

	// RedisURL is where the activation windows (dlr:block:<digits>) live. Empty
	// inherits dlr_lookup.redis_url; a redis-window connector with neither is a
	// configuration error rather than a connector that fails open on every
	// message.
	RedisURL string `json:"redis_url,omitempty"`

	// SpoolDBPath puts the message spool in a SQLite file instead of the
	// outbound PostgreSQL database. It exists for a single-node or local run;
	// production uses PostgreSQL, which is selected automatically whenever
	// outbound.postgres_dsn is set (and the gateway requires that DSN).
	SpoolDBPath string `json:"spool_db_path,omitempty"`

	// RetentionHours bounds how long decoded content is kept. Default 24.
	// Zero after defaulting is impossible; set it explicitly to a small value
	// for a deployment that wants less. Content here is OTP text, so this is a
	// breach-surface control, not housekeeping.
	RetentionHours     float64 `json:"retention_hours,omitempty"`
	RetentionBatchSize int     `json:"retention_batch_size,omitempty"`
	// PruneIntervalSeconds is how often retention is enforced. Default 1 h.
	//
	// It is deliberately NOT the CDR maintenance cadence: that defaults to 24 h,
	// and pruning a 24 h window every 24 h means content can live for 48 h.
	PruneIntervalSeconds float64 `json:"prune_interval_seconds,omitempty"`

	// PrefetchCount bounds unsettled deliveries per connector. Default 1.
	PrefetchCount int `json:"prefetch_count,omitempty"`

	// DeliveryIntervalSeconds and ReceiptIntervalSeconds pace the two runners.
	// Both default to 1 s. The receipt cadence adds directly to the partner's
	// observed receipt latency, so it should stay well under the connector's
	// receipt delay.
	DeliveryIntervalSeconds float64 `json:"delivery_interval_seconds,omitempty"`
	ReceiptIntervalSeconds  float64 `json:"receipt_interval_seconds,omitempty"`
	DeliveryBatchSize       int     `json:"delivery_batch_size,omitempty"`
	ReceiptBatchSize        int     `json:"receipt_batch_size,omitempty"`
	// ReceiptLeaseSeconds is how long one process's claim on a due receipt
	// holds. Default 30 s. Too short and two gateways emit the same receipt;
	// too long and a crashed gateway's receipts are late by that much.
	ReceiptLeaseSeconds float64 `json:"receipt_lease_seconds,omitempty"`
}

// HAConfig identifies one active-passive deployment. Gateways sharing the
// outbound PostgreSQL database and namespace contend for the same leader lock.
type HAConfig struct {
	Namespace           string  `json:"namespace"`
	StandbyRetrySeconds float64 `json:"standby_retry_seconds,omitempty"`
	// StandbyListenAddress exposes /live and /ready before promotion. It may
	// equal Outbound.ListenAddress: the standby server closes before the active
	// admission listener binds that address.
	StandbyListenAddress string `json:"standby_listen_address"`
}

// AdminConfig configures the runtime provisioning plane. DBPath is the SQLite
// file for single-node deployments. HA deployments use the outbound PostgreSQL
// database as the shared control plane and may leave DBPath empty. Token
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
	// APIListenAddress, when set, serves the token-authenticated admin REST API
	// (/admin/) on its own listener instead of on the public sendsms mux.
	//
	// It shares that mux by default for backward compatibility, which means a
	// deployment publishing the sendsms port also publishes an API that creates
	// users, changes balances and starts connectors. The web UI and jCli are
	// already isolated this way; set this to a loopback address to give /admin/
	// the same boundary. Leaving it empty logs a warning at startup.
	APIListenAddress string `json:"api_listen_address,omitempty"`
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
	if err := config.REST.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if config.REST.ListenAddress != "" {
		if _, _, err := net.SplitHostPort(config.REST.ListenAddress); err != nil {
			return fmt.Errorf("%w: rest_api.listen_address %q is not host:port: %v",
				ErrInvalidConfig, config.REST.ListenAddress, err)
		}
	}
	if config.HA != nil {
		if strings.TrimSpace(config.HA.Namespace) == "" {
			return fmt.Errorf("%w: ha requires a non-empty namespace", ErrInvalidConfig)
		}
		if math.IsNaN(config.HA.StandbyRetrySeconds) || math.IsInf(config.HA.StandbyRetrySeconds, 0) ||
			config.HA.StandbyRetrySeconds < 0 ||
			config.HA.StandbyRetrySeconds*float64(time.Second) >= float64(math.MaxInt64) {
			return fmt.Errorf("%w: ha standby retry must be finite, non-negative and representable", ErrInvalidConfig)
		}
		if config.HA.StandbyListenAddress == "" {
			return fmt.Errorf("%w: ha requires standby_listen_address for liveness and readiness", ErrInvalidConfig)
		}
		if _, _, err := net.SplitHostPort(config.HA.StandbyListenAddress); err != nil {
			return fmt.Errorf("%w: ha.standby_listen_address %q is not host:port: %v",
				ErrInvalidConfig, config.HA.StandbyListenAddress, err)
		}
	}
	if config.Admin != nil {
		if config.Admin.DBPath == "" && config.HA == nil {
			return fmt.Errorf("%w: admin requires db_path", ErrInvalidConfig)
		}
		if config.Admin.Token == "" {
			return fmt.Errorf("%w: admin requires a non-empty token", ErrInvalidConfig)
		}
		if config.Admin.APIListenAddress != "" {
			if _, _, err := net.SplitHostPort(config.Admin.APIListenAddress); err != nil {
				return fmt.Errorf("%w: admin.api_listen_address %q is not host:port: %v", ErrInvalidConfig, config.Admin.APIListenAddress, err)
			}
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
	}
	listeners := []listenerConfig{
		{name: "outbound.listen_address", address: config.Outbound.ListenAddress},
		{name: "rest_api.listen_address", address: config.REST.ListenAddress},
	}
	if config.Admin != nil {
		listeners = append(listeners,
			listenerConfig{name: "admin.api_listen_address", address: config.Admin.APIListenAddress},
			listenerConfig{name: "admin.web_listen_address", address: config.Admin.WebListenAddress},
			listenerConfig{name: "admin.jcli_listen_address", address: config.Admin.JCliListenAddress},
		)
	}
	if config.SMPPS != nil && config.SMPPS.BindAddr != "" {
		listeners = append(listeners, listenerConfig{name: "smpps.bind_addr", address: config.SMPPS.BindAddr})
	}
	if err := validateConcurrentListeners(listeners...); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	// The standby server closes before the public/REST/admin-web/PB servers
	// bind, so sharing one of those addresses is intentional and supports an
	// in-place /ready transition. jCli and SMPPs start inside NewRuntime while
	// standby health is still serving, so only those two overlap it.
	if config.HA != nil {
		overlappingStandby := []listenerConfig{
			{name: "ha.standby_listen_address", address: config.HA.StandbyListenAddress},
		}
		if config.Admin != nil {
			overlappingStandby = append(overlappingStandby,
				listenerConfig{name: "admin.jcli_listen_address", address: config.Admin.JCliListenAddress})
		}
		if config.SMPPS != nil {
			overlappingStandby = append(overlappingStandby,
				listenerConfig{name: "smpps.bind_addr", address: config.SMPPS.BindAddr})
		}
		if err := validateConcurrentListeners(overlappingStandby...); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
	}
	if len(config.MORoutes) > 0 {
		moConfig := modispatch.Config{AMQPURL: config.Outbound.AMQPURL, Routes: config.MORoutes}
		if err := modispatch.ValidateConfig(moConfig); err != nil {
			return fmt.Errorf("%w: mo_routes: %v", ErrInvalidConfig, err)
		}
	}
	// A deployment that terminates every message locally has no SMPP client
	// connector at all, which is the whole point of the termination connector.
	// Requiring one of either kind still refuses a gateway with nowhere to send
	// an MT message.
	if len(config.Connectors) == 0 && len(config.terminationConnectors()) == 0 {
		return fmt.Errorf("%w: at least one SMPPc or termination connector is required", ErrInvalidConfig)
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
	terminated, err := validateTerminationConfig(config, configured)
	if err != nil {
		return err
	}
	for _, route := range config.Outbound.Routes {
		// A route names which kind of connector its candidates are. Checking the
		// reference against the wrong set is how a "term" route survives config
		// validation and then fails at boot inside the routing table builder,
		// with an error naming a connector that does exist.
		known := configured
		switch route.ConnectorType {
		case "", string(routingtable.SMPPC):
		case string(routingtable.TERM):
			known = terminated
		default:
			return fmt.Errorf("%w: route connector_type %q is not %q or %q",
				ErrInvalidConfig, route.ConnectorType, routingtable.SMPPC, routingtable.TERM)
		}
		for _, connectorID := range route.ConnectorCandidates() {
			if _, exists := known[connectorID]; !exists {
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

// Termination defaults. They are here rather than in core/termination because
// they describe how this process schedules shared work, not what a connector
// promises a partner.
const (
	defaultSpoolPruneInterval    = time.Hour
	defaultDeliveryRunInterval   = time.Second
	defaultReceiptRunInterval    = time.Second
	defaultTerminationRetention  = 24 * time.Hour
	defaultTerminationPrefetch   = 1
	defaultTerminationLease      = 30 * time.Second
	defaultTerminationBatchLimit = 100
)

func (config Config) terminationConnectors() []termination.ConnectorConfig {
	if config.TerminationConnectors == nil {
		return nil
	}
	return config.TerminationConnectors.Connectors
}

// TerminationCIDs lists the config-owned termination connector ids, for the
// reserved set a management surface builds at construction.
func (config Config) TerminationCIDs() []string {
	connectors := config.terminationConnectors()
	ids := make([]string, 0, len(connectors))
	for _, connector := range connectors {
		ids = append(ids, connector.CID)
	}
	return ids
}

// ResolvedRedisURL is the activation-gate endpoint: the section's own URL, or
// the DLR lookup's when it has none. They are the same Redis in every
// deployment seen so far, but the gate reads a keyspace this platform does not
// own, so it stays separately settable.
func (config Config) ResolvedRedisURL() string {
	if config.TerminationConnectors != nil && config.TerminationConnectors.RedisURL != "" {
		return config.TerminationConnectors.RedisURL
	}
	if config.DLRLookup != nil {
		return config.DLRLookup.RedisURL
	}
	return ""
}

func (c TerminationConfig) prefetch() int {
	if c.PrefetchCount < 1 {
		return defaultTerminationPrefetch
	}
	return c.PrefetchCount
}

func (c TerminationConfig) retention() msgspool.RetentionPolicy {
	window := defaultTerminationRetention
	if c.RetentionHours > 0 {
		window = time.Duration(c.RetentionHours * float64(time.Hour))
	}
	batch := c.RetentionBatchSize
	if batch <= 0 {
		batch = msgspool.DefaultRetentionBatch
	}
	return msgspool.RetentionPolicy{Window: window, BatchSize: batch}
}

func (c TerminationConfig) pruneInterval() time.Duration {
	return positiveSeconds(c.PruneIntervalSeconds, defaultSpoolPruneInterval)
}

func (c TerminationConfig) deliveryInterval() time.Duration {
	return positiveSeconds(c.DeliveryIntervalSeconds, defaultDeliveryRunInterval)
}

func (c TerminationConfig) receiptInterval() time.Duration {
	return positiveSeconds(c.ReceiptIntervalSeconds, defaultReceiptRunInterval)
}

func (c TerminationConfig) receiptLease() time.Duration {
	return positiveSeconds(c.ReceiptLeaseSeconds, defaultTerminationLease)
}

func (c TerminationConfig) deliveryBatch() int {
	if c.DeliveryBatchSize <= 0 {
		return defaultTerminationBatchLimit
	}
	return c.DeliveryBatchSize
}

func (c TerminationConfig) receiptBatch() int {
	if c.ReceiptBatchSize <= 0 {
		return defaultTerminationBatchLimit
	}
	return c.ReceiptBatchSize
}

func positiveSeconds(seconds float64, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds * float64(time.Second))
}

// validateTerminationConfig checks the section and returns the set of
// config-declared termination cids, so route references can be resolved against
// the right connector kind.
func validateTerminationConfig(config Config, smppcCIDs map[string]struct{}) (map[string]struct{}, error) {
	section := config.TerminationConnectors
	terminated := make(map[string]struct{})
	if section == nil {
		return terminated, nil
	}
	for _, value := range []struct {
		name    string
		seconds float64
	}{
		{"retention_hours", section.RetentionHours},
		{"prune_interval_seconds", section.PruneIntervalSeconds},
		{"delivery_interval_seconds", section.DeliveryIntervalSeconds},
		{"receipt_interval_seconds", section.ReceiptIntervalSeconds},
		{"receipt_lease_seconds", section.ReceiptLeaseSeconds},
	} {
		if math.IsNaN(value.seconds) || math.IsInf(value.seconds, 0) || value.seconds < 0 ||
			value.seconds*float64(time.Hour) >= float64(math.MaxInt64) {
			return nil, fmt.Errorf("%w: termination_connectors.%s must be finite, non-negative and representable",
				ErrInvalidConfig, value.name)
		}
	}
	if err := section.retention().Validate(); err != nil {
		return nil, fmt.Errorf("%w: termination_connectors retention: %v", ErrInvalidConfig, err)
	}
	if section.retention().Window == 0 {
		// A zero window disables pruning, and this spool holds OTP bodies.
		return nil, fmt.Errorf("%w: termination_connectors.retention_hours must be positive", ErrInvalidConfig)
	}
	if config.DLRLookup == nil {
		// A termination connector publishes its synthesized submit_sm_resp and
		// receipt legs to dlr.submit_sm_resp / dlr.deliver_sm with mandatory set.
		// Those are routable only while the DLRLookup queue exists, and it is
		// DLRLookup that correlates the receipt back to the partner's message id.
		// Without it every terminated message requeues forever and no partner
		// ever receives a receipt — a failure that looks like a broker problem
		// from every angle except this one.
		return nil, fmt.Errorf("%w: termination_connectors requires dlr_lookup: "+
			"the synthesized submit_sm_resp and receipt legs are unroutable without the DLRLookup queue",
			ErrInvalidConfig)
	}
	redisURL := config.ResolvedRedisURL()
	for index, connector := range section.Connectors {
		if err := connector.Validate(); err != nil {
			return nil, fmt.Errorf("%w: termination connector %d: %v", ErrInvalidConfig, index, err)
		}
		if _, exists := terminated[connector.CID]; exists {
			return nil, fmt.Errorf("%w: duplicate termination connector %q", ErrInvalidConfig, connector.CID)
		}
		if _, exists := smppcCIDs[connector.CID]; exists {
			// One cid, two connector types: the submit queue name is derived
			// from the cid, so both would consume the same queue and each
			// message would go to whichever won the race.
			return nil, fmt.Errorf("%w: termination connector %q collides with an SMPPc connector id",
				ErrInvalidConfig, connector.CID)
		}
		terminated[connector.CID] = struct{}{}
		if connector.Verdict.Source == termination.SourceRedisWindow && redisURL == "" {
			return nil, fmt.Errorf(
				"%w: termination connector %q uses the %s verdict source but no redis_url is configured "+
					"(set termination_connectors.redis_url or dlr_lookup.redis_url)",
				ErrInvalidConfig, connector.CID, termination.SourceRedisWindow)
		}
	}
	return terminated, nil
}

type listenerConfig struct {
	name    string
	address string
}

func validateConcurrentListeners(listeners ...listenerConfig) error {
	for leftIndex, left := range listeners {
		if left.address == "" {
			continue
		}
		for rightIndex := leftIndex + 1; rightIndex < len(listeners); rightIndex++ {
			right := listeners[rightIndex]
			if right.address == "" {
				continue
			}
			conflict, err := listenerAddressesConflict(left.address, right.address)
			if err != nil {
				return err
			}
			if conflict {
				return fmt.Errorf("%s (%s) collides with %s (%s)",
					left.name, left.address, right.name, right.address)
			}
		}
	}
	return nil
}

func listenerAddressesConflict(left, right string) (bool, error) {
	leftHost, leftPort, err := net.SplitHostPort(left)
	if err != nil {
		return false, fmt.Errorf("listener address %q is not host:port: %v", left, err)
	}
	rightHost, rightPort, err := net.SplitHostPort(right)
	if err != nil {
		return false, fmt.Errorf("listener address %q is not host:port: %v", right, err)
	}
	if leftPort != rightPort {
		return false, nil
	}
	return wildcardHost(leftHost) || wildcardHost(rightHost) || strings.EqualFold(leftHost, rightHost), nil
}

func wildcardHost(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func (config Config) BindTimeout() time.Duration {
	if config.BindTimeoutSeconds == 0 {
		return 30 * time.Second
	}
	return time.Duration(config.BindTimeoutSeconds * float64(time.Second))
}

func (config HAConfig) StandbyRetryInterval() time.Duration {
	if config.StandbyRetrySeconds == 0 {
		return 2 * time.Second
	}
	return time.Duration(config.StandbyRetrySeconds * float64(time.Second))
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
