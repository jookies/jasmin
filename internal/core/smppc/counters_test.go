package smppc

import (
	"io"
	"net"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// The per-connector counters are what the web console's "Connector counters"
// panel and jCli's connector stats render. The SMPPcRegistry was plumbed to
// those readers and written by nothing, so a connector carrying real traffic
// reported zeros while the panel claimed to show live values -- worse than
// showing nothing, because an operator reads 0 as "no traffic" rather than "not
// measured".
//
// These tests assert the counters move. Without them the wiring can rot back to
// zero silently, which is exactly how it got there.
func TestSessionCountsInboundTraffic(t *testing.T) {
	registry := stats.NewSMPPcRegistry()
	// handleDeliver answers the PDU, so the session needs a real connection.
	// Drain the far end; the response bytes are not what this test asserts.
	local, remote := net.Pipe()
	defer func() { _ = local.Close() }()
	defer func() { _ = remote.Close() }()
	go func() { _, _ = io.Copy(io.Discard, remote) }()

	session := &Session{cfg: Config{CID: "carrier-a"}, conn: local}
	session.SetStats(registry)

	// deliver_sm and data_sm are counted separately: an operator seeing data_sm
	// traffic on a carrier that should only send deliver_sm wants to know.
	if err := session.handlePDU(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 1},
		SM:     &smppwire.SMBody{},
	}); err != nil {
		t.Logf("handleDeliver returned %v (no upstream configured; the count is what matters)", err)
	}
	if got := registry.Get("carrier-a", "deliver_sm_count"); got != 1 {
		t.Errorf("deliver_sm_count = %d, want 1", got)
	}

	if err := session.handlePDU(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDataSM, SequenceNumber: 2},
		SM:     &smppwire.SMBody{},
	}); err != nil {
		t.Logf("handleDeliver returned %v", err)
	}
	if got := registry.Get("carrier-a", "data_sm_count"); got != 1 {
		t.Errorf("data_sm_count = %d, want 1", got)
	}
	if got := registry.Get("carrier-a", "deliver_sm_count"); got != 1 {
		t.Errorf("data_sm must not also bump deliver_sm_count, got %d", got)
	}
}

// A submit_sm_resp is classified by command_status. Throttling is separated from
// other failures because it is a pacing signal an operator can act on rather
// than a fault in the message.
func TestSessionClassifiesSubmitResponses(t *testing.T) {
	cases := []struct {
		name   string
		status uint32
		metric string
	}{
		{"accepted", 0, "submit_sm_count"},
		{"throttled", statusThrottled, "throttling_error_count"},
		{"other failure", 0x0000000B, "other_submit_error_count"}, // ESME_RINVDSTADR
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := stats.NewSMPPcRegistry()
			session := &Session{cfg: Config{CID: "carrier-b"}}
			session.SetStats(registry)

			// No pending request matches, so handleResponse returns early --
			// deliberately: the counter must reflect what the SMSC answered,
			// independent of what the durability layer does with it.
			session.handleResponse(smppwire.PDU{
				Header: smppwire.Header{
					CommandID: smppwire.CommandSubmitSMResp, SequenceNumber: 99,
					CommandStatus: tc.status,
				},
			})
			if got := registry.Get("carrier-b", tc.metric); got != 1 {
				t.Errorf("%s = %d, want 1", tc.metric, got)
			}
		})
	}
}

// Counting must be optional: focused tests construct sessions without a registry.
func TestSessionWithoutStatsDoesNotPanic(t *testing.T) {
	session := &Session{cfg: Config{CID: "carrier-c"}}
	session.incStat("submit_sm_count")
	session.handleResponse(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandSubmitSMResp, SequenceNumber: 1},
	})
}
