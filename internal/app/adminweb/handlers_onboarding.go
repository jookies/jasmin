package adminweb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
)

// Partner onboarding provisions several resources that only make sense
// together: a billing group, a gateway user, a bind account, a connector and a
// route. Creating them one screen at a time is how an operator ends up with a
// user that has no route, or a connector nobody can reach.
//
// The admin services each own their own transaction, so this is not a single
// database transaction. Instead every created resource is recorded and undone in
// reverse order when a later step fails, and the response says exactly what was
// created — a half-built partner is reported as such rather than returned as
// success.

// SMPP 3.4 caps a bind password at eight characters. A longer generated secret
// cannot bind at all, which presents as a credential problem and is really a
// length problem, so the generator is capped rather than the operator surprised.
const smppPasswordLength = 8

// The HTTP front door accepts at most 16 characters.
const httpPasswordLength = 16

var partnerCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,14}$`)

type onboardingRequest struct {
	PartnerName string `json:"partner_name"`
	PartnerCode string `json:"partner_code"`
	// Direction: "inbound" (the partner binds to us), "outbound" (we bind to
	// them), or "bidirectional".
	Direction string `json:"direction"`

	CreateGroup   bool     `json:"create_group"`
	Balance       *float64 `json:"balance,omitempty"`
	SubmitSMCount *int     `json:"submit_sm_count,omitempty"`
	Throughput    *float64 `json:"throughput,omitempty"`

	InboundSystemID  string `json:"inbound_system_id,omitempty"`
	InboundBindMode  string `json:"inbound_bind_mode,omitempty"`
	InboundAllowlist string `json:"inbound_allowlist,omitempty"`
	MaxBindings      *int   `json:"max_bindings,omitempty"`

	OutboundHost     string `json:"outbound_host,omitempty"`
	OutboundPort     int    `json:"outbound_port,omitempty"`
	OutboundSystemID string `json:"outbound_system_id,omitempty"`
	OutboundPassword string `json:"outbound_password,omitempty"`
	OutboundBindMode string `json:"outbound_bind_mode,omitempty"`
	OutboundTLS      bool   `json:"outbound_tls,omitempty"`

	// Prefixes filters the created MT route by destination. Empty means the
	// route is not created at all: a route with no filter would take traffic
	// away from every existing route below it.
	Prefixes []string `json:"prefixes,omitempty"`
	Rate     float64  `json:"rate,omitempty"`
}

type createdResource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type onboardingResult struct {
	Created []createdResource `json:"created"`
	// Credentials are returned exactly once, at creation. Nothing stores the
	// plaintext, and no later request can retrieve it.
	HTTPPassword string   `json:"http_password,omitempty"`
	BindPassword string   `json:"bind_password,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

func generateSecret(length int) (string, error) {
	raw := make([]byte, (length+1)/2)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return hex.EncodeToString(raw)[:length], nil
}

// provisioner accumulates what has been created so a failure can undo it.
type provisioner struct {
	handler  *Handler
	created  []createdResource
	rollback []func(context.Context) error
}

func (p *provisioner) record(kind, id string, undo func(context.Context) error) {
	p.created = append(p.created, createdResource{Kind: kind, ID: id})
	p.rollback = append(p.rollback, undo)
}

// undo reverses everything created so far, newest first. A failure to undo is
// reported rather than swallowed: an operator must know which resources are
// still lying around.
func (p *provisioner) undo(ctx context.Context) []string {
	var problems []string
	for index := len(p.rollback) - 1; index >= 0; index-- {
		if err := p.rollback[index](ctx); err != nil {
			problems = append(problems, fmt.Sprintf("%s %q could not be removed: %v",
				p.created[index].Kind, p.created[index].ID, err))
		}
	}
	return problems
}

