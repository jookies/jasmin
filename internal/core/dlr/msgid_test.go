package dlr

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonicalizeSubmitRespID(t *testing.T) {
	cases := map[string]string{
		"6AAD5":   "6AAD5",   // already canonical
		"6aad5":   "6AAD5",   // lowercase -> upper
		"00436":   "436",     // leading zeros stripped
		"000":     "",        // all-zero -> empty (Q-006 edge preserved)
		"":        "",        // empty stays empty
		"abc00":   "ABC00",   // trailing zeros NOT stripped
		"0x1f":    "X1F",     // upper then strip leading 0 (no special hex handling here)
		"0000abc": "ABC",     // mixed
	}
	for in, want := range cases {
		if got := CanonicalizeSubmitRespID(in); got != want {
			t.Errorf("CanonicalizeSubmitRespID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCodeReceiptID_BaseSame(t *testing.T) {
	// Base 0 behaves like the resp canonicalization.
	cases := map[string]string{"6aad5": "6AAD5", "00A": "A", "000": ""}
	for in, want := range cases {
		got, err := CodeReceiptID(in, MsgIDBaseSame)
		if err != nil || got != want {
			t.Errorf("CodeReceiptID(%q, base0) = (%q,%v), want (%q,nil)", in, got, err, want)
		}
	}
}

func TestCodeReceiptID_BaseReceiptDec(t *testing.T) {
	// Base 1: decimal receipt id -> uppercase hex, leading zeros stripped.
	cases := map[string]string{
		"436949":  "6AAD5", // 436949 == 0x6AAD5
		"0":       "",      // 0 -> "0" -> upper -> lstrip -> ""
		"000123":  "7B",    // 123 == 0x7B, decimal leading zeros ignored by int parse
		"255":     "FF",
	}
	for in, want := range cases {
		got, err := CodeReceiptID(in, MsgIDBaseReceiptDec)
		if err != nil || got != want {
			t.Errorf("CodeReceiptID(%q, base1) = (%q,%v), want (%q,nil)", in, got, err, want)
		}
	}
	// Non-decimal input must error (Python int(id) would raise).
	if _, err := CodeReceiptID("6aad5", MsgIDBaseReceiptDec); !errors.Is(err, ErrInvalidMsgID) {
		t.Errorf("base1 non-decimal: want ErrInvalidMsgID, got %v", err)
	}
}

func TestCodeReceiptID_BaseReceiptHex(t *testing.T) {
	// Base 2: hex receipt id -> decimal string (NOT upper/lstrip'd — Jasmin asymmetry).
	cases := map[string]string{
		"6aad5": "436949",
		"6AAD5": "436949", // hex parse is case-insensitive
		"0":     "0",      // stays "0" (no lstrip on base 2)
		"ff":    "255",
	}
	for in, want := range cases {
		got, err := CodeReceiptID(in, MsgIDBaseReceiptHex)
		if err != nil || got != want {
			t.Errorf("CodeReceiptID(%q, base2) = (%q,%v), want (%q,nil)", in, got, err, want)
		}
	}
	if _, err := CodeReceiptID("xyz", MsgIDBaseReceiptHex); !errors.Is(err, ErrInvalidMsgID) {
		t.Errorf("base2 non-hex: want ErrInvalidMsgID, got %v", err)
	}
}

func TestUnknownBaseErrors(t *testing.T) {
	if _, err := CodeReceiptID("1", MsgIDBase(9)); !errors.Is(err, ErrInvalidMsgID) {
		t.Errorf("unknown base: want ErrInvalidMsgID, got %v", err)
	}
}

// TestArbitraryPrecision proves math/big is used: ids far beyond int64 must convert
// exactly (a 65-byte SMSC id is well past 2^63).
func TestArbitraryPrecision(t *testing.T) {
	bigDec := "123456789012345678901234567890" // 30 digits, > int64
	hex, err := CodeReceiptID(bigDec, MsgIDBaseReceiptDec)
	if err != nil {
		t.Fatalf("base1 big: %v", err)
	}
	// Round-trip back through base 2 must recover the decimal.
	back, err := CodeReceiptID(hex, MsgIDBaseReceiptHex)
	if err != nil {
		t.Fatalf("base2 big: %v", err)
	}
	if back != bigDec {
		t.Errorf("big round-trip: %q -> %q -> %q, want original", bigDec, hex, back)
	}
}

// TestCorrelationInvariant is the real proof: for each base, the canonicalized
// submit_sm_resp id and the coded deliver_sm receipt id must land on the SAME key, which
// is what makes the two DLR legs correlate.
func TestCorrelationInvariant(t *testing.T) {
	cases := []struct {
		name       string
		base       MsgIDBase
		respID     string // as returned in submit_sm_resp
		receiptID  string // as returned in the deliver_sm receipt
	}{
		{"base0 same hex", MsgIDBaseSame, "6AAD5", "6aad5"},
		{"base0 same decimal", MsgIDBaseSame, "436949", "436949"},
		{"base1 resp-hex receipt-dec", MsgIDBaseReceiptDec, "6AAD5", "436949"},
		{"base2 resp-dec receipt-hex", MsgIDBaseReceiptHex, "436949", "6aad5"},
		{"base1 with leading zeros", MsgIDBaseReceiptDec, "07B", "123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			respKey := CanonicalizeSubmitRespID(c.respID)
			receiptKey, err := CodeReceiptID(c.receiptID, c.base)
			if err != nil {
				t.Fatalf("code receipt: %v", err)
			}
			if respKey != receiptKey {
				t.Errorf("legs do not correlate: resp %q -> %q, receipt %q -> %q",
					c.respID, respKey, c.receiptID, receiptKey)
			}
			if strings.Contains(receiptKey, ":") {
				t.Errorf("key must not contain ':' (would break rediscompat key): %q", receiptKey)
			}
		})
	}
}
