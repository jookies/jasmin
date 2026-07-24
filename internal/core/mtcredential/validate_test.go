package mtcredential

import (
	"errors"
	"testing"
)

// baseSend is a minimal valid Send request: only the mandatory destination.
func baseSend() SendRequest {
	return SendRequest{Destination: []byte("+15551234567")}
}

// assertReason checks that err is a *ValidationError with the given reason and key.
func assertReason(t *testing.T, err error, reason Reason, key string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v", err)
	}
	if ve.Reason != reason || ve.Key != key {
		t.Fatalf("got %s/%s, want %s/%s", ve.Reason, ve.Key, reason, key)
	}
}

func TestValidateSend_DefaultAllows(t *testing.T) {
	// A fully-authorized user sending only a destination passes.
	if err := ValidateSend(New(true), baseSend()); err != nil {
		t.Errorf("default-authorized send rejected: %v", err)
	}
}

func TestValidateSend_HTTPSendDenied(t *testing.T) {
	c := New(false) // http_send = false
	assertReason(t, ValidateSend(c, baseSend()), ReasonAuthorization, AuthHTTPSend)
}

func TestValidateSend_Authorizations(t *testing.T) {
	// For each optional parameter, a user with everything authorized EXCEPT that one
	// parameter must be rejected on exactly that authorization key.
	cases := []struct {
		name    string
		revoke  string
		mutate  func(*SendRequest)
		wantKey string
	}{
		{"long content", AuthHTTPLongContent, func(r *SendRequest) { r.IsLongContent = true }, AuthHTTPLongContent},
		{"dlr-level", AuthSetDLRLevel, func(r *SendRequest) { r.HasDLRLevel = true }, AuthSetDLRLevel},
		{"dlr-method", AuthHTTPSetDLRMethod, func(r *SendRequest) { r.HasDLRMethod = true }, AuthHTTPSetDLRMethod},
		{"from", AuthSetSourceAddress, func(r *SendRequest) { r.HasSource = true; r.Source = []byte("X") }, AuthSetSourceAddress},
		{"priority", AuthSetPriority, func(r *SendRequest) { r.HasPriority = true; r.Priority = []byte("1") }, AuthSetPriority},
		{"validity-period", AuthSetValidityPeriod, func(r *SendRequest) { r.HasValidityPeriod = true; r.ValidityPeriod = []byte("60") }, AuthSetValidityPeriod},
		{"hex-content", AuthSetHexContent, func(r *SendRequest) { r.HasHexContent = true }, AuthSetHexContent},
		{"sdt", AuthSetScheduleDeliveryTime, func(r *SendRequest) { r.HasSDT = true }, AuthSetScheduleDeliveryTime},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(true)
			c.SetAuthorization(tc.revoke, false)
			r := baseSend()
			tc.mutate(&r)
			assertReason(t, ValidateSend(c, r), ReasonAuthorization, tc.wantKey)

			// With the authorization granted (default true), the same request passes
			// authorization (default value filters don't reject these values).
			if err := ValidateSend(New(true), r); err != nil {
				t.Errorf("authorized %s still rejected: %v", tc.name, err)
			}
		})
	}
}

func TestValidateSend_AuthorizationOrder(t *testing.T) {
	// http_send is checked before every parameter authorization.
	c := New(false)
	r := baseSend()
	r.HasPriority = true
	r.Priority = []byte("1")
	assertReason(t, ValidateSend(c, r), ReasonAuthorization, AuthHTTPSend)
}

func TestValidateSend_DefaultPriorityFilterSkipped(t *testing.T) {
	// Quirk: the default priority value filter ("^[0-3]$") is skipped at the credential
	// layer (Jasmin compares the pattern against its own default and short-circuits), so an
	// out-of-range priority passes the value filter here (the syntax validator catches it).
	c := New(true)
	r := baseSend()
	r.HasPriority = true
	r.Priority = []byte("9")
	if err := ValidateSend(c, r); err != nil {
		t.Errorf("default priority filter should be skipped, got %v", err)
	}
}