func (h *Handler) onboardPartner(w http.ResponseWriter, r *http.Request) {
	var request onboardingRequest
	if err := decodeBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	request.PartnerCode = strings.TrimSpace(strings.ToLower(request.PartnerCode))
	request.PartnerName = strings.TrimSpace(request.PartnerName)
	if !partnerCodePattern.MatchString(request.PartnerCode) {
		writeError(w, http.StatusBadRequest,
			"partner code must be 1-15 characters: lowercase letters, digits, dashes or underscores, starting with a letter or digit")
		return
	}
	switch request.Direction {
	case "inbound", "outbound", "bidirectional":
	default:
		writeError(w, http.StatusBadRequest, "direction must be inbound, outbound or bidirectional")
		return
	}
	wantsInbound := request.Direction == "inbound" || request.Direction == "bidirectional"
	wantsOutbound := request.Direction == "outbound" || request.Direction == "bidirectional"
	if wantsOutbound && (request.OutboundHost == "" || request.OutboundSystemID == "" || request.OutboundPort == 0) {
		writeError(w, http.StatusBadRequest,
			"an outbound connection needs a host, port and system id")
		return
	}

	ctx := r.Context()

	// Onboarding creates; it never adopts. The underlying services are upserts,
	// so without this an operator re-running the wizard for an existing partner
	// would silently rotate that customer's password and overwrite their
	// balance, group and throughput — found by re-running it against a live
	// gateway, where the second attempt returned success.
	inboundSystemID := strings.TrimSpace(request.InboundSystemID)
	if inboundSystemID == "" {
		inboundSystemID = request.PartnerCode
	}
	if taken, what := h.partnerIdentityTaken(ctx, request.PartnerCode, inboundSystemID, wantsInbound, wantsOutbound); taken {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"%s already exists. Onboarding only creates new partners; edit the existing one instead.", what))
		return
	}

	p := &provisioner{handler: h}
	result := onboardingResult{}

	fail := func(status int, message string) {
		problems := p.undo(ctx)
		if len(problems) > 0 {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"message": message + " — and the rollback did not complete, so some resources still exist",
				"created": p.created,
				"orphans": problems,
			})
			return
		}
		writeError(w, status, message+" (nothing was created)")
	}

	// 1. Group, when the partner's ceilings are shared across future accounts.
	groupID := ""
	if request.CreateGroup {
		groupID = request.PartnerCode
		spec, err := json.Marshal(outbound2GroupConfig(groupID, request))
		if err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		if err := h.deps.Groups.CreateGroup(ctx, groupID, string(spec)); err != nil {
			writeServiceError(w, err)
			return
		}
		p.record("group", groupID, func(ctx context.Context) error {
			return h.deps.Groups.DeleteGroup(ctx, groupID)
		})
	}

	// 2. Gateway user: the sending and billing identity, for either direction.
	httpPassword, err := generateSecret(httpPasswordLength)
	if err != nil {
		fail(http.StatusInternalServerError, err.Error())
		return
	}
	digest := sha256.Sum256([]byte(httpPassword))
	userSpec := outbound.UserConfig{
		Username:       request.PartnerCode,
		ExternalID:     request.PartnerCode,
		PasswordSHA256: hex.EncodeToString(digest[:]),
		GroupID:        groupID,
		Balance:        request.Balance,
		SubmitSMCount:  request.SubmitSMCount,
	}
	if request.Throughput != nil {
		userSpec.MTCredential = &outbound.MTCredentialConfig{
			HTTPThroughput:  request.Throughput,
			SMPPSThroughput: request.Throughput,
		}
	}
	spec, err := json.Marshal(userSpec)
	if err != nil {
		fail(http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Users.CreateUser(ctx, request.PartnerCode, string(spec)); err != nil {
		problems := p.undo(ctx)
		if len(problems) > 0 {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"message": err.Error(), "created": p.created, "orphans": problems,
			})
			return
		}
		writeServiceError(w, err)
		return
	}
	p.record("user", request.PartnerCode, func(ctx context.Context) error {
		return h.deps.Users.DeleteUser(ctx, request.PartnerCode)
	})
	result.HTTPPassword = httpPassword

	// 3. Bind account, when the partner connects to us.
	if wantsInbound {
		systemID := inboundSystemID
		bindPassword, secretErr := generateSecret(smppPasswordLength)
		if secretErr != nil {
			fail(http.StatusInternalServerError, secretErr.Error())
			return
		}
		account := smppsserver.UserConfig{
			SystemID:    systemID,
			Password:    bindPassword,
			IPWhitelist: strings.TrimSpace(request.InboundAllowlist),
			MaxBindings: request.MaxBindings,
		}
		accountSpec, marshalErr := json.Marshal(account)
		if marshalErr != nil {
			fail(http.StatusInternalServerError, marshalErr.Error())
			return
		}
		if err := h.deps.SMPPsUsers.PutUser(ctx, systemID, string(accountSpec)); err != nil {
			fail(http.StatusConflict, err.Error())
			return
		}
		p.record("smpps_bind_account", systemID, func(ctx context.Context) error {
			return h.deps.SMPPsUsers.DeleteUser(ctx, systemID)
		})
		result.BindPassword = bindPassword
	}

	// 4. Connector, when we connect to them.
	connectorID := ""
	if wantsOutbound {
		connectorID = request.PartnerCode
		config := smppc.Config{
			CID:        connectorID,
			Host:       strings.TrimSpace(request.OutboundHost),
			Port:       request.OutboundPort,
			SystemID:   strings.TrimSpace(request.OutboundSystemID),
			Password:   request.OutboundPassword,
			Bind:       normalizeBindMode(request.OutboundBindMode),
			TLSEnabled: request.OutboundTLS,
		}
		if err := h.deps.Connectors.CreateConnector(ctx, config, false); err != nil {
			fail(http.StatusConflict, err.Error())
			return
		}
		p.record("connector", connectorID, func(ctx context.Context) error {
			return h.deps.Connectors.DeleteConnector(ctx, connectorID)
		})
	}

	// 5. Route, only when there is a prefix to filter on and a connector to
	//    send to. An unfiltered route here would silently capture traffic that
	//    belongs to every route below it.
	prefixes := make([]string, 0, len(request.Prefixes))
	for _, prefix := range request.Prefixes {
		if trimmed := strings.TrimSpace(prefix); trimmed != "" {
			prefixes = append(prefixes, trimmed)
		}
	}
	switch {
	case len(prefixes) == 0:
		result.Warnings = append(result.Warnings,
			"No destination prefixes were given, so no MT route was created. This partner's traffic will follow the existing routes.")
	case connectorID == "":
		result.Warnings = append(result.Warnings,
			"No MT route was created: routes send traffic to a connector, and this partner does not provide one.")
	default:
		order, orderErr := h.nextRouteOrder(ctx)
		if orderErr != nil {
			fail(http.StatusInternalServerError, orderErr.Error())
			return
		}
		filters := make([]outbound.FilterConfig, 0, len(prefixes))
		for _, prefix := range prefixes {
			// Patterns are anchored, so a bare prefix already means "starts
			// with" — which is exactly what a destination prefix should mean.
			filters = append(filters, outbound.FilterConfig{Type: "destination_addr", Pattern: prefix})
		}
		route := outbound.RouteConfig{
			ConnectorID: connectorID,
			Rate:        request.Rate,
			Order:       order,
			Filters:     filters,
		}
		routeSpec, marshalErr := json.Marshal(route)
		if marshalErr != nil {
			fail(http.StatusInternalServerError, marshalErr.Error())
			return
		}
		if err := h.deps.Routes.PutRoute(ctx, order, string(routeSpec)); err != nil {
			fail(http.StatusConflict, err.Error())
			return
		}
		p.record("mt_route", fmt.Sprintf("order %d", order), func(ctx context.Context) error {
			return h.deps.Routes.DeleteRoute(ctx, order)
		})
		if len(prefixes) > 1 {
			result.Warnings = append(result.Warnings,
				"Every filter on a route must match, so a route with several destination prefixes matches none of them. Split them into one route per prefix.")
		}
	}

	result.Created = p.created
	writeJSON(w, http.StatusCreated, result)
}

