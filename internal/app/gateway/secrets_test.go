package gateway

import (
	"errors"
	"github.com/pumpitspace/synevyr/internal/core/termination"
	"os"
	"path/filepath"
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
)

func TestResolveSecretValue(t *testing.T) {
	t.Setenv("GATEWAY_SECRET_TEST", "from-env")
	secretFile := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{"plain_value_untouched", "amqp://guest:guest@rabbitmq:5672/", "amqp://guest:guest@rabbitmq:5672/", false},
		{"empty_value_untouched", "", "", false},
		{"env_resolves", "env:GATEWAY_SECRET_TEST", "from-env", false},
		{"env_unset_fails", "env:GATEWAY_SECRET_TEST_MISSING", "", true},
		{"env_empty_name_fails", "env:", "", true},
		{"file_resolves_trimmed", "file:" + secretFile, "from-file", false},
		{"file_missing_fails", "file:" + secretFile + ".missing", "", true},
		{"file_empty_fails", "file:" + emptyFile, "", true},
		{"literal_passthrough", "literal:env:not-a-ref", "env:not-a-ref", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := resolveSecretValue("field", testCase.value)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("value %q resolved to %q, want error", testCase.value, got)
				}
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("error %v is not ErrInvalidConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != testCase.want {
				t.Fatalf("resolved %q want %q", got, testCase.want)
			}
		})
	}
}

func TestResolveSecretRefsRewritesCredentialFields(t *testing.T) {
	t.Setenv("TEST_PG_DSN", "postgres://real")
	t.Setenv("TEST_AMQP_URL", "amqp://real")
	t.Setenv("TEST_SMSC_PASSWORD", "real-password")
	t.Setenv("TEST_ADMIN_TOKEN", "real-admin-token")

	config := Config{
		Outbound: outbound.Config{
			PostgresDSN: "env:TEST_PG_DSN",
			AMQPURL:     "env:TEST_AMQP_URL",
		},
		Connectors: []smppc.Config{{CID: "c1", Password: "env:TEST_SMSC_PASSWORD"}},
		Admin:      &AdminConfig{Token: "env:TEST_ADMIN_TOKEN"},
	}
	if err := resolveSecretRefs(&config); err != nil {
		t.Fatal(err)
	}
	if config.Outbound.PostgresDSN != "postgres://real" {
		t.Fatalf("postgres_dsn=%q", config.Outbound.PostgresDSN)
	}
	if config.Outbound.AMQPURL != "amqp://real" {
		t.Fatalf("amqp_url=%q", config.Outbound.AMQPURL)
	}
	if config.Connectors[0].Password != "real-password" {
		t.Fatalf("connector password=%q", config.Connectors[0].Password)
	}
	if config.Admin.Token != "real-admin-token" {
		t.Fatalf("admin token=%q", config.Admin.Token)
	}
}

func TestResolveSecretRefsFailsClosed(t *testing.T) {
	config := Config{
		Outbound: outbound.Config{PostgresDSN: "env:GATEWAY_TEST_UNSET_DSN"},
	}
	if err := resolveSecretRefs(&config); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}

// The delivery secret keys the HMAC a downstream application verifies, so it is a
// credential and must be keepable out of gateway.json. It was missing from the
// allowlist, and the failure was quiet: an "env:" value passed --check-config
// because it was non-empty and only failed at real boot when it was parsed as a
// URL, so the only way to configure one was to write it in clear.
func TestResolveSecretRefsCoversTerminationCredentials(t *testing.T) {
	t.Setenv("TERM_REDIS_URL", "redis://:hunter2@127.0.0.1:6379/0")
	t.Setenv("TERM_DELIVERY_SECRET", "s3cret")

	config := &Config{
		TerminationConnectors: &TerminationConfig{
			RedisURL: "env:TERM_REDIS_URL",
			Connectors: []termination.ConnectorConfig{
				{CID: "partner-a-term", Delivery: termination.DeliveryConfig{Secret: "env:TERM_DELIVERY_SECRET"}},
			},
		},
	}
	if err := resolveSecretRefs(config); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := config.TerminationConnectors.RedisURL; got != "redis://:hunter2@127.0.0.1:6379/0" {
		t.Errorf("redis url = %q, want the resolved value", got)
	}
	if got := config.TerminationConnectors.Connectors[0].Delivery.Secret; got != "s3cret" {
		t.Errorf("delivery secret = %q, want the resolved value", got)
	}
}

// A reference that cannot be resolved must fail here, at config load, not at the
// first delivery attempt hours later.
func TestResolveSecretRefsFailsClosedOnTerminationCredentials(t *testing.T) {
	config := &Config{
		TerminationConnectors: &TerminationConfig{
			Connectors: []termination.ConnectorConfig{
				{CID: "partner-a-term", Delivery: termination.DeliveryConfig{Secret: "env:TERM_SECRET_THAT_IS_NOT_SET"}},
			},
		},
	}
	if err := resolveSecretRefs(config); err == nil {
		t.Fatal("want an error for an unresolvable delivery secret, got nil")
	}
}
