// Package msgcontent decodes the body of an SMPP short_message into text.
//
// It is a port of the Python queue tap's app/decoder.py — the only piece of the
// platform that has ever read partner message content in production, and the
// behaviour it encodes was learned from real traffic rather than from a spec.
// The port is deliberately bug-for-bug: docs/plans/021 Step 3 requires the Go
// output to match the Python output byte for byte so the differential harness in
// Step 14 has a fixed point to compare against. Where the Python behaviour looks
// wrong it is reproduced and labelled with a KNOWN DIVERGENCE note rather than
// quietly corrected; correcting one is a separate, deliberate change with its own
// vector.
//
// Encoding coverage follows the Python docstring: GSM 7-bit (DCS 0x00), UCS-2 /
// UTF-16BE (0x08), Latin-1 (0x03), Cyrillic ISO-8859-5 / CP1251 / KOI8 (0x06),
// Hebrew ISO-8859-8 (0x07), Shift-JIS (0x05), ISO-2022-JP (0x0A), EUC-JP (0x0D),
// EUC-KR (0x0E), plus auto-detected Thai, Arabic and Greek, plus the restoration
// of 8-bit text whose MSB an upstream SMSC stripped to push it down a 7-bit
// channel.
//
// # Ordering constraint
//
// The auto-detection chain runs specific 8-bit codecs BEFORE any statistical
// charset detection, and both before the 7-bit-stripped restoration. Inverting
// the first two breaks Cyrillic restoration: a generic detector reads short
// Cyrillic or Thai byte runs as Big5/GB2312 because the byte patterns overlap,
// wins on confidence, and the message is delivered as CJK mojibake. The Python
// source carries the same warning at the same place. See decodeAuto.
package msgcontent

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/warthog618/sms/encoding/gsm7"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
)

// SMPP Data Coding Scheme values, simple 4-bit group 0x00-0x0F (3GPP TS 23.038).
// Values above 0x0F are collapsed onto these by resolveDCSGroup.
const (
	DCSGSM7          byte = 0x00 // GSM 7-bit default alphabet
	DCSASCII         byte = 0x01 // IA5 (CCITT T.50) / ASCII
	DCSBinary        byte = 0x02 // octet unspecified (8-bit binary)
	DCSLatin1        byte = 0x03 // Latin-1 (ISO-8859-1)
	DCSBinaryAlt     byte = 0x04 // octet unspecified (8-bit binary), alternate
	DCSJIS           byte = 0x05 // JIS X 0208-1990 (Shift-JIS on the wire)
	DCSCyrillic      byte = 0x06 // Cyrillic (ISO-8859-5 nominally, CP1251 in practice)
	DCSHebrew        byte = 0x07 // Hebrew (ISO-8859-8)
	DCSUCS2          byte = 0x08 // UCS-2 / UTF-16BE
	DCSPictogram     byte = 0x09 // pictogram
	DCSISO2022JP     byte = 0x0A // ISO-2022-JP
	DCSExtendedKanji byte = 0x0D // extended Kanji JIS X 0212-1990
	DCSKSC5601       byte = 0x0E // KS C 5601 (Korean, EUC-KR in practice)
)

// ErrUndecodable reports that the body produced no usable text: the result is
// empty or whitespace-only, or no codec claimed it. Text and encoding are still
// returned so the caller can spool the raw bytes and show what was attempted.
// This mirrors the "Decode issue" warning the Python decoder logs at the same
// point (decoder.py:862) — it is a signal, not a failure to be retried.
var ErrUndecodable = errors.New("msgcontent: body did not decode to usable text")

// Options tunes the parts of the port that deliberately depart from the Python
// source. The zero value is exact production parity.
type Options struct {
	// GSM7 enables the GSM 03.38 branches, which are dead code in the Python
	// service: decoder.py calls gsm0338.decode(), and the gsm0338 package (1.1.0,
	// the current release) exports no module-level decode — only Codec and
	// find_gsm0338. The AttributeError lands in a bare `except Exception: pass`,
	// so every GSM-7 decode silently falls through to auto-detection and DCS 0x00
	// bodies are read as UTF-8/ASCII instead.
	//
	// Enabling it is not free, which is why it is off by default. GSM 03.38 and
	// ASCII disagree at 0x00, 0x02, 0x03, 0x11, 0x24, 0x40, 0x5B-0x60 and
	// 0x7B-0x7F, and those positions are exactly the ones the 7-bit-stripped
	// Cyrillic heuristic keys on. Turning GSM-7 on rewrites '@' to '¡' before
	// isSuspiciousASCII ever sees it, the shadow-character ratio drops under its
	// threshold, and the flagship Cyrillic restoration stops firing. Flipping this
	// is a routing-level decision (a connector whose partner really does send
	// packed GSM-7), not a global default.
	GSM7 bool

	// PreserveOTPDigits fixes a defect in the Python source that destroys the
	// digits of an OTP code — the one thing an OTP message exists to carry.
	//
	// The whole-message restoration ORs 0x80 onto every byte in 0x21-0x7F, so a
	// digit run is rewritten as Cyrillic letters: "@>25@>G=K9 :>4 63125" comes
	// back as "РОВЕРОЧНЫЙ КОД ЖГБВЕ", where 63125 became ЖГБВЕ. The token-wise
	// restoration right next to it already handles this correctly — hasOTPDigitRun
	// keeps a digit token intact — but it is only reached through
	// isSuspiciousMixedASCII, which requires two characters in 0x70-0x7E, and an
	// all-uppercase Cyrillic message has none. So the correct code exists and
	// never runs. decoder.py:120-135's own comment says the intended output keeps
	// the digits, and no Python test asserts either result: this was not a choice
	// anyone made.
	//
	// With this set, a stripped body containing an OTP-shaped digit run is
	// restored token by token instead of wholesale, which recovers the Cyrillic
	// words and leaves the code alone.
	//
	// It is off in the zero value because the zero value is exact production
	// parity, which is what the byte-differential harness needs. Production
	// wiring turns it on; see termination.DefaultDecodeOptions.
	//
	// The residual ambiguity is real and unavoidable: ISO-8859-5 maps А-Й onto
	// '0'-'9', so a genuine Cyrillic word can arrive looking like digits. The
	// 80%-digit floor and the four-digit consecutive run are what keep that from
	// firing on ordinary words, but a four-letter word in А-Й with no other
	// letters would still be left as digits. In an OTP platform that trade is
	// worth making in this direction.
	PreserveOTPDigits bool
}

// Decode decodes a raw SMPP short_message field, stripping a User Data Header if
// one is structurally present, and returns the text, the label of the codec that
// actually matched, and ErrUndecodable when nothing usable came out. It is
// decoder.py:extract_sms_text, which also returns the label the Python version
// only logs.
func Decode(raw []byte, dataCoding byte) (string, string, error) {
	return DecodeWithOptions(raw, dataCoding, Options{})
}

