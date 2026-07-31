package msgcontent

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// vector is one row of testdata/vectors.json. Every expected value in that file
// was produced by running the production Python decoder
// (shadowsms/smsget-jasmin-sms-queues/app/decoder.py) over the input, not by
// reasoning about what it ought to produce — several of them are mojibake, and
// reproducing the mojibake exactly is the point.
//
// DataCoding is -1 for vectors that exercise the no-hint path
// (decode_with_auto_detection); any other value is the DCS byte handed to
// extract_sms_text.
type vector struct {
	Name             string `json:"name"`
	InputHex         string `json:"input_hex"`
	DataCoding       int    `json:"data_coding"`
	ExpectedText     string `json:"expected_text"`
	ExpectedEncoding string `json:"expected_encoding"`
	Source           string `json:"source"`
	Note             string `json:"note,omitempty"`
	KnownFailing     bool   `json:"known_failing,omitempty"`
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vs []vector
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if len(vs) == 0 {
		t.Fatal("vectors.json is empty")
	}
	return vs
}

func (v vector) decode(t *testing.T) (string, string, error) {
	t.Helper()
	raw, err := hex.DecodeString(v.InputHex)
	if err != nil {
		t.Fatalf("bad input_hex: %v", err)
	}
	if v.DataCoding < 0 {
		return DecodeAuto(raw, Options{})
	}
	return Decode(raw, byte(v.DataCoding))
}

func TestVectors(t *testing.T) {
	for _, v := range loadVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			text, enc, _ := v.decode(t)

			if v.KnownFailing {
				// A known-failing vector is a tripwire, not a licence to ignore the
				// case: if it starts passing, the divergence has been closed and the
				// flag has to come off so a future regression is caught again.
				if text == v.ExpectedText && enc == v.ExpectedEncoding {
					t.Fatalf("vector is marked known_failing but now matches Python; "+
						"remove known_failing from testdata/vectors.json\n  note: %s", v.Note)
				}
				t.Logf("known divergence from Python\n  python: enc=%q text=%q\n  go:     enc=%q text=%q\n  why:    %s",
					v.ExpectedEncoding, v.ExpectedText, enc, text, v.Note)
				return
			}

			if enc != v.ExpectedEncoding {
				t.Errorf("encoding = %q, want %q", enc, v.ExpectedEncoding)
			}
			if text != v.ExpectedText {
				t.Errorf("text = %q, want %q", text, v.ExpectedText)
			}
		})
	}
}

// TestVectorErrorSignal pins the ErrUndecodable contract to the same condition the
// Python decoder logs its "Decode issue" warning on, including the dead 'hex' arm
// that lets binary bodies through.
func TestVectorErrorSignal(t *testing.T) {
	for _, v := range loadVectors(t) {
		if v.KnownFailing {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			_, _, err := v.decode(t)
			want := v.ExpectedEncoding == "none" ||
				strings.TrimFunc(v.ExpectedText, isPythonSpace) == ""
			if got := errors.Is(err, ErrUndecodable); got != want {
				t.Errorf("ErrUndecodable = %v, want %v (enc=%q text=%q)",
					got, want, v.ExpectedEncoding, v.ExpectedText)
			}
		})
	}
}

