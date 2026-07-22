package gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

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
}
