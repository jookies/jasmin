package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigRejectsUnknownAndTrailingContent(t *testing.T) {
	base := `{"listen_address":"127.0.0.1:1","amqp_url":"amqp://localhost/","users":[],"routes":[]}`
	for name, content := range map[string]string{
		"unknown":  `{"listen_address":"127.0.0.1:1","amqp_url":"amqp://localhost/","unknown":true,"users":[],"routes":[]}`,
		"trailing": base + ` {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("LoadConfig accepted malformed configuration")
			}
		})
	}
}

func TestRuntimeDirectoryRejectsIdentityThatCannotBePickledAsLegacyUser(t *testing.T) {
	digest := sha256.Sum256([]byte("secret"))
	for name, user := range map[string]UserConfig{
		"username-too-long": {Username: "sixteen-characters", ExternalID: "valid-id", PasswordSHA256: hex.EncodeToString(digest[:])},
		"invalid-uid":       {Username: "alice", ExternalID: "not valid", PasswordSHA256: hex.EncodeToString(digest[:])},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newRuntimeDirectory(Config{Users: []UserConfig{user}}); err == nil {
				t.Fatal("legacy-incompatible identity accepted")
			}
		})
	}
}

func TestRuntimeDirectoryUsesOpaqueIdentityAndConstantTimeDigestComparison(t *testing.T) {
	digest := sha256.Sum256([]byte("secret"))
	balance := 10.0
	count := 2
	directory, err := newRuntimeDirectory(Config{Users: []UserConfig{{
		Username:       "alice",
		ExternalID:     "opaque-user-id",
		PasswordSHA256: hex.EncodeToString(digest[:]),
		Balance:        &balance,
		SubmitSMCount:  &count,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := directory.Authenticate(context.Background(), "alice", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := directory.Authenticate(context.Background(), "alice", "wrong"); err == nil {
		t.Fatal("invalid password accepted")
	}
	_, externalID, err := directory.users.GetUserIdentity("alice")
	if err != nil {
		t.Fatal(err)
	}
	if externalID != "opaque-user-id" {
		t.Fatalf("external ID=%q", externalID)
	}
}

func TestConnectorSelectorUsesOrderedAvailabilityAndExhausts(t *testing.T) {
	routes := []RouteConfig{{ConnectorIDs: []string{"first", "second", "third"}, Rate: 1, Default: true}}
	_, connectorIDs, _, err := buildRoutes(routes)
	if err != nil {
		t.Fatal(err)
	}
	if len(connectorIDs) != 3 {
		t.Fatalf("declared connectors=%v", connectorIDs)
	}
	selector := connectorSelector(routes, func(connectorID string) bool { return connectorID != "first" })
	if selected, ok := selector("first"); !ok || selected != "second" {
		t.Fatalf("selected=(%q,%v)", selected, ok)
	}
	selector = connectorSelector(routes, func(string) bool { return false })
	if selected, ok := selector("first"); ok || selected != "" {
		t.Fatalf("exhaustion=(%q,%v)", selected, ok)
	}
}
