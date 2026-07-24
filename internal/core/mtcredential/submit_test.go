package mtcredential

import (
	"bytes"
	"testing"
)

// baseSubmit is a minimal valid submit_sm: destination only, empty source, priority 0,
// no receipt requested.
func baseSubmit() SubmitRequest {
	return SubmitRequest{DestinationAddr: []byte("+15551234567")}
}

func TestValidateSubmit_DefaultAllows(t *testing.T) {
	if err := ValidateSubmit(New(true), baseSubmit()); err != nil {
		t.Errorf("default-authorized submit rejected: %v", err)
	}
}

func TestValidateSubmit_SMPPSSendDenied(t *testing.T) {
	assertReason(t, ValidateSubmit(New(false), baseSubmit()), ReasonAuthorization, AuthSMPPSSend)
}

func TestValidateSubmit_Authorizations(t *testing.T) {
	cases := []struct {
		name    string
		revoke  string
		mutate  func(*SubmitRequest)
		wantKey string
	}{
		{"dlr requested", AuthSetDLRLevel, func(r *SubmitRequest) { r.DLRRequested = true }, AuthSetDLRLevel},
		{"source set", AuthSetSourceAddress, func(r *SubmitRequest) { r.SourceAddr = []byte("SENDER") }, AuthSetSourceAddress},
		{"priority set", AuthSetPriority, func(r *SubmitRequest) { r.PriorityFlag = 2 }, AuthSetPriority},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(true)
			c.SetAuthorization(tc.revoke, false)
			r := baseSubmit()
			tc.mutate(&r)
			assertReason(t, ValidateSubmit(c, r), ReasonAuthorization, tc.wantKey)

			// Granted (default true) -> passes.
			if err := ValidateSubmit(New(true), r); err != nil {
				t.Errorf("authorized %s still rejected: %v", tc.name, err)
			}
		})
	}
}

func TestValidateSubmit_DefaultAttributesSkipAuthorization(t *testing.T) {
	// A user with NONE of the set_* authorizations may still submit a default PDU (no dlr,
	// empty source, priority 0) — the authorization triggers are field values, not presence.
	c := New(false)
	c.SetAuthorization(AuthSMPPSSend, true) // only smpps_send granted
	if err := ValidateSubmit(c, baseSubmit()); err != nil {
		t.Errorf("default-attribute submit with only smpps_send should pass: %v", err)
	}
}

func TestValidateSubmit_AuthorizationOrder(t *testing.T) {
	// smpps_send precedes the attribute checks.
	c := New(false)
	r := baseSubmit()
	r.PriorityFlag = 3
	assertReason(t, ValidateSubmit(c, r), ReasonAuthorization, AuthSMPPSSend)
}

func TestValidateSubmit_DefaultPriorityFilterSkipped(t *testing.T) {
	// As in the HTTP path, the default priority filter is skipped; but note priority 9 also
	// needs set_priority (non-zero), which the default-true credential grants.
	c := New(true)
	r := baseSubmit()
	r.PriorityFlag = 9
	if err := ValidateSubmit(c, r); err != nil {
		t.Errorf("default priority filter should be skipped, got %v", err)
	}
}

func TestValidateSubmit_CustomPriorityFilterUsesDecimalLevel(t *testing.T) {
	// The priority value filtered is the decimal level "0".."3". A custom ^[0-1]$ filter
	// rejects priority 3 and accepts priority 1.
	c := New(true)
	if err := c.SetValueFilter(FilterPriority, "^[0-1]$"); err != nil {
		t.Fatal(err)
	}
	r := baseSubmit()
	r.PriorityFlag = 3
	assertReason(t, ValidateSubmit(c, r), ReasonValueFilter, FilterPriority)

	r.PriorityFlag = 1
	if err := ValidateSubmit(c, r); err != nil {
		t.Errorf("priority 1 within ^[0-1]$ rejected: %v", err)
	}
}

func TestValidateSubmit_CustomFiltersEnforced(t *testing.T) {
	c := New(true)
	if err := c.SetValueFilter(FilterDestinationAddress, `^\+[0-9]+$`); err != nil {
		t.Fatal(err)
	}
	if err := c.SetValueFilter(FilterContent, `^[ -~]+$`); err != nil { // printable ASCII
		t.Fatal(err)
	}
	r := baseSubmit()
	r.ShortMessage = []byte("hello")
	if err := ValidateSubmit(c, r); err != nil {
		t.Errorf("valid submit rejected: %v", err)
	}

	bad := baseSubmit()
	bad.DestinationAddr = []byte("not-a-number")
	assertReason(t, ValidateSubmit(c, bad), ReasonValueFilter, FilterDestinationAddress)
}

func TestValidateSubmit_SourceFilterRunsUnconditionally(t *testing.T) {
	// Unlike the HTTP path (source filtered only when 'from' is present), smpps filters
	// source_addr unconditionally: a custom source filter that rejects empty strings will
	// reject an empty source_addr.
	c := New(true)
	if err := c.SetValueFilter(FilterSourceAddress, `^\+[0-9]+$`); err != nil {
		t.Fatal(err)
	}
	r := baseSubmit() // empty source
	assertReason(t, ValidateSubmit(c, r), ReasonValueFilter, FilterSourceAddress)
}

func TestValidateSubmit_FilterOrderAfterAuth(t *testing.T) {
	// Filters run after authorizations. destination is filtered before source.
	c := New(true)
	if err := c.SetValueFilter(FilterDestinationAddress, `^\+[0-9]+$`); err != nil {
		t.Fatal(err)
	}
	if err := c.SetValueFilter(FilterSourceAddress, `^\+[0-9]+$`); err != nil {
		t.Fatal(err)
	}
	r := SubmitRequest{DestinationAddr: []byte("bad"), SourceAddr: []byte("bad")}
	assertReason(t, ValidateSubmit(c, r), ReasonValueFilter, FilterDestinationAddress)
}

func TestApplyDefaultSourceAddressSubmit(t *testing.T) {
	c := New(true)
	// No default: empty stays empty.
	if got := c.ApplyDefaultSourceAddressSubmit(nil); got != nil {
		t.Errorf("no default: got %q, want nil", got)
	}
	c.SetDefaultSourceAddress([]byte("DEF"))
	// Absent (nil) AND empty both get the default — the smpps difference from HTTP.
	if got := c.ApplyDefaultSourceAddressSubmit(nil); !bytes.Equal(got, []byte("DEF")) {
		t.Errorf("nil source: got %q, want DEF", got)
	}
	if got := c.ApplyDefaultSourceAddressSubmit([]byte("")); !bytes.Equal(got, []byte("DEF")) {
		t.Errorf("empty source: got %q, want DEF (smpps replaces empty)", got)
	}
	// Present non-empty source is untouched.
	if got := c.ApplyDefaultSourceAddressSubmit([]byte("MINE")); !bytes.Equal(got, []byte("MINE")) {
		t.Errorf("present source: got %q, want MINE", got)
	}
}
