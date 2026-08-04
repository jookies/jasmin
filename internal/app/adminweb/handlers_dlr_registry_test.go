package adminweb

import (
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/pumpitspace/synevyr/internal/core/dlrgate"
)

// withDLRRegistry enables the registry surface on an existing fixture.
func withDLRRegistry(t *testing.T, f *webFixture) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	registry, err := dlrgate.NewRegistry(client, "")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	f.rebuildHandler(func(deps *Deps) { deps.DLRRegistry = registry })
	return server
}

// TestDLRRegistryDisabledIs404 holds the house convention: a capability this
// gateway cannot honour leaves no trace, rather than an empty list that reads as
// "none configured" and invites relying on it.
func TestDLRRegistryDisabledIs404(t *testing.T) {
	f := newWebFixture(t)
	f.do("GET", "/api/dlr-registry", "", http.StatusNotFound, nil)
	f.do("POST", "/api/dlr-registry", `{"msisdn":"380930242105"}`, http.StatusNotFound, nil)
}

func TestDLRRegistryCRUD(t *testing.T) {
	f := newWebFixture(t)
	withDLRRegistry(t, f)

	var created dlrRegistryResource
	f.do("POST", "/api/dlr-registry", `{"msisdn":"+380930242105","ttl_seconds":600,"note":"otp"}`,
		http.StatusCreated, &created)
	if created.MSISDN != "380930242105" {
		t.Fatalf("MSISDN = %q, want the normalized digits", created.MSISDN)
	}
	if created.AddedBy != "admin" {
		t.Fatalf("AddedBy = %q, want the session user", created.AddedBy)
	}
	if created.ExpiresInSeconds <= 0 || created.ExpiresInSeconds > 600 {
		t.Fatalf("ExpiresInSeconds = %d, want a window within the requested 600s", created.ExpiresInSeconds)
	}

	var listed []dlrRegistryResource
	f.do("GET", "/api/dlr-registry", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].MSISDN != "380930242105" || listed[0].Note != "otp" {
		t.Fatalf("list = %+v, want the one entry with its note", listed)
	}

	f.do("DELETE", "/api/dlr-registry/380930242105", "", http.StatusNoContent, nil)
	listed = nil
	f.do("GET", "/api/dlr-registry", "", http.StatusOK, &listed)
	if len(listed) != 0 {
		t.Fatalf("list after delete = %+v, want empty", listed)
	}
}

func TestDLRRegistryRejectsNonNumericMSISDN(t *testing.T) {
	f := newWebFixture(t)
	withDLRRegistry(t, f)
	f.do("POST", "/api/dlr-registry", `{"msisdn":"undefined"}`, http.StatusBadRequest, nil)
	f.do("POST", "/api/dlr-registry", `{"msisdn":"  "}`, http.StatusBadRequest, nil)
}

// TestDLRRegistryClampsTTL proves an over-long request is clamped rather than
// refused, so a caller asking for a day gets the operator's maximum window.
func TestDLRRegistryClampsTTL(t *testing.T) {
	f := newWebFixture(t)
	withDLRRegistry(t, f)

	var created dlrRegistryResource
	f.do("POST", "/api/dlr-registry", `{"msisdn":"380930242105","ttl_seconds":86400}`,
		http.StatusCreated, &created)
	if created.ExpiresInSeconds > int64(dlrgate.MaxTTL/time.Second) {
		t.Fatalf("ExpiresInSeconds = %d, want it clamped to %s", created.ExpiresInSeconds, dlrgate.MaxTTL)
	}
}

// TestUserDLRGateRoundTrip checks the per-user policy survives the console's
// create/read cycle, since that is the switch an operator actually flips.
func TestUserDLRGateRoundTrip(t *testing.T) {
	f := newWebFixture(t)

	body := `{"username":"demoesme","password":"s3cret","dlr_gate_enabled":true,` +
		`"dlr_gate_hit_status":"DELIVRD","dlr_gate_miss_status":"REJECTD","dlr_gate_miss_error":"008"}`
	f.do("POST", "/api/users", body, http.StatusCreated, nil)

	var fetched userResource
	f.do("GET", "/api/users/demoesme", "", http.StatusOK, &fetched)
	if !fetched.DLRGateEnabled {
		t.Fatal("dlr_gate_enabled did not survive the round trip")
	}
	if fetched.DLRGateHitStatus != "DELIVRD" || fetched.DLRGateMissStatus != "REJECTD" || fetched.DLRGateMissError != "008" {
		t.Fatalf("gate fields = %+v, want the submitted values", fetched)
	}
}

