package mtcredential

import (
	"bytes"
	"testing"
)

func TestNew_DefaultAuthorizations(t *testing.T) {
	// default_authorizations=True: every key true EXCEPT http_bulk (always false).
	c := New(true)
	for _, key := range []string{
		AuthHTTPSend, AuthHTTPBalance, AuthHTTPRate, AuthSMPPSSend, AuthHTTPLongContent,
		AuthSetDLRLevel, AuthHTTPSetDLRMethod, AuthSetSourceAddress, AuthSetPriority,
		AuthSetValidityPeriod, AuthSetHexContent, AuthSetScheduleDeliveryTime,
	} {
		if !c.Authorization(key) {
			t.Errorf("New(true): %s = false, want true", key)
		}
	}
	if c.Authorization(AuthHTTPBulk) {
		t.Error("New(true): http_bulk = true, want false (always false)")
	}

	// default_authorizations=False: everything false.
	d := New(false)
	if d.Authorization(AuthHTTPSend) || d.Authorization(AuthHTTPBulk) {
		t.Error("New(false): expected all authorizations false")
	}
}

func TestNew_DefaultValueFilters(t *testing.T) {
	c := New(true)
	want := map[string]string{
		FilterDestinationAddress: ".*",
		FilterSourceAddress:      ".*",
		FilterPriority:           "^[0-3]$",
		FilterValidityPeriod:     `^\d+$`,
		FilterContent:            ".*",
	}
	for key, pattern := range want {
		if got := c.ValueFilterPattern(key); got != pattern {
			t.Errorf("default filter %s = %q, want %q", key, got, pattern)
		}
	}
}

func TestSetAuthorization(t *testing.T) {
	c := New(false)
	c.SetAuthorization(AuthHTTPSend, true)
	if !c.Authorization(AuthHTTPSend) {
		t.Error("SetAuthorization did not grant http_send")
	}
	if c.Authorization("bogus_key") {
		t.Error("unknown authorization key should be false")
	}
}

func TestSetValueFilter(t *testing.T) {
	c := New(true)
	if err := c.SetValueFilter(FilterSourceAddress, `^\+?[0-9]{1,15}$`); err != nil {
		t.Fatalf("valid pattern rejected: %v", err)
	}
	if got := c.ValueFilterPattern(FilterSourceAddress); got != `^\+?[0-9]{1,15}$` {
		t.Errorf("pattern not stored: %q", got)
	}
	// A pattern RE2 cannot compile (backreference) must error, matching Python compiling
	// at set time — and it must NOT mutate the stored pattern.
	if err := c.SetValueFilter(FilterSourceAddress, `(a)\1`); err == nil {
		t.Error("expected error for backreference pattern (RE2 incompatible)")
	}
	if got := c.ValueFilterPattern(FilterSourceAddress); got != `^\+?[0-9]{1,15}$` {
		t.Errorf("invalid SetValueFilter mutated stored pattern: %q", got)
	}
}

func TestDefaultSourceAddress(t *testing.T) {
	c := New(true)
	if _, ok := c.DefaultSourceAddress(); ok {
		t.Error("fresh credential should have no default source address")
	}
	c.SetDefaultSourceAddress([]byte("SENDER"))
	got, ok := c.DefaultSourceAddress()
	if !ok || !bytes.Equal(got, []byte("SENDER")) {
		t.Errorf("DefaultSourceAddress = %q,%v; want SENDER,true", got, ok)
	}
	// Returned slice is a copy — mutating it must not affect the credential.
	got[0] = 'X'
	again, _ := c.DefaultSourceAddress()
	if !bytes.Equal(again, []byte("SENDER")) {
		t.Error("DefaultSourceAddress returned an aliased slice")
	}
	c.SetDefaultSourceAddress(nil)
	if _, ok := c.DefaultSourceAddress(); ok {
		t.Error("SetDefaultSourceAddress(nil) should clear the default")
	}
}

func TestApplyDefaultSourceAddress(t *testing.T) {
	c := New(true)
	// No default set: nil source stays nil.
	if got := c.ApplyDefaultSourceAddress(nil); got != nil {
		t.Errorf("no default, nil source: got %q, want nil", got)
	}
	c.SetDefaultSourceAddress([]byte("DEF"))
	// Absent (nil) source gets the default.
	if got := c.ApplyDefaultSourceAddress(nil); !bytes.Equal(got, []byte("DEF")) {
		t.Errorf("nil source: got %q, want DEF", got)
	}
	// Present source (even empty) is left untouched — Python tests `is None`, not truthiness.
	if got := c.ApplyDefaultSourceAddress([]byte("")); !bytes.Equal(got, []byte("")) {
		t.Errorf("empty source: got %q, want empty (unchanged)", got)
	}
	if got := c.ApplyDefaultSourceAddress([]byte("MINE")); !bytes.Equal(got, []byte("MINE")) {
		t.Errorf("present source: got %q, want MINE (unchanged)", got)
	}
}
