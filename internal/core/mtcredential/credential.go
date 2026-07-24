// Package mtcredential ports Jasmin's MtMessagingCredential authorization and
// value-filter model (jasmin/routing/jasminApi.py) together with the HTTP "Send"
// request validation performed on top of it (jasmin/protocols/http/validation.py's
// HttpAPICredentialValidator). It is the pure gate every MT submit passes through:
// which optional parameters a user may set (authorizations) and which values are
// admissible (value filters), plus the user's default source address.
//
// Quotas (balance, submit_sm_count, throughput) are intentionally NOT modelled here;
// those live in the billing package. This package covers only the authorization and
// value-filter halves of MtMessagingCredential.
package mtcredential

import "regexp"

// Authorization keys — the MtMessagingCredential.authorizations map (jasminApi.py:110).
const (
	AuthHTTPSend                = "http_send"
	AuthHTTPBulk                = "http_bulk"
	AuthHTTPBalance             = "http_balance"
	AuthHTTPRate                = "http_rate"
	AuthSMPPSSend               = "smpps_send"
	AuthHTTPLongContent         = "http_long_content"
	AuthSetDLRLevel             = "set_dlr_level"
	AuthHTTPSetDLRMethod        = "http_set_dlr_method"
	AuthSetSourceAddress        = "set_source_address"
	AuthSetPriority             = "set_priority"
	AuthSetValidityPeriod       = "set_validity_period"
	AuthSetHexContent           = "set_hex_content"
	AuthSetScheduleDeliveryTime = "set_schedule_delivery_time"
)

// Value-filter keys — the MtMessagingCredential.value_filters map (jasminApi.py:126).
const (
	FilterDestinationAddress = "destination_address"
	FilterSourceAddress      = "source_address"
	FilterPriority           = "priority"
	FilterValidityPeriod     = "validity_period"
	FilterContent            = "content"
)

// Jasmin's default value-filter patterns (byte regexes). Kept as exported names so the
// validator's fast-path comparisons (which compare against a literal pattern, matching
// Python's `_r.pattern != b'...'`) read clearly.
const (
	patternAny      = ".*"
	patternPriority = "^[0-3]$"
	patternDigits   = `^\d+$`
)

// Credential is a user's MtMessagingCredential authorization + value-filter + default
// state. Construct with New; operators mutate it via the setters. Not safe for concurrent
// mutation; treat as immutable once configured.
type Credential struct {
	authorizations       map[string]bool
	valueFilters         map[string]string         // key -> pattern source (byte regex)
	compiled             map[string]*regexp.Regexp // lazily-compiled cache of valueFilters
	defaultSourceAddress []byte                    // nil = unset (Python None)
	hasDefaultSource     bool
}

// New builds a credential with Jasmin's exact defaults. defaultAuthorizations sets every
// authorization except http_bulk (always false) to the given value — matching
// MtMessagingCredential.__init__(default_authorizations=True).
func New(defaultAuthorizations bool) *Credential {
	return &Credential{
		authorizations: map[string]bool{
			AuthHTTPSend:                defaultAuthorizations,
			AuthHTTPBulk:                false, // always false regardless of the default
			AuthHTTPBalance:             defaultAuthorizations,
			AuthHTTPRate:                defaultAuthorizations,
			AuthSMPPSSend:               defaultAuthorizations,
			AuthHTTPLongContent:         defaultAuthorizations,
			AuthSetDLRLevel:             defaultAuthorizations,
			AuthHTTPSetDLRMethod:        defaultAuthorizations,
			AuthSetSourceAddress:        defaultAuthorizations,
			AuthSetPriority:             defaultAuthorizations,
			AuthSetValidityPeriod:       defaultAuthorizations,
			AuthSetHexContent:           defaultAuthorizations,
			AuthSetScheduleDeliveryTime: defaultAuthorizations,
		},
		valueFilters: map[string]string{
			FilterDestinationAddress: patternAny,
			FilterSourceAddress:      patternAny,
			FilterPriority:           patternPriority,
			FilterValidityPeriod:     patternDigits,
			FilterContent:            patternAny,
		},
		compiled: map[string]*regexp.Regexp{},
	}
}

// Authorization reports whether an authorization key is granted. Unknown keys return
// false (in Jasmin an unknown key raises KeyError; callers only use the known constants).
func (c *Credential) Authorization(key string) bool {
	return c.authorizations[key]
}

// SetAuthorization grants or revokes an authorization key.
func (c *Credential) SetAuthorization(key string, value bool) {
	c.authorizations[key] = value
}

// ValueFilterPattern returns the byte-regex source for a value-filter key (empty if
// unknown). This is the pattern the validator's fast-path compares literally.
func (c *Credential) ValueFilterPattern(key string) string {
	return c.valueFilters[key]
}

// SetValueFilter replaces a value-filter pattern, validating that it compiles (Jasmin
// compiles at set time via re.compile). Gotcha: patterns must be RE2-compatible — Go's
// regexp rejects backreferences/lookaround that Python's re accepts.
func (c *Credential) SetValueFilter(key, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	c.valueFilters[key] = pattern
	c.compiled[key] = re
	return nil
}

// SetDefaultSourceAddress sets the user's default source address (Python
// defaults['source_address']). Pass nil to clear it back to unset.
func (c *Credential) SetDefaultSourceAddress(value []byte) {
	if value == nil {
		c.defaultSourceAddress = nil
		c.hasDefaultSource = false
		return
	}
	c.defaultSourceAddress = append([]byte(nil), value...)
	c.hasDefaultSource = true
}

// DefaultSourceAddress returns the default source address and whether one is set.
func (c *Credential) DefaultSourceAddress() ([]byte, bool) {
	if !c.hasDefaultSource {
		return nil, false
	}
	return append([]byte(nil), c.defaultSourceAddress...), true
}

// compiledFilter returns the compiled regex for a value-filter key, compiling and caching
// on first use. Defaults are only compiled when actually matched (the common default
// patterns are skipped by the validator's fast-path and never reach here).
func (c *Credential) compiledFilter(key string) (*regexp.Regexp, error) {
	if re, ok := c.compiled[key]; ok {
		return re, nil
	}
	re, err := regexp.Compile(c.valueFilters[key])
	if err != nil {
		return nil, err
	}
	c.compiled[key] = re
	return re, nil
}
