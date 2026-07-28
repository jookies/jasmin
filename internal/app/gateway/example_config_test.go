package gateway_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/app/gateway"
)

const (
	exampleConfigPath = "../../../configs/gateway.example.json"
	composeFilePath   = "../../../docker-compose.gateway.yml"
)

// The shipped example config is what docker-compose.gateway.yml mounts into the
// gateway container, so a config the current binary cannot load is a broken dev
// stack — and LoadConfig decodes with DisallowUnknownFields, meaning a key added
// to the example before the struct (or left behind after a rename) fails at boot
// rather than at review. That has bitten twice: once when the example gained
// `pickle_codec` while the running image predated it, and once when
// `web_password` referenced an env var compose did not set.
//
// This test is the guard: it loads the real file the way the binary does.
func TestExampleConfigLoads(t *testing.T) {
	// Secret refs must resolve for LoadConfig to succeed; these mirror the
	// values docker-compose.gateway.yml supplies.
	t.Setenv("ADMIN_TOKEN", "dev-admin-token")
	t.Setenv("ADMIN_WEB_PASSWORD", "dev-admin-password")
	t.Setenv("JCLI_PASSWORD", "dev-jcli-password")

	config, err := gateway.LoadConfig(exampleConfigPath)
	if err != nil {
		t.Fatalf("configs/gateway.example.json does not load: %v", err)
	}

	// Spot-check that the example still exercises the surfaces it documents, so
	// a silent deletion is caught too.
	if config.Admin == nil || config.Admin.WebListenAddress == "" {
		t.Fatal("example config no longer configures the admin plane and web UI")
	}
	if len(config.Connectors) == 0 {
		t.Fatal("example config no longer declares a connector")
	}
	if len(config.MORoutes) == 0 {
		t.Fatal("example config no longer declares MO routes")
	}
}

var envRefPattern = regexp.MustCompile(`env:([A-Za-z_][A-Za-z0-9_]*)`)

// Every environment variable the example references through a secret ref must be
// one the compose stack actually provides, or the container fails to boot with a
// "resolve secret" error that looks nothing like the root cause.
func TestExampleConfigSecretRefsAreProvidedByCompose(t *testing.T) {
	configData, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	composeData, err := os.ReadFile(composeFilePath)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(composeFilePath), err)
	}
	compose := string(composeData)

	matches := envRefPattern.FindAllStringSubmatch(string(configData), -1)
	if len(matches) == 0 {
		t.Skip("example config uses no env: secret references")
	}
	for _, match := range matches {
		name := match[1]
		if !strings.Contains(compose, name+":") {
			t.Errorf("configs/gateway.example.json references env:%s but docker-compose.gateway.yml never sets it", name)
		}
	}
}
