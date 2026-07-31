package msgspool

import (
	"errors"
	"testing"
	"time"
)

func TestScopeNormalizeAndValidate(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		scope   Scope
		want    []string
		wantErr error
	}{
		{
			name:  "trims deduplicates and orders",
			scope: Scope{Connectors: []string{" partner-b ", "partner-a", "partner-b", ""}},
			want:  []string{"partner-a", "partner-b"},
		},
		{
			// The one place this package deviates from "zero value means no
			// filter": an empty allow-list must deny, not admit everything.
			name:    "empty denies rather than admitting everything",
			scope:   Scope{},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "whitespace-only is empty",
			scope:   Scope{Connectors: []string{"  ", ""}},
			wantErr: ErrInvalidInput,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			normalized := testCase.scope.Normalize()
			err := normalized.Validate()
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("Validate()=%v want %v", err, testCase.wantErr)
			}
			if testCase.wantErr != nil {
				return
			}
			if len(normalized.Connectors) != len(testCase.want) {
				t.Fatalf("connectors=%v want %v", normalized.Connectors, testCase.want)
			}
			for index, connector := range testCase.want {
				if normalized.Connectors[index] != connector {
					t.Fatalf("connectors=%v want %v", normalized.Connectors, testCase.want)
				}
			}
		})
	}
}

func TestScopeValidateRefusesOversizedAllowList(t *testing.T) {
	connectors := make([]string, maxScopeConnectors+1)
	for index := range connectors {
		connectors[index] = string(rune('a'+index%26)) + time.Duration(index).String()
	}
	if err := (Scope{Connectors: connectors}).Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized scope error=%v", err)
	}
}

func TestScopeCompileRestrictsToTheAllowList(t *testing.T) {
	scope := Scope{Connectors: []string{"partner-b", "partner-a"}, IncludeText: true}

	request, err := scope.Compile(PullRequest{Cursor: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.ConnectorIDs) != 2 ||
		request.ConnectorIDs[0] != "partner-a" || request.ConnectorIDs[1] != "partner-b" {
		t.Fatalf("connector predicate=%v", request.ConnectorIDs)
	}
	if !request.IncludeContent {
		t.Fatal("include_text scope did not request content")
	}
	// The whole point of the field: a pull consumer must never be handed a
	// per-segment receipt row it would store as the message.
	if request.IncludeReceiptOnly {
		t.Fatal("pull request admitted receipt-only rows")
	}
	if request.Limit != DefaultPullLimit || request.Cursor != "abc" {
		t.Fatalf("request=%+v", request)
	}
}

func TestScopeCompileNarrowsToOneConnectorInsideTheScope(t *testing.T) {
	scope := Scope{Connectors: []string{"partner-a", "partner-b"}}
	request, err := scope.Compile(PullRequest{ConnectorID: "partner-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.ConnectorIDs) != 1 || request.ConnectorIDs[0] != "partner-b" {
		t.Fatalf("connector predicate=%v", request.ConnectorIDs)
	}
	if request.IncludeContent {
		t.Fatal("a scope without include_text requested content")
	}
}

// Asking for a connector outside the scope is refused rather than answered with
// an empty page: on this API an empty page means "no new messages", and a
// consumer that could not tell the two apart would wait forever for traffic it
// will never be shown.
func TestScopeCompileRefusesAConnectorOutsideTheScope(t *testing.T) {
	scope := Scope{Connectors: []string{"partner-a"}}
	if _, err := scope.Compile(PullRequest{ConnectorID: "partner-b"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("out-of-scope connector error=%v", err)
	}
}

func TestScopeCompileValidatesTheRequest(t *testing.T) {
	scope := Scope{Connectors: []string{"partner-a"}}
	early := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	for _, testCase := range []struct {
		name    string
		request PullRequest
	}{
		{name: "negative limit", request: PullRequest{Limit: -1}},
		{name: "limit above the ceiling", request: PullRequest{Limit: MaxPullLimit + 1}},
		{name: "unknown delivery state", request: PullRequest{DeliveryState: "shipped"}},
		{name: "inverted time window", request: PullRequest{ReceivedFrom: &late, ReceivedTo: &early}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := scope.Compile(testCase.request); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v want ErrInvalidInput", err)
			}
		})
	}
}

func TestScopeDescribeNamesWhatWasEnforced(t *testing.T) {
	scope := Scope{Connectors: []string{"partner-b", "partner-a"}, IncludeText: true}
	if got := scope.Describe(); got != "connectors=[partner-a|partner-b],text=true" {
		t.Fatalf("Describe()=%q", got)
	}
}
