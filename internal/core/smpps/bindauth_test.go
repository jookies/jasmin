package smpps

import "testing"

func intp(n int) *int { return &n }

// validUser returns a UserAuth that binds successfully for password "secret" from 10.0.0.5.
func validUser() UserAuth {
	return UserAuth{
		PasswordDigest: PasswordHash("secret"),
		UserEnabled:    true,
		GroupEnabled:   true,
		BindAuthorized: true,
		IPWhitelist:    "10.0.0.0/8",
		MaxBindings:    intp(5),
	}
}

func TestAuthorizeBind_Allowed(t *testing.T) {
	if s := AuthorizeBind(validUser(), "secret", "10.0.0.5", 0); s != StatusROK {
		t.Errorf("valid bind: got %#x, want ESME_ROK", s)
	}
}

func TestAuthorizeBind_PasswordFailures(t *testing.T) {
	// Wrong password.
	if s := AuthorizeBind(validUser(), "wrong", "10.0.0.5", 0); s != StatusInvalidPassword {
		t.Errorf("wrong password: got %#x, want ESME_RINVPASWD", s)
	}
	// Disabled user / group both collapse to ESME_RINVPASWD.
	u := validUser()
	u.UserEnabled = false
	if s := AuthorizeBind(u, "secret", "10.0.0.5", 0); s != StatusInvalidPassword {
		t.Errorf("disabled user: got %#x, want ESME_RINVPASWD", s)
	}
	u = validUser()
	u.GroupEnabled = false
	if s := AuthorizeBind(u, "secret", "10.0.0.5", 0); s != StatusInvalidPassword {
		t.Errorf("disabled group: got %#x, want ESME_RINVPASWD", s)
	}
}

func TestAuthorizeBind_IPWhitelist(t *testing.T) {
	if s := AuthorizeBind(validUser(), "secret", "192.168.1.1", 0); s != StatusBindFailed {
		t.Errorf("IP not whitelisted: got %#x, want ESME_RBINDFAIL", s)
	}
	// IPv6 peer against the default IPv4-only whitelist is rejected (the gotcha).
	u := validUser()
	u.IPWhitelist = DefaultIPWhitelist
	if s := AuthorizeBind(u, "secret", "2001:db8::1", 0); s != StatusBindFailed {
		t.Errorf("ipv6 vs default: got %#x, want ESME_RBINDFAIL", s)
	}
}

func TestAuthorizeBind_NotAuthorized(t *testing.T) {
	u := validUser()
	u.BindAuthorized = false
	if s := AuthorizeBind(u, "secret", "10.0.0.5", 0); s != StatusBindFailed {
		t.Errorf("bind not authorized: got %#x, want ESME_RBINDFAIL", s)
	}
}

func TestAuthorizeBind_MaxBindings(t *testing.T) {
	u := validUser() // MaxBindings = 5
	// At the cap -> rejected.
	if s := AuthorizeBind(u, "secret", "10.0.0.5", 5); s != StatusBindFailed {
		t.Errorf("at cap: got %#x, want ESME_RBINDFAIL", s)
	}
	// Below the cap -> allowed.
	if s := AuthorizeBind(u, "secret", "10.0.0.5", 4); s != StatusROK {
		t.Errorf("below cap: got %#x, want ESME_ROK", s)
	}
	// Unlimited (nil) -> allowed regardless of count.
	u.MaxBindings = nil
	if s := AuthorizeBind(u, "secret", "10.0.0.5", 1000); s != StatusROK {
		t.Errorf("unlimited: got %#x, want ESME_ROK", s)
	}
}

func TestAuthorizeBind_Order(t *testing.T) {
	// A bad password AND a bad IP: the password is checked first, so ESME_RINVPASWD.
	if s := AuthorizeBind(validUser(), "wrong", "192.168.1.1", 0); s != StatusInvalidPassword {
		t.Errorf("password precedes IP: got %#x, want ESME_RINVPASWD", s)
	}
	// A good password but bad IP AND unauthorized bind: IP is checked before bind auth.
	u := validUser()
	u.BindAuthorized = false
	if s := AuthorizeBind(u, "secret", "192.168.1.1", 0); s != StatusBindFailed {
		t.Errorf("IP precedes bind-auth (both fail -> RBINDFAIL): got %#x", s)
	}
}
