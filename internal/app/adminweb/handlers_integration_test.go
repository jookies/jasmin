package adminweb

import (
	"net/http"
	"strings"
	"testing"
)

// fullIngress is a deployment that publishes everything: the HTTP front door, a
// standalone JSON listener, an SMPP server, and both callback workers.
func fullIngress() IngressSnapshot {
	return IngressSnapshot{
		HTTPBindAddress:           "0.0.0.0:1401",
		RESTBindAddress:           "0.0.0.0:8080",
		SMPPSBindAddress:          "0.0.0.0:2775",
		SMPPSEnquireLinkTimeout:   30,
		SMPPSInactivityTimeout:    300,
		DLRThrowerRunning:         true,
		MOThrowerRunning:          true,
		CallbackTimeoutSeconds:    30,
		CallbackRetryDelaySeconds: 30,
		CallbackMaxRetries:        3,
	}
}

func channelByID(t *testing.T, guide integrationGuide, id string) integrationChannel {
	t.Helper()
	for _, channel := range guide.Channels {
		if channel.ID == id {
			return channel
		}
	}
	t.Fatalf("channel %q missing from guide: %+v", id, guide.Channels)
	return integrationChannel{}
}

func authorizationByKey(t *testing.T, guide integrationGuide, key string) integrationAuthorization {
	t.Helper()
	for _, entry := range guide.Authorizations {
		if entry.Key == key {
			return entry
		}
	}
	t.Fatalf("authorization %q missing: %+v", key, guide.Authorizations)
	return integrationAuthorization{}
}

func hasParameter(channel integrationChannel, name string) bool {
	for _, parameter := range channel.Parameters {
		if parameter.Name == name {
			return true
		}
	}
	return false
}

func blockedFor(channel integrationChannel, substring string) bool {
	for _, reason := range channel.BlockedBy {
		if strings.Contains(reason, substring) {
			return true
		}
	}
	return false
}

// A permissive account with a bind account can use every channel, and the
// examples carry its real username.
func TestIntegrationGuideForAPermissiveAccount(t *testing.T) {
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) {
		deps.Ingress = fullIngress
	})
	f.do("POST", "/api/users", `{"username":"acme","password":"acme-http-pw"}`, http.StatusCreated, nil)
	f.do("POST", "/api/smpps-users", `{"system_id":"acme","password":"acmepw8","ip_whitelist":"203.0.113.0/24","max_bindings":4}`,
		http.StatusCreated, nil)

	var guide integrationGuide
	f.do("GET", "/api/integration/users/acme", "", http.StatusOK, &guide)

	if guide.Kind != "account" || guide.Username != "acme" || guide.ManagedBy != "admin" {
		t.Fatalf("identity: %+v", guide)
	}
	for _, channelID := range []string{"http_send", "rest_send", "rest_sendbatch", "smpp_bind", "account_query"} {
		channel := channelByID(t, guide, channelID)
		if !channel.Available {
			t.Fatalf("channel %s should be available: %+v", channelID, channel.BlockedBy)
		}
	}

	send := channelByID(t, guide, "http_send")
	if len(send.Examples) == 0 || !strings.Contains(send.Examples[0].Command, "username=acme") {
		t.Fatalf("send example does not carry the real username: %+v", send.Examples)
	}
	if !strings.Contains(send.Examples[0].Command, "to=15551234567") {
		t.Fatalf("unconstrained account should get the documented example destination: %s", send.Examples[0].Command)
	}
	// A permissive account may set every optional parameter, so they are all
	// offered.
	for _, name := range []string{"from", "dlr-level", "dlr-method", "priority", "validity-period", "hex-content", "sdt"} {
		if !hasParameter(send, name) {
			t.Fatalf("permissive account is missing optional parameter %q", name)
		}
	}

	// Every token an example can contain is declared, so the reader is never
	// left guessing which angle brackets are literal.
	for _, token := range []string{hostPlaceholder, passwordPlaceholder, "<SENDER>"} {
		if _, declared := guide.Placeholders[token]; !declared {
			t.Fatalf("placeholder %q is emitted but not declared: %+v", token, guide.Placeholders)
		}
	}

	// http_bulk is false on a fresh credential and nothing enforces it; the pack
	// must say so rather than implying a restriction.
	bulk := authorizationByKey(t, guide, "http_bulk")
	if bulk.Allowed || !strings.Contains(bulk.Effect, "no check consults this flag") {
		t.Fatalf("http_bulk row: %+v", bulk)
	}

	bind := channelByID(t, guide, "smpp_bind")
	if !strings.Contains(strings.Join(exampleCommands(bind), "\n"), "system_id   acme") {
		t.Fatalf("bind settings do not carry the system id: %+v", bind.Examples)
	}
	if guide.Inbound.IPWhitelist != "203.0.113.0/24" ||
		guide.Inbound.MaxBindings == nil || *guide.Inbound.MaxBindings != 4 {
		t.Fatalf("inbound bind account: %+v", guide.Inbound)
	}

	if guide.Callbacks.AckBody != "ACK/Jasmin" || guide.Callbacks.SuccessStatus != "2xx" {
		t.Fatalf("callback contract: %+v", guide.Callbacks)
	}
	if guide.Callbacks.TotalAttempts != 4 {
		t.Fatalf("total attempts should be retries plus the first try: %+v", guide.Callbacks)
	}

	// With no declared public hostname every address is a placeholder, and the
	// pack must say so loudly rather than printing the bind address.
	http1 := guide.Endpoints[0]
	if !http1.AuthorityIsPlaceholder || !strings.Contains(http1.Authority, hostPlaceholder) {
		t.Fatalf("endpoint authority: %+v", http1)
	}
	if http1.BindAddress != "0.0.0.0:1401" {
		t.Fatalf("bind address should still be reported for the operator: %+v", http1)
	}
	if !strings.Contains(strings.Join(guide.Warnings, "\n"), "public hostname") {
		t.Fatalf("missing placeholder warning: %+v", guide.Warnings)
	}
}

