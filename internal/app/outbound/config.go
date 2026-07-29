package outbound

import (
	"bytes"
	"context"
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

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/mtcredential"
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
	Username                     string   `json:"username"`
	ExternalID                   string   `json:"external_id"`
	PasswordSHA256               string   `json:"password_sha256"`
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
	ConnectorID  string         `json:"connector_id"`
	ConnectorIDs []string       `json:"connector_ids,omitempty"`
	Rate         float64        `json:"rate"`
	Default      bool           `json:"default"`
	Order        int            `json:"order"`
	Filters      []FilterConfig `json:"filters,omitempty"`
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
	users       *billing.Manager
	defaultRate float64
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
	passwordHashes map[string][sha256.Size]byte
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
		passwordHashes:    make(map[string][sha256.Size]byte, len(config.Users)),
		groups:            make(map[string]*billing.Group, len(config.Groups)),
		groupIDs:          make(map[string]int64, len(config.Groups)),
		groupDisabled:     make(map[string]bool, len(config.Groups)),
		userDisabled:      make(map[string]bool, len(config.Users)),
		userGroup:         make(map[string]string, len(config.Users)),
		credentials:       make(map[string]*mtcredential.Credential, len(config.Users)),
		throughput:        make(map[string]userThroughput, len(config.Users)),
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

// applyUser validates a user entry (legacy identity + password + billing state)
// and installs it with the given internal uid. Shared by boot and admin
// provisioning; safe for concurrent use.
func (directory *runtimeDirectory) applyUser(entry UserConfig, uid int64) error {
	if !legacyUsernamePattern.MatchString(entry.Username) || !legacyUserIDPattern.MatchString(entry.ExternalID) || entry.PasswordSHA256 == "" {
		return fmt.Errorf("%w: user %q identity must match legacy username/uid constraints", ErrInvalidRuntimeConfig, entry.Username)
	}
	rawHash, err := hex.DecodeString(entry.PasswordSHA256)
	if err != nil || len(rawHash) != sha256.Size {
		return fmt.Errorf("%w: user %q password_sha256 must be 64 hexadecimal characters", ErrInvalidRuntimeConfig, entry.Username)
	}
	var passwordHash [sha256.Size]byte
	copy(passwordHash[:], rawHash)
	if entry.SubmitSMCount != nil && *entry.SubmitSMCount < 0 {
		return fmt.Errorf("%w: user %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.Username)
	}
	// Boot restore: the balance the customer has spent down to outranks the
	// provisioned grant, unless the operator changed that grant since it was
	// last flushed. See takeRestoredQuota and billing.QuotaRecord.Restore for
	// the full precedence rule and the top-up gesture it preserves.
	provisioned := billing.Quota{Balance: entry.Balance, SubmitSmCount: entry.SubmitSMCount}
	effective := directory.takeRestoredQuota(billing.QuotaScopeUser, entry.Username, provisioned)
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
	if !ok {
		return core.ErrAuthentication
	}
	actual := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
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

func (directory *runtimeDirectory) Rate(_ context.Context, username, _ string) (core.RateQuote, error) {
	if _, err := directory.users.GetUser(username); err != nil {
		return core.RateQuote{}, err
	}
	return core.RateQuote{UnitRate: directory.defaultRate, SubmitSMCount: 1}, nil
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
	_, _, _, err = buildRoutes(config.Routes, directory.resolveUID)
	return err
}
