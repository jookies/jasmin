package smppc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// TestSubmitAuditLineDifferential drives the exact legacy `%`-format for both
// SMS-MT line variants with real CommandStatus / RegisteredDeliveryReceipt / bytes
// objects and asserts the Go renderer produces the identical whole line — the
// guard that catches any per-field mismatch (e.g. smpp-msgid must be a bytes-repr).
func TestSubmitAuditLineDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	type spec struct {
		CID        string `json:"cid"`
		QueueMsgID string `json:"queue_msgid"`
		SMPPMsgID  string `json:"smpp_msgid"` // hex
		StatusName string `json:"status_name"`
		Prio       int    `json:"prio"`
		RegDel     int    `json:"reg_delivery"`
		Validity   string `json:"validity"`
		From       string `json:"from"`    // hex
		To         string `json:"to"`      // hex
		Content    string `json:"content"` // hex
		Privacy    bool   `json:"privacy"`
		WillRetry  bool   `json:"will_retry"`
	}
	specs := []spec{
		{CID: "smppc1", QueueMsgID: "abc-1", SMPPMsgID: hexOf("ABC123"), StatusName: "ESME_ROK",
			Prio: 1, RegDel: 1, Validity: "none", From: hexOf("1111"), To: hexOf("2222"), Content: hexOf("hi")},
		{CID: "gw", QueueMsgID: "m2", SMPPMsgID: hexOf("0f"), StatusName: "ESME_RINVDSTADR",
			Prio: 2, RegDel: 0, Validity: "2026-07-26 00:00:00", From: hexOf("it's"), To: hexOf(`q"x`),
			Content: hexOf("café"), WillRetry: true},
		{CID: "c3", QueueMsgID: "m3", SMPPMsgID: "00ff41", StatusName: "ESME_RSYSERR",
			Prio: 0, RegDel: 2, Validity: "none", From: hexOf("src"), To: hexOf("dst"),
			Content: hexOf("body"), Privacy: true},
	}
	payload, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	const oracle = `
import sys, json, io
from smpp.pdu.pdu_types import CommandStatus
from smpp.pdu.pdu_encoding import RegisteredDeliveryEncoder
enc = RegisteredDeliveryEncoder()
out = []
for s in json.load(sys.stdin):
    status = getattr(CommandStatus, s['status_name'])
    receipt = enc.decode(io.BytesIO(bytes([s['reg_delivery']]))).receipt
    frm = bytes.fromhex(s['from']); to = bytes.fromhex(s['to'])
    content = bytes.fromhex(s['content']); mid = bytes.fromhex(s['smpp_msgid'])
    logged = ('** %s byte content **' % len(content)) if s['privacy'] else ('%r' % content)
    success = "SMS-MT [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [status:%s] [prio:%s] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s] [tlvs:%s]" % (
        s['cid'], s['queue_msgid'], mid, status, s['prio'], receipt, s['validity'], frm, to, logged, 'none')
    error = "SMS-MT [cid:%s] [queue-msgid:%s] [status:ERROR/%s] [retry:%s] [prio:%s] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s] [tlvs:%s]" % (
        s['cid'], s['queue_msgid'], status, s['will_retry'], s['prio'], receipt, s['validity'], frm, to, logged, 'none')
    out.append({'success': success, 'error': error})
print(json.dumps(out))
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []struct {
		Success string `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	statusValue := map[string]uint32{"ESME_ROK": 0x00000000, "ESME_RINVDSTADR": 0x0000000b, "ESME_RSYSERR": 0x00000008}
	for i, s := range specs {
		fields := submitAuditFields{
			ConnectorID: s.CID, QueueMsgID: s.QueueMsgID, SMPPMsgID: unhex(t, s.SMPPMsgID),
			Status: statusValue[s.StatusName], WillRetry: s.WillRetry, Priority: uint8(s.Prio),
			RegisteredDelivery: byte(s.RegDel), Validity: s.Validity,
			SourceAddr: unhex(t, s.From), DestAddr: unhex(t, s.To), ShortMessage: unhex(t, s.Content),
			Privacy: s.Privacy,
		}
		if got := submitAuditLineSuccess(fields); got != want[i].Success {
			t.Errorf("spec %d success:\n go %q\n py %q", i, got, want[i].Success)
		}
		if got := submitAuditLineError(fields); got != want[i].Error {
			t.Errorf("spec %d error:\n go %q\n py %q", i, got, want[i].Error)
		}
	}
}