// DecodeWithOptions is Decode with the parity switches in Options applied.
func DecodeWithOptions(raw []byte, dataCoding byte, opts Options) (string, string, error) {
	if len(raw) == 0 {
		return "", "none", ErrUndecodable
	}

	body := raw
	// UDH detection goes through the strict IE-chain validator, not a "first byte
	// is small" heuristic: plain text starting with \x05 would otherwise lose its
	// first six characters.
	if udh, ok := ParseUDH(raw); ok {
		if start := int(udh.Length) + 1; start < len(raw) {
			body = raw[start:]
		}
	}

	var text, enc string
	if dcs := resolveDCSGroup(dataCoding); dcs != DCSGSM7 {
		text, enc = decodeDCS(body, dcs, opts)
	} else {
		// DCS 0x00 is not trusted. Legacy Cyrillic and Thai senders mislabel their
		// DCS far more often than they label it correctly, so a declared GSM-7 is
		// treated as "unknown" and sent through detection.
		text, enc = decodeAuto(body, opts)
	}

	return text, enc, decodeIssue(text, enc)
}

// DecodeDCS decodes a body whose UDH has already been removed, honouring the
// declared data coding. It is decoder.py:decode_with_dcs.
func DecodeDCS(body []byte, dataCoding byte, opts Options) (string, string, error) {
	text, enc := decodeDCS(body, dataCoding, opts)
	return text, enc, decodeIssue(text, enc)
}

// DecodeAuto decodes a body with no usable data-coding hint. It is
// decoder.py:decode_with_auto_detection.
func DecodeAuto(body []byte, opts Options) (string, string, error) {
	text, enc := decodeAuto(body, opts)
	return text, enc, decodeIssue(text, enc)
}

// decodeIssue reproduces the Python "Decode issue" warning condition exactly,
// including its dead arm: decoder.py:862 tests `encoding in ('hex', 'none')`, but
// the binary branch labels its output 'binary-hex', so a binary body has never
// tripped this check. Widening it to 'binary-hex' would start rejecting DCS 0x02
// traffic that the legacy path accepts.
func decodeIssue(text, enc string) error {
	if enc == "hex" || enc == "none" {
		return ErrUndecodable
	}
	if text == "" || strings.TrimFunc(text, isPythonSpace) == "" {
		return ErrUndecodable
	}
	return nil
}

// ---------------------------------------------------------------------------
// User Data Header
// ---------------------------------------------------------------------------

// UDH holds the concatenation and national-language fields of a User Data Header.
type UDH struct {
	Length       byte // the UDHL octet; the header occupies Length+1 bytes
	ConcatRef    uint16
	ConcatTotal  byte
	ConcatSeq    byte
	HasConcat    bool
	SingleShift  byte
	HasSingle    bool
	LockingShift byte
	HasLocking   bool
}

// ParseUDH parses a User Data Header, requiring the IE chain to walk cleanly to
// exactly UDHL. The strictness is the point: a lenient parser turns any message
// whose first byte happens to be small into a concatenation fragment, and the
// fragment then waits forever for siblings that do not exist.
func ParseUDH(shortMessage []byte) (UDH, bool) {
	if len(shortMessage) < 2 {
		return UDH{}, false
	}

	length := shortMessage[0]
	if length == 0 || length > 20 || int(length) >= len(shortMessage) {
		return UDH{}, false
	}

	udh := shortMessage[1 : 1+int(length)]
	info := UDH{Length: length}

	i := 0
	for i+2 <= len(udh) {
		iei := udh[i]
		iedl := int(udh[i+1])
		if i+2+iedl > len(udh) {
			return UDH{}, false // truncated IE
		}
		data := udh[i+2 : i+2+iedl]
		switch {
		case iei == 0x00 && iedl == 3:
			info.ConcatRef = uint16(data[0])
			info.ConcatTotal = data[1]
			info.ConcatSeq = data[2]
			info.HasConcat = true
		case iei == 0x08 && iedl == 4:
			info.ConcatRef = uint16(data[0])<<8 | uint16(data[1])
			info.ConcatTotal = data[2]
			info.ConcatSeq = data[3]
			info.HasConcat = true
		case iei == 0x24 && iedl == 1:
			// National Language Single Shift (3GPP TS 23.038 §9.2.3.24.10).
			info.SingleShift = data[0]
			info.HasSingle = true
		case iei == 0x25 && iedl == 1:
			// National Language Locking Shift (3GPP TS 23.038 §9.2.3.24.11).
			info.LockingShift = data[0]
			info.HasLocking = true
		}
		i += 2 + iedl
	}

	if i != len(udh) {
		return UDH{}, false // trailing bytes: the IE chain does not fill UDHL
	}
	return info, true
}

// ---------------------------------------------------------------------------
// Data Coding Scheme
// ---------------------------------------------------------------------------

// resolveDCSGroup collapses the extended DCS bit-group formats onto the simple
// 0x00-0x0E constants.
//
// Check order matters: the 4-bit message-class pattern (0xF0-0xFF) also satisfies
// the 2-bit message-waiting pattern (0xC0-0xFF), so testing the broader one first
// would shadow it and route every class-0 flash SMS to UCS-2.
//
// KNOWN DIVERGENCE from the spec, preserved from Python: the general data coding
// group is tested as (dcs & 0xC0) == 0x00, which covers 0x10-0x3F only. Values
// 0x40-0xBF fall through unresolved and end up in the unknown-DCS arm, which
// sends them to auto-detection. In practice that is close to the right answer, so
// the Python behaviour stands.
func resolveDCSGroup(dcs byte) byte {
	if dcs <= 0x0F {
		return dcs
	}
	if dcs&0xF0 == 0xF0 {
		if dcs&0x04 != 0 {
			return DCSBinary
		}
		return DCSGSM7
	}
	if dcs&0xC0 == 0xC0 {
		if dcs&0x20 != 0 {
			return DCSUCS2
		}
		return DCSGSM7
	}
	if dcs&0xC0 == 0x00 {
		switch (dcs >> 2) & 0x03 {
		case 0:
			return DCSGSM7
		case 1:
			return DCSBinary
		case 2:
			return DCSUCS2
		}
	}
	return dcs
}

// ---------------------------------------------------------------------------
// DCS-driven decoding
// ---------------------------------------------------------------------------

