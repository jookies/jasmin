package jcli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// SMPP client connector management (J-012). The field order in `smppccm -s` is
// the frozen SMPPClientConfig attribute order, captured in
// spec/compatibility/fixtures/jcli/J-012-smppccm.jsonl. It is not alphabetical
// and not grouped by meaning: it is insertion order, and scripts read it.

// smppccmShowOrder is that order, by console key.
var smppccmShowOrder = []string{
	"cid", "host", "port", "username", "logrotate", "password", "systype",
	"logfile", "loglevel", "logprivacy", "bind_to", "elink_interval", "res_to",
	"con_loss_retry", "bind_npi", "con_loss_delay", "con_fail_delay",
	"pdu_red_to", "bind", "bind_ton", "src_ton", "src_npi", "dst_ton",
	"addr_range", "src_addr", "proto_id", "priority", "validity", "ripf",
	"def_msg_id", "coding", "requeue_delay", "submit_throughput", "dlr_expiry",
	"dlr_msgid", "con_fail_retry", "dst_npi", "trx_to", "ssl", "custom_tlvs",
}

// connectorKind is the interactive contract for `smppccm -a` / `-u`.
var connectorKind = &entityKind{
	keyLabel:        "SMPPClientConfig",
	addAnnouncement: "Adding a new connector: (ok: save, ko: exit)",
	updateAnnouncement: func(cid string) string {
		return fmt.Sprintf("Updating connector id [%s]: (ok: save, ko: exit)", cid)
	},
	required: []string{"cid"},
	validate: validateConnectorKey,
	save:     saveConnector,
}

// validateConnectorKey accepts one key/value line, applying the oracle's casts.
func validateConnectorKey(is *interactiveSession, key, value string) (string, bool) {
	if !contains(smppccmShowOrder, key) {
		return fmt.Sprintf("Unknown SMPPClientConfig key: %s", key), false
	}
	switch key {
	case "con_fail_retry", "con_loss_retry", "ssl", "logprivacy":
		if value != "yes" && value != "no" {
			return fmt.Sprintf("Error: Unknown value for key %s: %s", key, value), false
		}
	case "loglevel":
		switch value {
		case "10", "20", "30", "40", "50":
		default:
			return "Error: loglevel must be numeric value of 10, 20, 30, 40 or 50.", false
		}
	case "port", "bind_to", "elink_interval", "res_to", "con_loss_delay",
		"con_fail_delay", "pdu_red_to", "bind_npi", "bind_ton", "src_ton",
		"src_npi", "dst_ton", "dst_npi", "proto_id", "priority", "ripf",
		"def_msg_id", "coding", "requeue_delay", "dlr_expiry", "dlr_msgid",
		"trx_to", "submit_throughput":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Sprintf("Error: Unknown value for key %s: %s", key, value), false
		}
	}
	is.set(key, value)
	return "", true
}

// saveConnector folds the session values onto the stored connector (or a fresh
// one) and applies it through the admin service.
func saveConnector(s *session, is *interactiveSession) (string, bool) {
	ctx, cancel := s.context()
	defer cancel()

	config := smppc.Config{}
	cid := is.values["cid"]
	starting := false
	if is.updating {
		view, err := s.server.deps.Connectors.GetConnector(ctx, is.targetID)
		if err != nil {
			return fmt.Sprintf("Unknown connector: %s", is.targetID), false
		}
		config = view.Config
		cid = is.targetID
		starting = view.DesiredStarted
		if len(is.values) == 0 {
			return "Nothing to save", false
		}
	}

	for _, key := range is.typed {
		if message, ok := applyConnectorValue(&config, key, is.values[key]); !ok {
			return message, false
		}
	}
	config.CID = cid
	applyLegacyConnectorDefaults(&config)

	if is.updating {
		if err := s.server.deps.Connectors.UpdateConnector(ctx, config); err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		return fmt.Sprintf("Successfully updated connector [%s]", cid), true
	}
	if err := s.server.deps.Connectors.CreateConnector(ctx, config, starting); err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return fmt.Sprintf("Successfully added connector [%s]", cid), true
}

