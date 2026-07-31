package gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/dlrlookup"
	"github.com/pumpitspace/synevyr/internal/app/gateway"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/termination"
)

func terminationBaseConfig() gateway.Config {
	hash := sha256.Sum256([]byte("secret"))
	return gateway.Config{
		Role: gateway.RoleHTTPAndSMPPc,
		Outbound: outbound.Config{
			ListenAddress: "127.0.0.1:0",
			AMQPURL:       "amqp://localhost",
			PythonPath:    "python3",
			PostgresDSN:   "postgres://localhost/test",
			Users: []outbound.UserConfig{{
				Username: "partner-a", ExternalID: "user1", PasswordSHA256: hex.EncodeToString(hash[:]),
			}},
			Routes: []outbound.RouteConfig{{ConnectorID: "smsc-a", Default: true}},
		},
		Connectors: []smppc.Config{{CID: "smsc-a", Host: "127.0.0.1", Port: 2775, SystemID: "system"}},
		// A termination connector's legs are unroutable without the DLRLookup
		// queue, so the section requires it.
		DLRLookup: &dlrlookup.Config{AMQPURL: "amqp://localhost", RedisURL: "redis://127.0.0.1:6379/0", PID: "main"},
	}
}

func terminationSection(connectors ...termination.ConnectorConfig) *gateway.TerminationConfig {
	return &gateway.TerminationConfig{
		Connectors: connectors,
		RedisURL:   "redis://127.0.0.1:6379/0",
	}
}

func staticTerminationConnector(cid string) termination.ConnectorConfig {
	return termination.ConnectorConfig{
		CID:     cid,
		Verdict: termination.VerdictConfig{Source: termination.SourceStatic},
	}
}

// TestValidateConfigResolvesTermRoutesAgainstTerminationConnectors is the check
// that keeps a "term" route from being validated against the SMPP connector
// list, where it would either be rejected as missing or — worse — accepted
// because an unrelated SMPP connector happens to share the id.
func TestValidateConfigResolvesTermRoutesAgainstTerminationConnectors(t *testing.T) {
	config := terminationBaseConfig()
	config.TerminationConnectors = terminationSection(staticTerminationConnector("partner-a-term"))
	config.Outbound.Routes = []outbound.RouteConfig{
		{ConnectorID: "partner-a-term", ConnectorType: "term", Default: true},
	}
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("term route against a declared termination connector: %v", err)
	}

	// The same cid on the wrong connector type is a missing reference.
	config.Outbound.Routes[0].ConnectorType = ""
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("an smppc route pointing at a termination connector was accepted")
	}

	config.Outbound.Routes[0].ConnectorType = "term"
	config.Outbound.Routes[0].ConnectorID = "smsc-a"
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("a term route pointing at an SMPP client connector was accepted")
	}

	config.Outbound.Routes[0].ConnectorID = "partner-a-term"
	config.Outbound.Routes[0].ConnectorType = "http"
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("an unknown route connector_type was accepted")
	}
}

func TestValidateConfigRejectsCollidingConnectorIDs(t *testing.T) {
	config := terminationBaseConfig()
	// One cid, two connector types: both would consume submit.sm.<cid> and each
	// message would land wherever the race fell.
	config.TerminationConnectors = terminationSection(staticTerminationConnector("smsc-a"))
	err := gateway.ValidateConfig(config)
	if err == nil {
		t.Fatal("a termination connector colliding with an SMPPc cid was accepted")
	}
	if !strings.Contains(err.Error(), "collides") {
		t.Fatalf("error = %v, want it to name the collision", err)
	}

	config.TerminationConnectors = terminationSection(
		staticTerminationConnector("partner-a-term"),
		staticTerminationConnector("partner-a-term"),
	)
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("a duplicate termination connector was accepted")
	}
}

// TestValidateConfigRequiresAGateForRedisWindow refuses the configuration whose
// only failure mode is invisible: a redis-window connector with no Redis fails
// open on every message, telling every partner DELIVRD.
func TestValidateConfigRequiresAGateForRedisWindow(t *testing.T) {
	config := terminationBaseConfig()
	config.DLRLookup.RedisURL = ""
	config.TerminationConnectors = &gateway.TerminationConfig{
		Connectors: []termination.ConnectorConfig{{
			CID:     "partner-a-term",
			Verdict: termination.VerdictConfig{Source: termination.SourceRedisWindow},
		}},
	}
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("a redis-window connector without a gate was accepted")
	}

	// The DLR lookup's Redis is inherited when the section names none.
	config.DLRLookup.RedisURL = "redis://127.0.0.1:6379/0"
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("redis-window inheriting dlr_lookup.redis_url: %v", err)
	}
	if got := config.ResolvedRedisURL(); got != "redis://127.0.0.1:6379/0" {
		t.Fatalf("resolved redis url = %q", got)
	}

	// Its own URL wins over the inherited one.
	config.TerminationConnectors.RedisURL = "redis://127.0.0.1:6380/1"
	if got := config.ResolvedRedisURL(); got != "redis://127.0.0.1:6380/1" {
		t.Fatalf("resolved redis url = %q, want the section's own", got)
	}
}