func decodeDCS(data []byte, dataCoding byte, opts Options) (string, string) {
	if len(data) == 0 {
		return "", "none"
	}

	switch resolveDCSGroup(dataCoding) {
	case DCSUCS2:
		// errors='ignore', then strip NULs: partner ESMEs pad UCS-2 payloads with
		// trailing nulls, and odd-length or surrogate-broken bodies are common
		// enough that erroring on them would drop real messages.
		return strings.Trim(decodeUTF16(data, true, utf16Ignore), "\x00"), "utf-16-be"

	case DCSLatin1:
		return decodeSingleByteReplace(latin1, data), "latin-1"

	case DCSASCII:
		if text, ok := decodeSingleByteStrict(asciiCodec, data); ok {
			return text, "ascii"
		}
		return decodeSingleByteReplace(asciiCodec, data), "ascii-lossy"

	case DCSCyrillic:
		// DCS 0x06 is officially ISO-8859-5, but Russian and Ukrainian providers
		// send CP1251 under it constantly. Decode with each candidate and keep the
		// one that yields the most actual Cyrillic; ties go to the first tried,
		// which is why CP1251 leads.
		best, bestEnc, bestScore := "", "iso-8859-5", -1
		for _, cs := range []*charset{cp1251, iso88595, koi8r, koi8u} {
			candidate, ok := decodeSingleByteStrict(cs, data)
			if !ok {
				continue
			}
			if score := countInRange(candidate, 0x0400, 0x04FF); score > bestScore {
				best, bestEnc, bestScore = candidate, cs.name, score
			}
		}
		if bestScore >= 0 {
			return best, bestEnc
		}
		// Unreachable: ISO-8859-5 defines all 256 bytes, so the loop always scores
		// at least once. Kept as the same backstop the Python source has.
		return decodeSingleByteReplace(iso88595, data), "iso-8859-5-lossy"

	case DCSHebrew:
		return decodeSingleByteReplace(iso88598, data), "iso-8859-8"

	case DCSJIS:
		// DCS 0x05 names JIS X 0208-1990; Shift-JIS is what actually arrives.
		for _, c := range []struct {
			name string
			enc  encoding.Encoding
		}{{"shift_jis", japanese.ShiftJIS}, {"iso-2022-jp", japanese.ISO2022JP}, {"euc-jp", japanese.EUCJP}} {
			if text, ok := decodeMultiByteStrict(c.enc, data); ok {
				return text, c.name
			}
		}
		return decodeMultiByteReplace(japanese.ShiftJIS, data), "shift_jis-lossy"

	case DCSISO2022JP:
		if text, ok := decodeMultiByteStrict(japanese.ISO2022JP, data); ok {
			return text, "iso-2022-jp"
		}
		return decodeMultiByteReplace(japanese.ISO2022JP, data), "iso-2022-jp-lossy"

	case DCSExtendedKanji:
		for _, c := range []struct {
			name string
			enc  encoding.Encoding
		}{{"euc-jp", japanese.EUCJP}, {"shift_jis", japanese.ShiftJIS}} {
			if text, ok := decodeMultiByteStrict(c.enc, data); ok {
				return text, c.name
			}
		}
		return decodeMultiByteReplace(japanese.EUCJP, data), "euc-jp-lossy"

	case DCSKSC5601:
		if text, ok := decodeMultiByteStrict(korean.EUCKR, data); ok {
			return text, "euc-kr"
		}
		return decodeMultiByteReplace(korean.EUCKR, data), "euc-kr-lossy"

	case DCSBinary, DCSBinaryAlt:
		// Binary stays binary: hand back hex and let the caller decide. Note this
		// label is 'binary-hex', which is why decodeIssue's 'hex' arm is dead.
		return hex.EncodeToString(data), "binary-hex"

	case DCSGSM7:
		if opts.GSM7 {
			if text, ok := decodeGSM7(data); ok {
				return text, "gsm0338"
			}
		}
		return decodeAuto(data, opts)

	default:
		return decodeAuto(data, opts)
	}
}

// ---------------------------------------------------------------------------
// Auto-detection
// ---------------------------------------------------------------------------

