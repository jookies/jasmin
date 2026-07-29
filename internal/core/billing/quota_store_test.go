package billing

import (
	"context"
	"errors"
	"testing"
)

func floatPtr(value float64) *float64 { return &value }
func intPtr(value int) *int           { return &value }

// TestQuotaRecordRestorePrecedence pins the boot precedence rule: a durable row
// wins while the provisioned baseline it was written with still matches, and
// loses to a changed baseline, which is how an operator tops an account up.
func TestQuotaRecordRestorePrecedence(t *testing.T) {
	cases := []struct {
		name        string
		record      QuotaRecord
		provisioned Quota
		want        Quota
	}{
		{
			name:        "plain restart keeps the spent balance",
			record:      QuotaRecord{Live: Quota{Balance: floatPtr(900)}, Provisioned: Quota{Balance: floatPtr(1000)}},
			provisioned: Quota{Balance: floatPtr(1000)},
			want:        Quota{Balance: floatPtr(900)},
		},
		{
			name:        "changed grant tops the account up",
			record:      QuotaRecord{Live: Quota{Balance: floatPtr(900)}, Provisioned: Quota{Balance: floatPtr(1000)}},
			provisioned: Quota{Balance: floatPtr(2500)},
			want:        Quota{Balance: floatPtr(2500)},
		},
		{
			name:        "balance top-up does not reset submit_sm_count",
			record:      QuotaRecord{Live: Quota{Balance: floatPtr(900), SubmitSmCount: intPtr(40)}, Provisioned: Quota{Balance: floatPtr(1000), SubmitSmCount: intPtr(100)}},
			provisioned: Quota{Balance: floatPtr(2500), SubmitSmCount: intPtr(100)},
			want:        Quota{Balance: floatPtr(2500), SubmitSmCount: intPtr(40)},
		},
		{
			name:        "count top-up does not reset the balance",
			record:      QuotaRecord{Live: Quota{Balance: floatPtr(900), SubmitSmCount: intPtr(40)}, Provisioned: Quota{Balance: floatPtr(1000), SubmitSmCount: intPtr(100)}},
			provisioned: Quota{Balance: floatPtr(1000), SubmitSmCount: intPtr(500)},
			want:        Quota{Balance: floatPtr(900), SubmitSmCount: intPtr(500)},
		},
		{
			name:        "spent to zero is restored, not refunded",
			record:      QuotaRecord{Live: Quota{Balance: floatPtr(0)}, Provisioned: Quota{Balance: floatPtr(1000)}},
			provisioned: Quota{Balance: floatPtr(1000)},
			want:        Quota{Balance: floatPtr(0)},
		},
		{
			name:        "unlimited grant restores the unlimited row",
			record:      QuotaRecord{Live: Quota{}, Provisioned: Quota{}},
			provisioned: Quota{},
			want:        Quota{},
		},
		{
			name:        "granting a ceiling to a previously unlimited account wins",
			record:      QuotaRecord{Live: Quota{}, Provisioned: Quota{}},
			provisioned: Quota{Balance: floatPtr(50)},
			want:        Quota{Balance: floatPtr(50)},
		},
		{
			name:        "lifting the ceiling to unlimited wins",
			record:      QuotaRecord{Live: Quota{Balance: floatPtr(10)}, Provisioned: Quota{Balance: floatPtr(50)}},
			provisioned: Quota{},
			want:        Quota{},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.record.Restore(testCase.provisioned)
			assertQuota(t, got, testCase.want)
		})
	}
}

// TestQuotaRecordRestoreDetachesPointers proves a restored quota never aliases
// the config struct it came from, so provisioning cannot be mutated in place.
func TestQuotaRecordRestoreDetachesPointers(t *testing.T) {
	provisioned := Quota{Balance: floatPtr(1000), SubmitSmCount: intPtr(10)}
	record := QuotaRecord{Live: Quota{Balance: floatPtr(900)}, Provisioned: Quota{Balance: floatPtr(1000)}}
	restored := record.Restore(provisioned)
	*provisioned.Balance = 1
	*provisioned.SubmitSmCount = 1
	if *restored.Balance != 900 {
		t.Fatalf("restored balance=%v want=900", *restored.Balance)
	}
	if *restored.SubmitSmCount != 10 {
		t.Fatalf("restored submit_sm_count=%v want=10", *restored.SubmitSmCount)
	}
}

// TestQuotaIndexTakeIsOneShot pins the rule that confines restoration to a
// key's first provisioning: boot restores, a later admin edit of the same
// account installs what the operator supplied.
func TestQuotaIndexTakeIsOneShot(t *testing.T) {
	index := NewQuotaIndex([]QuotaRecord{{
		Scope: QuotaScopeUser, Key: "alice",
		Live:        Quota{Balance: floatPtr(900)},
		Provisioned: Quota{Balance: floatPtr(1000)},
	}})
	provisioned := Quota{Balance: floatPtr(1000)}

	first := index.Take(QuotaScopeUser, "alice", provisioned)
	if first.Balance == nil || *first.Balance != 900 {
		t.Fatalf("first take=%v want=900", first.Balance)
	}
	second := index.Take(QuotaScopeUser, "alice", provisioned)
	if second.Balance == nil || *second.Balance != 1000 {
		t.Fatalf("second take=%v want the provisioned 1000", second.Balance)
	}
	unknown := index.Take(QuotaScopeUser, "bob", provisioned)
	if unknown.Balance == nil || *unknown.Balance != 1000 {
		t.Fatalf("unknown key take=%v want the provisioned 1000", unknown.Balance)
	}
	group := index.Take(QuotaScopeGroup, "alice", provisioned)
	if group.Balance == nil || *group.Balance != 1000 {
		t.Fatalf("group scope take=%v want the provisioned 1000", group.Balance)
	}
}

func TestLoadQuotaIndex(t *testing.T) {
	t.Run("nil store yields an empty index", func(t *testing.T) {
		index, err := LoadQuotaIndex(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := index.Take(QuotaScopeUser, "alice", Quota{Balance: floatPtr(7)})
		if got.Balance == nil || *got.Balance != 7 {
			t.Fatalf("take=%v want the provisioned 7", got.Balance)
		}
	})
	t.Run("store failure is wrapped", func(t *testing.T) {
		want := errors.New("connection refused")
		if _, err := LoadQuotaIndex(context.Background(), &memoryQuotaStore{loadErr: want}); !errors.Is(err, want) {
			t.Fatalf("err=%v want=%v", err, want)
		}
	})
}

func assertQuota(t *testing.T, got, want Quota) {
	t.Helper()
	if !sameFloat(got.Balance, want.Balance) {
		t.Fatalf("balance=%s want=%s", formatFloat(got.Balance), formatFloat(want.Balance))
	}
	if !sameInt(got.SubmitSmCount, want.SubmitSmCount) {
		t.Fatalf("submit_sm_count=%s want=%s", formatInt(got.SubmitSmCount), formatInt(want.SubmitSmCount))
	}
}
