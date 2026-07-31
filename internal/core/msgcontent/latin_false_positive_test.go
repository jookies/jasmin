package msgcontent

import (
	"encoding/hex"
	"strings"
	"testing"
)

// A real message pulled from a running gateway's spool: an Indonesian WhatsApp
// one-time passcode. Every byte is printable ASCII; there is no byte above 0x7F
// anywhere in it, so nothing was ever "stripped" and there is nothing to
// restore.
//
//	<#> Kode WhatsApp: 812-128
//	Jangan bagikan kode ini dengan orang lain
//	4sgLq1p5sV6
//
// The mixed 7-bit-stripped decoder used to claim it, because its only test for
// "this token is real ASCII" was "does it contain a character in 'p'-'~'" —
// and Kode, Jangan, bagikan, dengan and lain contain none. It rendered as
// "М#О Ыяфх WhatsApp: ИБВ-БВИ …", turning the passcode 812-128 into ИБВ-БВИ.
// Destroying a passcode is the one output this platform cannot produce.

func TestLatinBodyIsNeverRestoredAsCyrillic(t *testing.T) {
	raw, err := hex.DecodeString(
		"3c233e204b6f64652057686174734170703a203831322d3132380a4a616e67616e20626167696b616e206b6f646520696e692064656e67616e206f72616e67206c61696e0a3473674c71317035735636")
	if err != nil {
		t.Fatal(err)
	}
	// Both option sets, because PreserveOTPDigits is a mitigation for this
	// misfire and must not be the only thing standing between a passcode and
	// destruction.
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"legacy parity", Options{}},
		{"PreserveOTPDigits", Options{PreserveOTPDigits: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, encoding, err := DecodeWithOptions(raw, 0, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(encoding, "7bit-mixed") {
				t.Errorf("an all-ASCII Latin body was claimed by %s", encoding)
			}
			if !strings.Contains(text, "812-128") {
				t.Errorf("passcode destroyed: %q", text)
			}
			if !strings.Contains(text, "Jangan bagikan kode ini dengan orang lain") {
				t.Errorf("body mangled: %q", text)
			}
		})
	}
}

// The case the heuristic exists for must keep working: a genuinely stripped
// Cyrillic body still restores. "Ваш код подтверждения 12345" in ISO-8859-5
// with every high bit cleared, which reads as "2Ph Z^T _^TbRU`VTU]Xo 12345" —
// punctuation and capitals mid-word, which is what stripping actually produces
// and what tells it apart from Latin prose.
func TestStrippedCyrillicStillRestores(t *testing.T) {
	raw, err := hex.DecodeString("325068205a5e54205f5e54625255605654555d586f203132333435")
	if err != nil {
		t.Fatal(err)
	}
	text, encoding, err := DecodeWithOptions(raw, 0, Options{PreserveOTPDigits: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "код") {
		t.Errorf("stripped Cyrillic no longer restores: %q (encoding %s)", text, encoding)
	}
	if !strings.Contains(text, "12345") {
		t.Errorf("digits lost during restoration: %q", text)
	}
}

// The cases a fable-model audit of this decoder turned up after D-006 landed.
// Each one destroyed a passcode with BOTH D-004 and D-006 active, which is the
// point: the Latin gate guarded two of the three restoration paths and its
// refusal fell straight through into the third and most destructive one.
func TestPasscodeShapesSurviveRestoration(t *testing.T) {
	for _, tc := range []struct {
		name, body, mustKeep string
		alsoKeep             string
	}{
		{
			// Android SMS Retriever. ALL CAPS plus '<' and '>' (two of the
			// cyrillicShadows) tripped the uppercase-ratio gate; the body then
			// fell past the Latin gate into whole-message restoration and
			// 12345 became БВГДЕ.
			name:     "all-caps Latin with SMS Retriever prefix",
			body:     "594f555220434f4445203132333435203c233e",
			mustKeep: "12345",
			alsoKeep: "YOUR CODE",
		},
		{
			name:     "all-caps Latin with colon",
			body:     "594f555220434f44453a2031323334203c233e",
			mustKeep: "1234",
			alsoKeep: "YOUR CODE",
		},
		{
			// Genuine stripped Cyrillic, so restoration is CORRECT here -- but
			// the grouped passcode inside it must survive it. Two runs of three
			// digits satisfied neither the five-digit rule nor the four-digit
			// anchored one.
			name:     "grouped passcode inside real Cyrillic",
			body:     "325068205a5e54205f5e54625255605654555d586f203132332d343536",
			mustKeep: "123-456",
			alsoKeep: "подтверждения",
		},
		{
			// Alphanumeric code: 50% digits, longest run of one, so the digit
			// floor and the run rule both rejected it.
			name:     "alphanumeric code inside real Cyrillic",
			body:     "325068205a5e5420413142324333205e62204170706c65",
			mustKeep: "A1B2C3",
			alsoKeep: "Ваш код",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			text, encoding, err := DecodeWithOptions(raw, 0, Options{PreserveOTPDigits: true})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, tc.mustKeep) {
				t.Errorf("passcode destroyed: want %q in %q (encoding %s)",
					tc.mustKeep, text, encoding)
			}
			// Protecting the code must not be what makes the whole decode fail:
			// the surrounding words have to come out right too.
			if !strings.Contains(text, tc.alsoKeep) {
				t.Errorf("body mangled: want %q in %q (encoding %s)",
					tc.alsoKeep, text, encoding)
			}
		})
	}
}

// NUL padding is not UTF-16 interleaving. Fixed-width SMSC message stores pad
// short bodies with NULs; the null-counting heuristic saw three or more zeros
// on one byte parity and claimed the body as UTF-16, so "1234" plus padding
// decoded to the CJK "ㄲ㌴" and the passcode was gone.
func TestNULPaddedASCIIIsNotUTF16(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"bare code with padding", "3132333400000000000000", "1234"},
		{"sentence with padding", "436f64652031323334000000000000000000", "Code 1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			text, encoding, err := DecodeWithOptions(raw, 0, Options{PreserveOTPDigits: true})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("want %q, got %q (encoding %s)", tc.want, text, encoding)
			}
		})
	}
}

// Genuine UTF-16BE ASCII must still be recognised: its NULs are INTERLEAVED,
// one between every character, which is exactly what tells it apart from
// padding. Without this the fix above would break every UCS-2 ASCII message.
func TestInterleavedNULsAreStillUTF16(t *testing.T) {
	raw, err := hex.DecodeString("0031003200330034")
	if err != nil {
		t.Fatal(err)
	}
	text, encoding, err := DecodeWithOptions(raw, 0, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if text != "1234" || encoding != "utf-16-be" {
		t.Errorf("want 1234 via utf-16-be, got %q via %s", text, encoding)
	}
}