// decodeAuto tries codecs in priority order and returns the first that both
// decodes and passes its quality gate.
//
//  1. BOM (UTF-8, UTF-16 LE/BE)
//  2. UTF-16 null-byte heuristic over a 60-byte window
//  3. UTF-8 strict, gated by the 7-bit-stripped suspicion checks
//  4. explicit 8-bit codecs for Cyrillic / Thai / Arabic / Greek
//  5. (statistical charset detection — see the note below)
//  6. GSM 7-bit, when every byte is < 0x80
//  7. 7-bit-stripped restoration: MSB back on, re-decode
//  8. lossy UTF-8, then Latin-1, which cannot fail
//
// Steps 4 and 5 are the ordering constraint this package exists to protect. Any
// statistical detector belongs at step 5 and nowhere earlier: it reads a short
// run of Cyrillic or Thai bytes as Big5 or GB2312 — the byte patterns genuinely
// overlap — reports high confidence, and the partner receives CJK mojibake
// instead of their OTP. The explicit codecs must get first refusal.
//
// KNOWN DIVERGENCE: step 5 is not implemented. The Python service uses
// charset_normalizer 2.0.4, whose output is a property of that library's
// coherence tables rather than of any specification, and no Go port of it exists;
// approximating it with a different detector would produce silent, unpredictable
// divergence on exactly the traffic this step is reached for. Two vectors in
// testdata/vectors.json are marked known_failing because of this, and both are
// cases where the Python answer is itself mojibake. If a detector is ever added,
// it goes here, between step 4 and step 6.
func decodeAuto(data []byte, opts Options) (string, string) {
	if len(data) == 0 {
		return "", "none"
	}

	// 1. BOM. Note these use replace, not ignore, and do not strip NULs — the
	//    UCS-2 arm of decodeDCS does the opposite. Preserved as-is.
	switch {
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		return decodeUTF8Lossy(data[3:], true), "utf-8-bom"
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return decodeUTF16(data[2:], false, utf16Replace), "utf-16-le-bom"
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return decodeUTF16(data[2:], true, utf16Replace), "utf-16-be-bom"
	}

	// 2. UTF-16 null-byte heuristic. The window is 60 bytes rather than 20 so
	//    Korean fires: Hangul syllables occupy U+AC00-U+D7FF, so their high byte is
	//    never zero, and a short Korean greeting has no nulls at all. The quota is
	//    filled by the ASCII digits and punctuation further into the message.

	sample := data
	if len(sample) > 60 {
		sample = sample[:60]
	}
	// Padding is not interleaving: settle that before counting zeros.
	if text, ok := nulPaddedASCII(data); ok {
		return text, "utf-8"
	}
	if len(sample) >= 4 {
		nullEven, nullOdd := 0, 0
		for i := 0; i < len(sample); i += 2 {
			if sample[i] == 0 {
				nullEven++
			}
		}
		for i := 1; i < len(sample); i += 2 {
			if sample[i] == 0 {
				nullOdd++
			}
		}
		// The else-if and the inner emptiness test are load-bearing: when the BE
		// arm is chosen but decodes to nothing, Python does not then try LE, it
		// falls through to step 3. Restructuring this into two independent ifs
		// changes the answer for null-heavy binary payloads.
		if nullEven >= 3 && nullEven > nullOdd {
			text := strings.Trim(decodeUTF16(data, true, utf16Ignore), "\x00")
			if strings.TrimFunc(text, isPythonSpace) != "" {
				return text, "utf-16-be"
			}
		} else if nullOdd >= 3 && nullOdd > nullEven {
			text := strings.Trim(decodeUTF16(data, false, utf16Ignore), "\x00")
			if strings.TrimFunc(text, isPythonSpace) != "" {
				return text, "utf-16-le"
			}
		}
	}

	// 3. UTF-8 strict, before anything statistical so genuine UTF-8 is never
	//    second-guessed. The two suspicion gates are what stop 7-bit-stripped
	//    Cyrillic — which is, byte for byte, perfectly valid ASCII — from being
	//    accepted here and never reaching step 7.
	if utf8.Valid(data) {
		text := string(data)
		if strings.TrimFunc(text, isPythonSpace) != "" &&
			!isSuspiciousASCII(text) && !isSuspiciousMixedASCII(text) {
			return text, "utf-8"
		}
	}

	// 4. Explicit 8-bit codecs. See the ordering note in the doc comment: these
	//    run BEFORE any statistical detection, never after.
	high := 0
	for _, b := range data {
		if b >= 0x80 {
			high++
		}
	}
	if float64(high)/float64(len(data)) > 0.15 {
		// Cyrillic first, CP1251 ahead of ISO-8859-5 because it is far more common
		// on the wire. KNOWN DIVERGENCE, preserved: because Cyrillic is tried first
		// and the 0.25 bar is low, genuine Thai, Arabic and Greek 8-bit text is
		// usually claimed by CP1251 and never reaches its own branch below.
		for _, cs := range []*charset{cp1251, iso88595, koi8r, koi8u} {
			if text, ok := scoredSingleByte(cs, data, 0x0400, 0x04FF); ok {
				return text, cs.name
			}
		}
		for _, cs := range []*charset{tis620, cp874, iso885911} {
			if text, ok := scoredSingleByte(cs, data, 0x0E00, 0x0E7F); ok {
				return text, cs.name
			}
		}
		for _, cs := range []*charset{cp1256, iso88596} {
			if text, ok := scoredSingleByte(cs, data, 0x0600, 0x06FF); ok {
				return text, cs.name
			}
		}
		if text, ok := scoredSingleByte(iso88597, data, 0x0370, 0x03FF); ok {
			return text, iso88597.name
		}
	}

	// 5. Statistical charset detection would go here. It is not implemented; see
	//    the doc comment above for why, and do not move it earlier if it ever is.

	// 6. GSM 7-bit. Off by default — see Options.GSM7 for why enabling it costs
	//    the Cyrillic restoration below.
	if opts.GSM7 && allBytesBelow(data, 0x80) {
		if text, ok := decodeGSM7(data); ok {
			if strings.TrimFunc(text, isPythonSpace) != "" &&
				!isSuspiciousASCII(text) && !isSuspiciousMixedASCII(text) {
				return text, "gsm0338"
			}
		}
	}

	// 7. 7-bit-stripped restoration. Some legacy SMSCs push 8-bit Cyrillic or Thai
	//    down a 7-bit channel by masking every byte with & 0x7F. The result is
	//    valid ASCII, so nothing upstream complains and the partner receives
	//    "15@8B5 B>20@K" instead of "БЕРИТЕ ТОВАРЫ". Restoring the MSB and
	//    re-decoding is the only way back.
	if allBytesInRange(data, 0x09, 0x7F) {
		asciiText := decodeUTF8Lossy(data, false)

		// The Latin gate belongs HERE, guarding every restoration path, not
		// inside individual helpers. It used to live only in the two mixed
		// variants, so a body the gate refused fell straight through into
		// tryDecode7BitStripped below -- whole-message restoration, the most
		// destructive path of the three. "YOUR CODE 12345 <#>" (Android
		// SMS Retriever) was destroyed that way with both D-004 and D-006
		// active: the passcode 12345 came out as БВГДЕ.
		latin := looksLikeRealLatinText(asciiText)
		if isSuspiciousASCII(asciiText) && !latin {
			// The digit-preserving fix: whole-message restoration would rewrite
			// an OTP code as Cyrillic letters, so a body that carries one is
			// restored token by token instead. See Options.PreserveOTPDigits.
			if opts.PreserveOTPDigits && otpPreservationApplies(asciiText) {
				if text, enc, ok := tryDecode7BitStrippedMixedProtectingCodes(data); ok {
					return text, enc
				}
			}
			if text, enc, ok := tryDecode7BitStripped(data, iso88595, 0x0400, 0x04FF); ok {
				return text, enc
			}
			if text, enc, ok := tryDecode7BitStripped(data, iso885911, 0x0E00, 0x0E7F); ok {
				return text, enc
			}
			if text, enc, ok := tryDecode7BitStripped(data, iso88596, 0x0600, 0x06FF); ok {
				return text, enc
			}
		}

		// Mixed: stripped Cyrillic interleaved with genuine ASCII brand names.
		// These fail the pure gate above because lowercase brand letters ("Apple",
		// "Stripe") drag the uppercase ratio under its threshold. Checked
		// independently of isSuspiciousASCII, not nested inside it.
		if isSuspiciousMixedASCII(asciiText) && !latin {
			// Same digit-preserving preference as the pure path above. Without
			// it this branch reached only the legacy variant, whose acceptance
			// ratio counts a PROTECTED code in its own denominator: protecting
			// "A1B2C3" dropped the Cyrillic share below the bar, the restore was
			// rejected wholesale, and the customer got raw garble
			// ("2Ph Z^T A1B2C3 ^b Apple") instead of "Ваш код A1B2C3 от Apple".
			// Protecting the passcode must not be what causes the message to
			// fail to decode.
			if opts.PreserveOTPDigits && otpPreservationApplies(asciiText) {
				if text, enc, ok := tryDecode7BitStrippedMixedProtectingCodes(data); ok {
					return text, enc
				}
			}
			if text, enc, ok := tryDecode7BitStrippedMixed(data); ok {
				return text, enc
			}
		}
	}

	// 8. Lossy fallbacks. Strict UTF-8 is retried first so a message that only
	//    fell through because it tripped a suspicion gate is not mislabelled
	//    'utf-8-lossy'.
	if utf8.Valid(data) {
		text := string(data)
		if strings.TrimFunc(text, isPythonSpace) != "" {
			return text, "utf-8"
		}
	}
	if text := decodeUTF8Lossy(data, false); strings.TrimFunc(text, isPythonSpace) != "" {
		return text, "utf-8-lossy"
	}
	return decodeSingleByteReplace(latin1, data), "latin-1"
}

// ---------------------------------------------------------------------------
// 7-bit-stripped restoration
// ---------------------------------------------------------------------------

// cyrillicShadows are the characters an ISO-8859-5 Cyrillic byte becomes after
// & 0x7F: @ is Р, > is О, ? is П, [ is Э and so on. Their density is the primary
// signal that a message is stripped rather than genuinely ASCII.
const cyrillicShadows = "@[\\]^_`{|}~<>?"

// isSuspiciousASCII reports whether ASCII-decoded text looks like 7-bit-stripped
// Cyrillic or Thai.
//
// Primary signature: nearly all letters uppercase, plus an unusual density of
// shadow punctuation.
//
// The digit-heavy fallback exists because Cyrillic uppercase 0xB0-0xCF maps to
// 0x30-0x4F — that is, to the digits and @A-O. A word like "РОВЕРОЧНЫЙ" therefore
// arrives as mostly digits and @/>/?, leaving almost no Latin letters to compute
// an uppercase ratio from (only Г=G, К=K, Ч=G survive as letters). Below three
// letters the ratio is meaningless, so shadow density alone decides.
func isSuspiciousASCII(text string) bool {
	runes := []rune(text)
	if len(runes) < 6 {
		return false
	}

	letters, upper, shadows := 0, 0, 0
	for _, r := range runes {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
		if strings.ContainsRune(cyrillicShadows, r) {
			shadows++
		}
	}
	shadowRatio := float64(shadows) / float64(len(runes))

	if letters < 3 {
		return shadows >= 3 && shadowRatio > 0.15
	}
	return float64(upper)/float64(letters) > 0.75 && shadowRatio > 0.03
}

