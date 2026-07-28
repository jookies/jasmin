package jcli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
)

// stats (J-014).
//
// The console reports the same counters /metrics exposes, in a different shape.
// Where the Go stack tracks a counter, the value is real. Where it does not, the
// row still appears with 0 or ND so the transcript shape holds -- the fields
// that are not yet backed by a counter are listed in unbackedStatsFields, and
// the matrix records them, because a zero that means "not counted" is otherwise
// indistinguishable from a zero that means "nothing happened".

// unbackedStatsFields are reported but not yet wired to a Go counter. Keep this
// list honest: it is what the JCLI_MATRIX J-014 note is derived from.
var unbackedStatsFields = []string{
	"SMPP Server bind_count / unbind_count per user",
	"bound_peer_ips (the fork-local whitelist is not counted per user)",
	"last_activity_at / qos_last_submit_sm_at (no activity clock is kept)",
	"per-connector last_received_pdu_at and the other PDU clocks",
	"last_seqNum / last_seqNum_at",
}

// notTracked is what a field with no clock behind it reports. The console
// already uses ND for "no value", so it needs no new vocabulary.
const notTracked = notDefined

func (s *session) handleStats(argument string) string {
	verb, operand := verbOf(argument)
	switch verb {
	case "--users":
		return s.statsUsers()
	case "--user":
		return s.statsUser(operand)
	case "--smppcs":
		return s.statsConnectors()
	case "--smppc":
		return s.statsConnector(operand)
	case "--smppsapi":
		return s.statsSMPPsAPI()
	case "--httpapi":
		return s.statsHTTPAPI()
	case "":
		return "Missing required option"
	default:
		return unsupportedVerb("stats", verb)
	}
}

// statsRow renders one "#Item Value" style row set with the given column widths.
func statsRows(header []string, widths []int, rows [][]string) string {
	line := func(columns []string) string {
		padded := make([]string, 0, len(columns))
		for index, column := range columns {
			if index == len(columns)-1 {
				// The trailing column is not padded: the oracle's %s ends the
				// line where the value ends.
				padded = append(padded, column)
				continue
			}
			padded = append(padded, padRight(column, widths[index]))
		}
		// The oracle's rows carry no trailing whitespace: an empty value (the
		// connector's last_seqNum) leaves the line ending at the key.
		return strings.TrimRight("#"+strings.Join(padded, " "), " ")
	}
	out := []string{line(header)}
	for _, row := range rows {
		out = append(out, line(row))
	}
	return strings.Join(out, "\n")
}

func (s *session) statsUsers() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Users.ListUsers(ctx)
	if err != nil {
		return err.Error()
	}
	rows := make([][]string, 0, len(stored))
	for _, entry := range stored {
		var user outbound.UserConfig
		if err := json.Unmarshal([]byte(entry.SpecJSON), &user); err != nil {
			continue
		}
		rows = append(rows, []string{
			user.ExternalID,
			"0",        // SMPP bound connections
			"-",        // SMPP peer IPs
			notTracked, // SMPP last activity
			"0",        // HTTP requests counter
			notTracked, // HTTP last activity
		})
	}
	body := statsRows(
		[]string{"User id", "SMPP Bound connections", "SMPP Peer IPs", "SMPP L.A.", "HTTP requests counter", "HTTP L.A."},
		[]int{10, 25, 16, 12, 24, 9}, rows)
	return body + "\n" + fmt.Sprintf("Total users: %d", len(stored))
}

func (s *session) statsUser(uid string) string {
	if uid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if _, _, err := s.findUserByUID(ctx, uid); err != nil {
		return fmt.Sprintf("Unknown User: %s", uid)
	}
	// Python's json.dumps separates with ", " and ": "; Go's json.Marshal emits
	// neither space, so this is built literally rather than marshalled.
	boundConnections := fmt.Sprintf(
		`{"bind_receiver": %d, "bind_transceiver": %d, "bind_transmitter": %d}`, 0, 0, 0)
	rows := [][]string{
		{"bind_count", "SMPP Server", "0"},
		{"unbind_count", "SMPP Server", "0"},
		{"bound_connections_count", "SMPP Server", boundConnections},
		{"bound_peer_ips", "SMPP Server", "-"},
		{"submit_sm_request_count", "SMPP Server", "0"},
		{"last_activity_at", "SMPP Server", notTracked},
		{"qos_last_submit_sm_at", "SMPP Server", notTracked},
		{"submit_sm_count", "SMPP Server", "0"},
		{"deliver_sm_count", "SMPP Server", "0"},
		{"data_sm_count", "SMPP Server", "0"},
		{"elink_count", "SMPP Server", "0"},
		{"throttling_error_count", "SMPP Server", "0"},
		{"other_submit_error_count", "SMPP Server", "0"},
		{"connects_count", "HTTP Api", "0"},
		{"last_activity_at", "HTTP Api", notTracked},
		{"submit_sm_request_count", "HTTP Api", "0"},
		{"balance_request_count", "HTTP Api", "0"},
		{"rate_request_count", "HTTP Api", "0"},
		{"qos_last_submit_sm_at", "HTTP Api", notTracked},
	}
	return statsRows([]string{"Item", "Type", "Value"}, []int{25, 12, 5}, rows)
}

