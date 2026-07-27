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
)

type Config struct {
	ListenAddress string        `json:"listen_address"`
	AMQPURL       string        `json:"amqp_url"`
	PythonPath    string        `json:"python_path"`
	PostgresDSN   string        `json:"postgres_dsn"`
	Users         []UserConfig  `json:"users"`
	Routes        []RouteConfig `json:"routes"`
	// AMQPDurableTopology declares exchanges/queues durable (queued submits
	// survive a broker restart). Must match what the vhost already holds —
	// the legacy stack declares non-durable and AMQP 406s a mismatched
	// redeclare. The gateway propagates its top-level flag here.
	AMQPDurableTopology bool `json:"amqp_durable_topology,omitempty"`
}

type UserConfig struct {
	Username                     string   `json:"username"`
	ExternalID                   string   `json:"external_id"`
	PasswordSHA256               string   `json:"password_sha256"`
	Balance                      *float64 `json:"balance"`
	SubmitSMCount                *int     `json:"submit_sm_count"`
	EarlyDecrementBalancePercent *int     `json:"early_decrement_balance_percent"`
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
	// mu guards passwordHashes, read on the hot Authenticate path and written
	// by admin user provisioning. billing.Manager has its own lock.
	mu             sync.RWMutex
	passwordHashes map[string][sha256.Size]byte
}

func newRuntimeDirectory(config Config) (*runtimeDirectory, error) {
	directory := &runtimeDirectory{
		users:          billing.NewManager(),
		passwordHashes: make(map[string][sha256.Size]byte, len(config.Users)),
	}
	for index, entry := range config.Users {
		if err := directory.applyUser(entry, int64(index+1)); err != nil {
			return nil, err
		}
	}
	return directory, nil
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
