package adminweb

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

func decodeOnboarding(t *testing.T, body string) onboardingResult {
	t.Helper()
	var result onboardingResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("decode onboarding result: %v (%s)", err, body)
	}
	return result
}

// The point of the endpoint: one call produces a coherent partner rather than
// four screens' worth of resources an operator has to keep straight.
func TestOnboardingProvisionsABidirectionalPartnerInOneCall(t *testing.T) {
	f := newWebFixture(t)
	rec := f.do("POST", "/api/onboarding/partners", `{
		"partner_name":"Acme Telecom","partner_code":"acme","direction":"bidirectional",
		"create_group":true,"balance":250,"submit_sm_count":10000,
		"inbound_system_id":"acme-esme","inbound_allowlist":"203.0.113.0/24",
		"outbound_host":"smsc.example.net","outbound_port":2775,
		"outbound_system_id":"synevyr","outbound_password":"carrierpw",
		"prefixes":["44"],"rate":0.03
	}`, http.StatusCreated, nil)
	result := decodeOnboarding(t, rec.Body.String())

	kinds := map[string]string{}
	for _, created := range result.Created {
		kinds[created.Kind] = created.ID
	}
	for _, kind := range []string{"group", "user", "smpps_bind_account", "connector", "mt_route"} {
		if _, ok := kinds[kind]; !ok {
			t.Fatalf("%s was not created: %+v", kind, result.Created)
		}
	}
	if kinds["user"] != "acme" || kinds["smpps_bind_account"] != "acme-esme" {
		t.Fatalf("identities=%+v", kinds)
	}

	// Credentials are returned once, generated rather than typed.
	if len(result.HTTPPassword) != httpPasswordLength {
		t.Fatalf("http password %q is not %d characters", result.HTTPPassword, httpPasswordLength)
	}
	// SMPP 3.4 caps a bind password at eight characters; a longer one cannot bind.
	if len(result.BindPassword) != smppPasswordLength {
		t.Fatalf("bind password %q is not %d characters", result.BindPassword, smppPasswordLength)
	}

	// The stored user must carry the hash, never the plaintext.
	stored := f.users.installed["acme"]
	if strings.Contains(stored, result.HTTPPassword) {
		t.Fatal("the generated password was stored in plaintext")
	}
	var spec outbound.UserConfig
	if err := json.Unmarshal([]byte(stored), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.PasswordSHA256 != hashPassword(result.HTTPPassword) {
		t.Fatalf("stored verifier does not match the issued password")
	}
	if spec.GroupID != "acme" {
		t.Fatalf("user was not placed in the created group: %+v", spec)
	}
}

// A partner that only sends to us needs no connector, and therefore no route:
// a route with no connector cannot exist, and one with no filter would swallow
// other traffic. Both must be said out loud rather than silently skipped.
func TestOnboardingExplainsWhatItDidNotCreate(t *testing.T) {
	f := newWebFixture(t)
	rec := f.do("POST", "/api/onboarding/partners",
		`{"partner_code":"inbound-only","direction":"inbound","prefixes":["44"]}`,
		http.StatusCreated, nil)
	result := decodeOnboarding(t, rec.Body.String())

	for _, created := range result.Created {
		if created.Kind == "mt_route" || created.Kind == "connector" {
			t.Fatalf("unexpected %s for an inbound-only partner", created.Kind)
		}
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, " "), "connector") {
		t.Fatalf("the missing route was not explained: %+v", result.Warnings)
	}
	if result.BindPassword == "" {
		t.Fatal("an inbound partner got no bind credential")
	}
}

// Several destination prefixes on one route match nothing, because every filter
// on a route must match. Warn rather than quietly build a dead route.
func TestOnboardingWarnsThatMultiplePrefixesCannotShareARoute(t *testing.T) {
	f := newWebFixture(t)
	rec := f.do("POST", "/api/onboarding/partners", `{
		"partner_code":"multi","direction":"outbound",
		"outbound_host":"smsc.example.net","outbound_port":2775,"outbound_system_id":"s",
		"prefixes":["44","33"]
	}`, http.StatusCreated, nil)
	result := decodeOnboarding(t, rec.Body.String())
	if !strings.Contains(strings.Join(result.Warnings, " "), "one route per prefix") {
		t.Fatalf("warnings=%+v", result.Warnings)
	}
}