func exampleCommands(channel integrationChannel) []string {
	commands := make([]string, 0, len(channel.Examples))
	for _, example := range channel.Examples {
		commands = append(commands, example.Command)
	}
	return commands
}

// The account's real authorizations and filters shape the pack: a parameter it
// may not set is not offered, and the example destination satisfies its own
// destination filter.
func TestIntegrationGuideReflectsRestrictedAuthorizations(t *testing.T) {
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) {
		deps.Ingress = fullIngress
	})
	f.do("POST", "/api/users", `{
		"username":"fenced",
		"password":"fenced-pw",
		"set_source_address":false,
		"set_dlr_level":false,
		"http_set_dlr_method":false,
		"set_hex_content":false,
		"http_balance":false,
		"http_long_content":false,
		"filter_destination_address":"^33"
	}`, http.StatusCreated, nil)

	var guide integrationGuide
	f.do("GET", "/api/integration/users/fenced", "", http.StatusOK, &guide)

	send := channelByID(t, guide, "http_send")
	if !send.Available {
		t.Fatalf("http_send should still be available: %+v", send.BlockedBy)
	}
	for _, name := range []string{"from", "dlr-level", "dlr-method", "hex-content"} {
		if hasParameter(send, name) {
			t.Fatalf("parameter %q offered to an account that may not set it", name)
		}
	}
	if !hasParameter(send, "priority") {
		t.Fatal("priority is still authorized and should be offered")
	}
	command := send.Examples[0].Command
	if !strings.Contains(command, "to=33000000000") {
		t.Fatalf("example destination does not satisfy the account's own filter: %s", command)
	}
	if strings.Contains(command, "from=") {
		t.Fatalf("example sets a source address the account may not set: %s", command)
	}
	receipt := send.Examples[1].Command
	if strings.Contains(receipt, "dlr-level") || strings.Contains(receipt, "dlr-method") {
		t.Fatalf("receipt example uses parameters this account may not set: %s", receipt)
	}
	if !strings.Contains(receipt, "dlr=yes") || !strings.Contains(receipt, "dlr-url=") {
		t.Fatalf("receipt example must still request a receipt: %s", receipt)
	}
	if !strings.Contains(strings.Join(send.Notes, "\n"), "Multi-part content is not authorized") {
		t.Fatalf("long-content restriction not surfaced: %+v", send.Notes)
	}

	source := authorizationByKey(t, guide, "set_source_address")
	if source.Allowed ||
		source.Effect != `Authorization failed for user [fenced] (Setting source address not authorized).` {
		t.Fatalf("set_source_address row: %+v", source)
	}

	var destination integrationFilter
	for _, filter := range guide.Filters {
		if filter.Key == "destination_address" {
			destination = filter
		}
	}
	if !destination.Constrains || destination.Pattern != "^33" ||
		destination.Effect != `Value filter failed for user [fenced] (destination_address filter mismatch).` {
		t.Fatalf("destination filter row: %+v", destination)
	}

	// A batch is authenticated by calling /balance internally, so denying
	// http_balance silently breaks every batch. The pack has to name that.
	batch := channelByID(t, guide, "rest_sendbatch")
	if batch.Available || !blockedFor(batch, "http_balance") {
		t.Fatalf("sendbatch should be blocked by http_balance: %+v", batch)
	}
	query := channelByID(t, guide, "account_query")
	if query.Available || !blockedFor(query, "http_balance") {
		t.Fatalf("balance query should be blocked: %+v", query)
	}
	for _, example := range query.Examples {
		if strings.Contains(example.Command, "/balance") {
			t.Fatalf("a balance example was offered to an account that may not call it: %+v", example)
		}
	}
}

