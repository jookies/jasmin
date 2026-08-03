package gateway

import "testing"

// TestCDRPartKeyMatchesAdmittedKeys pins the mapping from a queue message id to
// the cdr_records part key admission wrote.
//
// The regression it guards: the acceptance hook used to append "/%06d" to every
// id unconditionally. A single-part submit is enqueued under a bare aggregate id
// so that was right, but every segment of a concatenated submit is already
// enqueued under its own suffixed id — so the hook produced
// "<uuid>/000002/000001", matched no row, and left the CDR in ADMITTED with its
// terminal receipt refused forever.
func TestCDRPartKeyMatchesAdmittedKeys(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		messageID string
		want      string
	}{
		{"single part submit carries the bare aggregate id", "abc-123", "abc-123/000001"},
		{"first segment of a concatenated submit", "abc-123/000001", "abc-123/000001"},
		{"later segment keeps its own part number", "abc-123/000002", "abc-123/000002"},
		{"hundredth segment", "abc-123/000100", "abc-123/000100"},
		{"an id containing a slash but no part suffix", "abc/123", "abc/123/000001"},
		{"a suffix of the wrong width is not a part number", "abc-123/0001", "abc-123/0001/000001"},
		{"a non-numeric suffix is not a part number", "abc-123/00000x", "abc-123/00000x/000001"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := cdrPartKey(testCase.messageID); got != testCase.want {
				t.Errorf("cdrPartKey(%q) = %q, want %q", testCase.messageID, got, testCase.want)
			}
		})
	}
}
