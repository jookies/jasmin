package mtcredential

import "fmt"

// Reason classifies why a Send request was rejected.
type Reason string

const (
	// ReasonAuthorization: the user is not authorized to set a supplied parameter
	// (Jasmin's CredentialValidationError from _checkSendAuthorizations).
	ReasonAuthorization Reason = "authorization"
	// ReasonValueFilter: a supplied value did not match the user's value filter
	// (Jasmin's CredentialValidationError from _checkSendFilters).
	ReasonValueFilter Reason = "value_filter"
)

// ValidationError is returned when a Send request fails credential validation. Key is the
// authorization key or value-filter key that failed, matching the specific Jasmin check.
type ValidationError struct {
	Reason Reason
	Key    string
}

func (e *ValidationError) Error() string {
	switch e.Reason {
	case ReasonAuthorization:
		return fmt.Sprintf("credential authorization failed: %s not authorized", e.Key)
	case ReasonValueFilter:
		return fmt.Sprintf("credential value filter failed: %s filter mismatch", e.Key)
	default:
		return "credential validation failed"
	}
}

func authErr(key string) *ValidationError   { return &ValidationError{ReasonAuthorization, key} }
func filterErr(key string) *ValidationError { return &ValidationError{ReasonValueFilter, key} }

// SendRequest projects an HTTP /send request for credential validation. The Has* flags
// mirror argument presence in Jasmin's request.args (e.g. HasSource == (b'from' in args));
// value fields carry the raw argument bytes for value-filter matching. IsLongContent is
// true when the message segments into more than one PDU (Python: submit_sm.nextPdu set).
type SendRequest struct {
	Destination []byte // 'to' — mandatory, always value-filtered

	Source    []byte // 'from' value
	HasSource bool

	Priority    []byte // 'priority' value
	HasPriority bool

	ValidityPeriod      []byte // 'validity-period' value
	HasValidityPeriod   bool
	ValidityPeriodIsInt bool // request.args value was an int (JSON API) — bypasses the filter

	Content    []byte // 'content' value
	HasContent bool

	HasDLRLevel   bool // 'dlr-level' present
	HasDLRMethod  bool // 'dlr-method' present
	HasHexContent bool // 'hex-content' present
	HasSDT        bool // 'sdt' present

	IsLongContent bool // multipart message
}

// ValidateSend runs Jasmin's HttpAPICredentialValidator for the 'Send' action:
// _checkSendAuthorizations then _checkSendFilters, in order, returning the first failure as
// a *ValidationError (nil when the request is allowed).
func ValidateSend(c *Credential, r SendRequest) error {
	if err := checkSendAuthorizations(c, r); err != nil {
		return err
	}
	return checkSendFilters(c, r)
}

// checkSendAuthorizations mirrors validation.py:_checkSendAuthorizations. Order is
// significant: the first unauthorized parameter is the one reported.
func checkSendAuthorizations(c *Credential, r SendRequest) error {
	if !c.Authorization(AuthHTTPSend) {
		return authErr(AuthHTTPSend)
	}
	if r.IsLongContent && !c.Authorization(AuthHTTPLongContent) {
		return authErr(AuthHTTPLongContent)
	}
	if r.HasDLRLevel && !c.Authorization(AuthSetDLRLevel) {
		return authErr(AuthSetDLRLevel)
	}
	if r.HasDLRMethod && !c.Authorization(AuthHTTPSetDLRMethod) {
		return authErr(AuthHTTPSetDLRMethod)
	}
	if r.HasSource && !c.Authorization(AuthSetSourceAddress) {
		return authErr(AuthSetSourceAddress)
	}
	if r.HasPriority && !c.Authorization(AuthSetPriority) {
		return authErr(AuthSetPriority)
	}
	if r.HasValidityPeriod && !c.Authorization(AuthSetValidityPeriod) {
		return authErr(AuthSetValidityPeriod)
	}
	if r.HasHexContent && !c.Authorization(AuthSetHexContent) {
		return authErr(AuthSetHexContent)
	}
	if r.HasSDT && !c.Authorization(AuthSetScheduleDeliveryTime) {
		return authErr(AuthSetScheduleDeliveryTime)
	}
	return nil
}

// checkSendFilters mirrors validation.py:_checkSendFilters. Each filter is skipped when its
// pattern still equals the literal Jasmin compares against — NOT necessarily that filter's
// own default. Note the validity_period quirk below.
func checkSendFilters(c *Credential, r SendRequest) error {
	// destination_address ('to') — mandatory. Skip-literal ".*".
	if err := applyFilter(c, FilterDestinationAddress, r.Destination, patternAny); err != nil {
		return err
	}
	// source_address ('from'). Skip-literal ".*".
	if r.HasSource {
		if err := applyFilter(c, FilterSourceAddress, r.Source, patternAny); err != nil {
			return err
		}
	}
	// priority. Skip-literal "^[0-3]$" (its own default).
	if r.HasPriority {
		if err := applyFilter(c, FilterPriority, r.Priority, patternPriority); err != nil {
			return err
		}
	}
	// validity_period. Quirk (validation.py:161): Jasmin compares the pattern against ".*",
	// NOT this filter's own default "^\d+$" — so the default validity_period filter is always
	// applied (never skipped), and an int-typed value bypasses the filter entirely.
	if r.HasValidityPeriod && !r.ValidityPeriodIsInt {
		if err := applyFilter(c, FilterValidityPeriod, r.ValidityPeriod, patternAny); err != nil {
			return err
		}
	}
	// content. Skip-literal ".*".
	if r.HasContent {
		if err := applyFilter(c, FilterContent, r.Content, patternAny); err != nil {
			return err
		}
	}
	return nil
}

// applyFilter returns a value-filter error when the credential's pattern for key differs
// from skipLiteral and does not match value (Python re.match semantics: anchored at the
// start of value, unanchored at the end). A malformed custom pattern that fails to compile
// is treated as a filter failure (Jasmin compiles at set time, so this cannot occur for the
// built-in defaults).
func applyFilter(c *Credential, key string, value []byte, skipLiteral string) error {
	if c.ValueFilterPattern(key) == skipLiteral {
		return nil
	}
	re, err := c.compiledFilter(key)
	if err != nil {
		return filterErr(key)
	}
	if loc := re.FindIndex(value); loc == nil || loc[0] != 0 {
		return filterErr(key)
	}
	return nil
}

// ValidateBalance runs the 'Balance' action check (validation.py:_checkBalanceAuthorizations).
func ValidateBalance(c *Credential) error {
	if !c.Authorization(AuthHTTPBalance) {
		return authErr(AuthHTTPBalance)
	}
	return nil
}

// ValidateRate runs the 'Rate' action check (validation.py:_checkRateAuthorizations).
func ValidateRate(c *Credential) error {
	if !c.Authorization(AuthHTTPRate) {
		return authErr(AuthHTTPRate)
	}
	return nil
}

// ApplyDefaultSourceAddress implements updatePDUWithUserDefaults: when the user has a
// default source address set and the submitted source is absent (nil), the default is
// substituted. A present-but-empty source ([]byte{}) is NOT absent and keeps its value,
// matching Python's `PDU.params['source_addr'] is None` test.
func (c *Credential) ApplyDefaultSourceAddress(source []byte) []byte {
	if def, ok := c.DefaultSourceAddress(); ok && source == nil {
		return def
	}
	return source
}
