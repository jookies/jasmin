package smppc_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math/big"
	"net"
	"reflect"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func submitWireDelivery(t *testing.T, messageID string, settled chan<- bool) *amqpcompat.Delivery {
	t.Helper()
	properties, err := amqpcompat.NewProperties(messageID, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm.wire-audit", properties, []byte("pickled"))
	if err != nil {
		t.Fatal(err)
	}
	return injectDelivery(envelope, func(requeue bool) {
		if settled != nil {
			settled <- requeue
		}
	})
}

func submitSMLengthOffset(t *testing.T, frame []byte) int {
	t.Helper()
	offset := 16
	skipCString := func() {
		terminator := bytes.IndexByte(frame[offset:], 0)
		if terminator < 0 {
			t.Fatal("submit_sm mandatory C-octet string is not terminated")
		}
		offset += terminator + 1
	}
	skipCString() // service_type
	offset += 2
	skipCString() // source_addr
	offset += 2
	skipCString() // destination_addr
	offset += 3
	skipCString() // schedule_delivery_time
	skipCString() // validity_period
	offset += 4
	if offset >= len(frame) {
		t.Fatal("submit_sm frame ended before sm_length")
	}
	return offset
}

func TestSessionSubmitWireFields(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	sarReference := uint16(0x1234)
	sarTotal, sarSequence := byte(3), byte(2)
	moreMessages, payloadType, privacy, language := byte(1), byte(1), byte(2), byte(3)
	sourcePort, destinationPort := uint16(9200), uint16(9201)
	userReference := uint16(0xabcd)
	sourceSubunit, destinationSubunit := byte(1), byte(2)
	userResponse, displayTime, numberOfMessages := byte(0x7f), byte(2), byte(4)
	body := smppwire.SubmitSMBody{
		ServiceType:           []byte("CMT"),
		SourceAddressTON:      5,
		SourceAddressNPI:      0,
		SourceAddress:         []byte("alpha"),
		DestinationAddressTON: 1,
		DestinationAddressNPI: 1,
		DestinationAddress:    []byte("15551230000"),
		ESMClass:              0x43,
		ProtocolID:            0x34,
		PriorityFlag:          3,
		ScheduleDeliveryTime:  []byte("260730120000000+"),
		ValidityPeriod:        []byte("000000000500000R"),
		RegisteredDelivery:    0x11,
		ReplaceIfPresentFlag:  1,
		DataCoding:            0xf2,
		SMDefaultMessageID:    0xfe,
		ShortMessage:          []byte{0x00, 0x7f, 0xff},
		Optional: smppwire.OptionalParameters{
			UserMessageReference: &userReference,
			SourcePort:           &sourcePort,
			SourceAddrSubunit:    &sourceSubunit,
			DestinationPort:      &destinationPort,
			DestAddrSubunit:      &destinationSubunit,
			SARMessageReference:  &sarReference,
			SARTotalSegments:     &sarTotal,
			SARSegmentSequence:   &sarSequence,
			MoreMessagesToSend:   &moreMessages,
			PayloadType:          &payloadType,
			PrivacyIndicator:     &privacy,
			CallbackNum: &smppwire.CallbackNumber{
				DigitMode: 1, TON: 1, NPI: 1, Digits: []byte("15551230000"),
			},
			SourceSubaddress:  &smppwire.Subaddress{TypeTag: 0x80, Value: []byte("source")},
			DestSubaddress:    &smppwire.Subaddress{TypeTag: 0x88, Value: []byte("dest")},
			UserResponseCode:  &userResponse,
			DisplayTime:       &displayTime,
			SMSSignal:         []byte{0x12, 0x34},
			NumberOfMessages:  &numberOfMessages,
			LanguageIndicator: &language,
		},
	}
	decoder := tupleSubmitDecoder{
		body: body,
		tuples: []tlv.TLV{{
			Tag: big.NewInt(0x1400), Type: tlv.TypeOctetString, Value: []byte{0xde, 0xad},
		}},
	}
	session := smppc.NewSessionWithDecoder(client, smppc.Config{CID: "wire-audit", ResTimeout: 5}, nil, nil, decoder, nil)

	frameResult := make(chan struct {
		frame []byte
		err   error
	}, 1)
	go func() {
		frame, err := readRawFrame(server)
		frameResult <- struct {
			frame []byte
			err   error
		}{frame: frame, err: err}
	}()
	if err := session.Submit(context.Background(), submitWireDelivery(t, "wire-fields", nil)); err != nil {
		t.Fatal(err)
	}
	result := <-frameResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	offset := submitSMLengthOffset(t, result.frame)
	if got := result.frame[offset]; got != byte(len(body.ShortMessage)) {
		t.Fatalf("sm_length = %d, want %d", got, len(body.ShortMessage))
	}
	pdu, err := smppwire.Decode(result.frame)
	if err != nil {
		t.Fatal(err)
	}
	got := pdu.SM
	if pdu.Header.CommandID != smppwire.CommandSubmitSM ||
		got == nil ||
		!bytes.Equal(got.ServiceType, body.ServiceType) ||
		got.SourceAddressTON != body.SourceAddressTON ||
		got.SourceAddressNPI != body.SourceAddressNPI ||
		!bytes.Equal(got.SourceAddress, body.SourceAddress) ||
		got.DestinationAddressTON != body.DestinationAddressTON ||
		got.DestinationAddressNPI != body.DestinationAddressNPI ||
		!bytes.Equal(got.DestinationAddress, body.DestinationAddress) ||
		got.ESMClass != body.ESMClass ||
		got.ProtocolID != body.ProtocolID ||
		got.PriorityFlag != body.PriorityFlag ||
		!bytes.Equal(got.ScheduleDeliveryTime, body.ScheduleDeliveryTime) ||
		!bytes.Equal(got.ValidityPeriod, body.ValidityPeriod) ||
		got.RegisteredDelivery != body.RegisteredDelivery ||
		got.ReplaceIfPresentFlag != body.ReplaceIfPresentFlag ||
		got.DataCoding != body.DataCoding ||
		got.SMDefaultMessageID != body.SMDefaultMessageID ||
		!bytes.Equal(got.ShortMessage, body.ShortMessage) {
		t.Fatalf("mandatory submit_sm fields changed:\n got %+v\nwant %+v", got, body)
	}
	if !reflect.DeepEqual(got.Optional, body.Optional) {
		t.Fatalf("standard optional TLVs changed:\n got %+v\nwant %+v", got.Optional, body.Optional)
	}
	if len(got.CapturedVendorTLVs) != 1 ||
		got.CapturedVendorTLVs[0].Tag != 0x1400 ||
		!bytes.Equal(got.CapturedVendorTLVs[0].Value, []byte{0xde, 0xad}) {
		t.Fatalf("vendor TLV changed: %+v", got.CapturedVendorTLVs)
	}
}

func TestSessionSubmitWireEmptyFieldsAndTLVPresence(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		messagePayload []byte
		wantTLV        bool
	}{
		{name: "absent_message_payload"},
		{name: "present_empty_message_payload", messagePayload: []byte{}, wantTLV: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			decoder := tupleSubmitDecoder{body: smppwire.SubmitSMBody{
				Optional: smppwire.OptionalParameters{MessagePayload: testCase.messagePayload},
			}}
			session := smppc.NewSessionWithDecoder(client, smppc.Config{CID: "wire-empty", ResTimeout: 5}, nil, nil, decoder, nil)
			frameResult := make(chan []byte, 1)
			go func() {
				frame, _ := readRawFrame(server)
				frameResult <- frame
			}()
			if err := session.Submit(context.Background(), submitWireDelivery(t, testCase.name, nil)); err != nil {
				t.Fatal(err)
			}
			frame := <-frameResult
			offset := submitSMLengthOffset(t, frame)
			if frame[offset] != 0 {
				t.Fatalf("sm_length = %d, want zero", frame[offset])
			}
			optional := frame[offset+1:]
			if testCase.wantTLV {
				want := make([]byte, 4)
				binary.BigEndian.PutUint16(want, 0x0424)
				if !bytes.Equal(optional, want) {
					t.Fatalf("optional section = %x, want present-empty message_payload %x", optional, want)
				}
			} else if len(optional) != 0 {
				t.Fatalf("optional section = %x, want absent", optional)
			}
		})
	}
}

