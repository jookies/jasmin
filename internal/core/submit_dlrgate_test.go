package core_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/core/dlrgate"
	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
)

// gatePolicies is the per-user policy source the outbound directory provides in
// production.
type gatePolicies map[string]dlrgate.Policy

func (g gatePolicies) ResolveDLRGatePolicy(username string) (dlrgate.Policy, bool) {
	policy, ok := g[username]
	return policy, ok
}

// capturingForwardPublisher records the receipts that would reach the partner.
type capturingForwardPublisher struct{ forwards []dlr.Forward }

func (c *capturingForwardPublisher) PublishDLR(_ context.Context, forward dlr.Forward) error {
	c.forwards = append(c.forwards, forward)
	return nil
}

// TestDLRGateEndToEnd runs the whole chain with real components: the registry
// writes an activation window, the submit service decides and stamps the verdict
// onto the real dlr:<msgid> record, and the real correlator applies it to the
// terminal receipt the partner receives.
//
// It is the test that would catch any of the five layers dropping the field,
// which unit tests of each layer separately cannot.
func TestDLRGateEndToEnd(t *testing.T) {
	const registered = "380930242105"
	const unregistered = "380930242199"

	cases := []struct {
		name        string
		destination string
		wantStatus  string
		wantErr     string
	}{
		{"a registered number is confirmed", registered, "DELIVRD", "000"},
		{"an unregistered number is rejected", unregistered, "REJECTD", "008"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			compat := rediscompat.NewClient(client)

			// 1. An application opens an activation window for one number.
			registry, err := dlrgate.NewRegistry(client, "")
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			if _, err := registry.Add(ctx, registered, dlrgate.AddOptions{TTL: 10 * time.Minute, AddedBy: "otp-service"}); err != nil {
				t.Fatalf("Add: %v", err)
			}

			// 2. The gated user submits. The submit service decides and stamps.
			store, err := dlr.NewRequestStore(compat)
			if err != nil {
				t.Fatalf("NewRequestStore: %v", err)
			}
			gate := dlrgate.NewGate(
				registry,
				gatePolicies{"alice": {Enabled: true}},
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)
			service, _ := newSubmitServiceWithDLRGate(t, store, gate)

			messageID, err := service.Submit(ctx, core.SubmitRequest{
				Username: "alice", Destination: testCase.destination, Content: "your code is 1234",
				DLR: true, DLRUrl: "http://sink.example/dlr", DLRLevel: 2, DLRMethod: "POST",
			})
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}

			// 3. The upstream accepts and later reports its own outcome. It says
			// the opposite of the gate in both cases, so a passthrough anywhere in
			// the chain fails this test rather than accidentally passing it.
			publisher := &capturingForwardPublisher{}
			correlator := dlr.NewCorrelator(compat, publisher, dlr.Config{})
			if err := correlator.OnSubmitResp(ctx, dlr.SubmitRespEvent{
				QueueMsgID: messageID, SMPPMsgID: "6AAD5", Status: "ESME_ROK",
			}); err != nil {
				t.Fatalf("OnSubmitResp: %v", err)
			}
			publisher.forwards = nil // the level-1 leg is not gated

			upstreamStatus, upstreamErr := "UNDELIV", "011"
			if testCase.wantStatus != "DELIVRD" {
				upstreamStatus, upstreamErr = "DELIVRD", "000"
			}
			if err := correlator.OnDeliverReceipt(ctx, dlr.DeliverReceiptEvent{
				RawDLRID: "6AAD5", Base: dlr.MsgIDBaseSame, ConnectorID: "connector-a",
				Status: upstreamStatus, Sub: "001", Dlvrd: "001",
				SubmitDate: "2601020304", DoneDate: "2601020305", Err: upstreamErr,
			}); err != nil {
				t.Fatalf("OnDeliverReceipt: %v", err)
			}

			// 4. The partner receives the gate's verdict, not the upstream's.
			if len(publisher.forwards) != 1 {
				t.Fatalf("expected one terminal forward, got %d", len(publisher.forwards))
			}
			forward := publisher.forwards[0]
			if forward.Status != testCase.wantStatus || forward.Err != testCase.wantErr {
				t.Fatalf("partner receipt = %s/%s, want %s/%s (upstream said %s/%s)",
					forward.Status, forward.Err, testCase.wantStatus, testCase.wantErr,
					upstreamStatus, upstreamErr)
			}
		})
	}
}

// TestDLRGateEndToEndUngatedUserIsUntouched is the control: the same wiring, a
// user with no policy, and the upstream's own receipt reaches the partner.
func TestDLRGateEndToEndUngatedUserIsUntouched(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	compat := rediscompat.NewClient(client)

	registry, err := dlrgate.NewRegistry(client, "")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store, err := dlr.NewRequestStore(compat)
	if err != nil {
		t.Fatalf("NewRequestStore: %v", err)
	}
	// The gate exists and the registry is empty, so a gated user would be told
	// REJECTD here. alice has no policy, so nothing must change.
	gate := dlrgate.NewGate(registry, gatePolicies{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service, _ := newSubmitServiceWithDLRGate(t, store, gate)

	messageID, err := service.Submit(ctx, core.SubmitRequest{
		Username: "alice", Destination: "380930242105", Content: "hi",
		DLR: true, DLRUrl: "http://sink.example/dlr", DLRLevel: 2, DLRMethod: "POST",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	publisher := &capturingForwardPublisher{}
	correlator := dlr.NewCorrelator(compat, publisher, dlr.Config{})
	if err := correlator.OnSubmitResp(ctx, dlr.SubmitRespEvent{
		QueueMsgID: messageID, SMPPMsgID: "6AAD5", Status: "ESME_ROK",
	}); err != nil {
		t.Fatalf("OnSubmitResp: %v", err)
	}
	publisher.forwards = nil
	if err := correlator.OnDeliverReceipt(ctx, dlr.DeliverReceiptEvent{
		RawDLRID: "6AAD5", Base: dlr.MsgIDBaseSame, ConnectorID: "connector-a",
		Status: "DELIVRD", Sub: "001", Dlvrd: "001", Err: "000",
	}); err != nil {
		t.Fatalf("OnDeliverReceipt: %v", err)
	}
	if len(publisher.forwards) != 1 || publisher.forwards[0].Status != "DELIVRD" {
		t.Fatalf("forwards = %+v, want the upstream's own DELIVRD", publisher.forwards)
	}
}
