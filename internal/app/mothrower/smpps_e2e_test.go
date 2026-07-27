package mothrower_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/app/mothrower"
	"github.com/pumpitspace/jasmin/internal/app/smppsdelivery"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type nopSubmitter struct{}

func (nopSubmitter) Submit(context.Context, core.SubmitRequest) (string, error) { return "", nil }

// TestMOThrowerSMPPSDeliversToBoundReceiver proves the full SMPPS MO throw leg
// over a real broker: a RoutedDeliverSmContent published to
// deliver_sm_thrower.smpps is consumed by the MO thrower, decoded through the
// bridge, and pushed down a receiver ESME bound to the SMPPS server — the same
// chain the MO dispatcher feeds. The HTTP leg is drilled on compose; this is
// its SMPPS twin, end to end.
func TestMOThrowerSMPPSDeliversToBoundReceiver(t *testing.T) {
	amqpURL := os.Getenv("AMQP_URL")
	pythonPath := os.Getenv("PYTHON_PATH")
	if amqpURL == "" || pythonPath == "" {
		t.Skip("AMQP_URL and PYTHON_PATH are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// SMPPS server + a receiver ESME bound as system_id "morx".
	smppsService, err := smppsserver.NewService(smppsserver.Config{
		BindAddr: "127.0.0.1:0",
		Users:    []smppsserver.UserConfig{{SystemID: "morx", Password: "pw"}},
	}, nopSubmitter{})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = smppsService.Run(ctx) }()
	t.Cleanup(func() { _ = smppsService.Close() })

	esme, err := net.Dial("tcp", smppsService.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer esme.Close()
	bindFrame, _ := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smpps.CommandBindReceiver, SequenceNumber: 1},
		Bind:   &smppwire.BindBody{SystemID: []byte("morx"), Password: []byte("pw"), SystemType: []byte(""), InterfaceVersion: 0x34},
	})
	if _, err := esme.Write(bindFrame); err != nil {
		t.Fatal(err)
	}
	_ = esme.SetReadDeadline(time.Now().Add(3 * time.Second))
	if resp, err := smppwire.Read(esme, smppwire.DefaultMaxSize); err != nil || resp.Header.CommandStatus != smpps.StatusROK {
		t.Fatalf("bind resp = %+v err=%v", resp.Header, err)
	}

	// Bridge for the routed-content decode, and the MO thrower with the SMPPS
	// sink wired to the server.
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	moSink, err := smppsdelivery.NewMOSink(smppsService.Server())
	if err != nil {
		t.Fatal(err)
	}
	// Durable topology matches the example compose broker; a mismatched
	// redeclare would 406. AMQP_DURABLE_TOPOLOGY=false targets a fresh broker.
	durable := os.Getenv("AMQP_DURABLE_TOPOLOGY") != "false"
	service, err := mothrower.NewService(mothrower.Config{
		AMQPURL: amqpURL, AMQPDurableTopology: durable, RetryDelaySeconds: 1,
	}, bridge, mothrower.WithSMPPSDeliverySink(moSink))
	if err != nil {
		t.Fatal(err)
	}
	service.OnError = func(err error) { t.Logf("mothrower: %v", err) }
	go func() { _ = service.Run(ctx) }()

	// Build the routed content: pickle a deliver_sm as RoutableDeliverSm, repickle
	// to the bare PDU body, and pickle the smpps dst-connectors header.
	deliverBody := &smppwire.SMBody{SourceAddress: []byte("31600000000"), DestinationAddress: []byte("morx"), ShortMessage: []byte("smpps-mo")}
	wire, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 9},
		SM:     deliverBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	routable, err := bridge.EncodeRoutableDeliverSM(ctx, wire, "smsc-in")
	if err != nil {
		t.Fatal(err)
	}
	pduPickle, _, err := bridge.RepickleRoutablePDU(ctx, routable)
	if err != nil {
		t.Fatal(err)
	}
	dstConnectors, err := bridge.EncodeConnectorList(ctx, []picklecompat.MOConnectorSpec{{Type: "smpps", SystemID: "morx"}})
	if err != nil {
		t.Fatal(err)
	}

	// Publish to deliver_sm_thrower.smpps through a declared topology.
	publishConn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatal(err)
	}
	defer publishConn.Close()
	topology := amqpcompat.NewTopology(publishConn, durable)
	if err := topology.DeclareQueue(ctx, "deliver_sm_thrower", "messaging", "deliver_sm_thrower.*"); err != nil {
		t.Fatal(err)
	}
	publisher, err := amqpcompat.NewPublisher(publishConn)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	properties, err := amqpcompat.NewProperties("smpps-mo-msgid", map[string]amqpcompat.Field{
		"route-type":       amqpcompat.StringField("simple"),
		"src-connector-id": amqpcompat.StringField("smsc-in"),
		"dst-connectors":   amqpcompat.BytesField(dstConnectors),
		"try-count":        amqpcompat.IntegerField(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("deliver_sm_thrower.smpps", properties, pduPickle)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(ctx, "messaging", "deliver_sm_thrower.smpps", envelope); err != nil {
		t.Fatal(err)
	}

	// The receiver ESME must get the deliver_sm.
	_ = esme.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, err := smppwire.Read(esme, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatalf("read deliver_sm: %v", err)
	}
	if got.Header.CommandID != smppwire.CommandDeliverSM {
		t.Fatalf("command = %#x want deliver_sm", got.Header.CommandID)
	}
	if string(got.SM.ShortMessage) != "smpps-mo" {
		t.Fatalf("short_message = %q want smpps-mo", got.SM.ShortMessage)
	}
	if string(got.SM.SourceAddress) != "31600000000" {
		t.Fatalf("source_addr = %q", got.SM.SourceAddress)
	}
}
