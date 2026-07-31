package main

import (
	"strings"
	"testing"
)

// TestReceiptCountersAgreeWithStatus is the regression this emulator existed to
// need: it used to interpolate an arbitrary stat into a hardcoded
// "dlvrd:001 err:000", so /inject/dlr?stat=UNDELIV produced a receipt claiming
// one message was delivered by a receipt saying none was.
func TestReceiptCountersAgreeWithStatus(t *testing.T) {
	cases := []struct {
		stat  string
		dlvrd string
		err   string
	}{
		{"DELIVRD", "dlvrd:001", "err:000"},
		{"REJECTD", "dlvrd:000", "err:008"},
		{"UNDELIV", "dlvrd:000", "err:008"},
		{"EXPIRED", "dlvrd:000", "err:008"},
		// An unrecognised state must fall back to "nothing delivered", never to
		// the success shape.
		{"WHAT_IS_THIS", "dlvrd:000", "err:008"},
	}
	for _, testCase := range cases {
		t.Run(testCase.stat, func(t *testing.T) {
			receipt := buildReceiptText("fake-1", testCase.stat, "2107261200", "2107261201")
			if !strings.Contains(receipt, testCase.dlvrd) {
				t.Errorf("stat %s: want %s in %q", testCase.stat, testCase.dlvrd, receipt)
			}
			if !strings.Contains(receipt, testCase.err) {
				t.Errorf("stat %s: want %s in %q", testCase.stat, testCase.err, receipt)
			}
			if !strings.Contains(receipt, "stat:"+testCase.stat) {
				t.Errorf("stat %s: status not carried verbatim in %q", testCase.stat, receipt)
			}
			// The contradiction itself, stated as an invariant rather than as
			// three separate field assertions.
			if strings.Contains(receipt, "dlvrd:001") && !strings.Contains(receipt, "stat:DELIVRD") {
				t.Errorf("non-delivered receipt claims a delivery: %q", receipt)
			}
		})
	}
}

// TestDeliveredReceiptIsUnchanged pins the bytes of the one case that existed
// before, so fixing the contradiction did not move the receipt every current
// test and compose drill depends on.
func TestDeliveredReceiptIsUnchanged(t *testing.T) {
	const want = "id:fake-1 sub:001 dlvrd:001 submit date:2107261200 done date:2107261201 stat:DELIVRD err:000 text:"
	if got := buildReceiptText("fake-1", "DELIVRD", "2107261200", "2107261201"); got != want {
		t.Errorf("receipt bytes changed\n got: %q\nwant: %q", got, want)
	}
}

func TestRegisteredDeliveryWanted(t *testing.T) {
	cases := []struct {
		name               string
		registeredDelivery byte
		success            bool
		want               bool
	}{
		{"none on success", 0x00, true, false},
		{"none on failure", 0x00, false, false},
		{"both on success", 0x01, true, true},
		{"both on failure", 0x01, false, true},
		{"failure-only on success", 0x02, true, false},
		{"failure-only on failure", 0x02, false, true},
		{"reserved treated as both", 0x03, true, true},
		// Only bits 0-1 select the receipt mode; the intermediate-notification
		// and id bits above them must not change the answer.
		{"upper bits ignored", 0x1D, true, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := registeredDeliveryWanted(testCase.registeredDelivery, testCase.success)
			if got != testCase.want {
				t.Errorf("registeredDeliveryWanted(%#02x, %t) = %t, want %t",
					testCase.registeredDelivery, testCase.success, got, testCase.want)
			}
		})
	}
}

func TestIsSuccessStatFollowsTheCounters(t *testing.T) {
	if !isSuccessStat("DELIVRD") {
		t.Error("DELIVRD must count as a success")
	}
	for _, stat := range []string{"UNDELIV", "REJECTD", "EXPIRED", "nonsense"} {
		if isSuccessStat(stat) {
			t.Errorf("%s must not count as a success", stat)
		}
	}
}

func TestParseCommandStatus(t *testing.T) {
	cases := []struct {
		input   string
		want    uint32
		wantErr bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"11", 11, false},
		{"0x0000000B", 0x0B, false},
		{"0X58", 0x58, false},
		// Names are refused rather than silently mapped to zero: an operator
		// asking for a failure must not be answered ESME_ROK.
		{"ESME_RTHROTTLED", 0, true},
		{"-1", 0, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			got, err := parseCommandStatus(testCase.input)
			if testCase.wantErr {
				if err == nil {
					t.Errorf("parseCommandStatus(%q) = %d, want an error", testCase.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCommandStatus(%q): %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Errorf("parseCommandStatus(%q) = %#x, want %#x", testCase.input, got, testCase.want)
			}
		})
	}
}
