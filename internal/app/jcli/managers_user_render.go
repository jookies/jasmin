package jcli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

// The console's credential key names are not the internal ones: `dlr_level` is
// set_dlr_level, `src_addr` is set_source_address, and so on
// (MtMessagingCredentialKeyMap, jasmin/protocols/cli/usersm.py:8-38). Order
// matters as much as spelling -- `user -s` prints these rows in exactly this
// sequence, and scripts read them positionally.

// mtAuthorizationOrder is the console authorization key order for `user -s`.
var mtAuthorizationOrder = []string{
	"http_send", "http_balance", "http_rate", "http_bulk", "smpps_send",
	"http_long_content", "dlr_level", "http_dlr_method", "src_addr",
	"priority", "validity_period", "schedule_delivery_time", "hex_content",
}

// mtValueFilterOrder is the console value-filter key order.
var mtValueFilterOrder = []string{
	"dst_addr", "src_addr", "priority", "validity_period", "content",
}

// legacyDefaultValueFilters are the patterns a fresh MtMessagingCredential
// carries (jasminApi.py:126).
var legacyDefaultValueFilters = map[string]string{
	"dst_addr":        ".*",
	"src_addr":        ".*",
	"priority":        "^[0-3]$",
	"validity_period": `^\d+$`,
	"content":         ".*",
}

// authorizationValue resolves one console authorization key against a stored
// credential, falling back to the legacy default: every authorization defaults
// true except http_bulk, which is always false on a fresh credential.
func authorizationValue(credential *outbound.MTCredentialConfig, key string) bool {
	if credential != nil {
		if explicit := consoleAuthorizationPointer(credential, key); explicit != nil {
			return *explicit
		}
	}
	return key != "http_bulk"
}

// consoleAuthorizationPointer maps a console key to its config field.
func consoleAuthorizationPointer(c *outbound.MTCredentialConfig, key string) *bool {
	switch key {
	case "http_send":
		return c.HTTPSend
	case "http_balance":
		return c.HTTPBalance
	case "http_rate":
		return c.HTTPRate
	case "http_bulk":
		return c.HTTPBulk
	case "smpps_send":
		return c.SMPPSSend
	case "http_long_content":
		return c.HTTPLongContent
	case "dlr_level":
		return c.SetDLRLevel
	case "http_dlr_method":
		return c.HTTPSetDLRMethod
	case "src_addr":
		return c.SetSourceAddress
	case "priority":
		return c.SetPriority
	case "validity_period":
		return c.SetValidityPeriod
	case "schedule_delivery_time":
		return c.SetScheduleDeliveryTime
	case "hex_content":
		return c.SetHexContent
	default:
		return nil
	}
}

// setConsoleAuthorization records a console authorization key.
func setConsoleAuthorization(c *outbound.MTCredentialConfig, key string, value bool) bool {
	switch key {
	case "http_send":
		c.HTTPSend = &value
	case "http_balance":
		c.HTTPBalance = &value
	case "http_rate":
		c.HTTPRate = &value
	case "http_bulk":
		c.HTTPBulk = &value
	case "smpps_send":
		c.SMPPSSend = &value
	case "http_long_content":
		c.HTTPLongContent = &value
	case "dlr_level":
		c.SetDLRLevel = &value
	case "http_dlr_method":
		c.HTTPSetDLRMethod = &value
	case "src_addr":
		c.SetSourceAddress = &value
	case "priority":
		c.SetPriority = &value
	case "validity_period":
		c.SetValidityPeriod = &value
	case "schedule_delivery_time":
		c.SetScheduleDeliveryTime = &value
	case "hex_content":
		c.SetHexContent = &value
	default:
		return false
	}
	return true
}

