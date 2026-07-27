package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
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

	config := Config{
		Outbound: outbound.Config{
			PostgresDSN: "env:TEST_PG_DSN",
			AMQPURL:     "env:TEST_AMQP_URL",
		},
		Connectors: []smppc.Config{{CID: "c1", Password: "env:TEST_SMSC_PASSWORD"}},
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
}

func TestResolveSecretRefsFailsClosed(t *testing.T) {
	config := Config{
		Outbound: outbound.Config{PostgresDSN: "env:GATEWAY_TEST_UNSET_DSN"},
	}
	if err := resolveSecretRefs(&config); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}
