package outbound

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/core/dlrgate"
	"github.com/pumpitspace/synevyr/internal/core/mtcredential"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

var (
	ErrInvalidRuntimeConfig = errors.New("invalid outbound runtime configuration")
	legacyUsernamePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,15}$`)
	legacyUserIDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)
	// Groups share the uid constraint (jasmin/routing/jasminApi.py:229).
	legacyGroupIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)
)

type Config struct {
	ListenAddress string        `json:"listen_address"`
	AMQPURL       string        `json:"amqp_url"`
	PythonPath    string        `json:"python_path"`
	PostgresDSN   string        `json:"postgres_dsn"`
	Users         []UserConfig  `json:"users"`
	Routes        []RouteConfig `json:"routes"`
	// Groups are billing groups users can belong to. Declaring one is optional;
	// a user referencing an undeclared group is a config error, because a
	// silently missing group would silently remove a spending ceiling.
	Groups []GroupConfig `json:"groups,omitempty"`
	// AMQPDurableTopology declares exchanges/queues durable (queued submits
	// survive a broker restart). Must match what the vhost already holds —
	// the legacy stack declares non-durable and AMQP 406s a mismatched
	// redeclare. The gateway propagates its top-level flag here.
	AMQPDurableTopology bool `json:"amqp_durable_topology,omitempty"`

	// LongContentSplit ("sar" or "udh") and LongContentMaxParts mirror the
	// legacy http-api long_content_split / long_content_max_parts settings.
	// Empty/zero take the legacy defaults, "udh" and 5.
	LongContentSplit    string `json:"long_content_split,omitempty"`
	LongContentMaxParts int    `json:"long_content_max_parts,omitempty"`
	// LongContentRejectOverMax refuses a submit whose content needs more than
	// LongContentMaxParts parts, instead of delivering the leading parts and
	// dropping the rest. Default false is the legacy behaviour; either way the
	// truncation is logged, which it previously was not.
	LongContentRejectOverMax bool `json:"long_content_reject_over_max,omitempty"`
	// MTInterceptors are the MT interception scripts run pre-routing, highest
	// order first, until one rejects (legacy MO/MTInterceptorTable). Requires
	// an interceptor runner to be wired; validation only checks shape.
	MTInterceptors []InterceptorConfig `json:"mt_interceptors,omitempty"`
	// MOInterceptors are the MO-direction interception scripts run on the
	// deliver path (inbound). Same shape as MTInterceptors; consumed by the
	// smppc deliver hook via the gateway, not by the submit pipeline. Filters
	// support source/destination/short_message/tag/date/time (no user filter).
	MOInterceptors []InterceptorConfig `json:"mo_interceptors,omitempty"`
	// QuotaPersistIntervalSeconds is how often mutated balances and
	// submit_sm_count quotas are flushed to the durable quota store. It bounds
	// how much spending a hard crash can refund, so shorter is safer; the cost
	// is one small batched UPSERT per interval, and only when something charged.
	// Zero selects billing.DefaultQuotaPersistInterval; negative is rejected.
	// There is no "off" — balances are money and always persist.
	QuotaPersistIntervalSeconds int `json:"quota_persist_interval_seconds,omitempty"`
	// CDRCurrency is assigned to newly admitted commercial records. Empty
	// retains ISO-4217 XXX for unitless legacy route prices.
	CDRCurrency string `json:"cdr_currency,omitempty"`
	// A positive retention period enables terminal-record pruning. Zero keeps
	// records indefinitely. The batch defaults to 1000 when retention is on.
	CDRRetentionDays      int `json:"cdr_retention_days,omitempty"`
	CDRRetentionBatchSize int `json:"cdr_retention_batch_size,omitempty"`
	// Zero runs CDR reconciliation/retention every 24 hours.
	CDRMaintenanceIntervalSeconds int `json:"cdr_maintenance_interval_seconds,omitempty"`
}

// quotaPersistInterval resolves the configured flush cadence, falling back to
// the package default. validateConfig has already rejected a negative value.
func (config Config) quotaPersistInterval() time.Duration {
	if config.QuotaPersistIntervalSeconds <= 0 {
		return billing.DefaultQuotaPersistInterval
	}
	return time.Duration(config.QuotaPersistIntervalSeconds) * time.Second
}

// InterceptorConfig is one MT interceptor: a Python script (py_code) run when
// all filters match, at the given order (higher wins, like routes).
type InterceptorConfig struct {
	Order   int            `json:"order"`
	Filters []FilterConfig `json:"filters,omitempty"`
	PyCode  string         `json:"py_code"`
}

type UserConfig struct {
	Username       string `json:"username"`
	ExternalID     string `json:"external_id"`
	PasswordSHA256 string `json:"password_sha256"`
	// PasswordMD5 accepts the frozen RouterPB User.password digest. New native
	// configuration should use SHA-256; the trusted PB translator cannot
	// recover plaintext from a pickled legacy User and therefore preserves its
	// existing 16-byte MD5 verifier during migration.
	PasswordMD5                  string   `json:"password_md5,omitempty"`
	Balance                      *float64 `json:"balance"`
	SubmitSMCount                *int     `json:"submit_sm_count"`
	EarlyDecrementBalancePercent *int     `json:"early_decrement_balance_percent"`
	// GroupID names the group this user belongs to. Legacy requires one for
	// every user ("Every user must have a group"); it stays optional here so
	// existing configs keep loading, and an empty value means "no group", which
	// billing already handles as "no group ceiling".
	GroupID string `json:"group_id,omitempty"`
	// Disabled mirrors the legacy user enable/disable flag, which jCli toggles
	// and the list renders with a "!" prefix. A disabled user is refused at the
	// front door: legacy checks it on every authentication.
	Disabled bool `json:"disabled,omitempty"`

	// MTCredential is the user's MtMessagingCredential: which optional
	// parameters they may set, which values are admissible, and the default
	// source address. The engine (internal/core/mtcredential) already enforces
	// this for SMPPs binds; these fields are what let the HTTP front door
	// enforce the same contract instead of only checking the password.
	MTCredential *MTCredentialConfig `json:"mt_credential,omitempty"`

	// SMPPSCredential is the legacy SmppsCredential half of the same user: may
	// they bind, from which IPs, and how many concurrent binds. In legacy this
	// lives on the one user record that serves both protocols; the Go model
	// keeps SMPPs bind accounts as their own entity, so the console mirrors
	// these into it (system_id = username) rather than storing a flag that
	// nothing enforces.
	SMPPSCredential *SMPPSCredentialConfig `json:"smpps_credential,omitempty"`

	// DLRGate is the fork-local per-user activation-registry gate: when enabled,
	// the terminal receipt this user is told is decided by whether the
	// destination is in the registry rather than by what the upstream reported.
	// Absent means disabled, which is every user until an operator sets one.
	//
	// It lives here rather than on MTCredential because mtcredential.Credential
	// deliberately models only Jasmin's authorizations and value filters and is
	// parity-frozen; the throughput ceilings above take the same route for the
	// same reason.
	DLRGate *DLRGateConfig `json:"dlr_gate,omitempty"`
}

// DLRGateConfig provisions the per-user DLR registry gate.
type DLRGateConfig struct {
	Enabled bool `json:"enabled"`
	// HitStatus/HitError are the receipt fields reported when the destination is
	// in the registry; MissStatus/MissError when it is not. Empty fields take
	// the activation window's own defaults (DELIVRD/000 and REJECTD/008).
	HitStatus  string `json:"hit_status,omitempty"`
	HitError   string `json:"hit_error,omitempty"`
	MissStatus string `json:"miss_status,omitempty"`
	MissError  string `json:"miss_error,omitempty"`
	// KeyID is the public identifier in this user's registry URL, and
	// TokenSHA256 the stored proof of its bearer token. The plaintext token is
	// never stored: it is returned once, by whichever surface minted it.
	//
	// They live on the user spec rather than in a table of their own for the
	// same reason password_sha256 does — the spec is already the durable,
	// replicated home of this user's credentials, and it already propagates to
	// the running gateway on every write.
	KeyID       string `json:"key_id,omitempty"`
	TokenSHA256 string `json:"token_sha256,omitempty"`
}

// dlrGatePolicy turns the provisioned config into the engine's form. A nil
// config is a disabled gate, so a user provisioned before this existed behaves
// exactly as before.
func dlrGatePolicy(config *DLRGateConfig) dlrgate.Policy {
	if config == nil {
		return dlrgate.Policy{}
	}
	return dlrgate.Policy{
		Enabled:    config.Enabled,
		HitStatus:  config.HitStatus,
		HitError:   config.HitError,
		MissStatus: config.MissStatus,
		MissError:  config.MissError,
	}.WithDefaults()
}

// SMPPSCredentialConfig provisions SmppsCredential.
type SMPPSCredentialConfig struct {
	Bind        *bool  `json:"bind,omitempty"`
	IP          string `json:"ip,omitempty"`
	MaxBindings *int   `json:"max_bindings,omitempty"`
}

// MTCredentialConfig provisions MtMessagingCredential. Every authorization is a
// pointer so "omitted" (legacy default: true, except http_bulk) is
// distinguishable from an explicit false; every value filter is a regex source
// that defaults to the legacy pattern when empty.
type MTCredentialConfig struct {
	HTTPSend                *bool `json:"http_send,omitempty"`
	HTTPBulk                *bool `json:"http_bulk,omitempty"`
	HTTPBalance             *bool `json:"http_balance,omitempty"`
	HTTPRate                *bool `json:"http_rate,omitempty"`
	SMPPSSend               *bool `json:"smpps_send,omitempty"`
	HTTPLongContent         *bool `json:"http_long_content,omitempty"`
	SetDLRLevel             *bool `json:"set_dlr_level,omitempty"`
	HTTPSetDLRMethod        *bool `json:"http_set_dlr_method,omitempty"`
	SetSourceAddress        *bool `json:"set_source_address,omitempty"`
	SetPriority             *bool `json:"set_priority,omitempty"`
	SetValidityPeriod       *bool `json:"set_validity_period,omitempty"`
	SetHexContent           *bool `json:"set_hex_content,omitempty"`
	SetScheduleDeliveryTime *bool `json:"set_schedule_delivery_time,omitempty"`

	FilterDestinationAddress string `json:"filter_destination_address,omitempty"`
	FilterSourceAddress      string `json:"filter_source_address,omitempty"`
	FilterPriority           string `json:"filter_priority,omitempty"`
	FilterValidityPeriod     string `json:"filter_validity_period,omitempty"`
	FilterContent            string `json:"filter_content,omitempty"`

	// DefaultSourceAddress is substituted when the request omits one. A nil
	// pointer means "unset" (Python None), which is not the same as "".
	DefaultSourceAddress *string `json:"default_source_address,omitempty"`

	// HTTPThroughput and SMPPSThroughput are the per-second submit ceilings,
	// enforced at the front door by internal/core/throughput. Zero or negative
	// means unlimited, matching Python's `if quota and quota >= 0` guard.
	HTTPThroughput  *float64 `json:"http_throughput,omitempty"`
	SMPPSThroughput *float64 `json:"smpps_throughput,omitempty"`
}

// GroupConfig is a billing group: a balance and quota ceiling shared by its
// users. The charge itself is already group-aware (billing.User.CanApply and
// ApplyBill consult the group), so this is the provisioning half that was
// missing.
type GroupConfig struct {
	GID           string   `json:"gid"`
	Balance       *float64 `json:"balance,omitempty"`
	SubmitSMCount *int     `json:"submit_sm_count,omitempty"`
	Disabled      bool     `json:"disabled,omitempty"`
}

type RouteConfig struct {
	ConnectorID  string   `json:"connector_id"`
	ConnectorIDs []string `json:"connector_ids,omitempty"`
	// ConnectorType selects what kind of connector the candidates are. Empty
	// means "smppc", because every route persisted before the termination
	// connector existed is an outbound SMPP route and must keep loading as one.
	// It applies to every candidate: a pool mixing an upstream carrier with a
	// local termination endpoint would make failover mean two different things
	// in one route.
	ConnectorType string         `json:"connector_type,omitempty"`
	Rate          float64        `json:"rate"`
	Default       bool           `json:"default"`
	Order         int            `json:"order"`
	Filters       []FilterConfig `json:"filters,omitempty"`
}

func (route RouteConfig) ConnectorCandidates() []string {
	if len(route.ConnectorIDs) > 0 {
		return append([]string(nil), route.ConnectorIDs...)
	}
	if route.ConnectorID == "" {
		return nil
	}
	return []string{route.ConnectorID}
}

type runtimeDirectory struct {
	users *billing.Manager
	// defaultRate is the rate of the highest-order configured route, captured at
	// boot -- not the default route's rate, despite the name. It survives only as
	// the fallback for a destination no route matches (which means a table with
	// no default route, since Select always returns that one when it exists).
	// A real quote comes from the live table below. See Rate.
	defaultRate float64
	// routes is the same live MT table the submit path selects on, so a rate
	// quote prices the route the message would actually take -- including routes
	// an operator added after boot. Nil only in tests that never quote.
	routes *routingtable.AtomicTable
	// groups maps a legacy gid to its billing group, and groupIDs to the
	// internal numeric id the routing filters compare against. Both are guarded
	// by mu, like passwordHashes.
	groups   map[string]*billing.Group
	groupIDs map[string]int64
	// groupDisabled and userDisabled carry the legacy enable/disable flags. A
	// disabled user, or a user in a disabled group, is refused at
	// authentication -- legacy checks both there, so a suspended customer stops
	// sending the moment the flag flips rather than at the next restart.
	groupDisabled map[string]bool
	userDisabled  map[string]bool
	userGroup     map[string]string
	// credentials is each user's MtMessagingCredential, consulted by the HTTP
	// front door on every send.
	credentials map[string]*mtcredential.Credential
	// throughput is each user's per-ingress QoS ceiling, guarded by mu like
	// credentials. Kept beside them rather than inside mtcredential, which
	// deliberately models only authorizations and value filters.
	throughput map[string]userThroughput
	// dlrGate is each user's DLR registry gate policy, guarded by mu like the
	// maps above. An absent entry is a disabled gate.
	dlrGate map[string]dlrgate.Policy
	// dlrGateKeys indexes the public registry key id to the user that owns it
	// and the digest of its token, so the public API authenticates in one map
	// read instead of scanning every user.
	dlrGateKeys map[string]dlrGateKey
	// provisionedUsers and provisionedGroups keep the balance/submit_sm_count
	// each principal was last provisioned with. They are written beside the live
	// value in the durable store so the next boot can tell a plain restart from
	// an operator top-up (billing.QuotaRecord.Restore).
	provisionedUsers  map[string]billing.Quota
	provisionedGroups map[string]billing.Quota
	// restore holds the durable quotas loaded at boot, consumed once per key by
	// applyUser/applyGroup. Guarded by mu like the maps above.
	restore billing.QuotaIndex
	// mu guards passwordHashes, read on the hot Authenticate path and written
	// by admin user provisioning. billing.Manager has its own lock.
	mu             sync.RWMutex
	passwordHashes map[string]passwordDigest
}

type passwordDigest struct {
	algorithm string
	value     [sha256.Size]byte
}

func newRuntimeDirectory(config Config) (*runtimeDirectory, error) {
	return newRuntimeDirectoryWithQuotas(config, nil)
}

// newRuntimeDirectoryWithQuotas builds the directory, restoring each principal's
// balance and submit_sm_count from the durable quota set instead of the
// provisioned value where the precedence rule says so. A nil index (config
// validation, tests) provisions straight from the spec, which is exactly the
// "no durable row" branch.
func newRuntimeDirectoryWithQuotas(config Config, restore billing.QuotaIndex) (*runtimeDirectory, error) {
	if restore == nil {
		restore = billing.NewQuotaIndex(nil)
	}
	directory := &runtimeDirectory{
		users:             billing.NewManager(),
		passwordHashes:    make(map[string]passwordDigest, len(config.Users)),
		groups:            make(map[string]*billing.Group, len(config.Groups)),
		groupIDs:          make(map[string]int64, len(config.Groups)),
		groupDisabled:     make(map[string]bool, len(config.Groups)),
		userDisabled:      make(map[string]bool, len(config.Users)),
		userGroup:         make(map[string]string, len(config.Users)),
		credentials:       make(map[string]*mtcredential.Credential, len(config.Users)),
		throughput:        make(map[string]userThroughput, len(config.Users)),
		dlrGate:           make(map[string]dlrgate.Policy, len(config.Users)),
		dlrGateKeys:       make(map[string]dlrGateKey, len(config.Users)),
		provisionedUsers:  make(map[string]billing.Quota, len(config.Users)),
		provisionedGroups: make(map[string]billing.Quota, len(config.Groups)),
		restore:           restore,
	}
	// Groups first: a user entry resolves its group by gid, so the groups must
	// exist before any user is installed.
	for index, entry := range config.Groups {
		if err := directory.applyGroup(entry, int64(index+1)); err != nil {
			return nil, err
		}
	}
	for index, entry := range config.Users {
		if err := directory.applyUser(entry, int64(index+1)); err != nil {
			return nil, err
		}
	}
	return directory, nil
}

// applyGroup validates a group entry and installs it with the given internal
// gid. Shared by boot and admin provisioning; safe for concurrent use.
func (directory *runtimeDirectory) applyGroup(entry GroupConfig, gid int64) error {
	if !legacyGroupIDPattern.MatchString(entry.GID) {
		return fmt.Errorf("%w: group %q gid must match the legacy constraint", ErrInvalidRuntimeConfig, entry.GID)
	}
	if entry.SubmitSMCount != nil && *entry.SubmitSMCount < 0 {
		return fmt.Errorf("%w: group %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.GID)
	}
	provisioned := billing.Quota{Balance: entry.Balance, SubmitSmCount: entry.SubmitSMCount}
	effective := directory.takeRestoredQuota(billing.QuotaScopeGroup, entry.GID, provisioned)
	group := billing.NewGroup(gid)
	if effective.Balance != nil {
		if err := group.SetBalance(*effective.Balance); err != nil {
			return fmt.Errorf("%w: group %q balance: %v", ErrInvalidRuntimeConfig, entry.GID, err)
		}
	}
	if effective.SubmitSmCount != nil {
		group.SetSubmitSmCountQuota(*effective.SubmitSmCount)
	}

	directory.mu.Lock()
	defer directory.mu.Unlock()
	if _, duplicate := directory.groups[entry.GID]; duplicate {
		return fmt.Errorf("%w: duplicate group %q", ErrInvalidRuntimeConfig, entry.GID)
	}
	directory.groups[entry.GID] = group
	directory.groupIDs[entry.GID] = gid
	directory.groupDisabled[entry.GID] = entry.Disabled
	directory.provisionedGroups[entry.GID] = provisioned.Clone()
	return nil
}

type groupDirectoryReplacement struct {
	directory           *runtimeDirectory
	group               *billing.GroupReprovision
	gid                 string
	previousDisabled    bool
	previousProvisioned billing.Quota
	active              bool
}

func (replacement *groupDirectoryReplacement) Commit() {
	if replacement == nil || !replacement.active {
		return
	}
	replacement.active = false
	replacement.group.Commit()
	replacement.directory.mu.Unlock()
}

func (replacement *groupDirectoryReplacement) Rollback() {
	if replacement == nil || !replacement.active {
		return
	}
	replacement.directory.groupDisabled[replacement.gid] = replacement.previousDisabled
	replacement.directory.provisionedGroups[replacement.gid] = replacement.previousProvisioned.Clone()
	replacement.active = false
	replacement.group.Rollback()
	replacement.directory.mu.Unlock()
}

// beginReplaceGroup applies an online admin edit to the existing live group and
// keeps both the directory and group locked until Commit or Rollback. Users
// hold direct group pointers, so replacing the map entry would leave them
// charging an orphan while persistence snapshots the new object.
func (directory *runtimeDirectory) beginReplaceGroup(entry GroupConfig, gid int64) (AdminReplacement, error) {
	if !legacyGroupIDPattern.MatchString(entry.GID) {
		return nil, fmt.Errorf("%w: group %q gid must match the legacy constraint", ErrInvalidRuntimeConfig, entry.GID)
	}
	if entry.Balance != nil {
		if err := billing.ValidateParams(*entry.Balance, nil); err != nil {
			return nil, fmt.Errorf("%w: group %q balance: %v", ErrInvalidRuntimeConfig, entry.GID, err)
		}
	}
	if entry.SubmitSMCount != nil && *entry.SubmitSMCount < 0 {
		return nil, fmt.Errorf("%w: group %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.GID)
	}
	next := billing.Quota{Balance: entry.Balance, SubmitSmCount: entry.SubmitSMCount}
	directory.mu.Lock()
	group, known := directory.groups[entry.GID]
	currentID := directory.groupIDs[entry.GID]
	previous, baselineKnown := directory.provisionedGroups[entry.GID]
	if !known || !baselineKnown {
		directory.mu.Unlock()
		return nil, fmt.Errorf("%w: group %q has no live provisioning baseline", ErrInvalidRuntimeConfig, entry.GID)
	}
	if currentID != gid {
		directory.mu.Unlock()
		return nil, fmt.Errorf("%w: group %q number changed from %d to %d", ErrInvalidRuntimeConfig, entry.GID, currentID, gid)
	}
	transaction, err := group.BeginReprovision(previous, next)
	if err != nil {
		directory.mu.Unlock()
		return nil, fmt.Errorf("%w: group %q quota: %v", ErrInvalidRuntimeConfig, entry.GID, err)
	}
	replacement := &groupDirectoryReplacement{
		directory:           directory,
		group:               transaction,
		gid:                 entry.GID,
		previousDisabled:    directory.groupDisabled[entry.GID],
		previousProvisioned: previous.Clone(),
		active:              true,
	}
	directory.groupDisabled[entry.GID] = entry.Disabled
	directory.provisionedGroups[entry.GID] = next.Clone()
	return replacement, nil
}

func (directory *runtimeDirectory) replaceGroup(entry GroupConfig, gid int64) error {
	replacement, err := directory.beginReplaceGroup(entry, gid)
	if err != nil {
		return err
	}
	replacement.Commit()
	return nil
}

// takeRestoredQuota resolves what a principal must be provisioned with, letting
// a durable row override the spec where billing.QuotaRecord.Restore says the
// spec has not changed since that row was written. Each row is consumed once,
// so this only ever applies to a key's *first* provisioning — boot, including
// the admin plane replaying its stored users. A later admin edit of the same
// account installs exactly what the operator just typed.
//
// A durable balance below zero can only come from a corrupted or hand-edited
// row (no production decrement path goes negative). It is clamped rather than
// rejected: refusing to boot on it would take the gateway down, and clamping
// errs towards the customer being unable to spend rather than towards a refund.
func (directory *runtimeDirectory) takeRestoredQuota(scope billing.QuotaScope, key string, provisioned billing.Quota) billing.Quota {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	effective := directory.restore.Take(scope, key, provisioned)
	if effective.Balance != nil && *effective.Balance < 0 {
		zero := 0.0
		effective.Balance = &zero
	}
	if effective.SubmitSmCount != nil && *effective.SubmitSmCount < 0 {
		zero := 0
		effective.SubmitSmCount = &zero
	}
	return effective
}

// lookupGroup resolves a gid to its billing group.
func (directory *runtimeDirectory) lookupGroup(gid string) (*billing.Group, bool) {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	group, ok := directory.groups[gid]
	return group, ok
}

// lookupGroupID resolves a gid to the numeric id routing filters compare.
func (directory *runtimeDirectory) lookupGroupID(gid string) (int64, bool) {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	id, ok := directory.groupIDs[gid]
	return id, ok
}

// groupIdentity returns the stable legacy gid attached to a username. The
// numeric billing.Group id is only a process-local routing-filter identity and
// is therefore unsuitable for durable commercial records.
func (directory *runtimeDirectory) groupIdentity(username string) (string, bool) {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	gid, known := directory.userGroup[username]
	return gid, known && gid != ""
}

// applyUser validates a user entry (legacy identity + password + billing state)
// and installs it with the given internal uid. Shared by boot and admin
// provisioning; safe for concurrent use.
func (directory *runtimeDirectory) applyUser(entry UserConfig, uid int64) error {
	// Validate before consuming the one-shot restore row. A malformed boot
	// entry must not discard the customer's last durable balance and turn a
	// corrected retry in the same process into a fresh grant.
	if err := directory.validateNewUser(entry); err != nil {
		return err
	}
	// Boot restore: the balance the customer has spent down to outranks the
	// provisioned grant, unless the operator changed that grant since it was
	// last flushed. See takeRestoredQuota and billing.QuotaRecord.Restore for
	// the full precedence rule and the top-up gesture it preserves.
	provisioned := billing.Quota{Balance: entry.Balance, SubmitSmCount: entry.SubmitSMCount}
	effective := directory.takeRestoredQuota(billing.QuotaScopeUser, entry.Username, provisioned)
	return directory.installUser(entry, uid, provisioned, effective)
}

func (directory *runtimeDirectory) validateNewUser(entry UserConfig) error {
	if !legacyUsernamePattern.MatchString(entry.Username) || !legacyUserIDPattern.MatchString(entry.ExternalID) {
		return fmt.Errorf("%w: user %q identity must match legacy username/uid constraints", ErrInvalidRuntimeConfig, entry.Username)
	}
	if err := dlrGatePolicy(entry.DLRGate).Validate(); err != nil {
		return fmt.Errorf("%w: user %q dlr_gate: %v", ErrInvalidRuntimeConfig, entry.Username, err)
	}
	if _, err := parsePasswordDigest(entry); err != nil {
		return err
	}
	if entry.Balance != nil {
		if err := billing.ValidateParams(*entry.Balance, nil); err != nil {
			return fmt.Errorf("%w: user %q balance: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
	}
	if entry.SubmitSMCount != nil && *entry.SubmitSMCount < 0 {
		return fmt.Errorf("%w: user %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.Username)
	}
	if entry.EarlyDecrementBalancePercent != nil {
		if err := billing.ValidateParams(0, entry.EarlyDecrementBalancePercent); err != nil {
			return fmt.Errorf("%w: user %q early percentage: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
	}
	if entry.GroupID != "" {
		if _, known := directory.lookupGroup(entry.GroupID); !known {
			return fmt.Errorf("%w: user %q references unknown group %q",
				ErrInvalidRuntimeConfig, entry.Username, entry.GroupID)
		}
	}
	directory.mu.RLock()
	_, duplicate := directory.passwordHashes[entry.Username]
	directory.mu.RUnlock()
	if duplicate {
		return fmt.Errorf("%w: duplicate username %q", ErrInvalidRuntimeConfig, entry.Username)
	}
	return nil
}

func parsePasswordDigest(entry UserConfig) (passwordDigest, error) {
	if (entry.PasswordSHA256 == "") == (entry.PasswordMD5 == "") {
		return passwordDigest{}, fmt.Errorf("%w: user %q must set exactly one of password_sha256 or password_md5",
			ErrInvalidRuntimeConfig, entry.Username)
	}
	algorithm := "sha256"
	encoded := entry.PasswordSHA256
	expectedSize := sha256.Size
	if entry.PasswordMD5 != "" {
		algorithm = "md5"
		encoded = entry.PasswordMD5
		expectedSize = md5.Size
	}
	rawHash, err := hex.DecodeString(encoded)
	if err != nil || len(rawHash) != expectedSize {
		return passwordDigest{}, fmt.Errorf("%w: user %q password_%s must be %d hexadecimal characters",
			ErrInvalidRuntimeConfig, entry.Username, algorithm, expectedSize*2)
	}
	result := passwordDigest{algorithm: algorithm}
	copy(result.value[:], rawHash)
	return result, nil
}

// userDirectoryReplacement carries the values an admin edit will install once
// its durable write commits.
//
// They are held here rather than written into the directory at begin time
// because the caller keeps this replacement open across that write. The
// directory lock used to be held for the same span, and every submit
// authentication and every SMPPs bind takes it to read a password hash — so a
// slow admin store (or a degraded network path to it) stopped the gateway
// authenticating anything at all, for as long as the write took.
//
// Nothing is visible to a reader until Commit, which is the same guarantee the
// held lock gave: a reader never sees an edit the durable store has not
// accepted. It just no longer waits to find that out.
type userDirectoryReplacement struct {
	directory        *runtimeDirectory
	user             *billing.UserReprovision
	username         string
	nextPasswordHash passwordDigest
	nextDisabled     bool
	nextGroupID      string
	nextCredential   *mtcredential.Credential
	nextThroughput   userThroughput
	nextDLRGate      dlrgate.Policy
	nextDLRGateKey   dlrGateKey
	nextProvisioned  billing.Quota
	active           bool
}

func (replacement *userDirectoryReplacement) Commit() {
	if replacement == nil || !replacement.active {
		return
	}
	replacement.active = false
	directory := replacement.directory
	username := replacement.username
	// Taken only now, and only for these map writes: the durable write is
	// already done. The quota transaction commits inside the same critical
	// section so a reader cannot observe the new credential with the old quota.
	directory.mu.Lock()
	directory.passwordHashes[username] = replacement.nextPasswordHash
	directory.userDisabled[username] = replacement.nextDisabled
	directory.userGroup[username] = replacement.nextGroupID
	directory.credentials[username] = replacement.nextCredential
	directory.throughput[username] = replacement.nextThroughput
	directory.dlrGate[username] = replacement.nextDLRGate
	directory.setDLRGateKeyLocked(username, replacement.nextDLRGateKey)
	directory.provisionedUsers[username] = replacement.nextProvisioned
	replacement.user.Commit()
	directory.mu.Unlock()
}

func (replacement *userDirectoryReplacement) Rollback() {
	if replacement == nil || !replacement.active {
		return
	}
	replacement.active = false
	// The directory was never mutated, so there is nothing to put back. Only the
	// quota reprovision has to be released.
	replacement.user.Rollback()
}

// beginReplaceUser preserves each live mutable quota whose provisioned
// baseline is unchanged by an admin edit. Changing a quota in the replacement
// spec is the explicit top-up/reset gesture and installs the new value. The
// returned replacement keeps the directory and user locked until the admin
// store write commits or rolls back.
func (directory *runtimeDirectory) beginReplaceUser(entry UserConfig, uid int64) (AdminReplacement, error) {
	if !legacyUsernamePattern.MatchString(entry.Username) || !legacyUserIDPattern.MatchString(entry.ExternalID) {
		return nil, fmt.Errorf("%w: user %q identity must match legacy username/uid constraints", ErrInvalidRuntimeConfig, entry.Username)
	}
	if err := dlrGatePolicy(entry.DLRGate).Validate(); err != nil {
		return nil, fmt.Errorf("%w: user %q dlr_gate: %v", ErrInvalidRuntimeConfig, entry.Username, err)
	}
	passwordHash, err := parsePasswordDigest(entry)
	if err != nil {
		return nil, err
	}
	if entry.Balance != nil {
		if err := billing.ValidateParams(*entry.Balance, nil); err != nil {
			return nil, fmt.Errorf("%w: user %q balance: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
	}
	if entry.SubmitSMCount != nil && *entry.SubmitSMCount < 0 {
		return nil, fmt.Errorf("%w: user %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.Username)
	}
	if entry.EarlyDecrementBalancePercent != nil {
		if err := billing.ValidateParams(0, entry.EarlyDecrementBalancePercent); err != nil {
			return nil, fmt.Errorf("%w: user %q early percentage: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
	}

	directory.mu.Lock()
	current, previousExternalID, err := directory.users.GetUserIdentity(entry.Username)
	if err != nil {
		directory.mu.Unlock()
		return nil, err
	}
	currentUID := current.UID()
	if currentUID != uid {
		directory.mu.Unlock()
		return nil, fmt.Errorf("%w: user %q uid changed from %d to %d", ErrInvalidRuntimeConfig, entry.Username, currentUID, uid)
	}
	// The external id is embedded in AMQP billing/reply routing keys. Changing
	// it while submits are unresolved would make their late charges impossible
	// to correlate, so it is immutable for an online replacement.
	if entry.ExternalID != previousExternalID {
		directory.mu.Unlock()
		return nil, fmt.Errorf("%w: user %q external_id cannot change from %q to %q",
			ErrInvalidRuntimeConfig, entry.Username, previousExternalID, entry.ExternalID)
	}
	previousProvisioned, known := directory.provisionedUsers[entry.Username]
	if !known {
		directory.mu.Unlock()
		return nil, fmt.Errorf("%w: user %q has no provisioning baseline", ErrInvalidRuntimeConfig, entry.Username)
	}

	provisioned := billing.Quota{Balance: entry.Balance, SubmitSmCount: entry.SubmitSMCount}
	var group *billing.Group
	if entry.GroupID != "" {
		resolved, groupKnown := directory.groups[entry.GroupID]
		if !groupKnown {
			directory.mu.Unlock()
			return nil, fmt.Errorf("%w: user %q references unknown group %q",
				ErrInvalidRuntimeConfig, entry.Username, entry.GroupID)
		}
		group = resolved
	}
	transaction, err := current.BeginReprovision(previousProvisioned, provisioned, entry.EarlyDecrementBalancePercent, group)
	if err != nil {
		directory.mu.Unlock()
		return nil, fmt.Errorf("%w: user %q quota: %v", ErrInvalidRuntimeConfig, entry.Username, err)
	}

	replacement := &userDirectoryReplacement{
		directory:        directory,
		user:             transaction,
		username:         entry.Username,
		nextPasswordHash: passwordHash,
		nextDisabled:     entry.Disabled,
		nextGroupID:      entry.GroupID,
		nextCredential:   buildMTCredential(entry.MTCredential),
		nextThroughput:   throughputQuotas(entry.MTCredential),
		nextDLRGate:      dlrGatePolicy(entry.DLRGate),
		nextDLRGateKey:   dlrGateKeyOf(entry.Username, entry.DLRGate),
		nextProvisioned:  provisioned.Clone(),
		active:           true,
	}
	// The lock is released here, not at Commit. Everything above needed a
	// consistent view of the directory; the durable write the caller is about to
	// make does not, and holding it there blocked every authentication.
	directory.mu.Unlock()
	return replacement, nil
}

func (directory *runtimeDirectory) replaceUser(entry UserConfig, uid int64) error {
	replacement, err := directory.beginReplaceUser(entry, uid)
	if err != nil {
		return err
	}
	replacement.Commit()
	return nil
}

// installUser validates and installs one user with separately supplied
// provisioned and effective quotas. They differ during boot restoration and
// quota-safe online replacement.
func (directory *runtimeDirectory) installUser(entry UserConfig, uid int64, provisioned, effective billing.Quota) error {
	if !legacyUsernamePattern.MatchString(entry.Username) || !legacyUserIDPattern.MatchString(entry.ExternalID) {
		return fmt.Errorf("%w: user %q identity must match legacy username/uid constraints", ErrInvalidRuntimeConfig, entry.Username)
	}
	if err := dlrGatePolicy(entry.DLRGate).Validate(); err != nil {
		return fmt.Errorf("%w: user %q dlr_gate: %v", ErrInvalidRuntimeConfig, entry.Username, err)
	}
	passwordHash, err := parsePasswordDigest(entry)
	if err != nil {
		return err
	}
	if entry.SubmitSMCount != nil && *entry.SubmitSMCount < 0 {
		return fmt.Errorf("%w: user %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.Username)
	}
	user := billing.NewUser(uid)
	if effective.Balance != nil {
		if err := user.SetBalance(*effective.Balance); err != nil {
			return fmt.Errorf("%w: user %q balance: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
	}
	if effective.SubmitSmCount != nil {
		user.SetSubmitSmCountQuota(*effective.SubmitSmCount)
	}
	if entry.EarlyDecrementBalancePercent != nil {
		if err := user.SetEarlyDecrementPercent(*entry.EarlyDecrementBalancePercent); err != nil {
			return fmt.Errorf("%w: user %q early percentage: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
	}
	if entry.GroupID != "" {
		group, known := directory.lookupGroup(entry.GroupID)
		if !known {
			return fmt.Errorf("%w: user %q references unknown group %q",
				ErrInvalidRuntimeConfig, entry.Username, entry.GroupID)
		}
		user.SetGroup(group)
	}

	directory.mu.Lock()
	defer directory.mu.Unlock()
	if _, duplicate := directory.passwordHashes[entry.Username]; duplicate {
		return fmt.Errorf("%w: duplicate username %q", ErrInvalidRuntimeConfig, entry.Username)
	}
	if err := directory.users.AddUserWithID(entry.Username, entry.ExternalID, user); err != nil {
		return fmt.Errorf("%w: user %q: %v", ErrInvalidRuntimeConfig, entry.Username, err)
	}
	directory.passwordHashes[entry.Username] = passwordHash
	directory.userDisabled[entry.Username] = entry.Disabled
	directory.userGroup[entry.Username] = entry.GroupID
	directory.credentials[entry.Username] = buildMTCredential(entry.MTCredential)
	directory.throughput[entry.Username] = throughputQuotas(entry.MTCredential)
	directory.dlrGate[entry.Username] = dlrGatePolicy(entry.DLRGate)
	directory.setDLRGateKeyLocked(entry.Username, dlrGateKeyOf(entry.Username, entry.DLRGate))
	directory.provisionedUsers[entry.Username] = provisioned.Clone()
	return nil
}

// quotaPrincipals projects the live directory into the durable-quota view the
// persister flushes. It is called on every tick, so accounts provisioned after
// boot (admin-created users and groups) are picked up without a restart.
//
// Keys are the legacy identities, sorted: the write batch is one transaction
// per tick, and a stable row order keeps concurrent writers from deadlocking on
// each other's locks.
func (directory *runtimeDirectory) quotaPrincipals() []billing.QuotaPrincipal {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	gids := make([]string, 0, len(directory.groups))
	for gid := range directory.groups {
		gids = append(gids, gid)
	}
	sort.Strings(gids)
	usernames := make([]string, 0, len(directory.passwordHashes))
	for username := range directory.passwordHashes {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	principals := make([]billing.QuotaPrincipal, 0, len(gids)+len(usernames))
	for _, gid := range gids {
		principals = append(principals, billing.QuotaPrincipal{
			Scope:       billing.QuotaScopeGroup,
			Key:         gid,
			Group:       directory.groups[gid],
			Provisioned: directory.provisionedGroups[gid],
		})
	}
	for _, username := range usernames {
		user, err := directory.users.GetUser(username)
		if err != nil {
			// The password hash and the billing user are installed under the
			// same lock, so this cannot happen; skipping beats persisting a
			// half-provisioned account.
			continue
		}
		principals = append(principals, billing.QuotaPrincipal{
			Scope:       billing.QuotaScopeUser,
			Key:         username,
			User:        user,
			GroupKey:    directory.userGroup[username],
			Provisioned: directory.provisionedUsers[username],
		})
	}
	return principals
}

// userThroughput is a user's per-ingress QoS ceiling in submits per second. A
// nil member means "unset", which legacy treats as unlimited.
type userThroughput struct {
	http  *float64
	smpps *float64
}

func throughputQuotas(config *MTCredentialConfig) userThroughput {
	if config == nil {
		return userThroughput{}
	}
	return userThroughput{http: config.HTTPThroughput, smpps: config.SMPPSThroughput}
}

// ThroughputQuota returns the ceiling that applies to an ingress. The ingress
// name is the submit request's source connector, so it is "smppsapi" or
// "httpapi" — the same two buckets legacy keeps on CnxStatus. Anything else
// falls back to the HTTP ceiling rather than going unmetered, so a new front
// door cannot silently escape the limit.
func (directory *runtimeDirectory) ThroughputQuota(username, ingress string) *float64 {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	quotas, known := directory.throughput[username]
	if !known {
		return nil
	}
	if ingress == "smppsapi" {
		return quotas.smpps
	}
	return quotas.http
}

// ResolveDLRGatePolicy returns the user's DLR registry gate policy. The second
// return is false for a user the directory does not know, which the gate treats
// the same as a disabled policy.
func (directory *runtimeDirectory) ResolveDLRGatePolicy(username string) (dlrgate.Policy, bool) {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	policy, known := directory.dlrGate[username]
	return policy, known
}

// dlrGateKey is one user's registry credential as the public API needs it.
type dlrGateKey struct {
	username    string
	keyID       string
	tokenSHA256 string
}

// dlrGateKeyOf projects the provisioned block into the index entry. A gate with
// no minted credential, or one that is switched off, yields the zero value and
// is therefore not routable: disabling the gate must also close its endpoint,
// or a partner would keep opening windows nobody reads.
func dlrGateKeyOf(username string, config *DLRGateConfig) dlrGateKey {
	if config == nil || !config.Enabled || config.KeyID == "" || config.TokenSHA256 == "" {
		return dlrGateKey{}
	}
	return dlrGateKey{username: username, keyID: config.KeyID, tokenSHA256: config.TokenSHA256}
}

// setDLRGateKeyLocked installs a user's key id, dropping whichever id they held
// before. The caller must hold mu.
//
// Removing the previous id is what makes a rotation a rotation: leaving it in
// place would keep the old URL answering with the old token indefinitely.
func (directory *runtimeDirectory) setDLRGateKeyLocked(username string, key dlrGateKey) {
	for id, existing := range directory.dlrGateKeys {
		if existing.username == username {
			delete(directory.dlrGateKeys, id)
		}
	}
	if key.keyID != "" {
		directory.dlrGateKeys[key.keyID] = key
	}
}

// ResolveDLRGateKey maps a public registry key id to its user and token digest.
func (directory *runtimeDirectory) ResolveDLRGateKey(keyID string) (string, string, bool) {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	key, known := directory.dlrGateKeys[keyID]
	if !known {
		return "", "", false
	}
	return key.username, key.tokenSHA256, true
}

// buildMTCredential turns the provisioned credential into the engine's form.
// A nil config yields Jasmin's permissive default (every authorization but
// http_bulk, and the default value filters), so a user provisioned before this
// existed behaves exactly as before.
func buildMTCredential(config *MTCredentialConfig) *mtcredential.Credential {
	credential := mtcredential.New(true)
	if config == nil {
		return credential
	}
	for key, value := range map[string]*bool{
		mtcredential.AuthHTTPSend:                config.HTTPSend,
		mtcredential.AuthHTTPBulk:                config.HTTPBulk,
		mtcredential.AuthHTTPBalance:             config.HTTPBalance,
		mtcredential.AuthHTTPRate:                config.HTTPRate,
		mtcredential.AuthSMPPSSend:               config.SMPPSSend,
		mtcredential.AuthHTTPLongContent:         config.HTTPLongContent,
		mtcredential.AuthSetDLRLevel:             config.SetDLRLevel,
		mtcredential.AuthHTTPSetDLRMethod:        config.HTTPSetDLRMethod,
		mtcredential.AuthSetSourceAddress:        config.SetSourceAddress,
		mtcredential.AuthSetPriority:             config.SetPriority,
		mtcredential.AuthSetValidityPeriod:       config.SetValidityPeriod,
		mtcredential.AuthSetHexContent:           config.SetHexContent,
		mtcredential.AuthSetScheduleDeliveryTime: config.SetScheduleDeliveryTime,
	} {
		if value != nil {
			credential.SetAuthorization(key, *value)
		}
	}
	for key, pattern := range map[string]string{
		mtcredential.FilterDestinationAddress: config.FilterDestinationAddress,
		mtcredential.FilterSourceAddress:      config.FilterSourceAddress,
		mtcredential.FilterPriority:           config.FilterPriority,
		mtcredential.FilterValidityPeriod:     config.FilterValidityPeriod,
		mtcredential.FilterContent:            config.FilterContent,
	} {
		if pattern != "" {
			// A pattern that does not compile was already rejected at
			// provisioning time; ignoring the error here keeps a bad stored
			// value from taking the gateway down at boot.
			_ = credential.SetValueFilter(key, pattern)
		}
	}
	if config.DefaultSourceAddress != nil {
		credential.SetDefaultSourceAddress([]byte(*config.DefaultSourceAddress))
	}
	return credential
}

// ResolveCredential implements the front door's credential lookup.
func (directory *runtimeDirectory) ResolveCredential(username string) (*mtcredential.Credential, bool) {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	credential, ok := directory.credentials[username]
	return credential, ok
}

// removeUser deletes a provisioned user and its password hash.
func (directory *runtimeDirectory) removeUser(username string) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if _, ok := directory.passwordHashes[username]; !ok {
		return fmt.Errorf("%w: user %q not found", ErrInvalidRuntimeConfig, username)
	}
	if err := directory.users.RemoveUser(username); err != nil {
		return err
	}
	delete(directory.passwordHashes, username)
	delete(directory.userDisabled, username)
	delete(directory.userGroup, username)
	delete(directory.credentials, username)
	delete(directory.throughput, username)
	delete(directory.dlrGate, username)
	directory.setDLRGateKeyLocked(username, dlrGateKey{})
	delete(directory.provisionedUsers, username)
	// Drop any unconsumed durable row for this name too. Otherwise creating a
	// *new* account that reuses a deleted one's username could silently inherit
	// the deleted customer's spent balance. The row itself is left in the store
	// (removal has no context to do I/O with, and the admin plane owns account
	// lifecycle); it is simply no longer restorable in this process.
	if scoped, known := directory.restore[billing.QuotaScopeUser]; known {
		delete(scoped, username)
	}
	return nil
}

// removeGroup drops an admin-provisioned group from the directory.
func (directory *runtimeDirectory) removeGroup(gid string) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if _, known := directory.groups[gid]; !known {
		return fmt.Errorf("%w: unknown group %q", ErrInvalidRuntimeConfig, gid)
	}
	delete(directory.groups, gid)
	delete(directory.groupIDs, gid)
	delete(directory.groupDisabled, gid)
	delete(directory.provisionedGroups, gid)
	if scoped, known := directory.restore[billing.QuotaScopeGroup]; known {
		delete(scoped, gid)
	}
	return nil
}

// configGroupIDs extracts the config-owned gids.
func configGroupIDs(groups []GroupConfig) []string {
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.GID)
	}
	return ids
}

// resolveUID resolves a configured username to its internal billing uid, for
// route user-filters. An unknown username yields false.
func (directory *runtimeDirectory) resolveUID(username string) (int64, bool) {
	user, err := directory.users.GetUser(username)
	if err != nil {
		return 0, false
	}
	return user.UID(), true
}

func (directory *runtimeDirectory) Authenticate(_ context.Context, username, password string) error {
	directory.mu.RLock()
	expected, ok := directory.passwordHashes[username]
	directory.mu.RUnlock()
	if !ok {
		return core.ErrAuthentication
	}
	var actual [sha256.Size]byte
	size := sha256.Size
	if expected.algorithm == "md5" {
		digest := md5.Sum([]byte(password))
		copy(actual[:], digest[:])
		size = md5.Size
	} else {
		actual = sha256.Sum256([]byte(password))
	}
	if subtle.ConstantTimeCompare(actual[:size], expected.value[:size]) != 1 {
		return core.ErrAuthentication
	}
	return directory.authenticateEnabled(username)
}

func (directory *runtimeDirectory) AuthenticateDigest(_ context.Context, username string, digest []byte) error {
	directory.mu.RLock()
	expected, ok := directory.passwordHashes[username]
	directory.mu.RUnlock()
	if !ok || expected.algorithm != "sha256" {
		return core.ErrAuthentication
	}
	if len(digest) != sha256.Size || subtle.ConstantTimeCompare(digest, expected.value[:]) != 1 {
		return core.ErrAuthentication
	}
	return directory.authenticateEnabled(username)
}

func (directory *runtimeDirectory) authenticateEnabled(username string) error {
	directory.mu.RLock()
	_, stillKnown := directory.passwordHashes[username]
	userDisabled := directory.userDisabled[username]
	groupDisabled := false
	if gid, grouped := directory.userGroup[username]; grouped && gid != "" {
		disabled, known := directory.groupDisabled[gid]
		// Fail closed on a dangling group reference. Legacy cascades a group
		// removal to its users (router.py perspective_group_remove), so a user
		// still pointing at a gid that is no longer installed can only mean the
		// group vanished under us. Reading the zero value here would silently
		// promote a *disabled* group's users back to "no group ceiling" and let
		// a suspended account authenticate.
		groupDisabled = !known || disabled
	}
	directory.mu.RUnlock()
	if !stillKnown {
		return core.ErrAuthentication
	}
	// Legacy refuses a disabled user, and a user whose group is disabled, with
	// the same authentication failure rather than a distinct message.
	if userDisabled || groupDisabled {
		return core.ErrAuthentication
	}
	return nil
}

func (directory *runtimeDirectory) Balance(_ context.Context, username string) (core.BalanceSnapshot, error) {
	user, err := directory.users.GetUser(username)
	if err != nil {
		return core.BalanceSnapshot{}, err
	}
	state := user.GetState()
	result := core.BalanceSnapshot{}
	if state.Balance != nil {
		value := strconv.FormatFloat(*state.Balance, 'f', -1, 64)
		result.Balance = &value
	}
	if state.SubmitSmCountQuota != nil {
		value := strconv.Itoa(*state.SubmitSmCountQuota)
		result.SMSCount = &value
	}
	return result, nil
}

// Rate quotes what one submit to this destination would cost the user. It
// resolves the destination through the same live MT table the submit path
// selects on, because a quote that ignored the destination priced every message
// at the default route -- and kept quoting the boot-time table after an operator
// changed routing. The default rate remains the answer when nothing matches, so
// the endpoint's error surface is unchanged.
func (directory *runtimeDirectory) Rate(_ context.Context, username, destination string) (core.RateQuote, error) {
	user, err := directory.users.GetUser(username)
	if err != nil {
		return core.RateQuote{}, err
	}
	quote := core.RateQuote{UnitRate: directory.defaultRate, SubmitSMCount: 1}
	if directory.routes == nil {
		return quote, nil
	}
	state := user.GetState()
	var groupID int64
	if state.GID != nil {
		groupID = *state.GID
	}
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		UserID:          user.UID(),
		GroupID:         groupID,
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(destination)},
		Timestamp:       time.Now(),
	})
	if err != nil {
		return core.RateQuote{}, fmt.Errorf("%w: %v", core.ErrInvalidParameter, err)
	}
	route, found, err := directory.routes.Select(routable)
	if err != nil {
		return core.RateQuote{}, err
	}
	if found {
		quote.UnitRate = route.Rate()
	}
	return quote, nil
}

func LoadConfig(path string) (Config, error) {
	if path == "" {
		return Config{}, fmt.Errorf("%w: empty config path", ErrInvalidRuntimeConfig)
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
			return Config{}, fmt.Errorf("%w: trailing JSON content", ErrInvalidRuntimeConfig)
		}
		return Config{}, fmt.Errorf("decode trailing config content: %w", err)
	}
	return config, nil
}

// ValidateConfig validates all bootstrap state without opening sockets or
// starting the trusted bridge.
func ValidateConfig(config Config) error {
	if err := validateConfig(config); err != nil {
		return err
	}
	directory, err := newRuntimeDirectory(config)
	if err != nil {
		return err
	}
	_, _, _, err = buildRoutes(config.Routes, directory.resolveUID, directory.lookupGroupID)
	return err
}
