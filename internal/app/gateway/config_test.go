package gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/gateway"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

func TestValidateConfigRequiresRouteConnectorClosure(t *testing.T) {
	hash := sha256.Sum256([]byte("secret"))
	config := gateway.Config{
		Role: gateway.RoleHTTPAndSMPPc,
		Outbound: outbound.Config{
			ListenAddress: "127.0.0.1:0", AMQPURL: "amqp://localhost", PythonPath: "python3", PostgresDSN: "postgres://localhost/test",
			Users:  []outbound.UserConfig{{Username: "alice", ExternalID: "user1", PasswordSHA256: hex.EncodeToString(hash[:])}},
			Routes: []outbound.RouteConfig{{ConnectorID: "missing", Default: true}},
		},
		Connectors: []smppc.Config{{CID: "present", Host: "127.0.0.1", Port: 2775, SystemID: "system"}},
	}
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("missing routed connector was accepted")
	}
	config.Outbound.Routes[0].ConnectorID = "present"
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("valid gateway config: %v", err)
	}
	config.HA = &gateway.HAConfig{}
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("HA without a namespace was accepted")
	}
	config.HA.Namespace = "production"
	config.HA.StandbyListenAddress = config.Outbound.ListenAddress
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("valid HA gateway config: %v", err)
	}
	config.Admin = &gateway.AdminConfig{
		DBPath: "admin.db",
		Token:  "admin-token",
	}
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("valid admin config: %v", err)
	}
	config.Admin.DBPath = ""
	config.HA.StandbyRetrySeconds = 0.01
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("HA PostgreSQL admin store without SQLite path: %v", err)
	}
	if got := config.HA.StandbyRetryInterval(); got != 10*time.Millisecond {
		t.Fatalf("standby retry=%s", got)
	}
	config.HA.StandbyListenAddress = ""
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("HA without standby health listener was accepted")
	}
	config.HA.StandbyListenAddress = config.Outbound.ListenAddress
	config.REST.ListenAddress = "0.0.0.0:0"
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("wildcard REST listener colliding with public HTTP was accepted")
	}
	// The PB facade listener is gone; the admin API listener is the remaining
	// loopback surface a REST listener can collide with.
	config.Admin.APIListenAddress = "127.0.0.1:8405"
	config.REST.ListenAddress = "127.0.0.1:8405"
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("REST listener colliding with the admin API was accepted")
	}
	config.REST.ListenAddress = "127.0.0.1:8080"
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("valid distinct REST listener: %v", err)
	}
	config.Admin.JCliListenAddress = "127.0.0.1:8990"
	config.Admin.JCliUsername = "jcli"
	config.Admin.JCliPassword = "secret"
	config.HA.StandbyListenAddress = "0.0.0.0:8990"
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("standby listener overlapping jCli during promotion was accepted")
	}
}
