// Package smpps implements Jasmin's SMPP server flow (customer-facing binds). This first
// unit is the per-user IP whitelist matcher — a fork feature applied at bind time after
// password authentication.
package smpps

import (
	"fmt"
	"net"
	"strings"
)

// DefaultIPWhitelist allows any IPv4 — the fork's SmppsCredential 'ip' authorization
// default. Note it is IPv4-only: an IPv6 peer is NOT admitted by this default.
const DefaultIPWhitelist = "0.0.0.0/0"

// splitWhitelist splits a comma-separated whitelist, trimming entries and dropping empties.
func splitWhitelist(whitelist string) []string {
	var out []string
	for _, part := range strings.Split(whitelist, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseEntry parses one whitelist entry — a CIDR ("10.0.0.0/8") or a bare address
// ("192.168.1.5" → /32, "2001:db8::1" → /128) — returning nil when malformed. CIDR host
// bits are masked off (non-strict), matching Python's ipaddress.ip_network(strict=False).
func parseEntry(entry string) *net.IPNet {
	if _, network, err := net.ParseCIDR(entry); err == nil {
		return network
	}
	if ip := net.ParseIP(entry); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}
	}
	return nil
}

// parseNetworks parses the whitelist into networks, silently skipping malformed entries —
// deliberate, so a bad config row cannot crash the bind authentication path.
func parseNetworks(whitelist string) []*net.IPNet {
	var networks []*net.IPNet
	for _, entry := range splitWhitelist(whitelist) {
		if n := parseEntry(entry); n != nil {
			networks = append(networks, n)
		}
	}
	return networks
}

// ValidateWhitelist validates a whitelist string for the operator at config-set time. An
// empty whitelist is an error (set "0.0.0.0/0" explicitly to allow any IPv4), which
// prevents an accidental lock-out; every entry must be a valid address or CIDR.
func ValidateWhitelist(whitelist string) error {
	parts := splitWhitelist(whitelist)
	if len(parts) == 0 {
		return fmt.Errorf(`whitelist is empty (use "0.0.0.0/0" to allow any IPv4)`)
	}
	for _, entry := range parts {
		if parseEntry(entry) == nil {
			return fmt.Errorf("invalid whitelist entry %q", entry)
		}
	}
	return nil
}

// IsIPAllowed reports whether ip falls inside any network in the whitelist. An empty or
// unparseable ip, or an empty whitelist, returns false. The address family must match the
// network's, so the default "0.0.0.0/0" (IPv4) does NOT admit an IPv6 peer — a preserved
// Jasmin gotcha, not a bug to "fix" silently.
func IsIPAllowed(ip, whitelist string) bool {
	if ip == "" || whitelist == "" {
		return false
	}
	addr := net.ParseIP(ip)
	if addr == nil {
		return false
	}
	addrIsV4 := addr.To4() != nil
	for _, network := range parseNetworks(whitelist) {
		if addrIsV4 != (network.IP.To4() != nil) {
			continue // address-family mismatch
		}
		if network.Contains(addr) {
			return true
		}
	}
	return false
}
