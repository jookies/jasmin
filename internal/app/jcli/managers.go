package jcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/modispatch"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
)

// Column widths and headers below are the legacy ones (jasmin/protocols/cli/
// *m.py). Each row is prefixed with "#" and columns are ljust-padded then joined
// by a single space, exactly as the oracle formats them — scripts parse this.

const commandTimeout = 10 * time.Second

func (s *session) context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), commandTimeout)
}

// unsupportedVerb is what an implemented command says for a verb that is not
// wired yet. It is deliberately explicit rather than silently doing nothing.
func unsupportedVerb(command, verb string) string {
	return fmt.Sprintf("%s: %s is not implemented in this console yet; use the admin API or web UI", command, verb)
}

// verbOf extracts the leading option of a manager argument, e.g. "-l" from
// "-l" or "--list", and any operand that follows ("-s cid" → "-s", "cid").
func verbOf(argument string) (string, string) {
	trimmed := strings.TrimSpace(argument)
	if trimmed == "" {
		return "", ""
	}
	verb, operand, _ := strings.Cut(trimmed, " ")
	return verb, strings.TrimSpace(operand)
}

func isListVerb(verb string) bool { return verb == "-l" || verb == "--list" }
func isShowVerb(verb string) bool { return verb == "-s" || verb == "--show" }

// ---------------------------------------------------------------- smppccm ---

func (s *session) handleSMPPCCM(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listConnectors()
	case isShowVerb(verb):
		return s.showConnector(operand)
	case verb == "":
		return commandDocs["smppccm"]
	default:
		return unsupportedVerb("smppccm", verb)
	}
}

