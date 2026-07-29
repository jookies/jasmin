package adminweb

import (
	"net/http"
	"testing"
)

// TestUserSaveDoesNotWidenTheBindAccount guards a quiet privilege escalation.
//
// A user's SMPPs bind account is mirrored from the user form, but the form does
// not render every field the bind account has. Rebuilding the account wholesale
// therefore resets set_dlr_level / set_source_address / set_priority to their
// permissive defaults and blanks ip_whitelist, which means "any IPv4". Editing
// an unrelated field — a balance, say — would silently hand a customer back
// source-address spoofing and drop their network restriction, with nothing in
// the response to say so.
func TestUserSaveDoesNotWidenTheBindAccount(t *testing.T) {
	f := newWebFixture(t)

	// A locked-down bind account: one subnet, no source-address spoofing.
	f.do("POST", "/api/smpps-users", `{
		"system_id": "acme",
		"password": "bindpw",
		"ip_whitelist": "10.1.0.0/16",
		"set_source_address": false,
		"set_priority": false
	}`, http.StatusCreated, nil)

	// The same identity edited through the *user* page, which carries neither
	// the whitelist nor those authorizations.
	f.do("POST", "/api/users", `{
		"username": "acme",
		"password": "httppw",
		"balance": 100,
		"smpps_bind": true
	}`, http.StatusCreated, nil)

	var account map[string]any
	f.do("GET", "/api/smpps-users/acme", "", http.StatusOK, &account)

	if got, _ := account["ip_whitelist"].(string); got != "10.1.0.0/16" {
		t.Fatalf("ip_whitelist = %q after a user save, want 10.1.0.0/16 — the bind account was opened to any IPv4", got)
	}
	if spoof, ok := account["set_source_address"].(bool); !ok || spoof {
		t.Fatalf("set_source_address = %v after a user save, want false — source-address spoofing was re-enabled", account["set_source_address"])
	}
	if priority, ok := account["set_priority"].(bool); !ok || priority {
		t.Fatalf("set_priority = %v after a user save, want false", account["set_priority"])
	}
}
