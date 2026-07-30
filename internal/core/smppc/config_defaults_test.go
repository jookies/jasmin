package smppc_test

import (
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
)

// TestConnectorDefaultsMatchTheFrozenConfig pins every default a connector gets
// when the operator provisions only the essentials. The values are read off the
// frozen SMPPClientConfig; the address ones are wire-visible, so a drift here
// changes the submit_sm bytes a default connector puts on the wire.
func TestConnectorDefaultsMatchTheFrozenConfig(t *testing.T) {
	config := smppc.Config{CID: "defaults", Host: "smsc.example", Port: 2775, SystemID: "u"}
	if err := config.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	for _, testCase := range []struct {
		name string
		got  float64
		want float64
	}{
		{"trx_to (inactivityTimerSecs)", config.TrxTimeout, 300},
		{"res_to (responseTimerSecs)", config.ResTimeout, 120},
		{"pdu_red_to (pduReadTimerSecs)", config.PDUTimeout, 10},
		{"elink_interval (enquireLinkTimerSecs)", config.EnquireLinkInterval, 30},
		{"bind_to (sessionInitTimerSecs)", config.SessionInitTimeout, 30},
		{"requeue_delay", config.RequeueDelay, 120},
		{"src_ton (NATIONAL)", float64(config.SrcTON), 2},
		{"src_npi (ISDN)", float64(config.SrcNPI), 1},
		{"dst_ton (INTERNATIONAL)", float64(config.DstTON), 1},
		{"dst_npi (ISDN)", float64(config.DstNPI), 1},
	} {
		if testCase.got != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.name, testCase.got, testCase.want)
		}
	}
}
