package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/termination"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
)

type planeStubPublisher struct{}

func (planeStubPublisher) Publish(context.Context, string, string, amqpcompat.Envelope) error {
	return nil
}

func planeTestConfig(t *testing.T, connectors ...termination.ConnectorConfig) Config {
	t.Helper()
	return Config{
		Outbound: outbound.Config{AMQPURL: "amqp://127.0.0.1:5672/"},
		TerminationConnectors: &TerminationConfig{
			Connectors:  connectors,
			SpoolDBPath: filepath.Join(t.TempDir(), "spool.db"),
			RedisURL:    "redis://127.0.0.1:6379/0",
		},
	}
}

func newPlaneForTest(t *testing.T, config Config) *terminationPlane {
	t.Helper()
	plane, err := newTerminationPlane(context.Background(), terminationDeps{
		Config:    config,
		Publisher: planeStubPublisher{},
		Submits:   picklecompat.NewNativeCodec(),
	})
	if err != nil {
		t.Fatalf("build termination plane: %v", err)
	}
	if plane == nil {
		t.Fatal("plane is nil for a configured section")
	}
	t.Cleanup(func() { _ = plane.Close() })
	return plane
}

func TestTerminationPlaneIsNilWithoutASection(t *testing.T) {
	plane, err := newTerminationPlane(context.Background(), terminationDeps{
		Config:    Config{},
		Publisher: planeStubPublisher{},
		Submits:   picklecompat.NewNativeCodec(),
	})
	if err != nil {
		t.Fatalf("no section: %v", err)
	}
	if plane != nil {
		t.Fatal("a config with no termination section must build no plane")
	}
}

// TestTerminationPlaneReservesConfigConnectors is the config-owns-it boundary:
// the admin plane may start and stop a config-declared connector, never delete
// or rewrite it.
func TestTerminationPlaneReservesConfigConnectors(t *testing.T) {
	config := planeTestConfig(t, termination.ConnectorConfig{
		CID:     "partner-a-term",
		Verdict: termination.VerdictConfig{Source: termination.SourceStatic},
	})
	plane := newPlaneForTest(t, config)

	manager := plane.Manager()
	if list := manager.List(); len(list) != 1 || list[0].CID != "partner-a-term" {
		t.Fatalf("managed connectors = %+v", list)
	}
	if !manager.Reserved("partner-a-term") {
		t.Fatal("a config-declared connector must be reserved")
	}
	if err := manager.Remove("partner-a-term"); !errors.Is(err, termination.ErrReserved) {
		t.Fatalf("remove reserved error = %v, want ErrReserved", err)
	}
	// The spool service exists and carries the configured retention, so a prune
	// actually bounds the OTP content this connector stores.
	if plane.Spool() == nil {
		t.Fatal("the plane must expose the audited spool service")
	}
	if got := plane.Spool().Retention().Window; got != 24*time.Hour {
		t.Fatalf("retention window = %s, want the 24 h default", got)
	}
	if plane.owner == "" {
		t.Fatal("the receipt runner needs a per-process owner")
	}
}

// TestTerminationPlaneSkipsTheDeliveryRunnerWhenNothingPushes keeps a pull-only
// deployment's rows pending for their retention instead of counting attempts
// nobody made and dead-lettering every one of them.
func TestTerminationPlaneSkipsTheDeliveryRunnerWhenNothingPushes(t *testing.T) {
	pullOnly := newPlaneForTest(t, planeTestConfig(t, termination.ConnectorConfig{
		CID:     "partner-a-term",
		Verdict: termination.VerdictConfig{Source: termination.SourceStatic},
	}))
	if pullOnly.delivery != nil {
		t.Fatal("no connector has an endpoint; the delivery runner must not run")
	}

	pushing := newPlaneForTest(t, planeTestConfig(t, termination.ConnectorConfig{
		CID:      "partner-b-term",
		Verdict:  termination.VerdictConfig{Source: termination.SourceStatic},
		Delivery: termination.DeliveryConfig{Endpoint: "https://app.example.com/messages"},
	}))
	if pushing.delivery == nil {
		t.Fatal("a connector with an endpoint must get a delivery runner")
	}
}

