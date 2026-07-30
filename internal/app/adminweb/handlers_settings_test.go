package adminweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/admin"
)

// recordingApplier stands in for the live runtime, so these tests can assert the
// order that matters: applied first, stored second.
type recordingApplier struct {
	applied []struct {
		Name  string
		Value int
	}
	err error
}

func (a *recordingApplier) ApplySetting(name string, value int) error {
	if a.err != nil {
		return a.err
	}
	a.applied = append(a.applied, struct {
		Name  string
		Value int
	}{name, value})
	return nil
}

func newSettingsFixture(t *testing.T) (*webFixture, *recordingApplier) {
	t.Helper()
	f := newWebFixture(t)
	applier := &recordingApplier{}
	service, err := admin.NewSettingsService(f.store, applier, func() string {
		return "2026-07-30T00:00:00Z"
	})
	if err != nil {
		t.Fatalf("new settings service: %v", err)
	}
	f.rebuildHandler(func(deps *Deps) {
		deps.Settings = service
		deps.BillingSettings = func() BillingSettings {
			return BillingSettings{
				Currency: "EUR", RetentionDays: 30, RetentionBatchSize: 500,
				MaintenanceIntervalSeconds: 86400, QuotaPersistIntervalSeconds: 10,
			}
		}
	})
	return f, applier
}

type settingsPayload struct {
	Settings         BillingSettings `json:"settings"`
	Overrides        map[string]int  `json:"overrides"`
	Editable         bool            `json:"editable"`
	EditableSettings []string        `json:"editable_settings"`
}

// An override must be applied to the running process, not merely stored — a
// stored-but-unapplied setting is the exact failure the read-only card avoided.
func TestSettingOverrideIsAppliedLiveAndThenReported(t *testing.T) {
	f, applier := newSettingsFixture(t)

	var before settingsPayload
	f.do("GET", "/api/billing/settings", "", http.StatusOK, &before)
	if !before.Editable || before.Settings.RetentionDays != 30 {
		t.Fatalf("before=%+v", before)
	}

	var after settingsPayload
	f.do("PUT", "/api/billing/settings", `{"name":"cdr_retention_days","value":7}`,
		http.StatusOK, &after)

	if len(applier.applied) != 1 || applier.applied[0].Value != 7 {
		t.Fatalf("the runtime was not told: %+v", applier.applied)
	}
	// The reported value is the one in force, with the override marked, so the
	// card can say it is overriding the file rather than disagreeing with it.
	if after.Settings.RetentionDays != 7 {
		t.Fatalf("reported retention=%d want the override", after.Settings.RetentionDays)
	}
	if after.Overrides["cdr_retention_days"] != 7 {
		t.Fatalf("overrides=%+v", after.Overrides)
	}
}

// Clearing hands the setting back to the configuration file.
func TestClearingAnOverrideRestoresTheConfiguredValue(t *testing.T) {
	f, applier := newSettingsFixture(t)
	f.do("PUT", "/api/billing/settings", `{"name":"cdr_retention_days","value":7}`, http.StatusOK, nil)

	var after settingsPayload
	f.do("PUT", "/api/billing/settings", `{"name":"cdr_retention_days","value":null}`,
		http.StatusOK, &after)

	if after.Settings.RetentionDays != 30 {
		t.Fatalf("retention=%d want the configured 30", after.Settings.RetentionDays)
	}
	if _, present := after.Overrides["cdr_retention_days"]; present {
		t.Fatalf("the override survived a clear: %+v", after.Overrides)
	}
	last := applier.applied[len(applier.applied)-1]
	if last.Value != 30 {
		t.Fatalf("the runtime was not restored to the configured value: %+v", last)
	}
}

// Currency is deliberately not overridable: it stamps new records only, so
// changing it mid-window splits a customer's usage across two units.
func TestCurrencyIsNotOverridable(t *testing.T) {
	f, applier := newSettingsFixture(t)
	f.do("PUT", "/api/billing/settings", `{"name":"cdr_currency","value":1}`,
		http.StatusBadRequest, nil)
	if len(applier.applied) != 0 {
		t.Fatalf("an unsupported setting reached the runtime: %+v", applier.applied)
	}

	var payload settingsPayload
	f.do("GET", "/api/billing/settings", "", http.StatusOK, &payload)
	for _, name := range payload.EditableSettings {
		if name == "cdr_currency" {
			t.Fatal("currency is advertised as editable")
		}
	}
}

// If the runtime refuses the value, nothing is stored — otherwise a restart
// would apply a setting the running process had already rejected.
func TestARefusedOverrideIsNotStored(t *testing.T) {
	f, applier := newSettingsFixture(t)
	applier.err = errors.New("the maintenance interval must be a positive number of seconds")

	f.do("PUT", "/api/billing/settings",
		`{"name":"cdr_maintenance_interval_seconds","value":0}`, http.StatusBadRequest, nil)

	var payload settingsPayload
	applier.err = nil
	f.do("GET", "/api/billing/settings", "", http.StatusOK, &payload)
	if len(payload.Overrides) != 0 {
		t.Fatalf("a refused override was persisted: %+v", payload.Overrides)
	}
}

// Without a settings service the card must report itself read-only rather than
// offering controls that cannot take effect.
func TestSettingsAreReadOnlyWithoutAService(t *testing.T) {
	f := newWebFixture(t)
	var payload settingsPayload
	f.do("GET", "/api/billing/settings", "", http.StatusOK, &payload)
	if payload.Editable {
		t.Fatal("settings claimed to be editable with no service configured")
	}
	f.do("PUT", "/api/billing/settings", `{"name":"cdr_retention_days","value":7}`,
		http.StatusServiceUnavailable, nil)
}

// Overrides survive a restart: the gateway re-applies them at boot, so the
// value an operator set is still in force rather than quietly reverting.
func TestStoredOverridesAreReadableForBootReapplication(t *testing.T) {
	f, _ := newSettingsFixture(t)
	f.do("PUT", "/api/billing/settings",
		`{"name":"quota_persist_interval_seconds","value":45}`, http.StatusOK, nil)

	service, err := admin.NewSettingsService(f.store, &recordingApplier{}, func() string {
		return "2026-07-30T00:00:00Z"
	})
	if err != nil {
		t.Fatal(err)
	}
	overrides, err := service.Overrides(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overrides["quota_persist_interval_seconds"] != 45 {
		t.Fatalf("overrides did not survive: %+v", overrides)
	}
	_ = json.Marshal
}
