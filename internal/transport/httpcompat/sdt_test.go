package httpcompat

import (
	"testing"
	"time"
)

// The `sdt` argument schedules a message for later delivery. parseLegacyTime was
// previously a stub: it checked only that the string was at least 15 characters
// and then returned time.Now(), so every valid-looking value scheduled the
// message for immediately. The value did reach ScheduleAt intact, so scheduling
// worked -- a customer asking for 03:00 silently got delivery now, on a feature
// they were billed for. These tests exist so that cannot regress.
func TestParseLegacyTimeAbsolute(t *testing.T) {
	// 2026-07-30 12:34:56.0 UTC, zero offset.
	got, err := parseLegacyTime("260730123456000+")
	if err != nil {
		t.Fatalf("absolute time rejected: %v", err)
	}
	want := time.Date(2026, 7, 30, 12, 34, 56, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
	if got.Equal(time.Now().Truncate(time.Hour)) {
		t.Error("parsed value tracks the clock; the stub behaviour is back")
	}
}

func TestParseLegacyTimeAbsoluteAppliesQuarterHourOffset(t *testing.T) {
	// Same wall clock, +02:00 (eight quarter-hours), so 10:34:56 UTC.
	got, err := parseLegacyTime("260730123456008+")
	if err != nil {
		t.Fatalf("offset time rejected: %v", err)
	}
	want := time.Date(2026, 7, 30, 10, 34, 56, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	negative, err := parseLegacyTime("260730123456008-")
	if err != nil {
		t.Fatalf("negative offset rejected: %v", err)
	}
	if wantNeg := time.Date(2026, 7, 30, 14, 34, 56, 0, time.UTC); !negative.Equal(wantNeg) {
		t.Errorf("negative offset got %s, want %s", negative.Format(time.RFC3339), wantNeg.Format(time.RFC3339))
	}
}

// A relative value legitimately depends on the current time: the digits are an
// offset from now rather than a calendar date.
func TestParseLegacyTimeRelative(t *testing.T) {
	before := time.Now().UTC()
	got, err := parseLegacyTime("000000020000000R") // +2 hours
	if err != nil {
		t.Fatalf("relative time rejected: %v", err)
	}
	delta := got.Sub(before)
	if delta < 119*time.Minute || delta > 121*time.Minute {
		t.Errorf("relative +2h produced a %s offset", delta)
	}
}

func TestParseLegacyTimeRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"too short":             "26073012345",
		"fifteen chars":         "260730123456000",
		"eighteen chars":        "260730123456000+XX",
		"non-digit in the body": "2607301234X6000+",
		"bad final character":   "260730123456000Z",
		"month out of range":    "261330123456000+",
		"day out of range":      "260732123456000+",
		"hour out of range":     "260730253456000+",
		"empty":                 "",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseLegacyTime(value); err == nil {
				t.Errorf("parseLegacyTime(%q) was accepted; a bad sdt must be "+
					"rejected rather than silently scheduled for now", value)
			}
		})
	}
}
