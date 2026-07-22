package outbound_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

func randomExternalID(t *testing.T) string {
	t.Helper()
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	return "u" + hex.EncodeToString(token[:])[1:]
}

func TestOutboundHTTPToRabbitMQAndLateBilling(t *testing.T) {
	amqpURL := os.Getenv("AMQP_URL")
	pythonPath := os.Getenv("PYTHON_PATH")
	postgresDSN := os.Getenv("TEST_POSTGRES_DSN")
	if amqpURL == "" || pythonPath == "" || postgresDSN == "" {
		t.Skip("AMQP_URL, PYTHON_PATH and TEST_POSTGRES_DSN are required for live Macro 1.3 E2E")
	}
	passwordHash := sha256.Sum256([]byte("secret"))
	balance := 10.0
	count := 10
	early := 50
	runID := time.Now().UnixNano()
	connectorID := fmt.Sprintf("macro13-e2e-%d", runID)
	externalID := randomExternalID(t)
	config := outbound.Config{
		ListenAddress: "127.0.0.1:0",
		AMQPURL:       amqpURL,
		PythonPath:    pythonPath,
		PostgresDSN:   postgresDSN,
		Users: []outbound.UserConfig{{
			Username:                     "alice",
			ExternalID:                   externalID,
			PasswordSHA256:               hex.EncodeToString(passwordHash[:]),
			Balance:                      &balance,
			SubmitSMCount:                &count,
			EarlyDecrementBalancePercent: &early,
		}},
		Routes: []outbound.RouteConfig{{ConnectorID: connectorID, Rate: 1, Default: true}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtime, err := outbound.NewRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	connection, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	deliverSMQueue, err := channel.QueueDeclare(amqpcompat.RouterDeliverSMQueue, false, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if deliverSMQueue.Consumers != 0 {
		t.Fatalf("outbound runtime must not consume deliver.sm queue; consumers=%d", deliverSMQueue.Consumers)
	}
	queueName := "submit.sm." + connectorID
	deliveries, err := channel.Consume(queueName, "macro13-e2e-test", false, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer channel.QueueDelete(queueName, false, false, false)

	server := httptest.NewServer(runtime.Handler)
	defer server.Close()
	response, err := http.PostForm(server.URL+"/send", url.Values{
		"username": {"alice"},
		"password": {"secret"},
		"to":       {"15551230000"},
		"from":     {"1111"},
		"content":  {"hello"},
		"priority": {"2"},
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

	var delivery amqp.Delivery
	select {
	case delivery = <-deliveries:
	case <-ctx.Done():
		t.Fatal("timed out waiting for outbound SubmitSM")
	}
	defer delivery.Ack(false)
	if delivery.RoutingKey != queueName || delivery.MessageId != messageID || delivery.ReplyTo != "submit.sm.resp."+externalID || delivery.Priority != 2 || delivery.ContentType != "application/octet-stream" || delivery.DeliveryMode != amqp.Persistent {
		t.Fatalf("delivery route/properties=%q %q %d", delivery.RoutingKey, delivery.ReplyTo, delivery.Priority)
	}
	if delivery.Headers["source_connector"] != "httpapi" {
		t.Fatalf("source_connector=%v", delivery.Headers["source_connector"])
	}
	billWire, ok := delivery.Headers["submit_sm_bill"].([]byte)
	if !ok || len(billWire) == 0 {
		t.Fatalf("submit_sm_bill=%T", delivery.Headers["submit_sm_bill"])
	}

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	decoded, err := bridge.Decode(ctx, delivery.Body)
	if err != nil {
		t.Fatal(err)
	}
	var submit picklecompat.SubmitSM
	if err := json.Unmarshal(decoded, &submit); err != nil {
		t.Fatal(err)
	}
	if submit.ClassName != "smpp.pdu.operations.SubmitSM" || string(submit.Params.ShortMessage) != "hello" || string(submit.Params.DestinationAddr) != "15551230000" {
		t.Fatalf("decoded SubmitSM=%+v", submit)
	}
	decodedBill, err := bridge.Decode(ctx, billWire)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decodedBill), "jasmin.routing.Bills.SubmitSmBill") || !strings.Contains(string(decodedBill), externalID) {
		t.Fatalf("decoded bill=%s", decodedBill)
	}

	assertBalance(t, server.URL, "9.5", "9")

	multipartResponse, err := http.PostForm(server.URL+"/send", url.Values{
		"username": {"alice"}, "password": {"secret"}, "to": {"15551230000"},
		"from": {"1111"}, "content": {strings.Repeat("a", 161)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, multipartResponse.Body)
	multipartResponse.Body.Close()
	if multipartResponse.StatusCode != http.StatusInternalServerError {
		t.Fatalf("multipart status=%d, want fail-closed 500", multipartResponse.StatusCode)
	}
	assertBalance(t, server.URL, "9.5", "9")
	select {
	case unexpected := <-deliveries:
		_ = unexpected.Reject(false)
		t.Fatal("multipart rejection published an unexpected PDU")
	case <-time.After(200 * time.Millisecond):
	}

	publisher, err := amqpcompat.NewPublisher(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	properties, err := amqpcompat.NewProperties("orphan-late-bill-e2e", map[string]amqpcompat.Field{
		"user-id": amqpcompat.StringField(externalID),
		"amount":  amqpcompat.StringField("0.5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("bill_request.submit_sm_resp."+externalID, properties, []byte("orphan-late-bill-e2e"))
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(ctx, "billing", envelope.RoutingKey(), envelope); err != nil {
		t.Fatal(err)
	}
	// An orphan legacy billing delivery has no durable intent and is poison:
	// it must be rejected without mutating balance or entering a requeue loop.
	time.Sleep(200 * time.Millisecond)
	assertBalance(t, server.URL, "9.5", "9")
}

func assertBalance(t *testing.T, serverURL, wantBalance, wantCount string) {
	t.Helper()
	values := balanceResponse(t, serverURL)
	if values["balance"] != wantBalance || values["sms_count"] != wantCount {
		t.Fatalf("balance response=%v want balance=%s count=%s", values, wantBalance, wantCount)
	}
}

func balanceValue(t *testing.T, serverURL string) string {
	t.Helper()
	return balanceResponse(t, serverURL)["balance"]
}

func balanceResponse(t *testing.T, serverURL string) map[string]string {
	t.Helper()
	response, err := http.PostForm(serverURL+"/balance", url.Values{
		"username": {"alice"},
		"password": {"secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var values map[string]string
	if err := json.NewDecoder(response.Body).Decode(&values); err != nil {
		t.Fatal(err)
	}
	return values
}