func TestValidateSend_CustomPriorityFilterEnforced(t *testing.T) {
	// Once the operator sets a non-default priority filter, it IS enforced.
	c := New(true)
	if err := c.SetValueFilter(FilterPriority, "^[0-1]$"); err != nil {
		t.Fatal(err)
	}
	r := baseSend()
	r.HasPriority = true
	r.Priority = []byte("3")
	assertReason(t, ValidateSend(c, r), ReasonValueFilter, FilterPriority)

	r.Priority = []byte("1")
	if err := ValidateSend(c, r); err != nil {
		t.Errorf("priority 1 within ^[0-1]$ rejected: %v", err)
	}
}

func TestValidateSend_ValidityPeriodQuirk(t *testing.T) {
	// The default validity_period filter ("^\d+$") is ALWAYS applied (Jasmin compares its
	// pattern against ".*", never its own default), so a non-numeric value is rejected...
	c := New(true)
	r := baseSend()
	r.HasValidityPeriod = true
	r.ValidityPeriod = []byte("2h")
	assertReason(t, ValidateSend(c, r), ReasonValueFilter, FilterValidityPeriod)

	// ...a numeric value passes...
	r.ValidityPeriod = []byte("3600")
	if err := ValidateSend(c, r); err != nil {
		t.Errorf("numeric validity period rejected: %v", err)
	}

	// ...and an int-typed value bypasses the filter entirely (Python's isinstance(_value, int)).
	r.ValidityPeriod = []byte("2h")
	r.ValidityPeriodIsInt = true
	if err := ValidateSend(c, r); err != nil {
		t.Errorf("int validity period should bypass the filter, got %v", err)
	}
}

func TestValidateSend_CustomDestinationFilter(t *testing.T) {
	// Default destination filter (".*") is skipped; a custom one is enforced.
	c := New(true)
	if err := c.SetValueFilter(FilterDestinationAddress, `^\+[0-9]+$`); err != nil {
		t.Fatal(err)
	}
	r := SendRequest{Destination: []byte("not-a-number")}
	assertReason(t, ValidateSend(c, r), ReasonValueFilter, FilterDestinationAddress)

	r = SendRequest{Destination: []byte("+15551234567")}
	if err := ValidateSend(c, r); err != nil {
		t.Errorf("valid destination rejected: %v", err)
	}
}

func TestValidateSend_FilterUsesReMatchSemantics(t *testing.T) {
	// Python re.match anchors at the START of the value but not the end. A pattern with no
	// trailing anchor matches a value with an unmatched suffix.
	c := New(true)
	if err := c.SetValueFilter(FilterContent, `[A-Z]+`); err != nil {
		t.Fatal(err)
	}
	r := baseSend()
	r.HasContent = true

	r.Content = []byte("ABC123") // starts with [A-Z]+ -> matches
	if err := ValidateSend(c, r); err != nil {
		t.Errorf("re.match should accept a matching prefix, got %v", err)
	}
	r.Content = []byte("123ABC") // does not start with [A-Z] -> no match
	assertReason(t, ValidateSend(c, r), ReasonValueFilter, FilterContent)
}

func TestValidateSend_FilterOrder(t *testing.T) {
	// Filters run after all authorizations and in field order: destination is filtered
	// before source. With both destination and source failing a custom filter, destination
	// is reported.
	c := New(true)
	if err := c.SetValueFilter(FilterDestinationAddress, `^\+[0-9]+$`); err != nil {
		t.Fatal(err)
	}
	if err := c.SetValueFilter(FilterSourceAddress, `^\+[0-9]+$`); err != nil {
		t.Fatal(err)
	}
	r := SendRequest{Destination: []byte("bad"), HasSource: true, Source: []byte("bad")}
	assertReason(t, ValidateSend(c, r), ReasonValueFilter, FilterDestinationAddress)
}

func TestValidateBalanceAndRate(t *testing.T) {
	if err := ValidateBalance(New(true)); err != nil {
		t.Errorf("balance authorized: %v", err)
	}
	assertReason(t, ValidateBalance(New(false)), ReasonAuthorization, AuthHTTPBalance)

	if err := ValidateRate(New(true)); err != nil {
		t.Errorf("rate authorized: %v", err)
	}
	assertReason(t, ValidateRate(New(false)), ReasonAuthorization, AuthHTTPRate)
}