// isSuspiciousMixedASCII reports whether text looks like 7-bit-stripped Cyrillic
// mixed with genuine ASCII words.
//
// The tell is bytes 0x70-0x7E ('p'-'~') co-existing with shadow characters. Those
// bytes have no mainstream Russian mapping in ISO-8859-5 — restored they give №,
// ё and rare South-Slavic glyphs — so inside an otherwise garbled message they
// mark real ASCII islands, which in practice means brand names: Apple, Stripe,
// Google. Two of each is the floor, so a stray '<' next to the letter 'p' in an
// English sentence does not fire.
func isSuspiciousMixedASCII(text string) bool {
	runes := []rune(text)
	if len(runes) < 8 {
		return false
	}
	shadows, highRange := 0, 0
	for _, r := range runes {
		if strings.ContainsRune(cyrillicShadows, r) {
			shadows++
		}
		if r >= 0x70 && r <= 0x7E {
			highRange++
		}
	}
	return shadows >= 2 && highRange >= 2
}

// tryDecode7BitStripped restores the MSB and re-decodes with cs, accepting the
// result only when at least 35% of the characters land in the expected Unicode
// block. Space (0x20) is left alone deliberately: OR-ing it would produce U+00A0
// NO-BREAK SPACE and word boundaries would stop being spaces.
func tryDecode7BitStripped(data []byte, cs *charset, lo, hi rune) (string, string, bool) {
	restored := make([]byte, len(data))
	for i, b := range data {
		if b >= 0x21 && b <= 0x7F {
			restored[i] = b | 0x80
		} else {
			restored[i] = b
		}
	}
	text, ok := decodeSingleByteStrict(cs, restored)
	if !ok {
		return "", "", false
	}
	if float64(countInRange(text, lo, hi)) >= float64(utf8.RuneCountInString(text))*0.35 {
		return text, cs.name + "-7bit", true
	}
	return "", "", false
}

// mainCyrillicISO88595 holds the restored byte values that map to mainstream
// Russian in ISO-8859-5: А-Я at 0xB0-0xCF, а-я at 0xD0-0xEF, and Ё/ё at 0xA1/0xF1.
// Everything else — № at 0xF0, Ў at 0xAE, the rare South-Slavic letters — is left
// as its original ASCII character, which is what keeps '.' and ',' from turning
// into Ў and Ќ.
func inMainCyrillicISO88595(b byte) bool {
	return (b >= 0xB0 && b <= 0xEF) || b == 0xA1 || b == 0xF1
}

// restoreTokenCyrillic restores one whitespace-free token byte by byte.
func restoreTokenCyrillic(token string) string {
	var out strings.Builder
	for _, ch := range token {
		if ch >= 0x21 && ch <= 0x6F {
			restored := byte(ch) | 0x80
			if inMainCyrillicISO88595(restored) {
				out.WriteRune(iso88595.table[restored])
				continue
			}
		}
		out.WriteRune(ch)
	}
	return out.String()
}

// looksLikeGroupedCode recognises the passcode shapes the consecutive-run rule
// above cannot see, and which real senders overwhelmingly use.
//
// hasOTPDigitRun wants a CONSECUTIVE run (five digits, or four beside an anchor
// word) and an 80% digit floor. Every common grouped format fails one or both:
//
//	"123-456"   two runs of three
//	"123 456"   two tokens of three, split before this is ever called
//	"12-34-56"  75% digits, under the floor
//	"A1B2C3"    50% digits, longest run of one
//
// All four were destroyed inside genuinely stripped-Cyrillic bodies, where
// restoration is otherwise correct: "Ваш код подтверждения 123-456" became
// "… БВГ-ДЕЖ". The same message with "12345" survived, so the FORMAT of the
// code alone decided whether it reached the customer.
//
// Two shapes are protected:
//
//  1. digits and code separators only, with three or more digits — covers
//     123-456, 12-34-56, 123.456 and a bare 123 or 456 from a split pair;
//  2. mixed letters and digits, at least two of each, four or more characters —
//     covers A1B2C3 and similar alphanumeric codes.
//
// Both risk leaving a rare all-shadow Cyrillic word undecoded: А-Й strip onto
// '0'-'9', so a Cyrillic word built only from those letters looks like digits.
// That trade is deliberate and is the same one D-004 already made — a word
// rendered as digits is legible and reportable, while a destroyed passcode is
// unrecoverable and silent.
func looksLikeGroupedCode(token string) bool {
	digits, letters, separators := 0, 0, 0
	for _, r := range token {
		switch {
		case unicode.IsDigit(r):
			digits++
		case r == '-' || r == '.' || r == '/' || r == '_':
			separators++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			letters++
		default:
			// Anything else (punctuation from a stripped Cyrillic byte, say)
			// means this is not a code.
			return false
		}
	}
	// A SEPARATOR is required for the all-digit shape, and that is the whole
	// difference between protecting a passcode and destroying a word. Cyrillic
	// А-Й strip onto '0'-'9', so a four-letter word built from them arrives as
	// four contiguous digits: "БЕДА" is indistinguishable from "1540". Requiring
	// a separator keeps 123-456 and 12-34-56 while leaving bare digit runs to
	// the consecutive-run rule below, which is calibrated against exactly that
	// collision (see TestPreserveOTPDigits... in decode_test.go, which fails
	// loudly if this is loosened).
	//
	// The cost is that a space-separated "123 456" is tokenised into two bare
	// three-digit tokens and stays unprotected. Fixing that needs cross-token
	// lookahead, which is a larger change than this one.
	if digits >= 4 && letters == 0 && separators >= 1 &&
		digits+separators == len([]rune(token)) {
		return true
	}
	// Mixed letters and digits is safe without a separator: a stripped Cyrillic
	// word restores to letters only, never to an alternating letter/digit mix.
	return digits >= 2 && letters >= 2 && digits+letters >= 4
}