// TestValidateConfigAllowsATerminationOnlyGateway is the deployment this
// feature exists for: partners bind to the SMPPs server and every message
// terminates locally, so there is no upstream SMPP client connector at all.
func TestValidateConfigAllowsATerminationOnlyGateway(t *testing.T) {
	config := terminationBaseConfig()
	config.Connectors = nil
	config.TerminationConnectors = terminationSection(staticTerminationConnector("partner-a-term"))
	config.Outbound.Routes = []outbound.RouteConfig{
		{ConnectorID: "partner-a-term", ConnectorType: "term", Default: true},
	}
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("termination-only gateway: %v", err)
	}

	config.TerminationConnectors = nil
	config.Outbound.Routes = nil
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("a gateway with no connectors of either kind was accepted")
	}
}

// TestValidateConfigRequiresDLRLookupForTermination turns an invisible runtime
// failure into a startup one: the synthesized legs are published mandatory, so
// without the DLRLookup queue every terminated message requeues forever and no
// partner ever receives a receipt.
func TestValidateConfigRequiresDLRLookupForTermination(t *testing.T) {
	config := terminationBaseConfig()
	config.TerminationConnectors = terminationSection(staticTerminationConnector("partner-a-term"))
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("termination with dlr_lookup: %v", err)
	}
	config.DLRLookup = nil
	err := gateway.ValidateConfig(config)
	if err == nil {
		t.Fatal("termination connectors without dlr_lookup were accepted")
	}
	if !strings.Contains(err.Error(), "dlr_lookup") {
		t.Fatalf("error = %v, want it to name dlr_lookup", err)
	}
}

func TestValidateConfigRejectsUnusableTerminationTimings(t *testing.T) {
	cases := map[string]func(*gateway.TerminationConfig){
		"negative retention":       func(c *gateway.TerminationConfig) { c.RetentionHours = -1 },
		"negative prune interval":  func(c *gateway.TerminationConfig) { c.PruneIntervalSeconds = -1 },
		"negative receipt lease":   func(c *gateway.TerminationConfig) { c.ReceiptLeaseSeconds = -1 },
		"oversized retention":      func(c *gateway.TerminationConfig) { c.RetentionHours = 1e18 },
		"oversized prune interval": func(c *gateway.TerminationConfig) { c.PruneIntervalSeconds = 1e18 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := terminationBaseConfig()
			config.TerminationConnectors = terminationSection(staticTerminationConnector("partner-a-term"))
			mutate(config.TerminationConnectors)
			if err := gateway.ValidateConfig(config); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestValidateConfigRejectsInvalidTerminationConnector(t *testing.T) {
	config := terminationBaseConfig()
	// An empty verdict source must never default to "accept everything": an
	// accept cannot be un-sent.
	config.TerminationConnectors = terminationSection(termination.ConnectorConfig{CID: "partner-a-term"})
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("a connector with no verdict source was accepted")
	}

	config.TerminationConnectors = terminationSection(termination.ConnectorConfig{
		CID:      "partner-a-term",
		Verdict:  termination.VerdictConfig{Source: termination.SourceStatic},
		Delivery: termination.DeliveryConfig{Endpoint: "ftp://app.example.com/messages"},
	})
	if err := gateway.ValidateConfig(config); err == nil {
		t.Fatal("a non-HTTP delivery endpoint was accepted")
	}
}

func TestTerminationCIDsListsTheReservedSet(t *testing.T) {
	config := terminationBaseConfig()
	config.TerminationConnectors = terminationSection(
		staticTerminationConnector("partner-a-term"),
		staticTerminationConnector("partner-b-term"),
	)
	got := config.TerminationCIDs()
	if len(got) != 2 || got[0] != "partner-a-term" || got[1] != "partner-b-term" {
		t.Fatalf("termination cids = %v", got)
	}
	if len(gateway.Config{}.TerminationCIDs()) != 0 {
		t.Fatal("a config with no termination section must reserve nothing")
	}
}

// TestTerminationDurationsAreNanoseconds pins the JSON shape an operator has to
// type, because it is the least obvious thing in this section.
func TestTerminationDurationsAreNanoseconds(t *testing.T) {
	config := terminationBaseConfig()
	config.TerminationConnectors = terminationSection(termination.ConnectorConfig{
		CID:          "partner-a-term",
		Verdict:      termination.VerdictConfig{Source: termination.SourceStatic},
		ReceiptDelay: 5 * time.Second,
	})
	if err := gateway.ValidateConfig(config); err != nil {
		t.Fatalf("explicit receipt delay: %v", err)
	}
	if got := config.TerminationConnectors.Connectors[0].ReceiptDelay; got != 5*time.Second {
		t.Fatalf("receipt delay = %s", got)
	}
}
