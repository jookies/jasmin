package routingfilter

import (
	"testing"
	"time"
)

func TestEvalPyFilterRED(t *testing.T) {
	// Setup a routable similar to the one in Python fixtures
	// Note: We need to decide how to represent UID/GID since Jasmin Go uses int64
	// and Jasmin Python uses strings for some of these.
	// For now, we'll use string-converted values or just match what's in filter.go
	routable, err := NewRoutable(RoutableInput{
		Direction:   MT,
		ConnectorID: "abc",
		UserID:      101, // Mapping 'uid1' to 101 for test
		GroupID:     201, // Mapping 'group1' to 201
		SourceAddr:  BytesField{Present: true, Value: []byte("123")},
		ShortMessage: BytesField{Present: true, Value: []byte("hello")},
		Timestamp:   time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Tags:        []string{"initial"},
	})
	if err != nil {
		t.Fatalf("Failed to create routable: %v", err)
	}

	tests := []struct {
		name    string
		pyCode  string
		want    bool
		wantErr bool
	}{
		{"True", "result = True", true, false},
		{"False", "result = False", false, false},
		{"ConnectorCID", "result = routable.connector.cid == 'abc'", true, false},
		{"UserUID", "result = routable.user.uid == '101'", true, false},
		{"GroupGID", "result = routable.user.group.gid == '201'", true, false},
		{"SourceAddr", "result = routable.pdu.params['source_addr'] == b'123'", true, false},
		{"ShortMessage", "result = routable.pdu.params['short_message'] == b'hello'", true, false},
		{"Tags", "routable.addTag('captured'); result = routable.hasTag('captured')", true, false},
		{"DatetimeYear", "result = routable.datetime.year > 2000", true, false},
		{"ComplexLogic", "if routable.user.uid == '101' and routable.connector.cid == 'abc':\n    result = True\nelse:\n    result = False", true, false},
		{"DivisionByZero", "result = 1 / 0", false, true},
		{"SyntaxError", "invalid syntax", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// NewEvalPyFilter doesn't exist yet, so this will fail to compile
			f, err := NewEvalPyFilter(tt.pyCode)
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("NewEvalPyFilter() unexpected error: %v", err)
				}
				return
			}

			got, err := f.Match(routable)
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("Match() unexpected error: %v", err)
				}
				return
			}

			if got != tt.want {
				t.Errorf("Match() got = %v, want %v", got, tt.want)
			}
		})
	}
}