// The failure case is the reason this endpoint exists. A conflict partway
// through must leave nothing behind, not a user with no bind account.
func TestOnboardingRollsBackEverythingWhenAStepFails(t *testing.T) {
	f := newWebFixture(t)
	// Reserve the connector id so connector creation — step four — fails after
	// the group, user and bind account have already been created.
	f.do("POST", "/api/connectors",
		`{"cid":"clash","host":"127.0.0.1","port":2775,"system_id":"x","password":"y"}`,
		http.StatusCreated, nil)

	f.do("POST", "/api/onboarding/partners", `{
		"partner_code":"clash","direction":"bidirectional",
		"outbound_host":"smsc.example.net","outbound_port":2775,"outbound_system_id":"s"
	}`, http.StatusConflict, nil)

	var users []userResource
	f.do("GET", "/api/users", "", http.StatusOK, &users)
	for _, user := range users {
		if user.Username == "clash" {
			t.Fatal("the gateway user survived a failed onboarding")
		}
	}
	var groups []groupResource
	f.do("GET", "/api/groups", "", http.StatusOK, &groups)
	for _, group := range groups {
		if group.GID == "clash" {
			t.Fatal("the group survived a failed onboarding")
		}
	}
	var accounts []smppsUserResource
	f.do("GET", "/api/smpps-users", "", http.StatusOK, &accounts)
	for _, account := range accounts {
		if account.SystemID == "clash" {
			t.Fatal("the bind account survived a failed onboarding")
		}
	}
}

func TestOnboardingRejectsAnUnusablePartnerCode(t *testing.T) {
	f := newWebFixture(t)
	for _, code := range []string{"", "Has Spaces", "way-too-long-a-code", "-leading-dash"} {
		body := `{"partner_code":"` + code + `","direction":"inbound"}`
		f.do("POST", "/api/onboarding/partners", body, http.StatusBadRequest, nil)
	}
}

// An uppercase code is normalised rather than refused: the resulting username is
// legal either way, and rejecting it would be pedantry the operator has to work
// around.
func TestOnboardingNormalisesThePartnerCode(t *testing.T) {
	f := newWebFixture(t)
	rec := f.do("POST", "/api/onboarding/partners",
		`{"partner_code":"  ACME-EU ","direction":"inbound"}`, http.StatusCreated, nil)
	result := decodeOnboarding(t, rec.Body.String())
	for _, created := range result.Created {
		if created.Kind == "user" && created.ID != "acme-eu" {
			t.Fatalf("code was not normalised: %q", created.ID)
		}
	}
}

// Onboarding creates; it never adopts. The services underneath are upserts, so
// without an explicit guard a re-run would rotate a live customer's password and
// overwrite their balance, group and throughput — which is what happened when
// this was first exercised against a running gateway.
func TestOnboardingRefusesAnExistingPartnerRatherThanOverwritingIt(t *testing.T) {
	f := newWebFixture(t)
	first := f.do("POST", "/api/onboarding/partners",
		`{"partner_code":"acme","direction":"inbound"}`, http.StatusCreated, nil)
	issued := decodeOnboarding(t, first.Body.String())

	f.do("POST", "/api/onboarding/partners",
		`{"partner_code":"acme","direction":"inbound"}`, http.StatusConflict, nil)

	// The original credential must still be the one that authenticates.
	var spec outbound.UserConfig
	if err := json.Unmarshal([]byte(f.users.installed["acme"]), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.PasswordSHA256 != hashPassword(issued.HTTPPassword) {
		t.Fatal("a refused re-run still rotated the customer's password")
	}

	var accounts []smppsUserResource
	f.do("GET", "/api/smpps-users", "", http.StatusOK, &accounts)
	matches := 0
	for _, account := range accounts {
		if account.SystemID == "acme" {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly one bind account for acme, found %d", matches)
	}
}
