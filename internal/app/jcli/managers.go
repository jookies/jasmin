package jcli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/core/smppc"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
)

// Column widths and headers below are the legacy ones (jasmin/protocols/cli/
// *m.py). Each row is prefixed with "#" and columns are ljust-padded then joined
// by a single space, exactly as the oracle formats them — scripts parse this.

const commandTimeout = 10 * time.Second

// legacyGroupID is the frozen gid constraint (jasmin/routing/jasminApi.py:229).
var legacyGroupID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)

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
	case verb == "-a" || verb == "--add":
		return s.startInteractive(connectorKind, false, "")
	case verb == "-u" || verb == "--update":
		if operand == "" {
			return "Missing required option"
		}
		ctx, cancel := s.context()
		defer cancel()
		if _, err := s.server.deps.Connectors.GetConnector(ctx, operand); err != nil {
			return fmt.Sprintf("Unknown connector: %s", operand)
		}
		return s.startInteractive(connectorKind, true, operand)
	case verb == "-r" || verb == "--remove":
		return s.removeConnector(operand)
	case verb == "-1" || verb == "--start":
		return s.setConnectorStarted(operand, true)
	case verb == "-0" || verb == "--stop":
		return s.setConnectorStarted(operand, false)
	case verb == "":
		return "Missing required option"
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
	views = append(s.configConnectorViews(views), views...)
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
				padRight(legacySessionState(view.DesiredStarted, view.Observed), 16),
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
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	view, err := s.server.deps.Connectors.GetConnector(ctx, cid)
	if err != nil {
		return fmt.Sprintf("Unknown connector: %s", cid)
	}
	return showConnectorRows(view.Config)
}

func (s *session) removeConnector(cid string) string {
	if cid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Connectors.DeleteConnector(ctx, cid); err != nil {
		return fmt.Sprintf("Unknown connector: %s", cid)
	}
	return fmt.Sprintf("Successfully removed connector id:%s", cid)
}

func (s *session) setConnectorStarted(cid string, start bool) string {
	if cid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Connectors.SetStarted(ctx, cid, start); err != nil {
		return fmt.Sprintf("Failed starting/stopping connector id:%s", cid)
	}
	if start {
		return fmt.Sprintf("Successfully started connector id:%s", cid)
	}
	return fmt.Sprintf("Successfully stopped connector id:%s", cid)
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

// ------------------------------------------------------------------- user ---

func (s *session) handleUser(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listUsers()
	case isShowVerb(verb):
		return s.showUser(operand)
	case verb == "-a" || verb == "--add":
		return s.startInteractive(userKind, false, "")
	case verb == "-u" || verb == "--update":
		if operand == "" {
			return "Missing required option"
		}
		ctx, cancel := s.context()
		defer cancel()
		if _, _, err := s.findUserByUID(ctx, operand); err != nil {
			return fmt.Sprintf("Unknown User: %s", operand)
		}
		return s.startInteractive(userKind, true, operand)
	case verb == "-r" || verb == "--remove":
		return s.removeUser(operand)
	case verb == "-e" || verb == "--enable":
		return s.setUserEnabled(operand, true)
	case verb == "-d" || verb == "--disable":
		return s.setUserEnabled(operand, false)
	case verb == "":
		return "Missing required option"
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
	disabledGroups := s.disabledGroups(ctx)
	users := s.configUsers()
	for _, entry := range stored {
		var user outbound.UserConfig
		if err := json.Unmarshal([]byte(entry.SpecJSON), &user); err != nil {
			return fmt.Sprintf("user %q: stored spec is not valid JSON: %v", entry.Username, err)
		}
		users = append(users, user)
	}

	var lines []string
	if len(users) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("User id", 16),
			padRight("Group id", 16),
			padRight("Username", 16),
			padRight("Balance", 7),
			padRight("MT SMS", 6),
			padRight("Throughput", 8),
		}, " "))
		for _, user := range users {
			lines = append(lines, "#"+strings.Join(userListColumns(user, disabledGroups), " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total Users: %d", len(users)))
	return strings.Join(lines, "\n")
}

// userListColumns renders one user row. Balance and MT SMS both gain a "(!)"
// marker when *both* are undefined -- the oracle's way of flagging a user with
// no spending ceiling at all (usersm.py:417).
func userListColumns(user outbound.UserConfig, disabledGroups map[string]bool) []string {
	userPrefix := ""
	if user.Disabled {
		userPrefix = "!"
	}
	groupPrefix := ""
	if disabledGroups[user.GroupID] {
		groupPrefix = "!"
	}
	balance := renderQuotaBalance(user.Balance)
	smsCount := renderQuotaInt(user.SubmitSMCount)
	if balance == notDefined && smsCount == notDefined {
		balance, smsCount = "ND (!)", "ND (!)"
	}
	httpThroughput, smppsThroughput := notDefined, notDefined
	if user.MTCredential != nil {
		httpThroughput = renderQuotaFloat(user.MTCredential.HTTPThroughput)
		smppsThroughput = renderQuotaFloat(user.MTCredential.SMPPSThroughput)
	}
	return []string{
		padRight(userPrefix+user.ExternalID, 16),
		padRight(groupPrefix+user.GroupID, 16),
		padRight(user.Username, 16),
		padRight(balance, 7),
		padRight(smsCount, 6),
		padRight(httpThroughput+"/"+smppsThroughput, 8),
	}
}

