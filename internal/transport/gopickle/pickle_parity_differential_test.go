package gopickle

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// emitScript pickles a matrix of Python values (protocol 2) and prints, per
// line, "base64(pickle)\trepr(value)".
const emitScript = `
import sys, base64, pickle
from smpp.pdu.pdu_types import (CommandId, AddrTon, DataCoding, DataCodingDefault,
    EsmClass, EsmClassMode, EsmClassType, EsmClassGsmFeatures,
    RegisteredDelivery, RegisteredDeliveryReceipt)
VALUES = [
    None, True, False,
    0, 1, 255, 256, 65535, 65536, -1, -2**31, 2**31-1,
    0.0, 3.14, -1.5,
    "", "hello", "héllo unicode ☃",
    b"", b"ABC", b"\x00\xff\x80",
    [], [1, "a", b"b"],
    (), (1,), (1, 2), (1, 2, 3), (1, 2, 3, 4),
    {}, {"k": 1, "k2": b"v"},
    # Simple enums (Enum(value) reduce) and compound objects that carry data in
    # the NEWOBJ args tuple (EsmClass/RegisteredDelivery) or a nested state dict
    # of enum reduces (DataCoding) — the machine's Object.Args + set paths.
    CommandId(8), AddrTon.NATIONAL,
    DataCoding(schemeData=DataCodingDefault.SMSC_DEFAULT_ALPHABET),
    EsmClass(EsmClassMode.STORE_AND_FORWARD, EsmClassType.DEFAULT),
    EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT, [EsmClassGsmFeatures.UDHI_INDICATOR_SET]),
    RegisteredDelivery(RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED),
]
for v in VALUES:
    print(base64.b64encode(pickle.dumps(v, 2)).decode() + "\t" + repr(v))
`

// checkScript loads each base64 pickle from stdin and prints its repr.
const checkScript = `
import sys, base64, pickle
for line in sys.stdin:
    line = line.strip()
    if line:
        print(repr(pickle.loads(base64.b64decode(line))))
`

// TestPickleParityWithPython proves the machine against CPython in both
// directions at once: Python pickles a value -> Go Load decodes it -> Go Dump
// re-encodes it -> Python loads the re-encoding, and its repr must match the
// original. So Go reads what Python writes AND writes what Python reads back to
// the same value, across the int/str/bytes/collection/enum matrix.
func TestPickleParityWithPython(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	emit, err := runPython(ctx, python, emitScript, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	lines := splitNonEmpty(emit)

	wantReprs := make([]string, len(lines))
	var goReencoded bytes.Buffer
	for i, line := range lines {
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			t.Fatalf("line %d missing tab: %q", i, line)
		}
		pyPickle, decErr := base64.StdEncoding.DecodeString(line[:tab])
		if decErr != nil {
			t.Fatalf("line %d base64: %v", i, decErr)
		}
		wantReprs[i] = line[tab+1:]

		value, loadErr := Load(pyPickle)
		if loadErr != nil {
			t.Fatalf("value %d (%s): Load: %v", i, wantReprs[i], loadErr)
		}
		reencoded, dumpErr := Dump(value)
		if dumpErr != nil {
			t.Fatalf("value %d (%s): Dump: %v", i, wantReprs[i], dumpErr)
		}
		goReencoded.WriteString(base64.StdEncoding.EncodeToString(reencoded))
		goReencoded.WriteByte('\n')
	}

	check, err := runPython(ctx, python, checkScript, goReencoded.Bytes())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	gotReprs := splitNonEmpty(check)
	if len(gotReprs) != len(wantReprs) {
		t.Fatalf("got %d reprs, want %d", len(gotReprs), len(wantReprs))
	}
	for i := range wantReprs {
		if gotReprs[i] != wantReprs[i] {
			t.Errorf("value %d: Python(Go(Python)) repr = %q, want %q", i, gotReprs[i], wantReprs[i])
		}
	}
}

func runPython(ctx context.Context, python, script string, stdin []byte) (string, error) {
	command := exec.CommandContext(ctx, python, "-c", script)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	out, err := command.Output()
	return string(out), err
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