// valueFilterPattern resolves a console value-filter key, defaulting to the
// legacy pattern.
func valueFilterPattern(c *outbound.MTCredentialConfig, key string) string {
	if c != nil {
		var stored string
		switch key {
		case "dst_addr":
			stored = c.FilterDestinationAddress
		case "src_addr":
			stored = c.FilterSourceAddress
		case "priority":
			stored = c.FilterPriority
		case "validity_period":
			stored = c.FilterValidityPeriod
		case "content":
			stored = c.FilterContent
		}
		if stored != "" {
			return stored
		}
	}
	return legacyDefaultValueFilters[key]
}

// setValueFilter records a console value-filter key.
func setValueFilter(c *outbound.MTCredentialConfig, key, pattern string) bool {
	switch key {
	case "dst_addr":
		c.FilterDestinationAddress = pattern
	case "src_addr":
		c.FilterSourceAddress = pattern
	case "priority":
		c.FilterPriority = pattern
	case "validity_period":
		c.FilterValidityPeriod = pattern
	case "content":
		c.FilterContent = pattern
	default:
		return false
	}
	return true
}

// pythonBool renders Go's bool the way Python's %s does.
func pythonBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

// notDefined is what the console prints for an unset quota.
const notDefined = "ND"

// renderQuotaFloat prints a float quota the way Python's %s renders a float:
// an integral value still carries its ".0", because the oracle cast the typed
// value with float() before storing it. `balance 50` comes back as "50.0".
func renderQuotaFloat(value *float64) string {
	if value == nil {
		return notDefined
	}
	return pythonFloat(*value)
}

// renderQuotaBalance is renderQuotaFloat under the name the list code reads by.
func renderQuotaBalance(value *float64) string { return renderQuotaFloat(value) }

// pythonFloat renders a float the way Python does: integral values keep one
// decimal place, everything else uses the shortest exact representation.
func pythonFloat(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatFloat(value, 'f', 1, 64)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func renderQuotaInt(value *int) string {
	if value == nil {
		return notDefined
	}
	return strconv.Itoa(*value)
}

// showUserRows renders `user -s` in the oracle's field order.
func showUserRows(uid, gid string, user outbound.UserConfig) string {
	credential := user.MTCredential
	var lines []string
	lines = append(lines,
		"uid "+uid,
		"gid "+gid,
		"username "+user.Username,
	)
	for _, key := range mtAuthorizationOrder {
		lines = append(lines, fmt.Sprintf("mt_messaging_cred authorization %s %s",
			key, pythonBool(authorizationValue(credential, key))))
	}
	for _, key := range mtValueFilterOrder {
		lines = append(lines, fmt.Sprintf("mt_messaging_cred valuefilter %s %s",
			key, valueFilterPattern(credential, key)))
	}
	// An unset default source address prints Python's None, not an empty value.
	defaultSource := "None"
	if credential != nil && credential.DefaultSourceAddress != nil {
		defaultSource = *credential.DefaultSourceAddress
	}
	lines = append(lines, "mt_messaging_cred defaultvalue src_addr "+defaultSource)

	earlyPercent := (*float64)(nil)
	if user.EarlyDecrementBalancePercent != nil {
		value := float64(*user.EarlyDecrementBalancePercent)
		earlyPercent = &value
	}
	var httpThroughput, smppsThroughput *float64
	if credential != nil {
		httpThroughput, smppsThroughput = credential.HTTPThroughput, credential.SMPPSThroughput
	}
	lines = append(lines,
		"mt_messaging_cred quota balance "+renderQuotaBalance(user.Balance),
		"mt_messaging_cred quota early_percent "+renderEarlyPercent(earlyPercent),
		"mt_messaging_cred quota sms_count "+renderQuotaInt(user.SubmitSMCount),
		"mt_messaging_cred quota http_throughput "+renderQuotaFloat(httpThroughput),
		"mt_messaging_cred quota smpps_throughput "+renderQuotaFloat(smppsThroughput),
	)
	return strings.Join(lines, "\n")
}

// renderEarlyPercent prints early_percent with the same float rendering: the
// oracle stores it as a float, so an operator's "10" comes back as "10.0".
func renderEarlyPercent(value *float64) string { return renderQuotaFloat(value) }