func applyConnectorValue(config *smppc.Config, key, value string) (string, bool) {
	atoi := func() int { parsed, _ := strconv.Atoi(value); return parsed }
	atof := func() float64 { parsed, _ := strconv.ParseFloat(value, 64); return parsed }
	yes := value == "yes"

	switch key {
	case "cid":
		config.CID = value
	case "host":
		config.Host = value
	case "port":
		config.Port = atoi()
	case "username":
		config.SystemID = value
	case "password":
		config.Password = value
	case "systype":
		config.SystemType = value
	case "bind":
		config.Bind = smppc.BindType(value)
	case "logfile":
		config.LogFile = value
	case "logrotate":
		config.LogRotate = value
	case "loglevel":
		config.LogLevel = value
	case "logprivacy":
		config.LogPrivacy = yes
	case "bind_to":
		config.SessionInitTimeout = atof()
	case "elink_interval":
		config.EnquireLinkInterval = atof()
	case "res_to":
		config.ResTimeout = atof()
	case "pdu_red_to":
		config.PDUTimeout = atof()
	case "trx_to":
		config.TrxTimeout = atof()
	case "con_loss_retry":
		config.ConLossRetry = &yes
	case "con_fail_retry":
		config.ConFailRetry = &yes
	case "con_loss_delay":
		config.ConLossDelay = atof()
	case "con_fail_delay":
		config.ConFailDelay = atof()
	case "bind_npi":
		config.AddrNPI = atoi()
	case "bind_ton":
		config.AddrTON = atoi()
	case "src_ton":
		config.SrcTON = atoi()
	case "src_npi":
		config.SrcNPI = atoi()
	case "dst_ton":
		config.DstTON = atoi()
	case "dst_npi":
		config.DstNPI = atoi()
	case "addr_range":
		config.AddressRange = noneToEmpty(value)
	case "src_addr":
		config.SourceAddr = noneToEmpty(value)
	case "proto_id":
		config.ProtocolID = atoi()
	case "priority":
		config.Priority = atoi()
	case "validity":
		config.ValidityPeriod = noneToEmpty(value)
	case "ripf":
		config.ReplaceIfPresentFlag = atoi()
	case "def_msg_id":
		config.SmDefaultMsgID = atoi()
	case "coding":
		config.DataCoding = atoi()
	case "requeue_delay":
		config.RequeueDelay = atof()
	case "submit_throughput":
		throughput := atof()
		config.SubmitSMThroughput = &throughput
	case "dlr_expiry":
		config.DLRExpiry = atoi()
	case "dlr_msgid":
		config.DLRMsgIDBases = atoi()
	case "ssl":
		config.TLSEnabled = yes
	case "custom_tlvs":
		rules, err := parseCustomTLVs(value)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		config.CustomTLVs = rules
	default:
		return fmt.Sprintf("Unknown SMPPClientConfig key: %s", key), false
	}
	return "", true
}

// applyLegacyConnectorDefaults fills what a frozen SMPPClientConfig would fill.
//
// The oracle accepts `smppccm -a` with nothing but a cid and produces a working
// connector on 127.0.0.1:2775 as smppclient/password, and `smppccm -s` then
// reports those values. This lives in the console rather than in
// smppc.Config.Validate() on purpose: the Go config file should keep rejecting
// a connector with no host, and a default bind password is not something to
// hand to every programmatic caller. The console is the legacy-compatibility
// face, so it is where legacy's generosity belongs.
func applyLegacyConnectorDefaults(config *smppc.Config) {
	if config.Host == "" {
		config.Host = "127.0.0.1"
	}
	if config.Port == 0 {
		config.Port = 2775
	}
	if config.SystemID == "" {
		config.SystemID = "smppclient"
	}
	if config.Password == "" {
		config.Password = "password"
	}
	if config.LogLevel == "" {
		config.LogLevel = "20"
	}
	if config.LogRotate == "" {
		config.LogRotate = "midnight"
	}
	// Validate() fills the numeric and address defaults (and is re-run by the
	// admin service); the error is the service's to report, not ours.
	_ = config.Validate()
}