func hexOf(s string) string { return hex.EncodeToString([]byte(s)) }

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// TestFormatTLVsForLogDifferential proves formatTLVsForLog matches the legacy
// format_tlvs_for_log (order, key:value rendering, enum/bytes/int values, and the
// privacy mode) for the optional params + custom TLVs a Go submit surfaces.
func TestFormatTLVsForLogDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	type custom struct {
		Tag  uint64  `json:"tag"`
		SVal *string `json:"sval,omitempty"`
		IVal *int64  `json:"ival,omitempty"`
		BVal *string `json:"bval,omitempty"` // hex
	}
	type spec struct {
		SARRef   *int     `json:"sar_ref,omitempty"`
		SARTotal *int     `json:"sar_total,omitempty"`
		SARSeq   *int     `json:"sar_seq,omitempty"`
		More     *int     `json:"more,omitempty"`
		Payload  *string  `json:"payload,omitempty"` // hex
		Customs  []custom `json:"customs,omitempty"`
	}
	intp := func(v int) *int { return &v }
	strp := func(v string) *string { return &v }
	i64p := func(v int64) *int64 { return &v }
	specs := []spec{
		{}, // none
		{Payload: strp(hexOf("hi there"))},
		{SARRef: intp(5), SARTotal: intp(3), SARSeq: intp(1), More: intp(1)},
		{SARRef: intp(9), Payload: strp(hexOf("body")), Customs: []custom{{Tag: 0x1400, SVal: strp("hello")}, {Tag: 0x1401, IVal: i64p(42)}}},
		{Customs: []custom{{Tag: 0x1500, BVal: strp(hexOf("a'b"))}, {Tag: 0x1501, SVal: strp("plain")}}},
	}
	payload, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	const oracle = `
import sys, json, binascii
from smpp.pdu.operations import SubmitSM
from jasmin.tools.tlv import format_tlvs_for_log
from smpp.pdu.pdu_types import MoreMessagesToSend
out = []
for s in json.load(sys.stdin):
    p = SubmitSM(source_addr=b'1', destination_addr=b'2', short_message=b'm')
    if s.get('sar_ref') is not None: p.params['sar_msg_ref_num'] = s['sar_ref']
    if s.get('sar_total') is not None: p.params['sar_total_segments'] = s['sar_total']
    if s.get('sar_seq') is not None: p.params['sar_segment_seqnum'] = s['sar_seq']
    if s.get('more') is not None:
        p.params['more_messages_to_send'] = MoreMessagesToSend.MORE_MESSAGES if s['more'] else MoreMessagesToSend.NO_MORE_MESSAGES
    if s.get('payload') is not None: p.params['message_payload'] = binascii.unhexlify(s['payload'])
    customs = []
    for c in s.get('customs', []):
        if c.get('sval') is not None: v = c['sval']
        elif c.get('ival') is not None: v = c['ival']
        else: v = binascii.unhexlify(c['bval'])
        customs.append((c['tag'], 0, 'T', v))
    p.custom_tlvs = customs
    out.append({'plain': format_tlvs_for_log(p, False), 'priv': format_tlvs_for_log(p, True)})
print(json.dumps(out))
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []struct {
		Plain string `json:"plain"`
		Priv  string `json:"priv"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	for i, s := range specs {
		var opt smppwire.OptionalParameters
		if s.SARRef != nil {
			v := uint16(*s.SARRef)
			opt.SARMessageReference = &v
		}
		if s.SARTotal != nil {
			v := byte(*s.SARTotal)
			opt.SARTotalSegments = &v
		}
		if s.SARSeq != nil {
			v := byte(*s.SARSeq)
			opt.SARSegmentSequence = &v
		}
		if s.More != nil {
			v := byte(*s.More)
			opt.MoreMessagesToSend = &v
		}
		if s.Payload != nil {
			opt.MessagePayload = unhex(t, *s.Payload)
		}
		var customs []tlv.TLV
		for _, c := range s.Customs {
			entry := tlv.TLV{Tag: new(big.Int).SetUint64(c.Tag)}
			switch {
			case c.SVal != nil:
				entry.Value = *c.SVal
			case c.IVal != nil:
				entry.Value = *c.IVal
			default:
				entry.Value = unhex(t, *c.BVal)
			}
			customs = append(customs, entry)
		}
		if got := formatTLVsForLog(opt, customs, false); got != want[i].Plain {
			t.Errorf("spec %d plain: go %q, py %q", i, got, want[i].Plain)
		}
		if got := formatTLVsForLog(opt, customs, true); got != want[i].Priv {
			t.Errorf("spec %d priv: go %q, py %q", i, got, want[i].Priv)
		}
	}
}