func TestSessionRejectsSubmitSMShortMessageAboveSpecMaximum(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	decoder := tupleSubmitDecoder{body: smppwire.SubmitSMBody{
		DestinationAddress: []byte("15551230000"),
		ShortMessage:       make([]byte, 255),
	}}
	session := smppc.NewSessionWithDecoder(client, smppc.Config{CID: "wire-length"}, nil, nil, decoder, nil)
	go func() {
		_, _ = smppwire.Read(server, smppwire.DefaultMaxSize)
	}()
	settled := make(chan bool, 1)
	err := session.Submit(context.Background(), submitWireDelivery(t, "wire-length", settled))
	if !errors.Is(err, smppc.ErrInvalidOutboundSubmitSM) {
		t.Fatalf("Submit error = %v, want ErrInvalidOutboundSubmitSM", err)
	}
	if requeue := <-settled; requeue {
		t.Fatal("invalid submit_sm must be rejected without requeue")
	}
}

func TestSessionRejectsShortMessageWithMessagePayload(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	decoder := tupleSubmitDecoder{body: smppwire.SubmitSMBody{
		DestinationAddress: []byte("15551230000"),
		ShortMessage:       []byte("short"),
		Optional:           smppwire.OptionalParameters{MessagePayload: []byte("payload")},
	}}
	session := smppc.NewSessionWithDecoder(client, smppc.Config{CID: "wire-payload"}, nil, nil, decoder, nil)
	go func() {
		_, _ = smppwire.Read(server, smppwire.DefaultMaxSize)
	}()
	settled := make(chan bool, 1)
	err := session.Submit(context.Background(), submitWireDelivery(t, "wire-payload", settled))
	if !errors.Is(err, smppc.ErrInvalidOutboundSubmitSM) {
		t.Fatalf("Submit error = %v, want ErrInvalidOutboundSubmitSM", err)
	}
	if requeue := <-settled; requeue {
		t.Fatal("invalid submit_sm must be rejected without requeue")
	}
}

func TestSubmitSMLengthMaximumIsSMPPSpecValue(t *testing.T) {
	if smppc.MaxSubmitSMShortMessageLength != 254 {
		t.Fatalf("maximum short_message length = %d, want SMPP 3.4 maximum 254", smppc.MaxSubmitSMShortMessageLength)
	}
}
