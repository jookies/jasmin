package config

import "testing"

// withEnv returns a lookup backed by a map, for deterministic override tests.
func withEnv(pairs map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := pairs[name]
		return value, ok
	}
}

func mustParse(t *testing.T, text string) *File {
	t.Helper()
	file, err := ParseString(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	file.getenv = withEnv(nil)
	return file
}

func TestGetBasicsAndDefaults(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nhost = broker1\nport = 5672\n")
	if got := file.Get("amqp-broker", "host", "localhost"); got != "broker1" {
		t.Fatalf("host = %q", got)
	}
	if got := file.Get("amqp-broker", "missing", "fallback"); got != "fallback" {
		t.Fatalf("missing default = %q", got)
	}
	if got := file.Get("no-such-section", "x", "d"); got != "d" {
		t.Fatalf("missing section default = %q", got)
	}
}

func TestOptionNamesLowercasedSectionCaseSensitive(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nHost = broker1\nMixedKey = v\n")
	// Option lookup is case-insensitive (parsed lowercase).
	if file.Get("amqp-broker", "host", "") != "broker1" || file.Get("amqp-broker", "HOST", "") != "broker1" {
		t.Fatal("option lookup should be case-insensitive")
	}
	if file.Get("amqp-broker", "mixedkey", "") != "v" {
		t.Fatal("MixedKey should lowercase to mixedkey")
	}
	// Section names are case-sensitive.
	if file.HasSection("AMQP-BROKER") {
		t.Fatal("section lookup should be case-sensitive")
	}
	if !file.HasSection("amqp-broker") {
		t.Fatal("section should exist")
	}
}

func TestNoneHandlingDivergesBetweenGetAndGetInt(t *testing.T) {
	file := mustParse(t, "[s]\ntext = None\nnumber = None\n")
	// _get returns Python None -> "" here.
	if got := file.Get("s", "text", "def"); got != "" {
		t.Fatalf("Get None = %q, want empty", got)
	}
	// _getint maps "None" to the default (the legacy quirk).
	if got, err := file.GetInt("s", "number", 42); err != nil || got != 42 {
		t.Fatalf("GetInt None = %d, %v, want 42", got, err)
	}
}

func TestGetIntFloatParsing(t *testing.T) {
	file := mustParse(t, "[s]\nn = 30\nf = 1.5\nbad = abc\n")
	if got, err := file.GetInt("s", "n", 0); err != nil || got != 30 {
		t.Fatalf("GetInt = %d, %v", got, err)
	}
	if got, err := file.GetFloat("s", "f", 0); err != nil || got != 1.5 {
		t.Fatalf("GetFloat = %v, %v", got, err)
	}
	if _, err := file.GetInt("s", "bad", 0); err == nil {
		t.Fatal("GetInt on non-integer must error")
	}
	if got, err := file.GetInt("s", "absent", 7); err != nil || got != 7 {
		t.Fatalf("GetInt absent = %d, %v", got, err)
	}
}

func TestGetBoolConfigStrictVsEnvLoose(t *testing.T) {
	file := mustParse(t, "[s]\nyes = on\nno = off\nweird = t\n")
	// Config bools use the strict configparser set.
	if got, err := file.GetBool("s", "yes", false); err != nil || !got {
		t.Fatalf("GetBool on = %v, %v", got, err)
	}
	if got, err := file.GetBool("s", "no", true); err != nil || got {
		t.Fatalf("GetBool off = %v, %v", got, err)
	}
	// "t" is NOT valid in a config value (strict) — an error.
	if _, err := file.GetBool("s", "weird", false); err == nil {
		t.Fatal("config bool 't' must error (strict configparser set)")
	}
	// But an env override uses the loose set, where "t" and "y" are true.
	file.getenv = withEnv(map[string]string{"S_WEIRD": "y"})
	if got, err := file.GetBool("s", "weird", false); err != nil || !got {
		t.Fatalf("env bool 'y' = %v, %v, want true", got, err)
	}
}

func TestEnvOverridePrecedenceAndNaming(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nhost = broker1\nport = 5672\n")
	// SECTION_OPTION uppercased, dashes -> underscores; env wins over config.
	file.getenv = withEnv(map[string]string{
		"AMQP_BROKER_HOST": "envhost",
		"AMQP_BROKER_PORT": "5673",
	})
	if got := file.Get("amqp-broker", "host", "d"); got != "envhost" {
		t.Fatalf("env override host = %q", got)
	}
	if got, err := file.GetInt("amqp-broker", "port", 0); err != nil || got != 5673 {
		t.Fatalf("env override port = %d, %v", got, err)
	}
	// An env override even supplies a value the config lacks.
	file.getenv = withEnv(map[string]string{"AMQP_BROKER_MISSING": "fromenv"})
	if got := file.Get("amqp-broker", "missing", "def"); got != "fromenv" {
		t.Fatalf("env-only value = %q", got)
	}
	// A malformed env int errors.
	file.getenv = withEnv(map[string]string{"AMQP_BROKER_PORT": "notint"})
	if _, err := file.GetInt("amqp-broker", "port", 0); err == nil {
		t.Fatal("malformed env int must error")
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"duplicate option":  "[s]\nk = 1\nk = 2\n",
		"option no section": "k = 1\n",
		"malformed header":  "[unclosed\nk = 1\n",
		"no separator":      "[s]\njust_a_key\n",
	}
	for name, text := range cases {
		if _, err := ParseString(text); err == nil {
			t.Errorf("%s: expected parse error", name)
		}
	}
}

func TestCommentsAndWhitespace(t *testing.T) {
	file := mustParse(t, "# full-line comment\n; also a comment\n[s]\nk =   spaced value   \ninline = val ; not stripped\n")
	if got := file.Get("s", "k", ""); got != "spaced value" {
		t.Fatalf("value not trimmed: %q", got)
	}
	// RawConfigParser does not strip inline comments.
	if got := file.Get("s", "inline", ""); got != "val ; not stripped" {
		t.Fatalf("inline comment should not be stripped: %q", got)
	}
}

func TestColonSeparator(t *testing.T) {
	file := mustParse(t, "[s]\nkey: value\n")
	if got := file.Get("s", "key", ""); got != "value" {
		t.Fatalf("colon separator = %q", got)
	}
}