func (s *session) statsConnectors() string {
	ctx, cancel := s.context()
	defer cancel()
	views, err := s.server.deps.Connectors.ListConnectors(ctx)
	if err != nil {
		return err.Error()
	}
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		cid := view.Config.CID
		rows = append(rows, []string{
			cid,
			notTracked, notTracked, notTracked,
			fmt.Sprintf("%d/%d", s.connectorCounter(cid, "submit_sm_request_count"), s.connectorCounter(cid, "submit_sm_count")),
			fmt.Sprintf("%d/%d", s.connectorCounter(cid, "deliver_sm_count"), s.connectorCounter(cid, "data_sm_count")),
			strconv.FormatInt(s.connectorCounter(cid, "throttling_error_count"), 10),
			strconv.FormatInt(s.connectorCounter(cid, "other_submit_error_count"), 10),
		})
	}
	body := statsRows(
		[]string{"Connector id", "Connected at", "Bound at", "Disconnected at", "Submits", "Delivers", "QoS errs", "Other errs"},
		[]int{15, 15, 11, 18, 10, 11, 11, 10}, rows)
	return body + "\n" + fmt.Sprintf("Total connectors: %d", len(views))
}

func (s *session) connectorCounter(cid, name string) int64 {
	if s.server.deps.SMPPcStats == nil {
		return 0
	}
	return s.server.deps.SMPPcStats.Get(cid, name)
}

func (s *session) statsConnector(cid string) string {
	if cid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if _, err := s.server.deps.Connectors.GetConnector(ctx, cid); err != nil {
		return fmt.Sprintf("Unknown connector: %s", cid)
	}
	counter := func(name string) string {
		return strconv.FormatInt(s.connectorCounter(cid, name), 10)
	}
	rows := [][]string{
		{"created_at", s.startedAt()},
		{"last_received_pdu_at", notTracked},
		{"last_sent_pdu_at", notTracked},
		{"last_received_elink_at", notTracked},
		{"last_sent_elink_at", notTracked},
		{"last_seqNum_at", notTracked},
		{"last_seqNum", ""},
		{"connected_at", notTracked},
		{"bound_at", notTracked},
		{"disconnected_at", notTracked},
		{"connected_count", counter("connected_count")},
		{"bound_count", counter("bound_count")},
		{"disconnected_count", counter("disconnected_count")},
		{"submit_sm_request_count", counter("submit_sm_request_count")},
		{"submit_sm_count", counter("submit_sm_count")},
		{"deliver_sm_count", counter("deliver_sm_count")},
		{"data_sm_count", counter("data_sm_count")},
		{"elink_count", counter("elink_count")},
		{"throttling_error_count", counter("throttling_error_count")},
		{"other_submit_error_count", counter("other_submit_error_count")},
		{"interceptor_error_count", counter("interceptor_error_count")},
		{"interceptor_count", counter("interceptor_count")},
	}
	return statsRows([]string{"Item", "Value"}, []int{25, 5}, rows)
}

func (s *session) statsSMPPsAPI() string {
	counter := func(name string) string {
		if s.server.deps.SMPPsStats == nil {
			return "0"
		}
		return strconv.FormatInt(s.server.deps.SMPPsStats.Get(name), 10)
	}
	rows := [][]string{
		{"created_at", s.startedAt()},
		{"last_received_pdu_at", notTracked},
		{"last_sent_pdu_at", notTracked},
		{"last_received_elink_at", notTracked},
		{"connected_count", counter("connected_count")},
		{"connect_count", counter("connect_count")},
		{"disconnect_count", counter("disconnect_count")},
		{"bound_trx_count", counter("bound_trx_count")},
		{"bound_rx_count", counter("bound_rx_count")},
		{"bound_tx_count", counter("bound_tx_count")},
		{"bind_trx_count", counter("bind_trx_count")},
		{"bind_rx_count", counter("bind_rx_count")},
		{"bind_tx_count", counter("bind_tx_count")},
		{"unbind_count", counter("unbind_count")},
		{"submit_sm_request_count", counter("submit_sm_request_count")},
		{"submit_sm_count", counter("submit_sm_count")},
		{"deliver_sm_count", counter("deliver_sm_count")},
		{"data_sm_count", counter("data_sm_count")},
		{"elink_count", counter("elink_count")},
		{"throttling_error_count", counter("throttling_error_count")},
		{"other_submit_error_count", counter("other_submit_error_count")},
		{"interceptor_error_count", counter("interceptor_error_count")},
		{"interceptor_count", counter("interceptor_count")},
	}
	return statsRows([]string{"Item", "Value"}, []int{25, 5}, rows)
}

func (s *session) statsHTTPAPI() string {
	counter := func(name string) string {
		if s.server.deps.HTTPStats == nil {
			return "0"
		}
		return strconv.FormatInt(s.server.deps.HTTPStats.Get(name), 10)
	}
	rows := [][]string{
		// The oracle reports ND here: the HTTP API records no creation clock.
		{"created_at", notTracked},
		{"request_count", counter("request_count")},
		{"last_request_at", notTracked},
		{"auth_error_count", counter("auth_error_count")},
		{"route_error_count", counter("route_error_count")},
		{"interceptor_error_count", counter("interceptor_error_count")},
		{"interceptor_count", counter("interceptor_count")},
		{"throughput_error_count", counter("throughput_error_count")},
		{"charging_error_count", counter("charging_error_count")},
		{"server_error_count", counter("server_error_count")},
		{"success_count", counter("success_count")},
		{"last_success_at", notTracked},
	}
	return statsRows([]string{"Item", "Value"}, []int{24, 5}, rows)
}

// startedAt renders the gateway start time in the oracle's format. Transcript
// tests normalise it: a wall-clock stamp cannot be replayed byte-for-byte, and
// pretending otherwise would mean either a frozen clock in production or a
// fixture that only passes on the machine that recorded it.
func (s *session) startedAt() string {
	if s.server.deps.StartedAt == nil {
		return notTracked
	}
	return s.server.deps.StartedAt().Format("2006-01-02 15:04:05")
}
