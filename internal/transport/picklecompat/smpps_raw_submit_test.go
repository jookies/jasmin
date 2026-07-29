package picklecompat_test

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// TestRawSMPPSubmitRoundTrip proves that forwarding an ESME submit preserves
// the mandatory PDU octets that the HTTP-oriented builder cannot reconstruct,
// plus the retained standard optional parameters.
func TestRawSMPPSubmitRoundTrip(t *testing.T) {
	sarRef := uint16(0x1234)
	total, sequence, more := uint8(3), uint8(2), uint8(1)
	userRef, sourcePort, destinationPort := uint16(0x4321), uint16(9200), uint16(9201)
	payloadType, privacy, language := uint8(1), uint8(3), uint8(5)
	sourceSubunit, destSubunit := uint8(4), uint8(2)
	userResponse, displayTime, numberOfMessages := uint8(0xff), uint8(2), uint8(99)
	raw := &picklecompat.SubmitSMRawPDU{
		ServiceType:          picklecompat.Bytes("svc"),
		SourceAddrTON:        2,
		SourceAddrNPI:        8,
		SourceAddr:           picklecompat.Bytes("source"),
		DestAddrTON:          1,
		DestAddrNPI:          1,
		DestinationAddr:      picklecompat.Bytes("12025550123"),
		ESMClass:             0x43,
		ProtocolID:           0x7f,
		PriorityFlag:         3,
		ScheduleDeliveryTime: picklecompat.Bytes("000000000600000R"),
		ValidityPeriod:       picklecompat.Bytes("261231235959104-"),
		RegisteredDelivery:   0x11,
		ReplaceIfPresentFlag: 1,
		DataCoding:           0xf2,
		SMDefaultMessageID:   0xfe,
		ShortMessage:         picklecompat.Bytes{0, 1, 2, 3},
		Optional: picklecompat.SubmitSMRawOptionalParameters{
			SARMessageReference:  &sarRef,
			SARTotalSegments:     &total,
			SARSegmentSequence:   &sequence,
			MoreMessagesToSend:   &more,
			MessagePayload:       picklecompat.Bytes("payload"),
			UserMessageReference: &userRef,
			SourcePort:           &sourcePort,
			DestinationPort:      &destinationPort,
			SourceAddrSubunit:    &sourceSubunit,
			DestAddrSubunit:      &destSubunit,
			SourceSubaddress: &picklecompat.SubmitSMRawSubaddress{
				TypeTag: 0x88, Value: picklecompat.Bytes("source-subaddress"),
			},
			DestSubaddress: &picklecompat.SubmitSMRawSubaddress{
				TypeTag: 0, Value: picklecompat.Bytes("reserved-dest-subaddress"),
			},
			UserResponseCode:  &userResponse,
			PayloadType:       &payloadType,
			PrivacyIndicator:  &privacy,
			LanguageIndicator: &language,
			DisplayTime:       &displayTime,
			SMSSignal:         picklecompat.Bytes{0xde, 0xad},
			NumberOfMessages:  &numberOfMessages,
			CallbackNum: &picklecompat.SubmitSMRawCallbackNumber{
				DigitMode: 1, TON: 1, NPI: 1, Digits: picklecompat.Bytes("18005550199"),
			},
		},
	}
	request := picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, RawPDU: raw, IncludeBill: true,
		BillID: "bill", UserID: "uid", Username: "alice",
	}

	native := picklecompat.NewNativeCodec()
	encoded, err := native.EncodeSubmitSM(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := native.DecodeSubmitSM(context.Background(), encoded.Body)
	if err != nil {
		t.Fatal(err)
	}
	assertRawSubmitBody(t, got, raw)

	// The Python bridge is retained only as a compatibility oracle. When its
	// environment is available, prove it decodes the native pickle identically.
	if pythonPath := os.Getenv("PYTHON_PATH"); pythonPath != "" {
		bridge, err := picklecompat.NewBridge(context.Background(), pythonPath)
		if err != nil {
			t.Fatal(err)
		}
		defer bridge.Close()
		bridgeBody, _, err := bridge.DecodeSubmitSM(context.Background(), encoded.Body)
		if err != nil {
			t.Fatal(err)
		}
		assertRawSubmitBody(t, bridgeBody, raw)
		bridgeEncoded, err := bridge.EncodeSubmitSM(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		nativeBody, _, err := native.DecodeSubmitSM(context.Background(), bridgeEncoded.Body)
		if err != nil {
			t.Fatal(err)
		}
		assertRawSubmitBody(t, nativeBody, raw)
	}
}

func assertRawSubmitBody(t *testing.T, got smppwire.SubmitSMBody, want *picklecompat.SubmitSMRawPDU) {
	t.Helper()
	if !bytes.Equal(got.ServiceType, want.ServiceType) ||
		got.SourceAddressTON != want.SourceAddrTON ||
		got.SourceAddressNPI != want.SourceAddrNPI ||
		!bytes.Equal(got.SourceAddress, want.SourceAddr) ||
		got.DestinationAddressTON != want.DestAddrTON ||
		got.DestinationAddressNPI != want.DestAddrNPI ||
		!bytes.Equal(got.DestinationAddress, want.DestinationAddr) ||
		got.ESMClass != want.ESMClass ||
		got.ProtocolID != want.ProtocolID ||
		got.PriorityFlag != want.PriorityFlag ||
		!bytes.Equal(got.ScheduleDeliveryTime, want.ScheduleDeliveryTime) ||
		!bytes.Equal(got.ValidityPeriod, want.ValidityPeriod) ||
		got.RegisteredDelivery != want.RegisteredDelivery ||
		got.ReplaceIfPresentFlag != want.ReplaceIfPresentFlag ||
		got.DataCoding != want.DataCoding ||
		got.SMDefaultMessageID != want.SMDefaultMessageID ||
		!bytes.Equal(got.ShortMessage, want.ShortMessage) {
		t.Fatalf("mandatory PDU mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	expectedOptional := smppwire.OptionalParameters{
		SARMessageReference:  want.Optional.SARMessageReference,
		SARTotalSegments:     want.Optional.SARTotalSegments,
		SARSegmentSequence:   want.Optional.SARSegmentSequence,
		MoreMessagesToSend:   want.Optional.MoreMessagesToSend,
		MessagePayload:       []byte(want.Optional.MessagePayload),
		UserMessageReference: want.Optional.UserMessageReference,
		SourcePort:           want.Optional.SourcePort,
		DestinationPort:      want.Optional.DestinationPort,
		SourceAddrSubunit:    want.Optional.SourceAddrSubunit,
		DestAddrSubunit:      want.Optional.DestAddrSubunit,
		UserResponseCode:     want.Optional.UserResponseCode,
		PayloadType:          want.Optional.PayloadType,
		PrivacyIndicator:     want.Optional.PrivacyIndicator,
		LanguageIndicator:    want.Optional.LanguageIndicator,
		DisplayTime:          want.Optional.DisplayTime,
		SMSSignal:            []byte(want.Optional.SMSSignal),
		NumberOfMessages:     want.Optional.NumberOfMessages,
	}
	if subaddress := want.Optional.SourceSubaddress; subaddress != nil {
		expectedOptional.SourceSubaddress = &smppwire.Subaddress{
			TypeTag: subaddress.TypeTag, Value: []byte(subaddress.Value),
		}
	}
	if subaddress := want.Optional.DestSubaddress; subaddress != nil {
		expectedOptional.DestSubaddress = &smppwire.Subaddress{
			TypeTag: subaddress.TypeTag, Value: []byte(subaddress.Value),
		}
	}
	if callback := want.Optional.CallbackNum; callback != nil {
		expectedOptional.CallbackNum = &smppwire.CallbackNumber{
			DigitMode: callback.DigitMode, TON: callback.TON, NPI: callback.NPI,
			Digits: []byte(callback.Digits),
		}
	}
	if !reflect.DeepEqual(got.Optional, expectedOptional) {
		t.Fatalf("optional PDU mismatch:\n got=%+v\nwant=%+v", got.Optional, expectedOptional)
	}
}