// disabledGroups reports which gids are disabled, for the list's "!" prefix.
func (s *session) disabledGroups(ctx context.Context) map[string]bool {
	disabled := map[string]bool{}
	if s.server.deps.Groups == nil {
		return disabled
	}
	stored, err := s.server.deps.Groups.ListGroups(ctx)
	if err != nil {
		return disabled
	}
	for _, entry := range stored {
		var group outbound.GroupConfig
		if err := json.Unmarshal([]byte(entry.SpecJSON), &group); err != nil {
			continue
		}
		disabled[entry.GID] = group.Disabled
	}
	return disabled
}

func (s *session) showUser(uid string) string {
	if uid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	_, user, err := s.findUserByUID(ctx, uid)
	if err != nil {
		return fmt.Sprintf("Unknown User: %s", uid)
	}
	return showUserRows(uid, user.GroupID, user) + "\n" + s.showSMPPsCredentialRows(ctx, user)
}

// showSMPPsCredentialRows renders the smpps_cred half from the mirrored bind
// account, falling back to the legacy defaults when there is none.
func (s *session) showSMPPsCredentialRows(ctx context.Context, user outbound.UserConfig) string {
	bind, ip, maxBindings := true, "0.0.0.0/0", notDefined
	if user.SMPPSCredential != nil {
		if user.SMPPSCredential.Bind != nil {
			bind = *user.SMPPSCredential.Bind
		}
		if user.SMPPSCredential.IP != "" {
			ip = user.SMPPSCredential.IP
		}
		if user.SMPPSCredential.MaxBindings != nil {
			maxBindings = strconv.Itoa(*user.SMPPSCredential.MaxBindings)
		}
	}
	return strings.Join([]string{
		"smpps_cred authorization bind " + pythonBool(bind),
		"smpps_cred authorization ip " + ip,
		"smpps_cred quota max_bindings " + maxBindings,
	}, "\n")
}

func (s *session) removeUser(uid string) string {
	if uid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	_, user, err := s.findUserByUID(ctx, uid)
	if err != nil {
		return fmt.Sprintf("Unknown User: %s", uid)
	}
	if err := s.server.deps.Users.DeleteUser(ctx, user.Username); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if s.server.deps.SMPPsUsers != nil {
		// Best effort: the mirrored bind account may never have existed.
		_ = s.server.deps.SMPPsUsers.DeleteUser(ctx, user.Username)
	}
	return fmt.Sprintf("Successfully removed User id:%s", uid)
}

func (s *session) setUserEnabled(uid string, enabled bool) string {
	if uid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	_, user, err := s.findUserByUID(ctx, uid)
	if err != nil {
		return fmt.Sprintf("Unknown User: %s", uid)
	}
	user.Disabled = !enabled
	spec, err := json.Marshal(user)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if err := s.server.deps.Users.CreateUser(ctx, user.Username, string(spec)); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if _, ok := s.mirrorSMPPsAccount(ctx, user); !ok {
		return fmt.Sprintf("Error: could not update the SMPPs bind account for %s", user.Username)
	}
	if enabled {
		return fmt.Sprintf("Successfully enabled User id:%s", uid)
	}
	return fmt.Sprintf("Successfully disabled User id:%s", uid)
}

// optionalFloat renders a nil quota as the legacy "ND" (not defined).
func optionalFloat(value *float64) string {
	if value == nil {
		return notDefined
	}
	return fmt.Sprintf("%.2f", *value)
}

func optionalInt(value *int) string {
	if value == nil {
		return notDefined
	}
	return fmt.Sprint(*value)
}

// configConnectorViews projects the config-owned connectors into the same view
// the admin service returns, skipping any cid the admin plane also knows (it
// cannot, but a defensive skip keeps the list free of duplicates).
func (s *session) configConnectorViews(managed []admin.ConnectorView) []admin.ConnectorView {
	if s.server.deps.ConfigConnectors == nil {
		return nil
	}
	known := make(map[string]bool, len(managed))
	for _, view := range managed {
		known[view.Config.CID] = true
	}
	var views []admin.ConnectorView
	for _, config := range s.server.deps.ConfigConnectors() {
		if known[config.CID] {
			continue
		}
		observed := string(smppc.StatusDisconnected)
		started := false
		if s.server.deps.ConnectorStatus != nil {
			if status, err := s.server.deps.ConnectorStatus(config.CID); err == nil {
				observed = string(status.Observed)
				started = status.Desired
			}
		}
		views = append(views, admin.ConnectorView{
			Config: config, DesiredStarted: started, Observed: observed,
		})
	}
	return views
}

// configUsers lists the config-owned users, which the admin store never sees.
func (s *session) configUsers() []outbound.UserConfig {
	if s.server.deps.ConfigUsers == nil {
		return nil
	}
	return s.server.deps.ConfigUsers()
}