// TestPythonBytesReprDifferential proves pythonBytesRepr reproduces CPython's
// repr(bytes) byte-for-byte across every single byte value, quote/escape combos,
// and multi-byte strings — the highest-risk rendering in the SMS-MT audit line.
func TestPythonBytesReprDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	// Corpus: every single byte, then curated multi-byte edge cases.
	var corpus [][]byte
	for value := 0; value < 256; value++ {
		corpus = append(corpus, []byte{byte(value)})
	}
	corpus = append(corpus,
		[]byte(""), []byte("plain"), []byte("1111"),
		[]byte("it's"), []byte(`say "hi"`), []byte(`both ' and "`),
		[]byte("tab\there"), []byte("nl\nhere"), []byte("cr\rhere"), []byte(`back\slash`),
		[]byte("café mañana"), []byte{0, 1, 2, 39, 34, 92, 127, 128, 255},
		[]byte("a'b\"c\\d\te"),
	)
	hexInputs := make([]string, len(corpus))
	for i, b := range corpus {
		hexInputs[i] = hex.EncodeToString(b)
	}
	payload, err := json.Marshal(hexInputs)
	if err != nil {
		t.Fatal(err)
	}

	const oracle = `
import sys, json
his = json.load(sys.stdin)
print(json.dumps([repr(bytes.fromhex(h)) for h in his]))
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if len(want) != len(corpus) {
		t.Fatalf("oracle returned %d reprs, want %d", len(want), len(corpus))
	}
	for i, b := range corpus {
		if got := pythonBytesRepr(b); got != want[i] {
			t.Errorf("pythonBytesRepr(%x) = %s, want %s", b, got, want[i])
		}
	}
}

// TestStatusForLogDifferential confirms `%s` of a CommandStatus member is the
// fully-qualified CommandStatus.<NAME> in the target Python (the 3.11/3.12
// enum-__str__ version-sensitivity check), for the names statusForLog composes.
func TestStatusForLogDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	names := []string{"ESME_ROK", "ESME_RINVDSTADR", "ESME_RSYSERR", "ESME_RMSGQFUL", "ESME_RTHROTTLED"}
	payload, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	const oracle = `
import sys, json
from smpp.pdu.pdu_types import CommandStatus
names = json.load(sys.stdin)
print(json.dumps(['%s' % getattr(CommandStatus, n) for n in names]))
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	for i, name := range names {
		// statusForLog composes "CommandStatus." + smppStatusName(value); assert the
		// qualified form matches Python for the same member name.
		if got := "CommandStatus." + name; got != want[i] {
			t.Errorf("status %s: go %s, py %s", name, got, want[i])
		}
	}
}

// TestReceiptForLogDifferential confirms receiptForLog matches `%s` of the
// legacy-decoded registered_delivery.receipt for each valid wire value.
func TestReceiptForLogDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	const oracle = `
import sys, json, io
from smpp.pdu.pdu_encoding import RegisteredDeliveryEncoder
enc = RegisteredDeliveryEncoder()
wires = json.load(sys.stdin)
out = []
for w in wires:
    rd = enc.decode(io.BytesIO(bytes([w])))
    out.append('%s' % rd.receipt)
print(json.dumps(out))
`
	wires := []int{0, 1, 2, 0x10, 0x11, 0x12} // higher bits set: receipt still from low 2 bits
	payload, err := json.Marshal(wires)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	for i, wire := range wires {
		if got := receiptForLog(byte(wire)); got != want[i] {
			t.Errorf("receiptForLog(%#02x) = %s, want %s", wire, got, want[i])
		}
	}
}