// SMPP availability depends on the deployment and on a bind account, not on the
// MT credential alone.
func TestIntegrationGuideSMPPAvailability(t *testing.T) {
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) {
		deps.Ingress = fullIngress
	})
	f.do("POST", "/api/users", `{"username":"nobind","password":"nobind-pw"}`, http.StatusCreated, nil)

	var guide integrationGuide
	f.do("GET", "/api/integration/users/nobind", "", http.StatusOK, &guide)
	bind := channelByID(t, guide, "smpp_bind")
	if bind.Available || !blockedFor(bind, "no SMPP bind account exists with system_id nobind") {
		t.Fatalf("bindless account: %+v", bind)
	}
	if !strings.Contains(strings.Join(guide.Warnings, "\n"), "no bind account exists") {
		t.Fatalf("missing bindless warning: %+v", guide.Warnings)
	}

	// The same account on a deployment with no SMPP server is blocked for a
	// different reason, and the pack must not blame the account.
	f.rebuildHandler(func(deps *Deps) {
		deps.Ingress = func() IngressSnapshot {
			snapshot := fullIngress()
			snapshot.SMPPSBindAddress = ""
			return snapshot
		}
	})
	f.do("GET", "/api/integration/users/nobind", "", http.StatusOK, &guide)
	bind = channelByID(t, guide, "smpp_bind")
	if bind.Available || !blockedFor(bind, "runs no customer-facing SMPP server") {
		t.Fatalf("no SMPP server: %+v", bind)
	}
	if blockedFor(bind, "no SMPP bind account exists") == false {
		t.Fatalf("both causes should be reported: %+v", bind.BlockedBy)
	}

	// smpps_send denied: a bind would succeed and every submit would fail, which
	// is a distinct and confusing failure mode.
	f.rebuildHandler(func(deps *Deps) { deps.Ingress = fullIngress })
	f.do("POST", "/api/users", `{"username":"rxonly","password":"rxonly-pw","smpps_send":false}`, http.StatusCreated, nil)
	f.do("POST", "/api/smpps-users", `{"system_id":"rxonly","password":"rxonlypw"}`, http.StatusCreated, nil)
	f.do("GET", "/api/integration/users/rxonly", "", http.StatusOK, &guide)
	bind = channelByID(t, guide, "smpp_bind")
	if bind.Available || !blockedFor(bind, "smpps_send") {
		t.Fatalf("smpps_send denied: %+v", bind)
	}
}

// A deployment with no declared public hostname and no ingress at all must
// still answer, describing every channel as unreachable rather than inventing
// an address.
func TestIntegrationGuideWithoutIngressReporting(t *testing.T) {
	f := newWebFixture(t)
	f.do("POST", "/api/users", `{"username":"lonely","password":"lonely-pw"}`, http.StatusCreated, nil)

	var guide integrationGuide
	f.do("GET", "/api/integration/users/lonely", "", http.StatusOK, &guide)
	for _, endpoint := range guide.Endpoints {
		if endpoint.Enabled {
			t.Fatalf("endpoint %s reported enabled with no ingress snapshot: %+v", endpoint.ID, endpoint)
		}
	}
	send := channelByID(t, guide, "http_send")
	if send.Available || !blockedFor(send, "publishes no HTTP front door") {
		t.Fatalf("http_send with no ingress: %+v", send)
	}
	// The example still exists so an operator can see the shape, but it carries
	// the placeholder host rather than a fabricated one.
	if !strings.Contains(send.Examples[0].Command, hostPlaceholder) {
		t.Fatalf("example without an endpoint: %s", send.Examples[0].Command)
	}
	if !guide.Callbacks.Available {
		// No DLR thrower configured in the empty snapshot.
		if !strings.Contains(strings.Join(guide.Callbacks.Notes, "\n"), "never called") {
			t.Fatalf("missing dead-callback note: %+v", guide.Callbacks.Notes)
		}
	}
}