// TestOrderingConstraint guards the invariant this package exists for: a
// 7-bit-stripped Cyrillic body is byte-for-byte valid ASCII, so if the UTF-8 arm at
// step 3 is allowed to claim it — by dropping either suspicion gate, or by letting
// a charset detector run ahead of the explicit codecs — the partner silently
// receives punctuation soup instead of their message, and no test downstream of
// this one would notice.
func TestOrderingConstraint(t *testing.T) {
	body := []byte("15@8B5 B>20@K 2 ?C=:B5 2K40G8 4>")

	text, enc, err := DecodeAuto(body, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == "utf-8" {
		t.Fatal("stripped Cyrillic was claimed by the UTF-8 arm at step 3: " +
			"the isSuspiciousASCII gate is not firing")
	}
	if want := "БЕРИТЕ ТОВАРЫ В ПУНКТЕ ВЫДАЧИ ДО"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if enc != "iso-8859-5-7bit" {
		t.Errorf("encoding = %q, want %q", enc, "iso-8859-5-7bit")
	}

	// The same guard on the mixed path, where the brand name and the OTP have to
	// survive intact while the Cyrillic around them is restored.
	text, enc, err = DecodeAuto([]byte(">4 ::0C=B0 Apple: 706058. 5 45;8B5AL 8<."), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc != "iso-8859-5-7bit-mixed" {
		t.Fatalf("encoding = %q, want %q", enc, "iso-8859-5-7bit-mixed")
	}
	for _, want := range []string{"Apple", "706058", "ДЕЛИТЕСЬ"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q is missing %q", text, want)
		}
	}
}

// TestGSM7Option covers both halves of the Options.GSM7 trade-off: the decoder
// itself is correct, and switching it on costs the Cyrillic restoration. Both
// facts need to stay visible, because the default is the one that looks wrong.
func TestGSM7Option(t *testing.T) {
	// Unpacked GSM 03.38 septets whose meaning differs from ASCII at 0x00 (@),
	// 0x02 ($), 0x11 (_) and 0x5B (Ä).
	septets := []byte{0x00, 0x02, 0x11, 0x5B, 0x41, 0x42}

	text, ok := decodeGSM7(septets)
	if !ok {
		t.Fatal("decodeGSM7 refused valid septets")
	}
	if want := "@$_ÄAB"; text != want {
		t.Fatalf("decodeGSM7 = %q, want %q", text, want)
	}

	// Default (production parity): the same bytes are read as control characters
	// plus ASCII, because the Python gsm0338 branch is dead.
	if _, enc, _ := DecodeDCS(septets, DCSGSM7, Options{}); enc == "gsm0338" {
		t.Error("GSM-7 branch fired with the default options; it must be opt-in")
	}
	if _, enc, _ := DecodeDCS(septets, DCSGSM7, Options{GSM7: true}); enc != "gsm0338" {
		t.Errorf("encoding = %q, want %q with GSM7 enabled", enc, "gsm0338")
	}

	// The documented cost: with GSM-7 on, '@' becomes '¡' before the suspicion
	// gate sees it, the shadow ratio drops, and stripped Cyrillic stops being
	// restored.
	body := []byte("@>25@>G=K9 :>4 63125")
	_, encOff, _ := DecodeAuto(body, Options{})
	_, encOn, _ := DecodeAuto(body, Options{GSM7: true})
	if encOff != "iso-8859-5-7bit" {
		t.Errorf("with GSM7 off, encoding = %q, want %q", encOff, "iso-8859-5-7bit")
	}
	if encOn == "iso-8859-5-7bit" {
		t.Error("with GSM7 on, the Cyrillic restoration still fired; " +
			"the Options.GSM7 warning is stale and should be corrected")
	}
}

func TestResolveDCSGroup(t *testing.T) {
	cases := []struct {
		dcs  byte
		want byte
		why  string
	}{
		{0x00, DCSGSM7, "simple value passes through"},
		{0x08, DCSUCS2, "simple value passes through"},
		{0xF0, DCSGSM7, "message class group, bit 2 clear"},
		{0xF4, DCSBinary, "message class group, bit 2 set"},
		{0xC0, DCSGSM7, "message waiting group, bit 5 clear"},
		{0xE0, DCSUCS2, "message waiting group, bit 5 set"},
		{0x10, DCSGSM7, "general indication, coding 0"},
		{0x14, DCSBinary, "general indication, coding 1"},
		{0x18, DCSUCS2, "general indication, coding 2"},
		{0x1C, 0x1C, "general indication, coding 3 is reserved and passes through"},
		{0x40, 0x40, "0x40-0xBF is not matched by any group; see the KNOWN DIVERGENCE note"},
	}
	for _, c := range cases {
		if got := resolveDCSGroup(c.dcs); got != c.want {
			t.Errorf("resolveDCSGroup(%#02x) = %#02x, want %#02x (%s)", c.dcs, got, c.want, c.why)
		}
	}
}

func TestParseUDH(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
		ok    bool
		check func(*testing.T, UDH)
	}{
		{
			name:  "8bit_concat",
			input: append([]byte{0x05, 0x00, 0x03, 0xAA, 0x02, 0x01}, []byte("Hello")...),
			ok:    true,
			check: func(t *testing.T, u UDH) {
				if !u.HasConcat || u.ConcatRef != 0xAA || u.ConcatTotal != 2 || u.ConcatSeq != 1 {
					t.Errorf("concat = %+v", u)
				}
			},
		},
		{
			name:  "16bit_concat",
			input: append([]byte{0x06, 0x08, 0x04, 0x12, 0x34, 0x03, 0x02}, []byte("Hello")...),
			ok:    true,
			check: func(t *testing.T, u UDH) {
				if !u.HasConcat || u.ConcatRef != 0x1234 || u.ConcatTotal != 3 || u.ConcatSeq != 2 {
					t.Errorf("concat = %+v", u)
				}
			},
		},
		{
			name:  "nl_locking_shift",
			input: append([]byte{0x03, 0x25, 0x01, 0x07}, []byte("payload")...),
			ok:    true,
			check: func(t *testing.T, u UDH) {
				if !u.HasLocking || u.LockingShift != 7 {
					t.Errorf("locking shift = %+v", u)
				}
			},
		},
		{
			name:  "nl_single_shift",
			input: append([]byte{0x03, 0x24, 0x01, 0x01}, []byte("payload")...),
			ok:    true,
			check: func(t *testing.T, u UDH) {
				if !u.HasSingle || u.SingleShift != 1 {
					t.Errorf("single shift = %+v", u)
				}
			},
		},
		{
			name:  "concat_plus_nl",
			input: append([]byte{0x08, 0x00, 0x03, 0xBB, 0x02, 0x01, 0x25, 0x01, 0x07}, []byte("msg")...),
			ok:    true,
			check: func(t *testing.T, u UDH) {
				if !u.HasConcat || !u.HasLocking || u.LockingShift != 7 {
					t.Errorf("combined = %+v", u)
				}
			},
		},
		{name: "first_byte_too_large", input: []byte("\x80Hello")},
		{name: "truncated_ie", input: []byte{0x05, 0x00, 0x03, 0xAA}},
		{
			name:  "ie_chain_shorter_than_udhl",
			input: append([]byte{0x06, 0x00, 0x03, 0xAA, 0x02, 0x01, 0xFF}, []byte("body")...),
		},
		{name: "too_short", input: []byte{0x01}},
		{name: "zero_length", input: []byte{0x00, 0x41}},
		{
			// A plain message whose first byte happens to be small: the IE chain does
			// not fill UDHL, so the parser must refuse rather than eat six characters.
			name:  "plain_text_starting_with_05",
			input: []byte("\x05Hello World, this is a longer message that is definitely not UDH"),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			udh, ok := ParseUDH(c.input)
			if ok != c.ok {
				t.Fatalf("ParseUDH ok = %v, want %v", ok, c.ok)
			}
			if ok && c.check != nil {
				c.check(t, udh)
			}
		})
	}
}

