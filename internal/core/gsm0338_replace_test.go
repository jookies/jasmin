package core

import (
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Python consults messaging.sms.gsm0338.replace_encode_map before falling back
// to '?', and only in the "replace" error mode -- which is the mode Jasmin's
// HTTP front door uses (jasmin/protocols/http/endpoints/send.py:91,93). Go
// emitted '?' for all 15 of these, corrupting ordinary French/Portuguese/
// Catalan/Turkish text containing 'ç'.
func TestEncodeLegacyGSM0338AppliesTheReplacementMap(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // hex
	}{
		{"cedilla in ordinary French", "façade", "666109616465"},
		{"greek capitals with latin lookalikes", "ΑΒΕΖ", "4142455a"},
		{"plain ascii is untouched", "Ok", "4f6b"},
		{"genuinely unsupported still becomes a question mark", "☃", "3f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hex.EncodeToString(encodeLegacyGSM0338([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("encodeLegacyGSM0338(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// The table above is only trustworthy if it still matches the oracle. When the
// frozen interpreter is available, diff against it directly rather than against
// a transcription of it.
func TestEncodeLegacyGSM0338MatchesTheOracle(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH not set; skipping the oracle differential")
	}
	subjects := []string{"façade", "ΑΒΕΖΗΙΚΜΝΟΡΤΥΧ", "Ok", "Hello, world", "naïve", "ç"}
	for _, subject := range subjects {
		script := `import sys, messaging.sms.gsm0338
sys.stdout.write(sys.argv[1].encode('gsm0338','replace').hex())`
		out, err := exec.Command(python, "-c", script, subject).Output()
		if err != nil {
			t.Skipf("oracle interpreter unusable (%v); skipping", err)
		}
		want := strings.TrimSpace(string(out))
		got := hex.EncodeToString(encodeLegacyGSM0338([]byte(subject)))
		if got != want {
			t.Errorf("subject %q: go=%s oracle=%s", subject, got, want)
		}
	}
}