// When the operator declared a public hostname the pack stops hedging: the
// examples carry a real address and the substitution warning goes away.
func TestIntegrationGuideUsesTheDeclaredPublicHostname(t *testing.T) {
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) {
		deps.Ingress = func() IngressSnapshot {
			snapshot := fullIngress()
			snapshot.PublicHostname = "sms.example.com"
			return snapshot
		}
	})
	f.do("POST", "/api/users", `{"username":"named","password":"named-pw"}`, http.StatusCreated, nil)

	var guide integrationGuide
	f.do("GET", "/api/integration/users/named", "", http.StatusOK, &guide)
	endpoint := guide.Endpoints[0]
	if endpoint.AuthorityIsPlaceholder || endpoint.Authority != "sms.example.com:1401" {
		t.Fatalf("declared hostname was not used: %+v", endpoint)
	}
	send := channelByID(t, guide, "http_send")
	if !strings.Contains(send.Examples[0].Command, "http://sms.example.com:1401/send") {
		t.Fatalf("example does not carry the declared address: %s", send.Examples[0].Command)
	}
	if strings.Contains(strings.Join(guide.Warnings, "\n"), "public hostname") {
		t.Fatalf("placeholder warning survived a declared hostname: %+v", guide.Warnings)
	}
	// The bind address is still reported, because an operator diagnosing
	// exposure needs it — it is just never used to build a URL.
	if endpoint.BindAddress != "0.0.0.0:1401" {
		t.Fatalf("bind address: %+v", endpoint)
	}
}

// The pack is handed to a customer. It must contain no password material of any
// kind: not the plaintext, not a verifier, not a hash.
func TestIntegrationGuideNeverEmitsPasswordMaterial(t *testing.T) {
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) { deps.Ingress = fullIngress })
	const (
		httpPassword = "pw-integration-secret"
		bindPassword = "bindpw42"
	)
	f.do("POST", "/api/users", `{"username":"secretive","password":"`+httpPassword+`"}`, http.StatusCreated, nil)
	f.do("POST", "/api/smpps-users", `{"system_id":"secretive","password":"`+bindPassword+`"}`, http.StatusCreated, nil)

	body := f.do("GET", "/api/integration/users/secretive", "", http.StatusOK, nil).Body.String()
	for _, forbidden := range []string{httpPassword, bindPassword, hashPassword(httpPassword)} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("integration guide leaked password material %q", forbidden)
		}
	}
	if !strings.Contains(body, passwordPlaceholder) {
		t.Fatal("the guide must tell the reader where the password goes")
	}
}

func TestIntegrationGuideUnknownUser(t *testing.T) {
	f := newWebFixture(t)
	f.do("GET", "/api/integration/users/ghost", "", http.StatusNotFound, nil)
	f.do("GET", "/api/integration/connectors/ghost", "", http.StatusNotFound, nil)
}

// The connector direction is inverted: the artifact states what we will send and
// asks the carrier for what we do not know.
func TestConnectorIntegrationBrief(t *testing.T) {
	f := newWebFixture(t)
	f.do("POST", "/api/connectors",
		`{"cid":"carrier-a","host":"smsc.carrier.example","port":2775,"system_id":"synevyr","password":"conn-secret","bind":"transceiver"}`,
		http.StatusCreated, nil)

	var brief integrationConnectorBrief
	recorder := f.do("GET", "/api/integration/connectors/carrier-a", "", http.StatusOK, &brief)
	if strings.Contains(recorder.Body.String(), "conn-secret") {
		t.Fatal("connector brief leaked the bind password")
	}
	if brief.Kind != "connector" || brief.ConnectorID != "carrier-a" || brief.ManagedBy != "admin" {
		t.Fatalf("identity: %+v", brief)
	}
	if len(brief.RequestItems) == 0 {
		t.Fatal("the brief must list what to request from the carrier")
	}

	settings := map[string]integrationSetting{}
	for _, setting := range brief.WeWillSend {
		settings[setting.Name] = setting
	}
	for _, name := range []string{"source TON", "destination NPI", "service_type"} {
		setting, ok := settings[name]
		if !ok {
			t.Fatalf("missing wire setting %q", name)
		}
		if !setting.IsDefault {
			t.Fatalf("%q was never configured and must be flagged as a default: %+v", name, setting)
		}
	}
	joined := strings.Join(brief.Warnings, "\n")
	if !strings.Contains(joined, "TON and NPI") || !strings.Contains(joined, "throughput") {
		t.Fatalf("the silent zero defaults must be warned about: %+v", brief.Warnings)
	}
}