func TestIsSuspiciousASCII(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"15@8B5 B>20@K 2 ?C=:B5 2K40G8 4>", true},          // production Cyrillic garble
		{"@I2+I25H-@#20D!H2!@5H", true},                     // production Thai-pattern garble
		{"@>25@>G=K9 :>4 63125", true},                      // digit-heavy fallback arm
		{"Hello World, how are you?", false},                //
		{"Payment code: ABC123", false},                     //
		{"HELLO WORLD", false},                              // all caps, no shadow characters
		{"https://wa.me/77758968303", false},                //
		{"Hi", false},                                       // under the six-character floor
		{"", false},                                         //
		{">4 ::0C=B0 Apple: 706058. 5 45;8B5AL 8<.", false}, // mixed case, handled by the mixed gate
	}
	for _, c := range cases {
		if got := isSuspiciousASCII(c.text); got != c.want {
			t.Errorf("isSuspiciousASCII(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestIsSuspiciousMixedASCII(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{">4 ::0C=B0 Apple: 706058. 5 45;8B5AL 8<.", true},
		{">4 ::0C=B0 Apple: 825355. 5 45;8B5AL 8<.", true},
		{"15@8B5 B>20@K 2 ?C=:B5", false}, // pure garble, no 0x70-0x7E characters
		{"Hello World 12345", false},      // no shadow characters
		{"Price > 50 stripe", false},      // one shadow character, floor is two
	}
	for _, c := range cases {
		if got := isSuspiciousMixedASCII(c.text); got != c.want {
			t.Errorf("isSuspiciousMixedASCII(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestHasOTPDigitRun(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{"706058", true},
		{"706058.", true},
		{"45;8B5AL", false},       // stripped Cyrillic, under the 80% digit floor
		{"8972768#A?59B5", false}, // 71% digits: Cyrillic А-Й shadowed onto 0-9
		{"5", false},
		{"", false},
		{"123", false}, // digits, but no run of four
	}
	for _, c := range cases {
		if got := hasOTPDigitRun(c.token, 4); got != c.want {
			t.Errorf("hasOTPDigitRun(%q) = %v, want %v", c.token, got, c.want)
		}
	}
}

// TestCharsetOverrides pins the measured differences between x/text's WHATWG
// tables and CPython's. Each of these bytes decides a strict-decode outcome, and a
// silent x/text table update would otherwise reroute traffic without failing a test.
func TestCharsetOverrides(t *testing.T) {
	for _, cs := range []*charset{iso88595, iso88596, iso88597, iso88598, iso885911} {
		for b := 0x80; b <= 0x9F; b++ {
			if got := cs.table[b]; got != rune(b) {
				t.Errorf("%s[%#02x] = %#04x, want the C1 control %#04x", cs.name, b, got, b)
			}
		}
	}
	if koi8u.table[0xAE] != 0x255D || koi8u.table[0xBE] != 0x256C {
		t.Errorf("koi8-u 0xAE/0xBE = %#04x/%#04x, want the CPython box-drawing glyphs 0x255D/0x256C",
			koi8u.table[0xAE], koi8u.table[0xBE])
	}
	if tis620.table[0xA0] != undefinedByte {
		t.Errorf("tis-620[0xA0] = %#04x, want undefined", tis620.table[0xA0])
	}
	if iso885911.table[0xA0] != 0x00A0 {
		t.Errorf("iso-8859-11[0xA0] = %#04x, want U+00A0", iso885911.table[0xA0])
	}
	// CP1251's single hole is what lets the Cyrillic candidate loop reject a body
	// that ISO-8859-5 will happily take.
	if cp1251.table[0x98] != undefinedByte {
		t.Errorf("cp1251[0x98] = %#04x, want undefined", cp1251.table[0x98])
	}
}

func TestDecodeUTF16ErrorModes(t *testing.T) {
	loneHigh := []byte{0x00, 0x41, 0xD8, 0x00, 0x00, 0x42} // 'A', unpaired high surrogate, 'B'
	if got, want := decodeUTF16(loneHigh, true, utf16Ignore), "AB"; got != want {
		t.Errorf("ignore = %q, want %q", got, want)
	}
	if got, want := decodeUTF16(loneHigh, true, utf16Replace), "A�B"; got != want {
		t.Errorf("replace = %q, want %q", got, want)
	}

	odd := []byte{0x00, 0x41, 0x00} // trailing half unit
	if got, want := decodeUTF16(odd, true, utf16Ignore), "A"; got != want {
		t.Errorf("odd ignore = %q, want %q", got, want)
	}
	if got, want := decodeUTF16(odd, true, utf16Replace), "A�"; got != want {
		t.Errorf("odd replace = %q, want %q", got, want)
	}

	pair := []byte{0xD8, 0x3D, 0xDE, 0x00} // U+1F600
	if got, want := decodeUTF16(pair, true, utf16Ignore), "\U0001F600"; got != want {
		t.Errorf("surrogate pair = %q, want %q", got, want)
	}
}

// TestDecodeUTF8LossyMaximalSubpart pins CPython's error granularity: a truncated
// multi-byte sequence is one error, not one per byte. Go's utf8.DecodeRune reports
// a width of 1 on every failure, which would emit the wrong number of U+FFFDs.
func TestDecodeUTF8LossyMaximalSubpart(t *testing.T) {
	cases := []struct {
		name    string
		input   []byte
		ignore  string
		replace string
	}{
		{"truncated_3byte", []byte{0xE2, 0x82}, "", "�"},
		{"latin1_in_utf8", []byte("Voil\xe0 caf\xe9"), "Voil caf", "Voil� caf�"},
		{"lone_continuation", []byte{0x41, 0x80, 0x42}, "AB", "A�B"},
		// 0xC0 is an overlong lead and 0xAF a stray continuation: two separate
		// one-byte errors, not one two-byte sequence.
		{"overlong", []byte{0xC0, 0xAF}, "", "��"},
		{"valid", []byte("héllo"), "héllo", "héllo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeUTF8Lossy(c.input, false); got != c.ignore {
				t.Errorf("ignore = %q, want %q", got, c.ignore)
			}
			if got := decodeUTF8Lossy(c.input, true); got != c.replace {
				t.Errorf("replace = %q, want %q", got, c.replace)
			}
		})
	}
}

func TestSplitKeepingWhitespace(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a b", []string{"a", " ", "b"}},
		{"  a  ", []string{"  ", "a", "  "}},
		{"a", []string{"a"}},
		{"", nil},
		{"a\t\nb", []string{"a", "\t\n", "b"}},
	}
	for _, c := range cases {
		got := splitKeepingWhitespace(c.in)
		if strings.Join(got, "") != c.in {
			t.Errorf("splitKeepingWhitespace(%q) = %q, does not rejoin to the input", c.in, got)
		}
		if len(got) != len(c.want) {
			t.Errorf("splitKeepingWhitespace(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitKeepingWhitespace(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
}

// TestPreserveOTPDigitsRecoversTheCode pins the one deliberate divergence from
// the Python source. The legacy answer destroys the code the message exists to
// deliver; both behaviours are asserted here so neither can drift silently.
func TestPreserveOTPDigitsRecoversTheCode(t *testing.T) {
	body := []byte("@>25@>G=K9 :>4 63125")

	legacyText, legacyEnc, _ := DecodeAuto(body, Options{})
	if legacyText != "РОВЕРОЧНЫЙ КОД ЖГБВЕ" {
		t.Fatalf("legacy parity broke: got %q via %q", legacyText, legacyEnc)
	}

	fixedText, fixedEnc, err := DecodeAuto(body, Options{PreserveOTPDigits: true})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(fixedText, "63125") {
		t.Errorf("OTP code destroyed: got %q via %q", fixedText, fixedEnc)
	}
	if want := "РОВЕРОЧНЫЙ КОД 63125"; fixedText != want {
		t.Errorf("text = %q via %q, want %q", fixedText, fixedEnc, want)
	}
}

// A body with no code must decode identically with the fix on and off: the fix
// may only change messages that carry a digit run.
func TestPreserveOTPDigitsLeavesOtherBodiesAlone(t *testing.T) {
	bodies := [][]byte{
		[]byte("15@8B5 B>20@K"),
		[]byte("Hello there"),
		[]byte(">4 ::0C=B0 Apple: 706058. 5 45;8B5AL 8<."),
	}
	for _, body := range bodies {
		legacyText, legacyEnc, _ := DecodeAuto(body, Options{})
		fixedText, fixedEnc, _ := DecodeAuto(body, Options{PreserveOTPDigits: true})
		if legacyText != fixedText || legacyEnc != fixedEnc {
			t.Errorf("body %q diverged without a digit run:\n  legacy %q via %q\n  fixed  %q via %q",
				body, legacyText, legacyEnc, fixedText, fixedEnc)
		}
	}
}

// The counterexamples an adversarial review produced against the first version
// of the D-004 fix. A four-letter word drawn only from А-Й strips to four pure
// digits, so "any four-digit run is a code" turned ДЕДА into 4540 and БЕДА into
// 1540 — making the fix worse than the defect on messages that carry no code at
// all. Each of these must decode identically with the option on and off.
func TestPreserveOTPDigitsDoesNotEatCyrillicWords(t *testing.T) {
	cases := []struct {
		name  string
		hex   string
		wants string // substring that must survive
	}{
		{"deda", "3f40383235373b38203435343020343e3c3e39204135333e343d4f", "ДЕДА"},
		{"beda", "413b4347383b30414c20313534302037323e3d382041403e473d3e", "БЕДА"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := mustHex(t, tc.hex)
			legacyText, legacyEnc, _ := DecodeAuto(body, Options{})
			fixedText, fixedEnc, _ := DecodeAuto(body, Options{PreserveOTPDigits: true})
			if legacyText != fixedText || legacyEnc != fixedEnc {
				t.Errorf("a body with no code diverged:\n  legacy %q via %q\n  fixed  %q via %q",
					legacyText, legacyEnc, fixedText, fixedEnc)
			}
			if !strings.Contains(fixedText, tc.wants) {
				t.Errorf("word destroyed: %q does not contain %q", fixedText, tc.wants)
			}
		})
	}
}

// A long code is the case the first version silently declined: the preserved
// digits counted against the mixed decoder's Cyrillic ratio, the gate rejected
// the restore at 46.7%, and the whole-message path destroyed the code anyway —
// with the option on and nothing logged.
func TestPreserveOTPDigitsProtectsLongCodes(t *testing.T) {
	body := mustHex(t, "3f403e3235403e473d4b39203a3e342031313131323232323333333334343434")
	text, enc, _ := DecodeAuto(body, Options{PreserveOTPDigits: true})
	if !strings.Contains(text, "1111222233334444") {
		t.Errorf("long code destroyed: %q via %q", text, enc)
	}
}

// A four-digit code is protected when the message says it is a code. Without an
// anchor word four digits are indistinguishable from a four-letter А-Й word, so
// the anchor is what makes the shorter run safe to trust.
func TestPreserveOTPDigitsProtectsAnchoredFourDigitCode(t *testing.T) {
	body := mustHex(t, "323048203f403e3235403e473d4b39203a3e342031323334203d383a3e3c43203d3520413e3e314930394235")
	text, enc, _ := DecodeAuto(body, Options{PreserveOTPDigits: true})
	if !strings.Contains(text, "1234") {
		t.Errorf("anchored four-digit code destroyed: %q via %q", text, enc)
	}
	if !strings.Contains(text, "КОД") {
		t.Errorf("anchor word not restored: %q via %q", text, enc)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return raw
}
