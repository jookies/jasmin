package smpps

import "testing"

func TestIsIPAllowed(t *testing.T) {
	cases := []struct {
		name      string
		ip        string
		whitelist string
		want      bool
	}{
		{"host in /16", "192.168.1.5", "192.168.0.0/16", true},
		{"host outside /8", "192.168.1.5", "10.0.0.0/8", false},
		{"any-ipv4 default", "192.168.1.5", "0.0.0.0/0", true},
		{"bare host exact", "192.168.1.5", "192.168.1.5", true},
		{"bare host mismatch", "192.168.1.6", "192.168.1.5", false},
		{"ipv6 in v6 cidr", "2001:db8::1", "2001:db8::/32", true},
		{"ipv6 outside v6 cidr", "2001:dead::1", "2001:db8::/32", false},
		// The IPv4-only-default gotcha: 0.0.0.0/0 must NOT admit an IPv6 peer.
		{"ipv6 vs ipv4 default", "2001:db8::1", "0.0.0.0/0", false},
		{"ipv4 vs ipv6 default", "10.0.0.1", "::/0", false},
		{"mixed list v4 hit", "10.1.2.3", "10.0.0.0/8, 2001:db8::/32", true},
		{"mixed list v6 hit", "2001:db8::5", "10.0.0.0/8, 2001:db8::/32", true},
		// Malformed entries are skipped, not fatal.
		{"malformed skipped", "10.0.0.1", "garbage, not-an-ip, 10.0.0.0/8", true},
		{"all malformed -> deny", "10.0.0.1", "garbage, nope", false},
		{"empty ip", "", "0.0.0.0/0", false},
		{"empty whitelist", "10.0.0.1", "", false},
		{"invalid ip", "not-an-ip", "0.0.0.0/0", false},
		{"host bits in cidr (non-strict)", "10.5.6.7", "10.5.6.99/24", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsIPAllowed(c.ip, c.whitelist); got != c.want {
				t.Errorf("IsIPAllowed(%q, %q) = %v, want %v", c.ip, c.whitelist, got, c.want)
			}
		})
	}
}

func TestValidateWhitelist(t *testing.T) {
	valid := []string{
		"0.0.0.0/0",
		"::/0",
		"10.0.0.0/8",
		"10.0.0.0/8, 192.168.1.5",
		"10.0.0.0/8, 2001:db8::/32",
		"  10.0.0.0/8 ,  2001:db8::1  ",
	}
	for _, w := range valid {
		if err := ValidateWhitelist(w); err != nil {
			t.Errorf("ValidateWhitelist(%q) = %v, want nil", w, err)
		}
	}

	invalid := []string{
		"",                    // empty -> error (avoid lock-out)
		"   ",                 // whitespace only
		", ,",                 // only separators
		"10.0.0.0/8, garbage", // one bad entry
		"999.999.999.999",     // bad address
		"10.0.0.0/33",         // bad prefix
	}
	for _, w := range invalid {
		if err := ValidateWhitelist(w); err == nil {
			t.Errorf("ValidateWhitelist(%q) = nil, want error", w)
		}
	}
}

func TestDefaultWhitelistAllowsAnyIPv4(t *testing.T) {
	if !IsIPAllowed("203.0.113.9", DefaultIPWhitelist) {
		t.Error("default whitelist must allow any IPv4")
	}
	if err := ValidateWhitelist(DefaultIPWhitelist); err != nil {
		t.Errorf("default whitelist must validate: %v", err)
	}
}
