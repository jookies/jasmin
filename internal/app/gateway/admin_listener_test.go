package gateway_test

import (
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/gateway"
)

// /admin/ creates users, changes balances and starts and stops connectors. It
// shared the public sendsms mux, so publishing the send port also published it,
// guarded only by a bearer token -- while the admin web UI and jCli were
// deliberately given their own loopback listeners. api_listen_address closes
// that gap.
func TestAdminAPIListenAddressIsValidated(t *testing.T) {
	load := func(t *testing.T) gateway.Config {
		t.Helper()
		t.Setenv("ADMIN_TOKEN", "dev-admin-token")
		t.Setenv("ADMIN_WEB_PASSWORD", "dev-admin-password")
		t.Setenv("JCLI_PASSWORD", "dev-jcli-password")
		config, err := gateway.LoadConfig("../../../configs/gateway.example.json")
		if err != nil {
			t.Fatalf("example config does not load: %v", err)
		}
		if config.Admin == nil {
			t.Skip("example config has no admin section")
		}
		return config
	}

	for _, address := range []string{"127.0.0.1:8405", ":8405"} {
		config := load(t)
		config.Admin.APIListenAddress = address
		if err := gateway.ValidateConfig(config); err != nil {
			t.Errorf("api_listen_address %q rejected: %v", address, err)
		}
	}

	config := load(t)
	config.Admin.APIListenAddress = "not-a-host-port"
	if err := gateway.ValidateConfig(config); err == nil {
		t.Error("a malformed api_listen_address must be rejected at config load")
	}
}
