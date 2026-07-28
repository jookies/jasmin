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
	"strconv"
	"sync"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
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
	// and the list renders with a "!" prefix.
	Disabled bool `json:"disabled,omitempty"`
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
	// mu guards passwordHashes, read on the hot Authenticate path and written
	// by admin user provisioning. billing.Manager has its own lock.
	mu             sync.RWMutex
	passwordHashes map[string][sha256.Size]byte
}

func newRuntimeDirectory(config Config) (*runtimeDirectory, error) {
	directory := &runtimeDirectory{
		users:          billing.NewManager(),
		passwordHashes: make(map[string][sha256.Size]byte, len(config.Users)),
		groups:         make(map[string]*billing.Group, len(config.Groups)),
		groupIDs:       make(map[string]int64, len(config.Groups)),
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
	group := billing.NewGroup(gid)
	if entry.Balance != nil {
		if err := group.SetBalance(*entry.Balance); err != nil {
			return fmt.Errorf("%w: group %q balance: %v", ErrInvalidRuntimeConfig, entry.GID, err)
		}
	}
	if entry.SubmitSMCount != nil {
		if *entry.SubmitSMCount < 0 {
			return fmt.Errorf("%w: group %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.GID)
		}
		group.SetSubmitSmCountQuota(*entry.SubmitSMCount)
	}

	directory.mu.Lock()
	defer directory.mu.Unlock()
	if _, duplicate := directory.groups[entry.GID]; duplicate {
		return fmt.Errorf("%w: duplicate group %q", ErrInvalidRuntimeConfig, entry.GID)
	}
	directory.groups[entry.GID] = group
	directory.groupIDs[entry.GID] = gid
	return nil
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
	user := billing.NewUser(uid)
	if entry.Balance != nil {
		if err := user.SetBalance(*entry.Balance); err != nil {
			return fmt.Errorf("%w: user %q balance: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
	}
	if entry.SubmitSMCount != nil {
		if *entry.SubmitSMCount < 0 {
			return fmt.Errorf("%w: user %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.Username)
		}
		user.SetSubmitSmCountQuota(*entry.SubmitSMCount)
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
	return nil
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
	actual := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
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
