package adminweb

// Generated partner integration instructions.
//
// "How do I send you traffic" is not answerable from a URL. It depends on which
// ingress this deployment actually publishes, on what the account is authorized
// to do (internal/core/mtcredential), on which values its filters admit, on
// whether a bind account exists for it, and on whether the DLR/MO throwers are
// even running. Every one of those is per-deployment or per-account state that
// an operator otherwise retypes from memory into an email.
//
// Two rules shape this file:
//
//   - Nothing here emits password material. Verifiers are one-way (SHA-256 for
//     the HTTP password, MD5 for the bind password) and onboarding shows the
//     generated secret exactly once, so there is nothing to emit even if it were
//     wanted. Every example carries the passwordPlaceholder token instead.
//   - Nothing here invents a reachable address. Listeners bind 0.0.0.0, so the
//     process cannot know how a partner reaches it. Either the operator declared
//     public_hostname, or the payload says so and carries a placeholder the
//     console makes the operator substitute.

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/modispatch"
	"github.com/pumpitspace/synevyr/internal/core/mtcredential"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
)

// IngressSnapshot is what the gateway publishes to customers, reported by the
// runtime because the console has no other way to know. Bind addresses are the
// configured listen addresses (usually 0.0.0.0:port) and are shown to operators
// for diagnosis only — a URL is never built from one.
type IngressSnapshot struct {
	// PublicHostname is the operator-declared host partners use. Empty means the
	// deployment never declared one, and every example carries a placeholder.
	PublicHostname string
	// HTTPBindAddress is outbound.listen_address: the /send, /balance, /rate,
	// /ping, /metrics and /secure/* front door. HTTPTLS reports whether the
	// gateway terminates TLS on it.
	HTTPBindAddress string
	HTTPTLS         bool
	// RESTBindAddress is rest_api.listen_address, the historical standalone JSON
	// view. Empty is normal: /secure/* is served on the HTTP listener regardless.
	RESTBindAddress string
	// SMPPSBindAddress is smpps.bind_addr. Empty means this deployment runs no
	// customer-facing SMPP server at all, so no account can bind to it.
	SMPPSBindAddress        string
	SMPPSTLS                bool
	SMPPSEnquireLinkTimeout float64
	SMPPSInactivityTimeout  float64
	// DLRThrowerRunning and MOThrowerRunning report whether the workers that
	// actually perform HTTP callbacks are configured. Without them a partner's
	// dlr-url is accepted and never called.
	DLRThrowerRunning bool
	MOThrowerRunning  bool
	// The callback worker's effective HTTP policy. Retries and delay are taken
	// as configured: unlike the legacy .cfg path, an unset max_retries in JSON
	// really is zero retries, so reporting the documented "3" would be wrong.
	CallbackTimeoutSeconds    float64
	CallbackRetryDelaySeconds float64
	CallbackMaxRetries        int
}

const (
	// hostPlaceholder is what an example carries when the deployment never
	// declared a public hostname. It is deliberately shouty and not a valid
	// host, so a partner cannot paste it and get a confusing DNS error days
	// later; and it is a single distinct token, so the console can substitute it
	// with a plain string replace.
	hostPlaceholder = "YOUR-GATEWAY-HOST"
	// passwordPlaceholder stands in for the credential handed over out of band.
	passwordPlaceholder = "YOUR-PASSWORD"
	// callbackAck is the exact body a receiver must return; see
	// docs/api/callbacks.md and internal/core/dlr/http_thrower.go.
	callbackAck = "ACK/Jasmin"
)

// Value-filter skip literals. mtcredential keeps these unexported, so they are
// restated here; validate.go's applyFilter skips a filter whose pattern still
// equals its own skip literal. Note validity_period: the literal it is compared
// against is ".*", not that filter's own default `^\d+$` (Q: validation.py:161),
// so the default validity filter always applies.
const (
	skipAny      = ".*"
	skipPriority = "^[0-3]$"
)

type integrationEndpoint struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	Enabled  bool   `json:"enabled"`
	// Scheme and Authority build every example URL. Authority is host:port.
	Scheme                 string `json:"scheme,omitempty"`
	Authority              string `json:"authority,omitempty"`
	AuthorityIsPlaceholder bool   `json:"authority_is_placeholder"`
	// BindAddress is the raw configured listen address. Operator-facing only.
	BindAddress string `json:"bind_address,omitempty"`
	Port        int    `json:"port,omitempty"`
	TLS         bool   `json:"tls"`
	Detail      string `json:"detail,omitempty"`
}

