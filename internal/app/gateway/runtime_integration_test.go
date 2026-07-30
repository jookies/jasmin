package gateway_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/synevyr/internal/app/gateway"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

type fakeSMSCResult struct {
	submit smppwire.PDU
	err    error
}

func randomExternalID(t *testing.T) string {
	t.Helper()
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	return "u" + hex.EncodeToString(token[:])[1:]
}

func TestGatewayHTTPToDurableSMPPResponse(t *testing.T) {
	amqpURL := os.Getenv("AMQP_URL")
	postgresDSN := os.Getenv("TEST_POSTGRES_DSN")
	if amqpURL == "" || postgresDSN == "" {
		t.Skip("AMQP_URL and TEST_POSTGRES_DSN are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	smscResult := make(chan fakeSMSCResult, 2)
	smscMessageID := "smsc-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	go runFakeSMSC(listener, smscMessageID, smscResult)

	broker, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	channel, err := broker.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	if err := channel.ExchangeDeclare("messaging", "topic", false, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	responseQueue, err := channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	runID := time.Now().UnixNano()
	externalID := randomExternalID(t)
	responseKey := "submit.sm.resp." + externalID
	if err := channel.QueueBind(responseQueue.Name, responseKey, "messaging", false, nil); err != nil {
		t.Fatal(err)
	}
	responses, err := channel.Consume(responseQueue.Name, "wave1a-gateway-test", false, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	passwordHash := sha256.Sum256([]byte("secret"))
	balance := 10.0
	count := 10
	early := 50
	port := listener.Addr().(*net.TCPAddr).Port
	connectorID := "wave1a-" + strconv.FormatInt(runID, 10)
	config := gateway.Config{
		Role: gateway.RoleHTTPAndSMPPc,
		// PickleCodec is left empty on purpose: the native Go codec is now the
		// default, so this exercises it end to end (submit -> durable -> SMPP
		// submit -> submit_sm_resp -> DLR) with no subprocess. Per-action parity
		// with the Python bridge is proven by the picklecompat differentials.
		Outbound: outbound.Config{
			ListenAddress: "127.0.0.1:0", AMQPURL: amqpURL, PostgresDSN: postgresDSN,
			Users: []outbound.UserConfig{{
				Username: "alice", ExternalID: externalID, PasswordSHA256: hex.EncodeToString(passwordHash[:]),
				Balance: &balance, SubmitSMCount: &count, EarlyDecrementBalancePercent: &early,
			}},
			Routes: []outbound.RouteConfig{{ConnectorID: connectorID, Rate: 1, Default: true}},
		},
		Connectors: []smppc.Config{{
			CID: connectorID, Host: "127.0.0.1", Port: port, SystemID: "system", Password: "password",
			Bind: smppc.BindTransceiver, ResTimeout: 5, ConFailDelay: 0.05, ConLossDelay: 0.05,
		}},
		BindTimeoutSeconds: 5,
	}
	runtime, err := gateway.NewRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	// The runtime always declares the DLRLookup queue (bound to messaging/dlr.*)
	// even though this config runs no in-process DLRLookup worker, so the
	// response path's mandatory dlr.submit_sm_resp publish is routable. Consume
	// that gateway-declared queue directly to prove the DLR is published; without
	// the always-declare the publish would be unroutable and the outbox would
	// never drain (undispatched below would never reach 0).
	dlrDeliveries, err := channel.Consume(amqpcompat.DLRLookupQueue("main"), "wave1a-gateway-dlr", false, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(runtime.Handler)
	defer server.Close()

	response, err := http.PostForm(server.URL+"/send", url.Values{
		"username": {"alice"}, "password": {"secret"}, "to": {"15551230000"},
		"from": {"1111"}, "content": {strings.Repeat("A", 161)}, "priority": {"2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), `Success "`) {
		t.Fatalf("send response=%d %q", response.StatusCode, body)
	}
	messageID := strings.TrimSuffix(strings.TrimPrefix(string(body), `Success "`), `"`)

	for part := 1; part <= 2; part++ {
		select {
		case result := <-smscResult:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.submit.SM == nil || string(result.submit.SM.DestinationAddress) != "15551230000" {
				t.Fatalf("fake SMSC received %+v", result.submit.SM)
			}
			// The legacy http-api default is long_content_split=udh, so
			// concatenation rides in the UDH with the UDHI bit set in esm_class
			// rather than in SAR TLVs. Assert the bytes: UDH 05 00 03 <ref>
			// <total> <sequence> for an 8-bit reference.
			if result.submit.SM.Optional.SARTotalSegments != nil || result.submit.SM.Optional.SARSegmentSequence != nil {
				t.Fatalf("part %d carries SAR TLVs; the legacy default is UDH: %+v", part, result.submit.SM.Optional)
			}
			if result.submit.SM.ESMClass&0x40 == 0 {
				t.Fatalf("part %d has esm_class=%#02x, UDHI bit not set", part, result.submit.SM.ESMClass)
			}
			header := result.submit.SM.ShortMessage
			if len(header) < 6 || header[0] != 0x05 || header[1] != 0x00 || header[2] != 0x03 ||
				header[4] != 2 || header[5] != uint8(part) {
				t.Fatalf("part %d UDH header = % x, want 05 00 03 <ref> 02 %02x", part, header[:min(6, len(header))], part)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for decoded multipart submit_sm")
		}
	}

	seenResponseIDs := make(map[string]struct{}, 2)
	for part := 1; part <= 2; part++ {
		select {
		case delivery := <-responses:
			if delivery.RoutingKey != responseKey || len(delivery.Body) < 2 || delivery.Body[0] != 0x80 || delivery.Body[1] != 0x02 {
				t.Fatalf("response message-id=%q route=%q body-prefix=%x", delivery.MessageId, delivery.RoutingKey, delivery.Body[:min(2, len(delivery.Body))])
			}
			seenResponseIDs[delivery.MessageId] = struct{}{}
			_ = delivery.Ack(false)
		case <-ctx.Done():
			t.Fatal("timed out waiting for durable multipart submit_sm_resp publication")
		}
	}
	for part := 1; part <= 2; part++ {
		want := fmt.Sprintf("%s/%06d", messageID, part)
		if _, ok := seenResponseIDs[want]; !ok {
			t.Fatalf("missing response message-id %q; got=%v", want, seenResponseIDs)
		}
	}

	// Every final submit_sm_resp publishes a dlr.submit_sm_resp for DLRLookup.
	// Both parts succeed (ESME_ROK), so each carries its own SMSC message id
	// normalized upper-case with leading zeros stripped. The keying follows
	// legacy: intermediate parts keep their suffixed queue id, while the final
	// part is reprojected onto the aggregate message id, because that is where
	// the one pending DLR request lives and what the user's receipt must name.
	seenDLR := make(map[string]string, 2)
	for part := 1; part <= 2; part++ {
		select {
		case delivery := <-dlrDeliveries:
			if delivery.RoutingKey != "dlr.submit_sm_resp" || string(delivery.Body) != "ESME_ROK" {
				t.Fatalf("dlr message-id=%q route=%q body=%q", delivery.MessageId, delivery.RoutingKey, delivery.Body)
			}
			if typ, _ := delivery.Headers["type"].(string); typ != "submit_sm_resp" {
				t.Fatalf("dlr type header=%q, want submit_sm_resp", typ)
			}
			smppMsgID, _ := delivery.Headers["smpp_msgid"].(string)
			seenDLR[delivery.MessageId] = smppMsgID
			_ = delivery.Ack(false)
		case <-ctx.Done():
			t.Fatal("timed out waiting for dlr.submit_sm_resp publication")
		}
	}
	const finalPart = 2
	for part := 1; part <= finalPart; part++ {
		wantMsgID := fmt.Sprintf("%s/%06d", messageID, part)
		if part == finalPart {
			wantMsgID = messageID
		}
		wantSMPP := strings.TrimLeft(strings.ToUpper(fmt.Sprintf("%s-%d", smscMessageID, part)), "0")
		if got, ok := seenDLR[wantMsgID]; !ok || got != wantSMPP {
			t.Fatalf("DLR for %q: smpp_msgid=%q want %q (seen=%v)", wantMsgID, got, wantSMPP, seenDLR)
		}
	}
	if _, orphaned := seenDLR[fmt.Sprintf("%s/%06d", messageID, finalPart)]; orphaned {
		t.Errorf("the final part still published under its suffixed id; its receipt would find no DLR request (seen=%v)", seenDLR)
	}

	db, err := sql.Open("pgx", postgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var results, billing, undispatched int
		err = db.QueryRowContext(ctx, `
SELECT
 (SELECT count(*) FROM submit_results r JOIN submit_parts p ON p.part_key=r.part_key WHERE p.message_id=$1),
 (SELECT count(*) FROM submit_billing_intents b JOIN submit_parts p ON p.part_key=b.part_key WHERE p.message_id=$1 AND b.applied_at IS NOT NULL),
 (SELECT count(*) FROM submit_outbox o JOIN submit_parts p ON p.part_key=o.part_key WHERE p.message_id=$1 AND o.dispatched_at IS NULL)`, messageID).Scan(&results, &billing, &undispatched)
		if err == nil && results == 2 && billing == 2 && undispatched == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable state results=%d billing=%d undispatched=%d err=%v", results, billing, undispatched, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	rows, err := db.QueryContext(ctx, `SELECT p.part_number,r.smsc_message_id FROM submit_results r JOIN submit_parts p ON p.part_key=r.part_key WHERE p.message_id=$1 ORDER BY p.part_number`, messageID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantSMSCIDs := map[int]string{1: smscMessageID + "-1", 2: smscMessageID + "-2"}
	seenMappings := 0
	for rows.Next() {
		var partNumber int
		var smscMessageID string
		if err := rows.Scan(&partNumber, &smscMessageID); err != nil {
			t.Fatal(err)
		}
		if smscMessageID != wantSMSCIDs[partNumber] {
			t.Fatalf("part %d SMSC message ID=%q want=%q", partNumber, smscMessageID, wantSMSCIDs[partNumber])
		}
		seenMappings++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seenMappings != 2 {
		t.Fatalf("part/result mappings=%d want=2", seenMappings)
	}
	waitBalance(t, server.URL, "8")
}

func runFakeSMSC(listener net.Listener, messageID string, result chan<- fakeSMSCResult) {
	conn, err := listener.Accept()
	if err != nil {
		result <- fakeSMSCResult{err: err}
		return
	}
	defer conn.Close()
	bind, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
	if err != nil {
		result <- fakeSMSCResult{err: err}
		return
	}
	bindResponse, err := smppwire.Encode(smppwire.PDU{
		Header:       smppwire.Header{CommandID: smppwire.CommandBindTransceiverResp, SequenceNumber: bind.Header.SequenceNumber},
		BindResponse: &smppwire.BindResponseBody{SystemID: []byte("fake-smsc")},
	})
	if err != nil {
		result <- fakeSMSCResult{err: err}
		return
	}
	if _, err = conn.Write(bindResponse); err != nil {
		result <- fakeSMSCResult{err: err}
		return
	}
	for part := 1; part <= 2; part++ {
		submit, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
		if err != nil {
			result <- fakeSMSCResult{err: err}
			return
		}
		response, err := smppwire.Encode(smppwire.PDU{
			Header:         smppwire.Header{CommandID: smppwire.CommandSubmitSMResp, SequenceNumber: submit.Header.SequenceNumber},
			SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte(messageID + "-" + strconv.Itoa(part))},
		})
		if err != nil {
			result <- fakeSMSCResult{err: err}
			return
		}
		if _, err = conn.Write(response); err != nil {
			result <- fakeSMSCResult{err: err}
			return
		}
		result <- fakeSMSCResult{submit: submit}
	}
}

func waitBalance(t *testing.T, serverURL, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for {
		response, err := http.PostForm(serverURL+"/balance", url.Values{"username": {"alice"}, "password": {"secret"}})
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			last = string(body)
			var values map[string]string
			if json.Unmarshal(body, &values) == nil && values["balance"] == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("balance did not reach %s; last=%q", want, last)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