// noneToEmpty maps the console's literal "None" to an unset value, the way the
// oracle casts it (castInputToBuiltInType).
func noneToEmpty(value string) string {
	if strings.EqualFold(value, "none") {
		return ""
	}
	return value
}

// parseCustomTLVs reads the connector TLV rule syntax:
// tag,type,max_length[,required|optional] joined by ";".
func parseCustomTLVs(value string) ([]smppc.CustomTLVRule, error) {
	if strings.EqualFold(strings.TrimSpace(value), "none") {
		return nil, nil
	}
	validTypes := map[string]bool{
		"Int1": true, "Int2": true, "Int4": true, "Int8": true,
		"OctetString": true, "COctetString": true,
	}
	var rules []smppc.CustomTLVRule
	for _, entry := range strings.Split(value, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ",")
		if len(parts) < 3 {
			return nil, fmt.Errorf("TLV format must be: tag,type,max_length[,required|optional]. Got: %s", entry)
		}
		tagText := strings.TrimSpace(parts[0])
		tag, err := parseIntMaybeHex(tagText)
		if err != nil {
			return nil, fmt.Errorf("Invalid TLV tag: %s", tagText)
		}
		tlvType := strings.TrimSpace(parts[1])
		if !validTypes[tlvType] {
			return nil, fmt.Errorf("Invalid TLV type: %s. Must be one of: Int1, Int2, Int4, Int8, OctetString, COctetString", tlvType)
		}
		rule := smppc.CustomTLVRule{Tag: tag, Type: tlvType}
		if lengthText := strings.TrimSpace(parts[2]); lengthText != "" && lengthText != "-" {
			length, err := parseIntMaybeHex(lengthText)
			if err != nil || length <= 0 {
				return nil, fmt.Errorf("TLV max_length must be a positive integer (got %s)", lengthText)
			}
			rule.Length = &length
		}
		if len(parts) >= 4 {
			switch strings.ToLower(strings.TrimSpace(parts[3])) {
			case "required":
				rule.Required = true
			case "optional", "":
			default:
				return nil, fmt.Errorf("TLV flag must be \"required\" or \"optional\". Got: %s", parts[3])
			}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func parseIntMaybeHex(text string) (int, error) {
	if strings.HasPrefix(strings.ToLower(text), "0x") {
		parsed, err := strconv.ParseInt(text[2:], 16, 64)
		return int(parsed), err
	}
	parsed, err := strconv.Atoi(text)
	return parsed, err
}

// showConnectorRows renders `smppccm -s` in the frozen field order.
func showConnectorRows(config smppc.Config) string {
	lines := make([]string, 0, len(smppccmShowOrder))
	for _, key := range smppccmShowOrder {
		lines = append(lines, key+" "+connectorFieldValue(config, key))
	}
	return strings.Join(lines, "\n")
}

func connectorFieldValue(config smppc.Config, key string) string {
	pythonYesNo := func(value bool) string {
		if value {
			return "yes"
		}
		return "no"
	}
	orNone := func(value string) string {
		if value == "" {
			return "None"
		}
		return value
	}
	number := func(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

	switch key {
	case "cid":
		return config.CID
	case "host":
		return config.Host
	case "port":
		return strconv.Itoa(config.Port)
	case "username":
		return config.SystemID
	case "password":
		// The oracle prints the bind password in clear. Matching it keeps
		// scripts that read this field working; the console is an
		// authenticated management plane, and the deviation of hiding it is
		// recorded in spec/compatibility/DEVIATIONS.md rather than taken
		// silently here.
		return config.Password
	case "systype":
		return config.SystemType
	case "logrotate":
		return defaultString(config.LogRotate, "midnight")
	case "logfile":
		return config.LogFile
	case "loglevel":
		return defaultString(config.LogLevel, "20")
	case "logprivacy":
		return pythonYesNo(config.LogPrivacy)
	case "bind_to":
		return number(config.SessionInitTimeout)
	case "elink_interval":
		return number(config.EnquireLinkInterval)
	case "res_to":
		return number(config.ResTimeout)
	case "pdu_red_to":
		return number(config.PDUTimeout)
	case "trx_to":
		return number(config.TrxTimeout)
	case "con_loss_retry":
		return pythonYesNo(config.ConLossRetry == nil || *config.ConLossRetry)
	case "con_fail_retry":
		return pythonYesNo(config.ConFailRetry == nil || *config.ConFailRetry)
	case "con_loss_delay":
		return number(config.ConLossDelay)
	case "con_fail_delay":
		return number(config.ConFailDelay)
	case "bind":
		return string(config.Bind)
	case "bind_npi":
		return strconv.Itoa(config.AddrNPI)
	case "bind_ton":
		return strconv.Itoa(config.AddrTON)
	case "src_ton":
		return strconv.Itoa(config.SrcTON)
	case "src_npi":
		return strconv.Itoa(config.SrcNPI)
	case "dst_ton":
		return strconv.Itoa(config.DstTON)
	case "dst_npi":
		return strconv.Itoa(config.DstNPI)
	case "addr_range":
		return orNone(config.AddressRange)
	case "src_addr":
		return orNone(config.SourceAddr)
	case "proto_id":
		if config.ProtocolID == 0 {
			return "None"
		}
		return strconv.Itoa(config.ProtocolID)
	case "priority":
		return strconv.Itoa(config.Priority)
	case "validity":
		return orNone(config.ValidityPeriod)
	case "ripf":
		return strconv.Itoa(config.ReplaceIfPresentFlag)
	case "def_msg_id":
		return strconv.Itoa(config.SmDefaultMsgID)
	case "coding":
		return strconv.Itoa(config.DataCoding)
	case "requeue_delay":
		return number(config.RequeueDelay)
	case "submit_throughput":
		if config.SubmitSMThroughput == nil {
			return "1"
		}
		return number(*config.SubmitSMThroughput)
	case "dlr_expiry":
		if config.DLRExpiry == 0 {
			return "86400"
		}
		return strconv.Itoa(config.DLRExpiry)
	case "dlr_msgid":
		return strconv.Itoa(config.DLRMsgIDBases)
	case "ssl":
		return pythonYesNo(config.TLSEnabled)
	case "custom_tlvs":
		return renderCustomTLVs(config.CustomTLVs)
	default:
		return ""
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// renderCustomTLVs mirrors castOutputToBuiltInType's custom_tlvs branch.
func renderCustomTLVs(rules []smppc.CustomTLVRule) string {
	if len(rules) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(rules))
	for _, rule := range rules {
		length := "-"
		if rule.Length != nil {
			length = strconv.Itoa(*rule.Length)
		}
		required := "optional"
		if rule.Required {
			required = "required"
		}
		parts = append(parts, fmt.Sprintf("0x%04X,%s,%s,%s", rule.Tag, rule.Type, length, required))
	}
	return strings.Join(parts, ";")
}

// legacySessionState renders the "Session" column the way the oracle does. A
// connector whose service is not running has no session at all and shows NONE;
// the Go manager reports DISCONNECTED for that case, which is the same fact
// under a different word, and the word is what scripts read.
func legacySessionState(started bool, observed string) string {
	if !started {
		return "NONE"
	}
	switch observed {
	case "BOUND":
		return "BOUND_TRX"
	case "CONNECTING":
		return "CONNECTED"
	case "UNBINDING":
		return "UNBOUND"
	case "DISCONNECTED":
		return "NONE"
	default:
		return observed
	}
}