// hasOTPDigitRun reports whether a token looks like an OTP code or numeric
// reference, and so must be left alone rather than restored.
//
// Both conditions matter. The 80% digit floor rejects mixed tokens such as
// "8972768#A?59B5", which is only 71% digits — that is stripped Cyrillic whose
// digit-looking characters are А-Й mapped onto '0'-'9', not a number. The
// consecutive-run requirement stops a long Cyrillic word that merely begins with
// several digit shadows from being mistaken for a code and left undecoded.
func hasOTPDigitRun(token string, minLen int) bool {
	if token == "" {
		return false
	}
	if looksLikeGroupedCode(token) {
		return true
	}
	runes := []rune(token)
	digits := 0
	for _, r := range runes {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	if float64(digits)/float64(len(runes)) < 0.80 {
		return false
	}
	run := 0
	for _, r := range runes {
		if unicode.IsDigit(r) {
			run++
			if run >= minLen {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

// otpCodeAnchors are the words that make a four-digit token a code rather than a
// word. They are matched against the RESTORED text, so ":>4" has already become
// "КОД" by the time it is looked for.
var otpCodeAnchors = []string{"КОД", "ПАРОЛЬ", "ПИН", "CODE", "PIN", "OTP"}

// otpDigitPolicy decides, for one already-restored message, which digit tokens
// are codes worth protecting from restoration.
//
// The rule is deliberately narrower than "any run of four digits", because
// ISO-8859-5 maps А-Й onto '0'-'9' and a four-letter word drawn only from those
// ten letters strips to four pure digits. ДЕДА, БЕДА, ДЕВА and ЖАБА are all
// ordinary words that look exactly like a four-digit code, and treating them as
// codes leaves "ПРИВЕЗЛИ 4540 ДОМОЙ" — worse than the defect being fixed.
//
// So: five or more digits is a code on its own (no Russian word is five letters
// all drawn from А-Й), and exactly four digits is a code only when the message
// says it is one. Everything else restores as before.
type otpDigitPolicy struct {
	minRun int
}

// newOTPDigitPolicy inspects the restored tokens for an anchor word and returns
// the run length that qualifies as a code.
func newOTPDigitPolicy(restoredTokens []string) otpDigitPolicy {
	for _, token := range restoredTokens {
		upper := strings.ToUpper(token)
		for _, anchor := range otpCodeAnchors {
			if strings.Contains(upper, anchor) {
				return otpDigitPolicy{minRun: 4}
			}
		}
	}
	return otpDigitPolicy{minRun: 5}
}

// protects reports whether this token is a code to be left alone.
func (p otpDigitPolicy) protects(token string) bool {
	return hasOTPDigitRun(token, p.minRun)
}

// otpPreservationApplies reports whether the digit-preserving path would actually
// preserve something in this body.
//
// It is asked BEFORE choosing a decode path, so that a message with nothing to
// protect takes exactly the route it took before the option existed. Deciding
// afterwards would silently change the output of messages the fix is not for.
func otpPreservationApplies(text string) bool {
	tokens := splitKeepingWhitespace(text)
	restored := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if strings.TrimFunc(token, isPythonSpace) == "" {
			continue
		}
		restored = append(restored, restoreTokenCyrillic(token))
	}
	policy := newOTPDigitPolicy(restored)
	for _, token := range tokens {
		if strings.TrimFunc(token, isPythonSpace) == "" {
			continue
		}
		if policy.protects(token) {
			return true
		}
	}
	return false
}

// tryDecode7BitStrippedMixed restores stripped Cyrillic word by word, leaving
// genuine ASCII tokens intact. Per whitespace-separated token:
//
//  1. contains any of 'p'-'~' → keep, it is a real ASCII word;
//  2. has a run of four or more digits → keep, it is an OTP code;
//  3. otherwise → restore byte by byte.
//
// The result is accepted only when at least half the non-whitespace characters
// came out Cyrillic, so an English message that happens to contain '<', '>' and
// the letter 'p' is not mangled.

// looksLikeRealLatinText reports that the ASCII reading of this body is already
// a legitimate Latin message, and therefore must NOT be run through Cyrillic
// restoration.
//
// The 7-bit-stripped hypothesis is that a Cyrillic ISO-8859-5 body crossed a
// channel that cleared the high bit. Nothing else in this decoder establishes
// that the hypothesis HOLDS -- the token rules below only ask whether a token
// could survive restoration, and treat "no character in 'p'-'~'" as evidence
// FOR Cyrillic. It is not: most words in most Latin languages contain no letter
// from p to z.
//
// The discriminator is what stripping actually produces. Cyrillic uppercase
// A-YA (0xB0-0xCF) strips into '0'-'9' and ':;<=>?@' and 'A'-'O'; lowercase
// (0xD0-0xEF) strips into 'P'-'Z', '[\]^_`' and 'a'-'o'. So a stripped Cyrillic
// word lands on punctuation or mid-word capitals almost every time:
//
//	"Kod"           -> ":^T"
//	"podtverzhdeniya" (Cyrillic) -> "_^TbRU`VTU]Xo"
//
// while a real Latin word is purely alphabetic: Kode, Jangan, bagikan, orang.
//
// So: if the body already contains several purely-alphabetic words, it is Latin
// text that needs no restoration at all. Found by an Indonesian WhatsApp OTP --
// "<#> Kode WhatsApp: 812-128 / Jangan bagikan kode ini dengan orang lain",
// every byte of it printable ASCII with none above 0x7F -- being rendered as
// "M#O Yafh WhatsApp: IBV-BVI", destroying the passcode.
func looksLikeRealLatinText(text string) bool {
	words, total := 0, 0
	for _, token := range splitKeepingWhitespace(text) {
		trimmed := strings.Trim(strings.TrimFunc(token, isPythonSpace), ".,:;!?()[]{}\"'<>-")
		if len([]rune(trimmed)) < 2 {
			continue
		}
		total++
		alpha := true
		for _, r := range trimmed {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				alpha = false
				break
			}
		}
		if alpha {
			words++
		}
	}
	// Proportional, not an absolute count. The mixed path exists for genuine
	// stripped Cyrillic interleaved with ASCII brand names ("Apple", "Stripe"),
	// so a flat "two alphabetic words blocks restoration" would stop restoring
	// exactly the bodies that path was built for. Latin prose is dominated by
	// alphabetic words; a stripped body is dominated by punctuation-bearing
	// tokens:
	//
	//	"YOUR CODE 12345 <#>"            -> 2 of 4 tokens alphabetic (blocked)
	//	"2Ph Z^T 123-456 ^b Apple"       -> 1 of 5             (still restored)
	//	"_^TbRU`VTU]Xo" (stripped)       -> 0 of 3             (still restored)
	if total == 0 || words < 2 {
		return false
	}
	return words*2 >= total
}

func tryDecode7BitStrippedMixed(data []byte) (string, string, bool) {
	text := decodeASCIIReplace(data)

	// Positive evidence first: never "restore" a body that is already Latin.
	if looksLikeRealLatinText(text) {
		return "", "", false
	}

	var out strings.Builder
	for _, tok := range splitKeepingWhitespace(text) {
		if tok == "" || strings.TrimFunc(tok, isPythonSpace) == "" {
			out.WriteString(tok)
			continue
		}
		hasHigh := false
		for _, r := range tok {
			if r >= 0x70 && r <= 0x7E {
				hasHigh = true
				break
			}
		}
		if hasHigh || hasOTPDigitRun(tok, 4) {
			out.WriteString(tok)
			continue
		}
		out.WriteString(restoreTokenCyrillic(tok))
	}
	result := out.String()

	nonWS, cyrillic := 0, 0
	for _, r := range result {
		if isPythonSpace(r) {
			continue
		}
		nonWS++
		if r >= 0x0400 && r <= 0x04FF {
			cyrillic++
		}
	}
	if nonWS == 0 {
		return "", "", false
	}
	if float64(cyrillic)/float64(nonWS) >= 0.50 {
		return result, "iso-8859-5-7bit-mixed", true
	}
	return "", "", false
}

// tryDecode7BitStrippedMixedProtectingCodes is the digit-preserving variant of
// tryDecode7BitStrippedMixed. The legacy function is left exactly as it was: it
// is on the parity path, and every vector in testdata/vectors.json goes through
// it.
//
// Two differences, both required by the defect this fixes:
//
//  1. which tokens are protected comes from otpDigitPolicy, not from a bare
//     four-digit run, so ordinary words spelled from А-Й are still restored;
//  2. protected tokens are excluded from the Cyrillic-ratio denominator. The
//     ratio asks "did restoring produce Cyrillic"; counting the digits we
//     deliberately did not restore against it made the gate reject exactly the
//     bodies with the longest codes — "ПРОВЕРОЧНЫЙ КОД 1111222233334444" fell
//     through at 46.7% and had its code destroyed by the whole-message path,
//     silently, with the option on.
func tryDecode7BitStrippedMixedProtectingCodes(data []byte) (string, string, bool) {
	text := decodeASCIIReplace(data)

	// Same gate as the legacy variant: a Latin body is not a stripped one.
	if looksLikeRealLatinText(text) {
		return "", "", false
	}
	tokens := splitKeepingWhitespace(text)

	restoredTokens := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if strings.TrimFunc(token, isPythonSpace) == "" {
			continue
		}
		restoredTokens = append(restoredTokens, restoreTokenCyrillic(token))
	}
	policy := newOTPDigitPolicy(restoredTokens)

	var out strings.Builder
	nonWS, cyrillic := 0, 0
	for _, token := range tokens {
		if token == "" || strings.TrimFunc(token, isPythonSpace) == "" {
			out.WriteString(token)
			continue
		}
		hasHigh := false
		for _, r := range token {
			if r >= 0x70 && r <= 0x7E {
				hasHigh = true
				break
			}
		}
		if hasHigh || policy.protects(token) {
			// Kept as it arrived — a real ASCII word or a protected code. Neither
			// is evidence about whether restoration worked, so neither counts.
			out.WriteString(token)
			continue
		}
		restored := restoreTokenCyrillic(token)
		out.WriteString(restored)
		for _, r := range restored {
			if isPythonSpace(r) {
				continue
			}
			nonWS++
			if r >= 0x0400 && r <= 0x04FF {
				cyrillic++
			}
		}
	}
	if nonWS == 0 {
		return "", "", false
	}
	if float64(cyrillic)/float64(nonWS) >= 0.50 {
		return out.String(), "iso-8859-5-7bit-mixed", true
	}
	return "", "", false
}

// splitKeepingWhitespace is Python's re.split(r'(\s+)', text): alternating runs of
// non-whitespace and whitespace, with the separators kept so the message can be
// reassembled unchanged.
func splitKeepingWhitespace(text string) []string {
	var parts []string
	var current strings.Builder
	inSpace := false
	for i, r := range text {
		if i == 0 {
			inSpace = isPythonSpace(r)
		}
		if isPythonSpace(r) != inSpace {
			parts = append(parts, current.String())
			current.Reset()
			inSpace = !inSpace
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// ---------------------------------------------------------------------------
// Codecs
// ---------------------------------------------------------------------------

// undefinedByte marks a byte with no mapping in a codec. Its presence is what
// makes a strict decode fail, and strict failure is how the auto-detection chain
// eliminates candidates — so these tables have to match CPython's exactly, not
// approximately.
const undefinedByte rune = -1

type charset struct {
	name  string
	table [256]rune
}

// newCharset builds a CPython-equivalent table from an x/text charmap.
//
// x/text's tables follow the WHATWG Encoding Standard; CPython's follow the
// original ISO and vendor definitions, and they disagree in ways that change
// strict-decode outcomes. The overrides below are the complete measured
// difference, not a guess:
//
//   - ISO-8859-* : WHATWG leaves 0x80-0x9F unmapped, CPython maps them to the C1
//     controls U+0080-U+009F. Without this, any ISO-8859 strict decode of a body
//     containing a C1 byte fails in Go and succeeds in Python, and the chain picks
//     a different codec.
//   - KOI8-U     : WHATWG follows RFC 2319 at 0xAE/0xBE (Ў/ў), CPython keeps the
//     older box-drawing glyphs.
//   - TIS-620 and ISO-8859-11 are absent from x/text; both are CP874 with the C1
//     range restored, and TIS-620 additionally leaves 0xA0 unmapped.
func newCharset(name string, base *charmap.Charmap, overrides map[byte]rune) *charset {
	cs := &charset{name: name}
	for i := 0; i < 256; i++ {
		r := base.DecodeByte(byte(i))
		if r == utf8.RuneError {
			r = undefinedByte
		}
		cs.table[i] = r
	}
	for b, r := range overrides {
		cs.table[b] = r
	}
	return cs
}

// c1Identity maps 0x80-0x9F to the matching C1 control codepoint.
func c1Identity() map[byte]rune {
	m := make(map[byte]rune, 32)
	for b := 0x80; b <= 0x9F; b++ {
		m[byte(b)] = rune(b)
	}
	return m
}

func c1IdentityPlus(extra map[byte]rune) map[byte]rune {
	m := c1Identity()
	for b, r := range extra {
		m[b] = r
	}
	return m
}

var (
	latin1    = newCharset("latin-1", charmap.ISO8859_1, nil)
	iso88595  = newCharset("iso-8859-5", charmap.ISO8859_5, c1Identity())
	iso88596  = newCharset("iso-8859-6", charmap.ISO8859_6, c1Identity())
	iso88597  = newCharset("iso-8859-7", charmap.ISO8859_7, c1Identity())
	iso88598  = newCharset("iso-8859-8", charmap.ISO8859_8, c1Identity())
	cp1251    = newCharset("cp1251", charmap.Windows1251, nil)
	cp1256    = newCharset("cp1256", charmap.Windows1256, nil)
	koi8r     = newCharset("koi8-r", charmap.KOI8R, nil)
	koi8u     = newCharset("koi8-u", charmap.KOI8U, map[byte]rune{0xAE: 0x255D, 0xBE: 0x256C})
	cp874     = newCharset("cp874", charmap.Windows874, nil)
	iso885911 = newCharset("iso-8859-11", charmap.Windows874, c1Identity())
	tis620    = newCharset("tis-620", charmap.Windows874, c1IdentityPlus(map[byte]rune{0xA0: undefinedByte}))

	asciiCodec = newASCII()
)

func newASCII() *charset {
	cs := &charset{name: "ascii"}
	for i := 0; i < 256; i++ {
		if i < 0x80 {
			cs.table[i] = rune(i)
		} else {
			cs.table[i] = undefinedByte
		}
	}
	return cs
}

// decodeSingleByteStrict decodes with cs, failing if any byte is unmapped —
// CPython's errors='strict'.
func decodeSingleByteStrict(cs *charset, data []byte) (string, bool) {
	var out strings.Builder
	out.Grow(len(data))
	for _, b := range data {
		r := cs.table[b]
		if r == undefinedByte {
			return "", false
		}
		out.WriteRune(r)
	}
	return out.String(), true
}

// decodeSingleByteReplace substitutes U+FFFD for unmapped bytes — errors='replace'.
func decodeSingleByteReplace(cs *charset, data []byte) string {
	var out strings.Builder
	out.Grow(len(data))
	for _, b := range data {
		r := cs.table[b]
		if r == undefinedByte {
			r = utf8.RuneError
		}
		out.WriteRune(r)
	}
	return out.String()
}

func decodeASCIIReplace(data []byte) string {
	return decodeSingleByteReplace(asciiCodec, data)
}

// scoredSingleByte is one iteration of the step-4 candidate loop: strict decode,
// then require at least a quarter of the characters to sit in the script's block.
// A quarter is low on purpose — real SMS bodies mix Latin digits, spaces and
// punctuation with the script — and it is why the Cyrillic pass usually claims
// Thai, Arabic and Greek bodies before their own passes run.
func scoredSingleByte(cs *charset, data []byte, lo, hi rune) (string, bool) {
	candidate, ok := decodeSingleByteStrict(cs, data)
	if !ok {
		return "", false
	}
	score := countInRange(candidate, lo, hi)
	if float64(score) >= float64(utf8.RuneCountInString(candidate))*0.25 {
		return candidate, true
	}
	return "", false
}

// decodeMultiByteStrict decodes a stateful or multi-byte codec, treating any
// U+FFFD in the output as a decode failure. None of these codecs has a byte
// sequence that legitimately maps to U+FFFD, so its appearance means x/text
// substituted it for invalid input — which is what CPython would raise on.
func decodeMultiByteStrict(enc encoding.Encoding, data []byte) (string, bool) {
	out, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		return "", false
	}
	text := string(out)
	if strings.ContainsRune(text, utf8.RuneError) {
		return "", false
	}
	return text, true
}

func decodeMultiByteReplace(enc encoding.Encoding, data []byte) string {
	out, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		// x/text stops at the failure point; keep what it produced, which is what a
		// replace-mode decode would have shown up to there.
		return string(out)
	}
	return string(out)
}

// decodeGSM7 decodes unpacked GSM 03.38 septets, one per byte, reusing the same
// gsm7 package the submit path encodes with (internal/core/submit_service.go) so
// the two directions cannot drift apart.
//
// One table difference against CPython's gsm0338 codec, measured across the whole
// 0x00-0x7F range: a lone 0x1B (ESC, no extension byte after it) yields U+0020
// here and nothing at all in CPython. A trailing bare ESC is a malformed message
// either way.
func decodeGSM7(data []byte) (string, bool) {
	out, err := gsm7.Decode(data)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// ---------------------------------------------------------------------------
// UTF-16 and lossy UTF-8
// ---------------------------------------------------------------------------

type utf16ErrorMode int

const (
	utf16Ignore utf16ErrorMode = iota
	utf16Replace
)

// decodeUTF16 decodes UTF-16 with CPython's error semantics: an unpaired
// surrogate consumes two bytes and an odd trailing byte consumes one, each
// producing either nothing (ignore) or one U+FFFD (replace).
func decodeUTF16(data []byte, bigEndian bool, mode utf16ErrorMode) string {
	var out strings.Builder
	out.Grow(len(data) / 2)

	unit := func(i int) uint16 {
		if bigEndian {
			return uint16(data[i])<<8 | uint16(data[i+1])
		}
		return uint16(data[i+1])<<8 | uint16(data[i])
	}

	i := 0
	for i+1 < len(data) {
		u := unit(i)
		i += 2
		switch {
		case u >= 0xD800 && u <= 0xDBFF:
			if i+1 < len(data) {
				if low := unit(i); low >= 0xDC00 && low <= 0xDFFF {
					i += 2
					out.WriteRune(0x10000 + (rune(u-0xD800) << 10) + rune(low-0xDC00))
					continue
				}
			}
			if mode == utf16Replace {
				out.WriteRune(utf8.RuneError)
			}
		case u >= 0xDC00 && u <= 0xDFFF:
			if mode == utf16Replace {
				out.WriteRune(utf8.RuneError)
			}
		default:
			out.WriteRune(rune(u))
		}
	}
	if i < len(data) && mode == utf16Replace {
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}

// decodeUTF8Lossy decodes UTF-8, either dropping invalid input or replacing it,
// following CPython's maximal-subpart rule: one error covers the longest prefix
// that could still have become a valid sequence, so a truncated three-byte
// character is one error rather than three. Go's utf8.DecodeRune always reports a
// width of 1 on failure, which would emit a different number of U+FFFDs than the
// Python service does.
func decodeUTF8Lossy(data []byte, replace bool) string {
	var out strings.Builder
	out.Grow(len(data))
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || size > 1 {
			out.WriteRune(r)
			i += size
			continue
		}
		if replace {
			out.WriteRune(utf8.RuneError)
		}
		i += invalidUTF8Run(data[i:])
	}
	return out.String()
}

// invalidUTF8Run returns the length of the maximal invalid subpart at the start of
// data, per Unicode 3.9 / CPython's UTF-8 decoder.
func invalidUTF8Run(data []byte) int {
	b0 := data[0]
	var want int
	var lo, hi byte
	switch {
	case b0 >= 0xC2 && b0 <= 0xDF:
		want, lo, hi = 1, 0x80, 0xBF
	case b0 == 0xE0:
		want, lo, hi = 2, 0xA0, 0xBF
	case b0 >= 0xE1 && b0 <= 0xEC, b0 >= 0xEE && b0 <= 0xEF:
		want, lo, hi = 2, 0x80, 0xBF
	case b0 == 0xED:
		want, lo, hi = 2, 0x80, 0x9F
	case b0 == 0xF0:
		want, lo, hi = 3, 0x90, 0xBF
	case b0 >= 0xF1 && b0 <= 0xF3:
		want, lo, hi = 3, 0x80, 0xBF
	case b0 == 0xF4:
		want, lo, hi = 3, 0x80, 0x8F
	default:
		return 1 // continuation byte out of nowhere, 0xC0/0xC1, or 0xF5-0xFF
	}

	n := 1
	for k := 0; k < want; k++ {
		if n >= len(data) {
			return n
		}
		b := data[n]
		if b < lo || b > hi {
			return n
		}
		n++
		lo, hi = 0x80, 0xBF // only the first continuation byte is range-restricted
	}
	return n
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// isPythonSpace matches Python's str.isspace, which counts 0x1C-0x1F (the file,
// group, record and unit separators) as whitespace where Go's unicode.IsSpace does
// not. The difference decides whether a control-character-only body is treated as
// empty and rejected.
func isPythonSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1C && r <= 0x1F)
}

func countInRange(text string, lo, hi rune) int {
	n := 0
	for _, r := range text {
		if r >= lo && r <= hi {
			n++
		}
	}
	return n
}

func allBytesBelow(data []byte, limit byte) bool {
	for _, b := range data {
		if b >= limit {
			return false
		}
	}
	return true
}

func allBytesInRange(data []byte, lo, hi byte) bool {
	for _, b := range data {
		if b < lo || b > hi {
			return false
		}
	}
	return true
}

// nulPaddedASCII reports a body that is printable ASCII followed (or preceded)
// by NUL padding, rather than UTF-16 with its interleaved high bytes.
//
// Fixed-width SMSC message stores pad short bodies with NULs. The null-counting
// heuristic below sees three or more zeros on one byte parity and claims the
// body as UTF-16, so "1234" plus seven NULs decodes as the CJK "ㄲ㌴" and the
// passcode is gone.
//
// The discriminator is WHERE the NULs are. Genuine UTF-16BE ASCII interleaves
// them ("1234" is 00 31 00 32 00 33 00 34, a NUL between every character);
// padding puts them all outside the text. So: strip the outer NULs and, if what
// is left contains none, this was padded ASCII and never UTF-16.
func nulPaddedASCII(data []byte) (string, bool) {
	core := bytes.Trim(data, "\x00")
	if len(core) == 0 || bytes.IndexByte(core, 0) >= 0 {
		return "", false
	}
	if len(core) == len(data) {
		// No padding at all; nothing for this rule to say.
		return "", false
	}
	for _, b := range core {
		if b > 0x7E || (b < 0x20 && b != '\n' && b != '\r' && b != '\t') {
			return "", false
		}
	}
	return string(core), true
}