// TestUserWithoutDLRGateStoresNoBlock keeps the spec of an untouched user
// identical to one written before this feature existed.
func TestUserWithoutDLRGateStoresNoBlock(t *testing.T) {
	f := newWebFixture(t)
	f.do("POST", "/api/users", `{"username":"plainuser","password":"s3cret"}`, http.StatusCreated, nil)

	var fetched userResource
	f.do("GET", "/api/users/plainuser", "", http.StatusOK, &fetched)
	if fetched.DLRGateEnabled || fetched.DLRGateHitStatus != "" || fetched.DLRGateMissStatus != "" {
		t.Fatalf("an untouched user carries gate fields: %+v", fetched)
	}
}

// TestUserDLRGateMintsCredentialOnEnable covers the credential lifecycle the
// console drives: enabling the gate issues a URL and a token, the token is shown
// exactly once, an ordinary edit keeps it, and a rotation replaces it.
func TestUserDLRGateMintsCredentialOnEnable(t *testing.T) {
	f := newWebFixture(t)

	var created userResource
	f.do("POST", "/api/users",
		`{"username":"demoesme","password":"s3cret","dlr_gate_enabled":true}`,
		http.StatusCreated, &created)

	if created.DLRGateKeyID == "" {
		t.Fatal("enabling the gate did not mint a key id")
	}
	if created.DLRGateToken == "" || created.DLRGateTokenNotice == "" {
		t.Fatal("the create response did not carry the one-time token")
	}
	firstKeyID, firstToken := created.DLRGateKeyID, created.DLRGateToken

	// Re-reading must never hand the token back out.
	var fetched userResource
	f.do("GET", "/api/users/demoesme", "", http.StatusOK, &fetched)
	if fetched.DLRGateToken != "" {
		t.Fatal("the token is retrievable on a plain read")
	}
	if fetched.DLRGateKeyID != firstKeyID {
		t.Fatalf("key id = %q, want the minted %q", fetched.DLRGateKeyID, firstKeyID)
	}

	// An unrelated edit must not invalidate a partner's live credential.
	var edited userResource
	f.do("PUT", "/api/users/demoesme",
		`{"username":"demoesme","dlr_gate_enabled":true,"dlr_gate_miss_status":"UNDELIV"}`,
		http.StatusOK, &edited)
	if edited.DLRGateKeyID != firstKeyID {
		t.Fatalf("an ordinary edit rotated the key: %q -> %q", firstKeyID, edited.DLRGateKeyID)
	}
	if edited.DLRGateToken != "" {
		t.Fatal("an ordinary edit re-issued a token")
	}

	// An explicit rotation replaces both halves.
	var rotated userResource
	f.do("PUT", "/api/users/demoesme",
		`{"username":"demoesme","dlr_gate_enabled":true,"dlr_gate_rotate_token":true}`,
		http.StatusOK, &rotated)
	if rotated.DLRGateKeyID == firstKeyID {
		t.Fatal("rotation kept the old key id")
	}
	if rotated.DLRGateToken == "" || rotated.DLRGateToken == firstToken {
		t.Fatal("rotation did not issue a new token")
	}
}

func TestUserWithoutDLRGateMintsNothing(t *testing.T) {
	f := newWebFixture(t)
	var created userResource
	f.do("POST", "/api/users", `{"username":"plainuser","password":"s3cret"}`, http.StatusCreated, &created)
	if created.DLRGateKeyID != "" || created.DLRGateToken != "" {
		t.Fatalf("a user with no gate got a credential: %+v", created)
	}
}
