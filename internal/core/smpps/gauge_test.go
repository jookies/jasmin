package smpps

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/stats"
)

// The legacy SMPPS metric set mixes cumulative counters with gauges. connect_count
// and disconnect_count are totals; connected_count and the bound_*_count family
// describe a CURRENT population -- their HELP text says "Number of connected
// sessions" and "Number of bound sessions in <mode> mode".
//
// They were only ever incremented, so an operator watching connected_count saw it
// climb forever and could not tell how many sessions were actually live. These
// tests pin the gauge semantics: up on bind, back down on disconnect, and the
// cumulative counters still only ever rise.
func TestSMPPsGaugesReleaseOnDisconnect(t *testing.T) {
	registry := &stats.SMPPsStats{}
	server, err := NewServer(mapResolver{"u": testUser("p")}, ServerConfig{},
		WithStats(registry))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})

	conn := dial(t, listener.Addr().String())
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}
	waitForStat(t, registry, "bound_trx_count", 1)
	if got := registry.Get("connected_count"); got != 1 {
		t.Errorf("connected_count while bound = %d, want 1", got)
	}

	_ = conn.Close()

	// Both gauges must come back down once the session is gone.
	waitForStat(t, registry, "connected_count", 0)
	waitForStat(t, registry, "bound_trx_count", 0)

	// The cumulative counters are unaffected by the release.
	if got := registry.Get("connect_count"); got != 1 {
		t.Errorf("connect_count = %d, want 1 (cumulative, never released)", got)
	}
	if got := registry.Get("disconnect_count"); got != 1 {
		t.Errorf("disconnect_count = %d, want 1", got)
	}
}

// A gauge must never go negative, however the session ended.
func TestSMPPsGaugeNeverGoesNegative(t *testing.T) {
	registry := &stats.SMPPsStats{}
	registry.Dec("connected_count")
	registry.Dec("bound_rx_count")
	if got := registry.Get("connected_count"); got != 0 {
		t.Errorf("connected_count = %d after an unmatched release, want 0", got)
	}
	if got := registry.Get("bound_rx_count"); got != 0 {
		t.Errorf("bound_rx_count = %d after an unmatched release, want 0", got)
	}
}

func waitForStat(t *testing.T, registry *stats.SMPPsStats, name string, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if registry.Get(name) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s = %d, want %d", name, registry.Get(name), want)
}