// nextRouteOrder picks a free order above every existing admin route, so a new
// partner never displaces existing routing by accident.
func (h *Handler) nextRouteOrder(ctx context.Context) (int, error) {
	routes, err := h.deps.Routes.ListRoutes(ctx)
	if err != nil {
		return 0, err
	}
	highest := 0
	for _, route := range routes {
		if route.Order > highest {
			highest = route.Order
		}
	}
	if h.deps.ConfigRoutes != nil {
		for _, route := range h.deps.ConfigRoutes() {
			if route.Order > highest {
				highest = route.Order
			}
		}
	}
	return highest + 10, nil
}

func normalizeBindMode(mode string) smppc.BindType {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "transmitter":
		return smppc.BindTransmitter
	case "receiver":
		return smppc.BindReceiver
	default:
		return smppc.BindTransceiver
	}
}

func outbound2GroupConfig(gid string, request onboardingRequest) outbound.GroupConfig {
	return outbound.GroupConfig{
		GID:           gid,
		Balance:       request.Balance,
		SubmitSMCount: request.SubmitSMCount,
	}
}

// partnerIdentityTaken reports whether any identity this onboarding would create
// already exists, so the whole operation can be refused before it creates
// anything. It names the specific collision: "already exists" without saying
// what is a support ticket.
func (h *Handler) partnerIdentityTaken(
	ctx context.Context,
	code, systemID string,
	wantsInbound, wantsOutbound bool,
) (bool, string) {
	if _, err := h.deps.Users.GetUser(ctx, code); err == nil {
		return true, fmt.Sprintf("gateway user %q", code)
	}
	if _, ok := h.configUser(code); ok {
		return true, fmt.Sprintf("config-owned gateway user %q", code)
	}
	if _, err := h.deps.Groups.GetGroup(ctx, code); err == nil {
		return true, fmt.Sprintf("group %q", code)
	}
	if wantsInbound {
		if _, err := h.deps.SMPPsUsers.GetUser(ctx, systemID); err == nil {
			return true, fmt.Sprintf("SMPPs bind account %q", systemID)
		}
		if _, ok := h.configSMPPsUser(systemID); ok {
			return true, fmt.Sprintf("config-owned SMPPs bind account %q", systemID)
		}
	}
	if wantsOutbound {
		if _, err := h.deps.Connectors.GetConnector(ctx, code); err == nil {
			return true, fmt.Sprintf("connector %q", code)
		}
	}
	return false, ""
}
