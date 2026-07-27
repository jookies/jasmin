package picklecompat_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// TestNativeScheduleValidityMatchesBridge proves the native encode+decode of
// schedule_delivery_time / validity_period agrees with the bridge: a native
// submit pickle carrying the times decodes (native and bridge) to the same SMPP
// absolute-time wire bytes.
func TestNativeScheduleValidityMatchesBridge(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, python)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	native := picklecompat.NewNativeCodec()

	cases := map[string]struct{ schedule, validity string }{
		"utc":             {"2026-07-27T12:05:09Z", "2026-07-27T13:00:00Z"},
		"offset":          {"2026-07-27T12:05:09+02:00", "2026-07-27T18:30:00+02:00"},
		"fractional":      {"2026-07-27T12:05:09.5+00:00", ""},
		"negative offset": {"", "2026-07-27T09:00:00-05:00"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			request := picklecompat.SubmitSMEncodeRequest{
				Sequence: 1, SourceAddr: picklecompat.Bytes("1111"), DestinationAddr: picklecompat.Bytes("2222"),
				ShortMessage: picklecompat.Bytes("hi"), SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1,
				ScheduleAt: tc.schedule, ValidityUntil: tc.validity,
			}
			pickle, err := native.EncodeSubmitSM(ctx, request)
			if err != nil {
				t.Fatalf("native encode: %v", err)
			}
			nativeBody, _, err := native.DecodeSubmitSM(ctx, pickle.Body)
			if err != nil {
				t.Fatalf("native decode: %v", err)
			}
			bridgeBody, _, err := bridge.DecodeSubmitSM(ctx, pickle.Body)
			if err != nil {
				t.Fatalf("bridge decode: %v", err)
			}
			if !bytes.Equal(nativeBody.ScheduleDeliveryTime, bridgeBody.ScheduleDeliveryTime) {
				t.Fatalf("schedule wire differs: native %q bridge %q", nativeBody.ScheduleDeliveryTime, bridgeBody.ScheduleDeliveryTime)
			}
			if !bytes.Equal(nativeBody.ValidityPeriod, bridgeBody.ValidityPeriod) {
				t.Fatalf("validity wire differs: native %q bridge %q", nativeBody.ValidityPeriod, bridgeBody.ValidityPeriod)
			}
		})
	}
}