// TestTerminationPlaneRejectsAnUnbuildableConnector fails startup rather than
// running a gateway whose route points at a connector that does not exist.
func TestTerminationPlaneRejectsAnUnbuildableConnector(t *testing.T) {
	config := planeTestConfig(t, termination.ConnectorConfig{
		CID: "partner-a-term",
		// redis-window with no Redis: the source cannot be built, and a
		// connector that silently fell back would fail open on every message.
		Verdict: termination.VerdictConfig{Source: termination.SourceRedisWindow},
	})
	config.TerminationConnectors.RedisURL = ""
	_, err := newTerminationPlane(context.Background(), terminationDeps{
		Config:    config,
		Publisher: planeStubPublisher{},
		Submits:   picklecompat.NewNativeCodec(),
	})
	if err == nil {
		t.Fatal("a redis-window connector with no gate was accepted")
	}
}

func TestTerminationPlaneRequiresASpoolBackend(t *testing.T) {
	config := planeTestConfig(t)
	config.TerminationConnectors.SpoolDBPath = ""
	_, err := newTerminationPlane(context.Background(), terminationDeps{
		Config:    config,
		Publisher: planeStubPublisher{},
		Submits:   picklecompat.NewNativeCodec(),
	})
	if err == nil {
		t.Fatal("a plane with neither a DSN nor a SQLite path was accepted")
	}
}

// TestAvailabilityGateCombinesBothConnectorTypes is what MT route selection
// consults. Before the termination manager is installed the gate must answer
// exactly as it did before this feature existed.
func TestAvailabilityGateCombinesBothConnectorTypes(t *testing.T) {
	gate := &availabilityGate{smppc: func(cid string) bool { return cid == "smsc-a" }}
	if !gate.Available("smsc-a") {
		t.Fatal("a bound SMPP client connector must be routable")
	}
	if gate.Available("partner-a-term") {
		t.Fatal("no termination manager is installed yet; nothing else is routable")
	}

	plane := newPlaneForTest(t, planeTestConfig(t, termination.ConnectorConfig{
		CID:     "partner-a-term",
		Verdict: termination.VerdictConfig{Source: termination.SourceStatic},
	}))
	gate.install(plane.Manager())
	if gate.Available("partner-a-term") {
		t.Fatal("an unstarted termination connector must not be routable")
	}
	if !gate.Available("smsc-a") {
		t.Fatal("installing the termination manager must not change the SMPP answer")
	}
	if gate.Available("unknown") {
		t.Fatal("an unknown cid is never routable")
	}
}

// TestAvailabilityGateToleratesNoSMPPcSide covers the termination-only gateway,
// which has no SMPP client connectors at all.
func TestAvailabilityGateToleratesNoSMPPcSide(t *testing.T) {
	gate := &availabilityGate{}
	if gate.Available("anything") {
		t.Fatal("an empty gate routes nothing")
	}
	gate.install(nil)
	if gate.Available("anything") {
		t.Fatal("installing a nil manager must not change the answer")
	}
}

// The console reaches termination connectors only if the composition root builds
// the admin service and hands it to both the REST handler and the web BFF. That
// wiring was missing at first: the whole plane ran, the UI existed, and every
// console call answered 404 "not enabled on this gateway" — a feature that is
// present, working, and unreachable.
func TestTerminationAdminServiceIsBuiltFromThePlane(t *testing.T) {
	plane := newPlaneForTest(t, planeTestConfig(t, termination.ConnectorConfig{
		CID:     "partner-a-term",
		Verdict: termination.VerdictConfig{Source: termination.SourceStatic},
	}))
	t.Cleanup(func() { _ = plane.Close() })

	manager := plane.Manager()
	if manager == nil {
		t.Fatal("plane has no manager")
	}
	// A config-declared connector is reserved, which is what the admin service
	// needs so the API cannot delete something the config owns.
	reserved := manager.ReservedCIDs()
	if len(reserved) != 1 || reserved[0] != "partner-a-term" {
		t.Fatalf("reserved cids = %v, want [partner-a-term]", reserved)
	}
	if status := terminationStatusFunc(plane); status == nil {
		t.Error("no status function for a configured plane; the console cannot report connector state")
	}
	if _, err := manager.Status("partner-a-term"); err != nil {
		t.Errorf("status for a configured connector: %v", err)
	}
}

// Without a section there is nothing to report, and the console must be able to
// tell that apart from an enabled gateway with no connectors.
func TestTerminationStatusFuncIsNilWithoutAPlane(t *testing.T) {
	if terminationStatusFunc(nil) != nil {
		t.Error("status function returned for a gateway that does not terminate traffic")
	}
}