func (s *session) listConnectors() string {
	ctx, cancel := s.context()
	defer cancel()
	views, err := s.server.deps.Connectors.ListConnectors(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(views) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("Connector id", 35),
			padRight("Service", 7),
			padRight("Session", 16),
			padRight("Starts", 6),
			padRight("Stops", 5),
		}, " "))
		for _, view := range views {
			service := "stopped"
			if view.DesiredStarted {
				service = "started"
			}
			lines = append(lines, "#"+strings.Join([]string{
				padRight(view.Config.CID, 35),
				padRight(service, 7),
				padRight(view.Observed, 16),
				// Start/stop counters are not tracked by the Go manager; the
				// columns are kept so the transcript shape is unchanged.
				padRight("0", 6),
				padRight("0", 5),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total connectors: %d", len(views)))
	return strings.Join(lines, "\n")
}

func (s *session) showConnector(cid string) string {
	if cid == "" {
		return unsupportedVerb("smppccm", "-s without a connector id")
	}
	ctx, cancel := s.context()
	defer cancel()
	view, err := s.server.deps.Connectors.GetConnector(ctx, cid)
	if err != nil {
		return err.Error()
	}
	config := view.Config
	config.Password = "" // never echo bind credentials to the console
	return renderKeyValues([][2]string{
		{"cid", config.CID},
		{"host", config.Host},
		{"port", fmt.Sprint(config.Port)},
		{"username", config.SystemID},
		{"bind", string(config.Bind)},
		{"systype", config.SystemType},
		{"service", startedWord(view.DesiredStarted)},
		{"session", view.Observed},
	})
}

func startedWord(started bool) string {
	if started {
		return "started"
	}
	return "stopped"
}

// renderKeyValues renders the legacy "show" shape: key ljust(22) then value.
func renderKeyValues(pairs [][2]string) string {
	lines := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		lines = append(lines, padRight(pair[0], 22)+pair[1])
	}
	return strings.Join(lines, "\n")
}

// --------------------------------------------------------------- mtrouter ---

func (s *session) handleMTRouter(argument string) string {
	verb, _ := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listMTRoutes()
	case verb == "":
		return commandDocs["mtrouter"]
	default:
		return unsupportedVerb("mtrouter", verb)
	}
}

func (s *session) listMTRoutes() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Routes.ListRoutes(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(stored) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("Order", 5),
			padRight("Type", 23),
			padRight("Rate", 10),
			padRight("Connector ID(s)", 48),
			padRight("Filter(s)", 64),
		}, " "))
		for _, entry := range stored {
			var route outbound.RouteConfig
			if err := json.Unmarshal([]byte(entry.SpecJSON), &route); err != nil {
				return fmt.Sprintf("MT route %d: stored spec is not valid JSON: %v", entry.Order, err)
			}
			lines = append(lines, "#"+strings.Join([]string{
				padRight(fmt.Sprint(entry.Order), 5),
				padRight(routeType("MT", route.Default, len(route.ConnectorCandidates())), 23),
				padRight(fmt.Sprintf("%.2f", route.Rate), 10),
				padRight(strings.Join(route.ConnectorCandidates(), ", "), 48),
				padRight(summariseFilters(filterKinds(route.Filters)), 64),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total MT Routes: %d", len(stored)))
	return strings.Join(lines, "\n")
}

// --------------------------------------------------------------- morouter ---

func (s *session) handleMORouter(argument string) string {
	verb, _ := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listMORoutes()
	case verb == "":
		return commandDocs["morouter"]
	default:
		return unsupportedVerb("morouter", verb)
	}
}

func (s *session) listMORoutes() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.MORoutes.ListRoutes(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(stored) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("Order", 5),
			padRight("Type", 23),
			padRight("Connector ID(s)", 48),
			padRight("Filter(s)", 64),
		}, " "))
		for _, entry := range stored {
			var route modispatch.RouteConfig
			if err := json.Unmarshal([]byte(entry.SpecJSON), &route); err != nil {
				return fmt.Sprintf("MO route %d: stored spec is not valid JSON: %v", entry.Order, err)
			}
			destination := route.Connector.CID
			if route.Connector.Type == "smpps" {
				destination = route.Connector.SystemID
			}
			lines = append(lines, "#"+strings.Join([]string{
				padRight(fmt.Sprint(entry.Order), 5),
				padRight(routeType("MO", route.Default, 1), 23),
				padRight(destination, 48),
				padRight(summariseFilters(moFilterKinds(route.Filters)), 64),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total MO Routes: %d", len(stored)))
	return strings.Join(lines, "\n")
}

// routeType names the legacy route class for the list "Type" column. MO and MT
// are distinct class hierarchies in jasmin/routing/Routes.py (StaticMORoute vs
// StaticMTRoute), and the column shows the class name, so the direction matters.
func routeType(direction string, isDefault bool, connectors int) string {
	if isDefault {
		return "DefaultRoute"
	}
	if connectors > 1 {
		return "RandomRoundrobin" + direction + "Route"
	}
	return "Static" + direction + "Route"
}

func filterKinds(filters []outbound.FilterConfig) []string {
	kinds := make([]string, 0, len(filters))
	for _, filter := range filters {
		kinds = append(kinds, filter.Type)
	}
	return kinds
}

func moFilterKinds(filters []modispatch.FilterConfig) []string {
	kinds := make([]string, 0, len(filters))
	for _, filter := range filters {
		kinds = append(kinds, filter.Type)
	}
	return kinds
}

func summariseFilters(kinds []string) string {
	if len(kinds) == 0 {
		return ""
	}
	return strings.Join(kinds, ", ")
}

// ------------------------------------------------------------------- user ---

func (s *session) handleUser(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listUsers()
	case isShowVerb(verb):
		return s.showUser(operand)
	case verb == "":
		return commandDocs["user"]
	default:
		return unsupportedVerb("user", verb)
	}
}

func (s *session) listUsers() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Users.ListUsers(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(stored) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("User id", 16),
			padRight("Group id", 16),
			padRight("Username", 16),
			padRight("Balance", 7),
			padRight("MT SMS", 6),
			padRight("Throughput", 8),
		}, " "))
		for _, entry := range stored {
			var user outbound.UserConfig
			if err := json.Unmarshal([]byte(entry.SpecJSON), &user); err != nil {
				return fmt.Sprintf("user %q: stored spec is not valid JSON: %v", entry.Username, err)
			}
			lines = append(lines, "#"+strings.Join([]string{
				padRight(fmt.Sprint(entry.UID), 16),
				// Groups are not modelled yet (plan 012 Step 6); the column is
				// kept so the transcript shape does not change when they land.
				padRight("", 16),
				padRight(entry.Username, 16),
				padRight(optionalFloat(user.Balance), 7),
				padRight(optionalInt(user.SubmitSMCount), 6),
				padRight("ND", 8),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total Users: %d", len(stored)))
	return strings.Join(lines, "\n")
}

func (s *session) showUser(username string) string {
	if username == "" {
		return unsupportedVerb("user", "-s without a username")
	}
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Users.GetUser(ctx, username)
	if err != nil {
		return err.Error()
	}
	var user outbound.UserConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &user); err != nil {
		return fmt.Sprintf("user %q: stored spec is not valid JSON: %v", username, err)
	}
	return renderKeyValues([][2]string{
		{"uid", fmt.Sprint(stored.UID)},
		{"username", stored.Username},
		{"balance", optionalFloat(user.Balance)},
		{"mt_messaging_cred quota sms_count", optionalInt(user.SubmitSMCount)},
	})
}

// optionalFloat renders a nil quota as the legacy "ND" (not defined).
func optionalFloat(value *float64) string {
	if value == nil {
		return "ND"
	}
	return fmt.Sprintf("%.2f", *value)
}

func optionalInt(value *int) string {
	if value == nil {
		return "ND"
	}
	return fmt.Sprint(*value)
}
