package picklecompat_test

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// billLoadScript loads the native bill and prints its billable fields, proving
// it reconstructs as a real SubmitSmBill a legacy billing consumer can read.
const billLoadScript = `
import sys, base64, pickle
bill = pickle.loads(base64.b64decode(sys.argv[1]))
print("%s|%s|%s|%s|%s|%s" % (
    type(bill).__name__, bill.bid, bill.user.uid,
    bill.getAmount("submit_sm"), bill.getAmount("submit_sm_resp"),
    bill.getAction("decrement_submit_sm_count")))
`

// TestNativeBillLoadsWithBillableFields proves EncodeSubmitSM's bill is a valid
// SubmitSmBill whose bid/uid/amounts/actions match the request.
func TestNativeBillLoadsWithBillableFields(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native := picklecompat.NewNativeCodec()

	request := picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, SourceAddr: picklecompat.Bytes("1111"), DestinationAddr: picklecompat.Bytes("2222"),
		ShortMessage: picklecompat.Bytes("hi"), SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1,
		IncludeBill: true, BillID: "bill-xyz", UserID: "42", Username: "alice",
		SubmitSMAmount: 0.5, SubmitSMRespAmount: 0.25, DecrementSubmitSMCount: 1,
	}
	encoded, err := native.EncodeSubmitSM(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded.Bill) == 0 {
		t.Fatal("bill is empty")
	}
	command := exec.CommandContext(ctx, python, "-c", billLoadScript, base64.StdEncoding.EncodeToString(encoded.Bill))
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	out, err := command.Output()
	if err != nil {
		t.Fatalf("load bill: %v (%s)", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "SubmitSmBill|bill-xyz|42|0.5|0.25|1"
	if got != want {
		t.Fatalf("bill fields = %q, want %q", got, want)
	}
}