type integrationParameter struct {
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Constraint string `json:"constraint,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type integrationExample struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Command string `json:"command"`
}

type integrationChannel struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Protocol   string `json:"protocol"`
	EndpointID string `json:"endpoint_id"`
	Available  bool   `json:"available"`
	// BlockedBy names exactly what has to change for this channel to work. It is
	// populated even when Available, if a caveat applies.
	BlockedBy  []string               `json:"blocked_by,omitempty"`
	Summary    string                 `json:"summary"`
	Parameters []integrationParameter `json:"parameters,omitempty"`
	Examples   []integrationExample   `json:"examples,omitempty"`
	Notes      []string               `json:"notes,omitempty"`
}

type integrationAuthorization struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Allowed bool   `json:"allowed"`
	// Effect is the exact text the front door returns when the account uses the
	// parameter anyway, so a partner can match on it.
	Effect string `json:"effect,omitempty"`
}

type integrationFilter struct {
	Key        string `json:"key"`
	Applies    string `json:"applies"`
	Pattern    string `json:"pattern"`
	Constrains bool   `json:"constrains"`
	Effect     string `json:"effect,omitempty"`
}

type integrationPosition struct {
	BillingMode            string   `json:"billing_mode"`
	GrantedBalance         *float64 `json:"granted_balance"`
	RemainingBalance       *float64 `json:"remaining_balance"`
	GrantedSubmitSMCount   *int     `json:"granted_submit_sm_count"`
	RemainingSubmitSMCount *int     `json:"remaining_submit_sm_count"`
	LiveError              string   `json:"live_error,omitempty"`
	HTTPThroughput         *float64 `json:"http_throughput"`
	SMPPsThroughput        *float64 `json:"smpps_throughput"`
	GroupID                string   `json:"group_id,omitempty"`
	GroupRemainingBalance  *float64 `json:"group_remaining_balance,omitempty"`
	GroupDisabled          bool     `json:"group_disabled,omitempty"`
}

type integrationCallbackLevel struct {
	Requested int    `json:"requested"`
	Delivered string `json:"delivered"`
	Detail    string `json:"detail"`
}

type integrationCallbacks struct {
	Available         bool                       `json:"available"`
	AckBody           string                     `json:"ack_body"`
	SuccessStatus     string                     `json:"success_status"`
	TimeoutSeconds    float64                    `json:"timeout_seconds"`
	RetryDelaySeconds float64                    `json:"retry_delay_seconds"`
	MaxRetries        int                        `json:"max_retries"`
	TotalAttempts     int                        `json:"total_attempts"`
	ExampleResponse   string                     `json:"example_response"`
	Levels            []integrationCallbackLevel `json:"levels"`
	DLRFields         []integrationParameter     `json:"dlr_fields"`
	MOFields          []integrationParameter     `json:"mo_fields"`
	Notes             []string                   `json:"notes,omitempty"`
}

type integrationMORoute struct {
	Order       int    `json:"order"`
	Default     bool   `json:"default"`
	FromConn    string `json:"from_connector,omitempty"`
	Destination string `json:"destination"`
}

type integrationInbound struct {
	// BindAccount reports whether an SMPPs account exists whose system_id is
	// this username. Without one the account cannot bind, whatever its MT
	// credential says.
	BindAccount   bool                 `json:"bind_account"`
	SystemID      string               `json:"system_id,omitempty"`
	ManagedBy     string               `json:"managed_by,omitempty"`
	Disabled      bool                 `json:"disabled,omitempty"`
	IPWhitelist   string               `json:"ip_whitelist,omitempty"`
	MaxBindings   *int                 `json:"max_bindings,omitempty"`
	MORoutes      []integrationMORoute `json:"mo_routes"`
	MOThrower     bool                 `json:"mo_thrower_running"`
	Notes         []string             `json:"notes,omitempty"`
	ReceiveBySMPP bool                 `json:"receive_by_smpp"`
}

type integrationGuide struct {
	Kind        string `json:"kind"`
	GeneratedAt string `json:"generated_at"`

	Username   string `json:"username"`
	ExternalID string `json:"external_id,omitempty"`
	ManagedBy  string `json:"managed_by"`
	Disabled   bool   `json:"disabled"`

	Position       integrationPosition        `json:"position"`
	Endpoints      []integrationEndpoint      `json:"endpoints"`
	Channels       []integrationChannel       `json:"channels"`
	Authorizations []integrationAuthorization `json:"authorizations"`
	Filters        []integrationFilter        `json:"filters"`

	DefaultSourceAddress *string `json:"default_source_address,omitempty"`

	Callbacks integrationCallbacks `json:"callbacks"`
	Inbound   integrationInbound   `json:"inbound"`

	// Placeholders tells the console exactly which tokens must be replaced
	// before the pack is handed to anyone.
	Placeholders map[string]string `json:"placeholders"`
	Warnings     []string          `json:"warnings,omitempty"`
}

// ingress reports the deployment's customer-facing listeners, or an empty
// snapshot when the gateway did not supply them (tests, or a runtime built
// before this existed). An empty snapshot yields disabled endpoints rather than
// invented ones.
func (h *Handler) ingress() IngressSnapshot {
	if h.deps.Ingress == nil {
		return IngressSnapshot{}
	}
	return h.deps.Ingress()
}

// resolveEndpoint turns a configured bind address into something a partner could
// dial, or reports it disabled. The host half of the bind address is never used:
// 0.0.0.0 is not an address anyone can connect to, and a loopback bind would be
// just as misleading behind a port mapping.
func resolveEndpoint(id, protocol, bindAddress, publicHost string, tls bool) integrationEndpoint {
	endpoint := integrationEndpoint{ID: id, Protocol: protocol, TLS: tls}
	bindAddress = strings.TrimSpace(bindAddress)
	if bindAddress == "" {
		return endpoint
	}
	endpoint.BindAddress = bindAddress
	_, portText, err := net.SplitHostPort(bindAddress)
	if err != nil {
		endpoint.Detail = fmt.Sprintf("the configured listen address %q is not host:port", bindAddress)
		return endpoint
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		endpoint.Detail = fmt.Sprintf("the configured listen address %q has a non-numeric port", bindAddress)
		return endpoint
	}
	host := strings.TrimSpace(publicHost)
	if host == "" {
		host = hostPlaceholder
		endpoint.AuthorityIsPlaceholder = true
	}
	endpoint.Enabled = true
	endpoint.Port = port
	endpoint.Authority = net.JoinHostPort(host, portText)
	if id == "smpps" {
		endpoint.Scheme = "smpp"
		return endpoint
	}
	endpoint.Scheme = "http"
	if tls {
		endpoint.Scheme = "https"
	}
	return endpoint
}

func (endpoint integrationEndpoint) baseURL() string {
	if !endpoint.Enabled {
		return ""
	}
	return endpoint.Scheme + "://" + endpoint.Authority
}

// effectiveCredential mirrors outbound.buildMTCredential: start from Jasmin's
// permissive default (every authorization but http_bulk) and overlay only the
// values the account actually set. Reading the stored pointers directly would
// report "not set" where the front door will read "allowed", which is the exact
// class of mistake this whole feature exists to stop.
func effectiveCredential(user userResource) *mtcredential.Credential {
	credential := mtcredential.New(true)
	for key, value := range map[string]*bool{
		mtcredential.AuthHTTPSend:                user.HTTPSend,
		mtcredential.AuthHTTPBulk:                user.HTTPBulk,
		mtcredential.AuthHTTPBalance:             user.HTTPBalance,
		mtcredential.AuthHTTPRate:                user.HTTPRate,
		mtcredential.AuthSMPPSSend:               user.SMPPSSend,
		mtcredential.AuthHTTPLongContent:         user.HTTPLongContent,
		mtcredential.AuthSetDLRLevel:             user.SetDLRLevel,
		mtcredential.AuthHTTPSetDLRMethod:        user.HTTPSetDLRMethod,
		mtcredential.AuthSetSourceAddress:        user.SetSourceAddress,
		mtcredential.AuthSetPriority:             user.SetPriority,
		mtcredential.AuthSetValidityPeriod:       user.SetValidityPeriod,
		mtcredential.AuthSetHexContent:           user.SetHexContent,
		mtcredential.AuthSetScheduleDeliveryTime: user.SetScheduleDeliveryTime,
	} {
		if value != nil {
			credential.SetAuthorization(key, *value)
		}
	}
	for key, pattern := range map[string]string{
		mtcredential.FilterDestinationAddress: user.FilterDestinationAddress,
		mtcredential.FilterSourceAddress:      user.FilterSourceAddress,
		mtcredential.FilterPriority:           user.FilterPriority,
		mtcredential.FilterValidityPeriod:     user.FilterValidityPeriod,
		mtcredential.FilterContent:            user.FilterContent,
	} {
		if pattern != "" {
			// A pattern that does not compile was refused at provisioning time;
			// a stored bad value must not take the console down.
			_ = credential.SetValueFilter(key, pattern)
		}
	}
	if user.DefaultSourceAddress != nil {
		credential.SetDefaultSourceAddress([]byte(*user.DefaultSourceAddress))
	}
	return credential
}

// authorizationCatalogue is the display order and labels for the credential
// keys. Effects are the front door's own rejection text
// (internal/transport/httpcompat/credentials.go), so a partner can match the
// string they will actually receive.
var authorizationCatalogue = []struct {
	key    string
	label  string
	effect string
}{
	{mtcredential.AuthHTTPSend, "Send over HTTP", `Authorization failed for user [%s] (Cannot send MT messages).`},
	{mtcredential.AuthSMPPSSend, "Send over SMPP", "submit_sm is refused on the bind"},
	{mtcredential.AuthHTTPBalance, "Query balance", `Authorization failed for user [%s] (Cannot check balance).`},
	{mtcredential.AuthHTTPRate, "Query rate", `Authorization failed for user [%s] (Cannot check rate).`},
	{mtcredential.AuthHTTPLongContent, "Send multi-part content", `Authorization failed for user [%s] (Long content not authorized).`},
	{mtcredential.AuthSetSourceAddress, "Set the sender (from)", `Authorization failed for user [%s] (Setting source address not authorized).`},
	{mtcredential.AuthSetDLRLevel, "Choose the receipt level (dlr-level)", `Authorization failed for user [%s] (Setting dlr level not authorized).`},
	{mtcredential.AuthHTTPSetDLRMethod, "Choose the receipt method (dlr-method)", `Authorization failed for user [%s] (Setting dlr method not authorized).`},
	{mtcredential.AuthSetPriority, "Set priority", `Authorization failed for user [%s] (Setting priority not authorized).`},
	{mtcredential.AuthSetValidityPeriod, "Set validity-period", `Authorization failed for user [%s] (Setting validity period not authorized).`},
	{mtcredential.AuthSetHexContent, "Send hex-content", `Authorization failed for user [%s] (Setting hex content not authorized).`},
	{mtcredential.AuthSetScheduleDeliveryTime, "Schedule delivery (sdt)", `Authorization failed for user [%s] (Setting schedule delivery time not authorized).`},
	{mtcredential.AuthHTTPBulk, "Bulk flag (http_bulk)", "no check consults this flag today"},
}

func buildAuthorizations(credential *mtcredential.Credential, username string) []integrationAuthorization {
	rows := make([]integrationAuthorization, 0, len(authorizationCatalogue))
	for _, entry := range authorizationCatalogue {
		row := integrationAuthorization{
			Key:     entry.key,
			Label:   entry.label,
			Allowed: credential.Authorization(entry.key),
		}
		if !row.Allowed {
			if strings.Contains(entry.effect, "%s") {
				row.Effect = fmt.Sprintf(entry.effect, username)
			} else {
				row.Effect = entry.effect
			}
		}
		if entry.key == mtcredential.AuthHTTPBulk {
			// Reported honestly rather than omitted: the flag is stored, shown
			// on the user form and rendered by jCli, and nothing enforces it.
			row.Effect = "no check consults this flag today; batch access follows http_send and http_balance"
		}
		rows = append(rows, row)
	}
	return rows
}

var filterCatalogue = []struct {
	key     string
	applies string
	skip    string
}{
	{mtcredential.FilterDestinationAddress, "to", skipAny},
	{mtcredential.FilterSourceAddress, "from", skipAny},
	{mtcredential.FilterPriority, "priority", skipPriority},
	{mtcredential.FilterValidityPeriod, "validity-period", skipAny},
	{mtcredential.FilterContent, "content", skipAny},
}

func buildFilters(credential *mtcredential.Credential, username string) []integrationFilter {
	rows := make([]integrationFilter, 0, len(filterCatalogue))
	for _, entry := range filterCatalogue {
		pattern := credential.ValueFilterPattern(entry.key)
		row := integrationFilter{
			Key:        entry.key,
			Applies:    entry.applies,
			Pattern:    pattern,
			Constrains: pattern != entry.skip,
		}
		if row.Constrains {
			row.Effect = fmt.Sprintf("Value filter failed for user [%s] (%s filter mismatch).",
				username, entry.key)
		}
		rows = append(rows, row)
	}
	return rows
}

// literalDigitPrefix recovers the leading fixed digits of a value filter so an
// example destination satisfies the account's own filter. A digit followed by a
// quantifier is not fixed and ends the prefix.
func literalDigitPrefix(pattern string) string {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "^")
	var prefix strings.Builder
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		if character < '0' || character > '9' {
			break
		}
		if index+1 < len(pattern) {
			switch pattern[index+1] {
			case '*', '+', '?', '{':
				return prefix.String()
			}
		}
		prefix.WriteByte(character)
	}
	return prefix.String()
}

// destinationExample picks a destination the account's own filter admits. When
// the pattern has no recoverable literal prefix it returns no number at all and
// says why: a fabricated example that the account's filter rejects is worse than
// an explicit instruction to substitute one.
func destinationExample(filters []integrationFilter) (string, string) {
	for _, filter := range filters {
		if filter.Key != mtcredential.FilterDestinationAddress {
			continue
		}
		if !filter.Constrains {
			break
		}
		prefix := literalDigitPrefix(filter.Pattern)
		if prefix == "" {
			return "", fmt.Sprintf(
				"substitute a destination matching this account's filter %s (matched from the start of the value)",
				filter.Pattern)
		}
		number := prefix
		for len(number) < 11 {
			number += "0"
		}
		return number, ""
	}
	return "15551234567", ""
}

func (h *Handler) userIntegrationGuide(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	user, ok, err := h.integrationUser(r, username)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("gateway user %q not found", username))
		return
	}

	snapshot := h.ingress()
	credential := effectiveCredential(user)
	filters := buildFilters(credential, user.Username)
	destination, destinationNote := destinationExample(filters)

	httpEndpoint := resolveEndpoint("http", "HTTP", snapshot.HTTPBindAddress, snapshot.PublicHostname, snapshot.HTTPTLS)
	restEndpoint := resolveEndpoint("rest", "HTTP (JSON)", snapshot.RESTBindAddress, snapshot.PublicHostname, snapshot.HTTPTLS)
	smppEndpoint := resolveEndpoint("smpps", "SMPP 3.4", snapshot.SMPPSBindAddress, snapshot.PublicHostname, snapshot.SMPPSTLS)
	if restEndpoint.Enabled {
		restEndpoint.Detail = "the historical standalone JSON listener; /secure/* is also served on the HTTP front door"
	} else {
		restEndpoint.Detail = "no standalone JSON listener is configured; use /secure/* on the HTTP front door"
	}

	guide := integrationGuide{
		Kind:           "account",
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Username:       user.Username,
		ExternalID:     user.ExternalID,
		ManagedBy:      user.ManagedBy,
		Disabled:       user.Disabled,
		Endpoints:      []integrationEndpoint{httpEndpoint, restEndpoint, smppEndpoint},
		Authorizations: buildAuthorizations(credential, user.Username),
		Filters:        filters,
		Placeholders: map[string]string{
			hostPlaceholder:     "the host partners use to reach this gateway; include the published port if it differs from the bind port",
			passwordPlaceholder: "the password handed over at onboarding; it is stored one-way and cannot be shown again",
		},
	}
	if user.DefaultSourceAddress != nil {
		guide.DefaultSourceAddress = user.DefaultSourceAddress
	}

	guide.Position = h.integrationPosition(r, user)
	bindAccount, hasBind := h.integrationBindAccount(r, user.Username)
	guide.Channels = buildChannels(channelInput{
		user:            user,
		credential:      credential,
		filters:         filters,
		destination:     destination,
		destinationNote: destinationNote,
		httpEndpoint:    httpEndpoint,
		restEndpoint:    restEndpoint,
		smppEndpoint:    smppEndpoint,
		hasBindAccount:  hasBind,
		bindAccount:     bindAccount,
		snapshot:        snapshot,
	})
	// Every token an example can contain has to be declared, or the reader is
	// left to guess which angle brackets are literal.
	if credential.Authorization(mtcredential.AuthSetSourceAddress) {
		guide.Placeholders["<SENDER>"] = "the sender this account will present; it is optional and may be omitted"
	}
	if destination == "" {
		guide.Placeholders["<DESTINATION>"] = destinationNote
	}
	guide.Callbacks = buildCallbacks(snapshot)
	guide.Inbound = h.buildInbound(r, user, bindAccount, hasBind, smppEndpoint, snapshot)
	guide.Warnings = buildWarnings(user, guide, credential, hasBind, bindAccount, smppEndpoint, snapshot)

	writeJSON(w, http.StatusOK, guide)
}

// integrationUser resolves a username across both ownerships, exactly as the
// user and billing lists do, so the button is present on every row they render.
func (h *Handler) integrationUser(r *http.Request, username string) (userResource, bool, error) {
	stored, err := h.deps.Users.GetUser(r.Context(), username)
	if err == nil {
		resource, convertErr := toUserResource(stored)
		if convertErr != nil {
			return userResource{}, false, convertErr
		}
		return resource, true, nil
	}
	if resource, ok := h.configUser(username); ok {
		return resource, true, nil
	}
	return userResource{}, false, nil
}

func (h *Handler) integrationPosition(r *http.Request, user userResource) integrationPosition {
	position := integrationPosition{
		BillingMode:          billingModeFor(user.Balance, user.EarlyDecrementBalancePercent),
		GrantedBalance:       user.Balance,
		GrantedSubmitSMCount: user.SubmitSMCount,
		HTTPThroughput:       user.HTTPThroughput,
		SMPPsThroughput:      user.SMPPSThroughput,
		GroupID:              user.GroupID,
	}
	if live, err := h.liveQuota(r.Context(), user.Username); err != nil {
		// Blank, never zero: "cannot read" and "no money" call for opposite
		// actions, and this document is read by someone outside the company.
		position.LiveError = err.Error()
	} else {
		position.RemainingBalance = live.Balance
		position.RemainingSubmitSMCount = live.SubmitSMCount
	}
	if user.GroupID != "" && h.deps.GroupQuota != nil {
		if quota, found := h.deps.GroupQuota(user.GroupID); found {
			position.GroupRemainingBalance = quota.Balance
		}
	}
	if user.GroupID != "" {
		if groups, err := h.collectGroups(r.Context()); err == nil {
			if group, ok := groups[user.GroupID]; ok {
				position.GroupDisabled = group.Disabled
			}
		}
	}
	return position
}

// integrationBindAccount finds the SMPPs account whose system_id is this
// username. Legacy serves both protocols from one identity, and the console
// mirrors a user's SMPPs credential into a bind account of the same name, so
// this is the account's bind identity — not a guess.
func (h *Handler) integrationBindAccount(r *http.Request, username string) (smppsUserResource, bool) {
	if stored, err := h.deps.SMPPsUsers.GetUser(r.Context(), username); err == nil {
		if resource, convertErr := toSMPPsUserResource(stored); convertErr == nil {
			return resource, true
		}
	}
	if resource, ok := h.configSMPPsUser(username); ok {
		return resource, true
	}
	return smppsUserResource{}, false
}

type channelInput struct {
	user            userResource
	credential      *mtcredential.Credential
	filters         []integrationFilter
	destination     string
	destinationNote string
	httpEndpoint    integrationEndpoint
	restEndpoint    integrationEndpoint
	smppEndpoint    integrationEndpoint
	hasBindAccount  bool
	bindAccount     smppsUserResource
	snapshot        IngressSnapshot
}

func buildChannels(in channelInput) []integrationChannel {
	return []integrationChannel{
		httpSendChannel(in),
		restSendChannel(in),
		restBatchChannel(in),
		smppBindChannel(in),
		accountQueryChannel(in),
	}
}

// exampleDestinationValue renders the destination for a shell example; when no
// filter-satisfying number could be derived it emits a bracketed placeholder
// rather than a number the account's own filter would reject.
func (in channelInput) exampleDestinationValue() string {
	if in.destination != "" {
		return in.destination
	}
	return "<DESTINATION>"
}

func httpSendChannel(in channelInput) integrationChannel {
	channel := integrationChannel{
		ID:         "http_send",
		Title:      "Send one message over HTTP",
		Protocol:   "HTTP",
		EndpointID: "http",
		Summary:    "GET or POST /send with form, query or JSON parameters. A success is HTTP 200 with the exact body Success \"<message-id>\".",
	}
	if !in.httpEndpoint.Enabled {
		channel.BlockedBy = append(channel.BlockedBy, "this deployment publishes no HTTP front door")
	}
	if !in.credential.Authorization(mtcredential.AuthHTTPSend) {
		channel.BlockedBy = append(channel.BlockedBy, "this account's http_send authorization is denied")
	}
	if in.user.Disabled {
		channel.BlockedBy = append(channel.BlockedBy, "this account is disabled")
	}
	channel.Available = len(channel.BlockedBy) == 0

	channel.Parameters = []integrationParameter{
		{Name: "username", Required: true, Constraint: "1-16 characters", Detail: in.user.Username},
		{Name: "password", Required: true, Constraint: "1-16 characters", Detail: "handed over at onboarding"},
		{Name: "to", Required: true, Constraint: `optional leading +, then digits`, Detail: destinationDetail(in.filters)},
		{Name: "content", Required: true, Constraint: "one of content or hex-content must be present", Detail: "text; with coding 0 it is converted to GSM 03.38 and unencodable characters are replaced"},
	}
	optional := []struct {
		name       string
		auth       string
		constraint string
		detail     string
	}{
		{"from", mtcredential.AuthSetSourceAddress, "no syntax check at this boundary", "omit it to receive the account's default sender; sending from= empty still counts as setting it"},
		{"coding", "", "0-10, 13, 14", "becomes the SMPP data_coding and selects the segmentation limit"},
		{"dlr", "", "yes or no", "dlr=yes with a dlr-url requests a delivery receipt"},
		{"dlr-url", "", "must begin http:// or https://", "the callback endpoint; see the delivery receipts section"},
		{"dlr-level", mtcredential.AuthSetDLRLevel, "1, 2 or 3", "1 submit response, 2 carrier receipt, 3 both"},
		{"dlr-method", mtcredential.AuthHTTPSetDLRMethod, "GET or POST", "defaults to POST"},
		{"priority", mtcredential.AuthSetPriority, "0-3", "0 is the default"},
		{"validity-period", mtcredential.AuthSetValidityPeriod, "decimal minutes", "the default validity filter always applies, even when it was never changed"},
		{"hex-content", mtcredential.AuthSetHexContent, "hexadecimal", "mutually exclusive with content"},
		{"sdt", mtcredential.AuthSetScheduleDeliveryTime, "YYMMDDhhmmsstnnp, 16 characters", "absolute with + or -, relative with R"},
		{"tags", "", "letters, digits, dashes, commas", "routing tags"},
	}
	for _, entry := range optional {
		if entry.auth != "" && !in.credential.Authorization(entry.auth) {
			// Omitted deliberately: listing a parameter this account cannot set
			// is how a customer discovers an authorization as a 400.
			continue
		}
		channel.Parameters = append(channel.Parameters, integrationParameter{
			Name: entry.name, Constraint: entry.constraint, Detail: entry.detail,
		})
	}

	base := in.httpEndpoint.baseURL()
	if base == "" {
		base = "http://" + hostPlaceholder
	}
	lines := []string{
		"curl --fail-with-body \\",
		"  --data-urlencode 'username=" + in.user.Username + "' \\",
		"  --data-urlencode 'password=" + passwordPlaceholder + "' \\",
		"  --data-urlencode 'to=" + in.exampleDestinationValue() + "' \\",
		"  --data-urlencode 'content=Hello from " + in.user.Username + "' \\",
	}
	if in.credential.Authorization(mtcredential.AuthSetSourceAddress) {
		lines = append(lines, "  --data-urlencode 'from=<SENDER>' \\")
	}
	lines = append(lines, "  "+base+"/send")
	channel.Examples = append(channel.Examples, integrationExample{
		ID: "http_send_form", Title: "Send a message", Command: strings.Join(lines, "\n"),
	})

	receipt := []string{
		"curl --fail-with-body \\",
		"  --data-urlencode 'username=" + in.user.Username + "' \\",
		"  --data-urlencode 'password=" + passwordPlaceholder + "' \\",
		"  --data-urlencode 'to=" + in.exampleDestinationValue() + "' \\",
		"  --data-urlencode 'content=Hello from " + in.user.Username + "' \\",
		"  --data-urlencode 'dlr=yes' \\",
		"  --data-urlencode 'dlr-url=https://your-app.example/dlr' \\",
	}
	if in.credential.Authorization(mtcredential.AuthSetDLRLevel) {
		receipt = append(receipt, "  --data-urlencode 'dlr-level=3' \\")
	}
	if in.credential.Authorization(mtcredential.AuthHTTPSetDLRMethod) {
		receipt = append(receipt, "  --data-urlencode 'dlr-method=POST' \\")
	}
	receipt = append(receipt, "  "+base+"/send")
	channel.Examples = append(channel.Examples, integrationExample{
		ID: "http_send_dlr", Title: "Send and ask for a delivery receipt", Command: strings.Join(receipt, "\n"),
	})

	channel.Notes = []string{
		`Errors are text/plain, not JSON: the body is Error "<message>" with no trailing newline. Keep the HTTP status.`,
		"A 200 means the message was durably admitted, not that a handset received it. Use a receipt callback for carrier state.",
	}
	if !in.credential.Authorization(mtcredential.AuthHTTPLongContent) {
		channel.Notes = append(channel.Notes,
			"Multi-part content is not authorized for this account: content longer than one part is refused.")
	}
	if in.destinationNote != "" {
		channel.Notes = append(channel.Notes, in.destinationNote)
	}
	if in.user.HTTPThroughput != nil && *in.user.HTTPThroughput > 0 {
		channel.Notes = append(channel.Notes, fmt.Sprintf(
			`Throughput ceiling %g submits/second. It is minimum spacing, not a bucket: there is no burst allowance, and an exceeded request is HTTP 403 with body Error "User throughput exceeded".`,
			*in.user.HTTPThroughput))
	}
	return channel
}

func destinationDetail(filters []integrationFilter) string {
	for _, filter := range filters {
		if filter.Key == mtcredential.FilterDestinationAddress && filter.Constrains {
			return "must match " + filter.Pattern + " from the start of the value"
		}
	}
	return "any destination"
}

func restSendChannel(in channelInput) integrationChannel {
	channel := integrationChannel{
		ID:         "rest_send",
		Title:      "Send one message over the JSON API",
		Protocol:   "HTTP (JSON)",
		EndpointID: "http",
		Summary:    "POST /secure/send with HTTP Basic authentication. Underscores are accepted for the hyphenated parameter names.",
	}
	if !in.httpEndpoint.Enabled {
		channel.BlockedBy = append(channel.BlockedBy, "this deployment publishes no HTTP front door")
	}
	if !in.credential.Authorization(mtcredential.AuthHTTPSend) {
		channel.BlockedBy = append(channel.BlockedBy, "this account's http_send authorization is denied")
	}
	if in.user.Disabled {
		channel.BlockedBy = append(channel.BlockedBy, "this account is disabled")
	}
	channel.Available = len(channel.BlockedBy) == 0

	base := in.httpEndpoint.baseURL()
	if base == "" {
		base = "http://" + hostPlaceholder
	}
	body := `{"to":"` + in.exampleDestinationValue() + `","content":"Hello from ` + in.user.Username + `"`
	if in.credential.Authorization(mtcredential.AuthSetDLRLevel) {
		body += `,"dlr_level":3,"dlr_url":"https://your-app.example/dlr"`
	} else {
		body += `,"dlr":"yes","dlr_url":"https://your-app.example/dlr"`
	}
	body += `}`
	channel.Examples = append(channel.Examples, integrationExample{
		ID:    "rest_send",
		Title: "Send a message as JSON",
		Command: strings.Join([]string{
			"curl --fail-with-body \\",
			"  --user '" + in.user.Username + ":" + passwordPlaceholder + "' \\",
			"  --header 'Accept: application/json' \\",
			"  --header 'Content-Type: application/json' \\",
			"  --data '" + body + "' \\",
			"  " + base + "/secure/send",
		}, "\n"),
	})
	channel.Notes = []string{
		"A success is {\"data\":\"Success \\\"<message-id>\\\"\"}; the underlying plain-text response is wrapped, not replaced.",
		"An error from the send path keeps its HTTP status and arrives as {\"message\":\"Error \\\"…\\\"\"}.",
		"Credentials in the JSON body are ignored: Basic auth is authoritative.",
	}
	if in.restEndpoint.Enabled {
		channel.Notes = append(channel.Notes,
			"A standalone JSON listener also exists at "+in.restEndpoint.Authority+"; it serves the same resources plus a JSON /ping.")
	}
	return channel
}

func restBatchChannel(in channelInput) integrationChannel {
	channel := integrationChannel{
		ID:         "rest_sendbatch",
		Title:      "Send a batch over the JSON API",
		Protocol:   "HTTP (JSON)",
		EndpointID: "http",
		Summary:    "POST /secure/sendbatch accepts many messages, dispatches them asynchronously and answers with a batch id.",
	}
	if !in.httpEndpoint.Enabled {
		channel.BlockedBy = append(channel.BlockedBy, "this deployment publishes no HTTP front door")
	}
	if !in.credential.Authorization(mtcredential.AuthHTTPSend) {
		channel.BlockedBy = append(channel.BlockedBy, "this account's http_send authorization is denied")
	}
	if !in.credential.Authorization(mtcredential.AuthHTTPBalance) {
		// Non-obvious and load-bearing: a batch is authenticated by calling
		// /balance internally, which is credential-gated, so http_balance=false
		// fails every batch with a bare "Authentication failed for user".
		channel.BlockedBy = append(channel.BlockedBy,
			"this account's http_balance authorization is denied, and a batch is authenticated through /balance")
	}
	if in.user.Disabled {
		channel.BlockedBy = append(channel.BlockedBy, "this account is disabled")
	}
	channel.Available = len(channel.BlockedBy) == 0

	base := in.httpEndpoint.baseURL()
	if base == "" {
		base = "http://" + hostPlaceholder
	}
	channel.Examples = append(channel.Examples, integrationExample{
		ID:    "rest_sendbatch",
		Title: "Send two messages in one batch",
		Command: strings.Join([]string{
			"curl --fail-with-body \\",
			"  --user '" + in.user.Username + ":" + passwordPlaceholder + "' \\",
			"  --header 'Accept: application/json' \\",
			"  --header 'Content-Type: application/json' \\",
			`  --data '{"globals":{"content":"Hello from ` + in.user.Username + `"},` +
				`"messages":[{"to":"` + in.exampleDestinationValue() + `"},{"to":"` + in.exampleDestinationValue() + `"}]}' \`,
			"  " + base + "/secure/sendbatch",
		}, "\n"),
	})
	channel.Notes = []string{
		"globals apply to every message; a per-message value overrides them.",
		"The response acknowledges admission of the batch, not delivery of its messages. Supply batch_config.callback_url and errback_url to learn per-message outcomes.",
		"The batch backlog is bounded; a full queue answers HTTP 429.",
	}
	return channel
}

func smppBindChannel(in channelInput) integrationChannel {
	channel := integrationChannel{
		ID:         "smpp_bind",
		Title:      "Bind as an ESME over SMPP 3.4",
		Protocol:   "SMPP 3.4",
		EndpointID: "smpps",
		Summary:    "Open a TCP session, bind, and submit_sm. Receipts and inbound messages arrive as deliver_sm on a receiver or transceiver bind.",
	}
	if !in.smppEndpoint.Enabled {
		channel.BlockedBy = append(channel.BlockedBy, "this deployment runs no customer-facing SMPP server")
	}
	if !in.hasBindAccount {
		channel.BlockedBy = append(channel.BlockedBy,
			"no SMPP bind account exists with system_id "+in.user.Username)
	}
	if in.hasBindAccount && in.bindAccount.Disabled {
		channel.BlockedBy = append(channel.BlockedBy, "the SMPP bind account is disabled")
	}
	if in.hasBindAccount && in.bindAccount.Bind != nil && !*in.bindAccount.Bind {
		channel.BlockedBy = append(channel.BlockedBy, "the SMPP bind account's bind authorization is denied")
	}
	if !in.credential.Authorization(mtcredential.AuthSMPPSSend) {
		channel.BlockedBy = append(channel.BlockedBy,
			"this account's smpps_send authorization is denied, so a bind would succeed but every submit_sm would be refused")
	}
	if in.user.Disabled {
		channel.BlockedBy = append(channel.BlockedBy, "this account is disabled")
	}
	channel.Available = len(channel.BlockedBy) == 0

	host, port := hostPlaceholder, ""
	if in.smppEndpoint.Enabled {
		if splitHost, splitPort, err := net.SplitHostPort(in.smppEndpoint.Authority); err == nil {
			host, port = splitHost, splitPort
		}
	}
	channel.Parameters = []integrationParameter{
		{Name: "host", Required: true, Detail: host},
		{Name: "port", Required: true, Detail: port},
		{Name: "system_id", Required: true, Detail: in.user.Username},
		{Name: "password", Required: true, Constraint: "at most 8 characters", Detail: "handed over at onboarding; SMPP 3.4 caps the field at 8 octets plus a NUL"},
		{Name: "bind type", Required: true, Constraint: "transmitter, receiver or transceiver", Detail: "transmitter cannot receive deliver_sm; receiver cannot submit"},
		{Name: "TLS", Detail: boolText(in.smppEndpoint.TLS, "required", "not used on this listener")},
	}
	if in.snapshot.SMPPSEnquireLinkTimeout > 0 {
		channel.Parameters = append(channel.Parameters, integrationParameter{
			Name: "enquire_link", Detail: fmt.Sprintf("the server sends a keepalive after %g seconds of quiet", in.snapshot.SMPPSEnquireLinkTimeout),
		})
	}
	if in.snapshot.SMPPSInactivityTimeout > 0 {
		channel.Parameters = append(channel.Parameters, integrationParameter{
			Name: "inactivity", Detail: fmt.Sprintf("a session receiving nothing for %g seconds is dropped", in.snapshot.SMPPSInactivityTimeout),
		})
	}
	if in.hasBindAccount {
		if in.bindAccount.MaxBindings != nil {
			channel.Parameters = append(channel.Parameters, integrationParameter{
				Name: "max_bindings", Detail: fmt.Sprintf("%d concurrent sessions in total across RX, TX and TRX", *in.bindAccount.MaxBindings),
			})
		}
		if whitelist := strings.TrimSpace(in.bindAccount.IPWhitelist); whitelist != "" {
			channel.Parameters = append(channel.Parameters, integrationParameter{
				Name: "permitted source IPs", Detail: whitelist,
			})
		}
	}

	settings := []string{
		"host        " + host,
		"port        " + port,
		"system_id   " + in.user.Username,
		"password    " + passwordPlaceholder,
		"bind type   transceiver",
	}
	channel.Examples = append(channel.Examples, integrationExample{
		ID: "smpp_settings", Title: "Bind settings", Command: strings.Join(settings, "\n"),
	})

	channel.Notes = []string{
		"Ask for a receipt with registered_delivery on submit_sm; receipts arrive as deliver_sm on a receiver or transceiver bind and must be answered with deliver_sm_resp.",
		"Long messages are not split for you: send each concatenated segment as its own submit_sm, with UDH or the complete SAR triple.",
		"data_sm is refused deliberately. Use submit_sm with message_payload, SAR or UDH.",
		"The submitted short_message bytes and data_coding are preserved exactly; make them agree with each other.",
	}
	if in.hasBindAccount && strings.TrimSpace(in.bindAccount.IPWhitelist) == "" {
		channel.Notes = append(channel.Notes,
			"No IP whitelist is set on this bind account, so the effective value admits any IPv4 address. Send us the source addresses you will bind from.")
	}
	if in.user.SMPPSThroughput != nil && *in.user.SMPPSThroughput > 0 {
		channel.Notes = append(channel.Notes, fmt.Sprintf(
			"Throughput ceiling %g submits/second on this bind.", *in.user.SMPPSThroughput))
	}
	return channel
}

func accountQueryChannel(in channelInput) integrationChannel {
	channel := integrationChannel{
		ID:         "account_query",
		Title:      "Check the account balance and price",
		Protocol:   "HTTP",
		EndpointID: "http",
		Summary:    "/balance reports what is left; /rate quotes the price of the route a destination would take.",
	}
	if !in.httpEndpoint.Enabled {
		channel.BlockedBy = append(channel.BlockedBy, "this deployment publishes no HTTP front door")
	}
	if !in.credential.Authorization(mtcredential.AuthHTTPBalance) {
		channel.BlockedBy = append(channel.BlockedBy, "this account's http_balance authorization is denied")
	}
	if !in.credential.Authorization(mtcredential.AuthHTTPRate) {
		channel.BlockedBy = append(channel.BlockedBy, "this account's http_rate authorization is denied")
	}
	channel.Available = len(channel.BlockedBy) == 0

	base := in.httpEndpoint.baseURL()
	if base == "" {
		base = "http://" + hostPlaceholder
	}
	if in.credential.Authorization(mtcredential.AuthHTTPBalance) {
		channel.Examples = append(channel.Examples, integrationExample{
			ID:      "http_balance",
			Title:   "Read the remaining balance",
			Command: "curl --fail-with-body '" + base + "/balance?username=" + in.user.Username + "&password=" + passwordPlaceholder + "'",
		})
	}
	if in.credential.Authorization(mtcredential.AuthHTTPRate) {
		channel.Examples = append(channel.Examples, integrationExample{
			ID:    "http_rate",
			Title: "Quote the price for a destination",
			Command: "curl --fail-with-body '" + base + "/rate?username=" + in.user.Username +
				"&password=" + passwordPlaceholder + "&to=" + in.exampleDestinationValue() + "'",
		})
	}
	channel.Notes = []string{
		`An unlimited balance or message quota is reported as the literal string ND.`,
		`/rate always answers submit_sm_count 1: it prices one part, because it is given no content to segment.`,
		"Errors on these two endpoints are JSON strings, not objects.",
	}
	return channel
}

func boolText(value bool, whenTrue, whenFalse string) string {
	if value {
		return whenTrue
	}
	return whenFalse
}

func buildCallbacks(snapshot IngressSnapshot) integrationCallbacks {
	callbacks := integrationCallbacks{
		Available:         snapshot.DLRThrowerRunning,
		AckBody:           callbackAck,
		SuccessStatus:     "2xx",
		TimeoutSeconds:    snapshot.CallbackTimeoutSeconds,
		RetryDelaySeconds: snapshot.CallbackRetryDelaySeconds,
		MaxRetries:        snapshot.CallbackMaxRetries,
		TotalAttempts:     snapshot.CallbackMaxRetries + 1,
		ExampleResponse: strings.Join([]string{
			"HTTP/1.1 200 OK",
			"Content-Type: text/plain",
			"Content-Length: 10",
			"",
			callbackAck,
		}, "\n"),
		Levels: []integrationCallbackLevel{
			{Requested: 1, Delivered: "level=1", Detail: "one callback carrying the submit response"},
			{Requested: 2, Delivered: "level=2", Detail: "one callback carrying the carrier receipt; a failed submit produces no callback at all"},
			{Requested: 3, Delivered: "level=1 then level=2", Detail: "the submit response, then the carrier receipt once correlation succeeds"},
		},
		DLRFields: []integrationParameter{
			{Name: "id", Required: true, Detail: "the message id returned by /send"},
			{Name: "level", Required: true, Detail: "1 for the submit response, 2 for the carrier receipt"},
			{Name: "message_status", Required: true, Detail: "ESME_ROK style for level 1, DELIVRD style for level 2"},
			{Name: "connector", Required: true, Detail: "level 1 the routed connector id; level 2 the raw receipt id, an inherited quirk"},
			{Name: "id_smsc", Detail: "level 2 only: the coded SMSC receipt id used for correlation"},
			{Name: "sub, dlvrd, subdate, donedate, err, text", Detail: "level 2 only: forwarded receipt text, ND when absent"},
		},
		MOFields: []integrationParameter{
			{Name: "id", Required: true, Detail: "gateway message id for this inbound delivery"},
			{Name: "from", Required: true, Detail: "SMPP source_addr"},
			{Name: "to", Required: true, Detail: "SMPP destination_addr"},
			{Name: "origin-connector", Required: true, Detail: "the SMSC connector it arrived on"},
			{Name: "content", Required: true, Detail: "the selected raw message bytes as a form string"},
			{Name: "binary", Required: true, Detail: "the same bytes as lowercase hex; use this for lossless handling"},
			{Name: "coding", Required: true, Detail: "the raw one-byte data_coding, not decimal text: coding 8 is form-encoded as %08"},
		},
		Notes: []string{
			"A 2xx with an empty body, OK, JSON, or different capitalization is a failure and is retried.",
			"For a GET connector the fields are added to the URL query and existing query fields are preserved; otherwise the body is application/x-www-form-urlencoded.",
			"No authentication or signature header is added. Use an unguessable HTTPS URL, network controls, or your own scheme.",
			"HTTP 404 is not special: it retries like any other failure and is then purged.",
			"Make processing idempotent by id plus level: a callback is re-sent whenever no valid acknowledgement was observed.",
		},
	}
	if !snapshot.DLRThrowerRunning {
		callbacks.Notes = append([]string{
			"This deployment does not run the delivery-receipt worker, so a dlr-url is accepted and never called.",
		}, callbacks.Notes...)
	}
	return callbacks
}

func (h *Handler) buildInbound(
	r *http.Request,
	user userResource,
	bindAccount smppsUserResource,
	hasBind bool,
	smppEndpoint integrationEndpoint,
	snapshot IngressSnapshot,
) integrationInbound {
	inbound := integrationInbound{
		BindAccount:   hasBind,
		MOThrower:     snapshot.MOThrowerRunning,
		MORoutes:      []integrationMORoute{},
		ReceiveBySMPP: hasBind && smppEndpoint.Enabled,
	}
	if hasBind {
		inbound.SystemID = bindAccount.SystemID
		inbound.ManagedBy = bindAccount.ManagedBy
		inbound.Disabled = bindAccount.Disabled
		inbound.IPWhitelist = bindAccount.IPWhitelist
		inbound.MaxBindings = bindAccount.MaxBindings
	}
	for _, route := range h.integrationMORoutes(r) {
		if route.Connector.Type != "smpps" || route.Connector.SystemID != user.Username {
			continue
		}
		inbound.MORoutes = append(inbound.MORoutes, integrationMORoute{
			Order:       route.Order,
			Default:     route.Default,
			FromConn:    route.FilterConnectorID,
			Destination: "SMPP bind " + route.Connector.SystemID,
		})
	}
	sort.Slice(inbound.MORoutes, func(i, j int) bool {
		return inbound.MORoutes[i].Order > inbound.MORoutes[j].Order
	})
	switch {
	case len(inbound.MORoutes) > 0:
		inbound.Notes = append(inbound.Notes,
			"Inbound messages are routed to this account's SMPP bind. Keep a receiver or transceiver session open to receive them.")
	case hasBind:
		inbound.Notes = append(inbound.Notes,
			"No MO route currently delivers inbound messages to this account's SMPP bind. Delivery receipts for its own submissions still arrive.")
	default:
		inbound.Notes = append(inbound.Notes,
			"Inbound messages can also be delivered to an HTTP endpoint through an MO route; that is configured per deployment rather than per account.")
	}
	if !snapshot.MOThrowerRunning {
		inbound.Notes = append(inbound.Notes,
			"This deployment does not run the inbound-message worker, so HTTP MO callbacks are not delivered.")
	}
	return inbound
}

// integrationMORoutes merges config-owned and admin-owned MO routes. A failure
// to read the admin store yields the config half rather than an error: an
// incomplete inbound section is better than refusing the whole pack.
func (h *Handler) integrationMORoutes(r *http.Request) []modispatch.RouteConfig {
	routes := []modispatch.RouteConfig{}
	if h.deps.ConfigMORoutes != nil {
		routes = append(routes, h.deps.ConfigMORoutes()...)
	}
	stored, err := h.deps.MORoutes.ListRoutes(r.Context())
	if err != nil {
		return routes
	}
	for _, entry := range stored {
		resource, convertErr := toMORouteResource(entry)
		if convertErr != nil {
			continue
		}
		routes = append(routes, resource.RouteConfig)
	}
	return routes
}

func buildWarnings(
	user userResource,
	guide integrationGuide,
	credential *mtcredential.Credential,
	hasBind bool,
	bindAccount smppsUserResource,
	smppEndpoint integrationEndpoint,
	snapshot IngressSnapshot,
) []string {
	var warnings []string
	if strings.TrimSpace(snapshot.PublicHostname) == "" {
		warnings = append(warnings,
			"This deployment has not declared a public hostname, so every address below is a placeholder. Substitute the host and published port partners actually reach before sending this out.")
	}
	if user.Disabled {
		warnings = append(warnings, "This account is disabled: every authentication attempt fails, on every ingress.")
	}
	if guide.Position.GroupDisabled {
		warnings = append(warnings, "This account's group is disabled, which fails authentication exactly like a disabled account.")
	}
	if remaining := guide.Position.RemainingBalance; remaining != nil && *remaining <= 0 {
		warnings = append(warnings, "The remaining balance is spent: the next message is refused.")
	}
	if remaining := guide.Position.RemainingSubmitSMCount; remaining != nil && *remaining <= 0 {
		warnings = append(warnings, "The remaining message quota is spent: the next message is refused.")
	}
	if !credential.Authorization(mtcredential.AuthHTTPSend) {
		warnings = append(warnings, "http_send is denied, so this account cannot send over HTTP at all.")
	}
	if smppEndpoint.Enabled && !hasBind {
		warnings = append(warnings,
			"This deployment runs an SMPP server, but no bind account exists with system_id "+user.Username+", so this account cannot bind.")
	}
	if hasBind && strings.TrimSpace(bindAccount.IPWhitelist) == "" {
		warnings = append(warnings,
			"The SMPP bind account has no IP whitelist, so its effective value admits any IPv4 address. An eight-character bind password is not a network perimeter.")
	}
	if !snapshot.DLRThrowerRunning {
		warnings = append(warnings,
			"No delivery-receipt worker is running: a dlr-url is accepted and never called.")
	}
	return warnings
}

// -----------------------------------------------------------------------------
// Connector brief: the opposite direction.
//
// A connector is us dialling out to a carrier, so "how to connect to us" does not
// apply. What is missing there is the agreement: smppc.Config carries about
// fifteen wire-affecting fields and onboarding sets none of them, so every unset
// one silently becomes zero. This brief states what we will send and lists what
// has to be requested from the carrier, with the blanks left blank rather than
// guessed.

type integrationSetting struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	IsDefault bool   `json:"is_default"`
	Detail    string `json:"detail,omitempty"`
}

type integrationRequest struct {
	Item   string `json:"item"`
	Detail string `json:"detail"`
	Known  string `json:"known,omitempty"`
}

type integrationConnectorBrief struct {
	Kind         string               `json:"kind"`
	GeneratedAt  string               `json:"generated_at"`
	ConnectorID  string               `json:"connector_id"`
	ManagedBy    string               `json:"managed_by"`
	Link         []integrationSetting `json:"link"`
	WeWillSend   []integrationSetting `json:"we_will_send"`
	RequestItems []integrationRequest `json:"request_from_carrier"`
	Warnings     []string             `json:"warnings,omitempty"`
	Placeholders map[string]string    `json:"placeholders"`
}

func (h *Handler) connectorIntegrationBrief(w http.ResponseWriter, r *http.Request) {
	cid := r.PathValue("cid")
	config, managedBy, ok, err := h.integrationConnector(r, cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("connector %q not found", cid))
		return
	}

	brief := integrationConnectorBrief{
		Kind:        "connector",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ConnectorID: config.CID,
		ManagedBy:   managedBy,
		Placeholders: map[string]string{
			"(blank)": "we do not hold this value; it has to come from the carrier",
		},
		Link: []integrationSetting{
			{Name: "host", Value: config.Host},
			{Name: "port", Value: strconv.Itoa(config.Port)},
			{Name: "system_id", Value: config.SystemID},
			{Name: "password", Value: "", Detail: "held by this gateway and never displayed"},
			{Name: "bind type", Value: string(config.Bind)},
			{Name: "system_type", Value: config.SystemType, IsDefault: config.SystemType == ""},
			{Name: "TLS", Value: boolText(config.TLSEnabled, "enabled", "disabled")},
		},
		WeWillSend: []integrationSetting{
			{Name: "source TON", Value: strconv.Itoa(config.SrcTON), IsDefault: config.SrcTON == 0},
			{Name: "source NPI", Value: strconv.Itoa(config.SrcNPI), IsDefault: config.SrcNPI == 0},
			{Name: "destination TON", Value: strconv.Itoa(config.DstTON), IsDefault: config.DstTON == 0},
			{Name: "destination NPI", Value: strconv.Itoa(config.DstNPI), IsDefault: config.DstNPI == 0},
			{Name: "bind addr_ton", Value: strconv.Itoa(config.AddrTON), IsDefault: config.AddrTON == 0},
			{Name: "bind addr_npi", Value: strconv.Itoa(config.AddrNPI), IsDefault: config.AddrNPI == 0},
			{Name: "address_range", Value: config.AddressRange, IsDefault: config.AddressRange == ""},
			{Name: "service_type", Value: config.ServiceType, IsDefault: config.ServiceType == ""},
			{Name: "protocol_id", Value: strconv.Itoa(config.ProtocolID), IsDefault: config.ProtocolID == 0},
			{Name: "default source_addr", Value: config.SourceAddr, IsDefault: config.SourceAddr == ""},
			{Name: "submit_sm throughput", Value: throughputText(config.SubmitSMThroughput), IsDefault: config.SubmitSMThroughput == nil},
			{Name: "outstanding submit window", Value: strconv.Itoa(config.WindowSize), IsDefault: config.WindowSize == 0,
				Detail: "zero inherits the AMQP prefetch count"},
			{Name: "enquire_link interval", Value: secondsText(config.EnquireLinkInterval), IsDefault: config.EnquireLinkInterval == 0},
			{Name: "receipt id base", Value: dlrBaseText(config.DLRMsgIDBases), IsDefault: config.DLRMsgIDBases == 0},
		},
		RequestItems: []integrationRequest{
			{Item: "Host and port to bind to", Detail: "including any failover address and whether it is a VIP", Known: config.Host + ":" + strconv.Itoa(config.Port)},
			{Item: "system_id and password for our bind", Detail: "the password must be at most 8 characters: SMPP 3.4 caps the field", Known: config.SystemID},
			{Item: "Bind type they expect", Detail: "transmitter, receiver or transceiver, and how many concurrent sessions we may open", Known: string(config.Bind)},
			{Item: "Our source IP addresses to whitelist", Detail: "the addresses this gateway will connect from, and their change policy"},
			{Item: "Expected source TON/NPI and sender-id format", Detail: "alphanumeric, short code or MSISDN, and any registration requirement"},
			{Item: "Expected destination TON/NPI and number format", Detail: "international with or without a leading plus, national, or a prefix rule"},
			{Item: "service_type value, if any", Detail: "several carriers route on it and reject an empty value"},
			{Item: "Throughput ceiling and window size", Detail: "submits per second, outstanding PDUs allowed, and what happens on breach"},
			{Item: "Delivery receipt behaviour", Detail: "whether receipts are returned, in what text format, and whether the receipt id is the same base as the submit_sm_resp id — that last one selects dlr_msg_id_bases"},
			{Item: "Encoding and concatenation support", Detail: "which data_coding values are accepted, and whether UDH or SAR is expected for long messages"},
			{Item: "A test destination and expected result", Detail: "a number we may submit to during acceptance, and what a successful outcome looks like"},
			{Item: "Escalation contact and maintenance windows", Detail: "who to call when the link drops, and when they take it down"},
		},
	}

	if config.SrcTON == 0 && config.SrcNPI == 0 && config.DstTON == 0 && config.DstNPI == 0 {
		brief.Warnings = append(brief.Warnings,
			"Every TON and NPI on this connector is still zero (unknown). That is a real value on the wire, not an unset one — confirm it with the carrier rather than assuming it is ignored.")
	}
	if config.SubmitSMThroughput == nil {
		brief.Warnings = append(brief.Warnings,
			"No submit_sm throughput is configured, so this gateway will send as fast as the queue allows. Agree a ceiling before carrying live traffic.")
	}
	if !config.TLSEnabled {
		brief.Warnings = append(brief.Warnings,
			"This link is plaintext SMPP. The bind password crosses the network in the clear unless the path is otherwise private.")
	}
	writeJSON(w, http.StatusOK, brief)
}

func (h *Handler) integrationConnector(r *http.Request, cid string) (smppc.Config, string, bool, error) {
	if view, err := h.deps.Connectors.GetConnector(r.Context(), cid); err == nil {
		// The stored connector carries its bind password; nothing below reads it
		// and no field of the brief can hold it.
		return view.Config, "admin", true, nil
	}
	if h.deps.ConfigConnectors != nil {
		for _, config := range h.deps.ConfigConnectors() {
			if config.CID == cid {
				return config, "config", true, nil
			}
		}
	}
	return smppc.Config{}, "", false, nil
}

func throughputText(value *float64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%g/second", *value)
}

func secondsText(value float64) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%g seconds", value)
}

func dlrBaseText(value int) string {
	switch value {
	case 1:
		return "1 (receipt decimal, submit response hex)"
	case 2:
		return "2 (receipt hex, submit response decimal)"
	default:
		return "0 (same base)"
	}
}
