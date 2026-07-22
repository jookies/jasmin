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
	ConnectorID  string   `json:"connector_id"`
	ConnectorIDs []string `json:"connector_ids,omitempty"`
	Rate         float64  `json:"rate"`
	Default      bool     `json:"default"`
	Order        int      `json:"order"`
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
	users          *billing.Manager
	passwordHashes map[string][sha256.Size]byte
	defaultRate    float64
}

func newRuntimeDirectory(config Config) (*runtimeDirectory, error) {
	directory := &runtimeDirectory{
		users:          billing.NewManager(),
		passwordHashes: make(map[string][sha256.Size]byte, len(config.Users)),
	}
	for index, entry := range config.Users {
		if !legacyUsernamePattern.MatchString(entry.Username) || !legacyUserIDPattern.MatchString(entry.ExternalID) || entry.PasswordSHA256 == "" {
			return nil, fmt.Errorf("%w: user %d identity must match legacy username/uid constraints", ErrInvalidRuntimeConfig, index)
		}
		rawHash, err := hex.DecodeString(entry.PasswordSHA256)
		if err != nil || len(rawHash) != sha256.Size {
			return nil, fmt.Errorf("%w: user %q password_sha256 must be 64 hexadecimal characters", ErrInvalidRuntimeConfig, entry.Username)
		}
		var passwordHash [sha256.Size]byte
		copy(passwordHash[:], rawHash)
		if _, duplicate := directory.passwordHashes[entry.Username]; duplicate {
			return nil, fmt.Errorf("%w: duplicate username %q", ErrInvalidRuntimeConfig, entry.Username)
		}
		user := billing.NewUser(int64(index + 1))
		if entry.Balance != nil {
			if err := user.SetBalance(*entry.Balance); err != nil {
				return nil, fmt.Errorf("%w: user %q balance: %v", ErrInvalidRuntimeConfig, entry.Username, err)
			}
		}
		if entry.SubmitSMCount != nil {
			if *entry.SubmitSMCount < 0 {
				return nil, fmt.Errorf("%w: user %q negative submit_sm_count", ErrInvalidRuntimeConfig, entry.Username)
			}
			user.SetSubmitSmCountQuota(*entry.SubmitSMCount)
		}
		if entry.EarlyDecrementBalancePercent != nil {
			if err := user.SetEarlyDecrementPercent(*entry.EarlyDecrementBalancePercent); err != nil {
				return nil, fmt.Errorf("%w: user %q early percentage: %v", ErrInvalidRuntimeConfig, entry.Username, err)
			}
		}
		if err := directory.users.AddUserWithID(entry.Username, entry.ExternalID, user); err != nil {
			return nil, fmt.Errorf("%w: user %q: %v", ErrInvalidRuntimeConfig, entry.Username, err)
		}
		directory.passwordHashes[entry.Username] = passwordHash
	}
	return directory, nil
}

func (directory *runtimeDirectory) Authenticate(_ context.Context, username, password string) error {
	expected, ok := directory.passwordHashes[username]
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
	if _, err := newRuntimeDirectory(config); err != nil {
		return err
	}
	_, _, _, err := buildRoutes(config.Routes)
	return err
}
