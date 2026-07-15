package routingfilter_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

const baselineCommit = "0aac58e466d583d0f0436df7b8afa3dc96191263"

type goldenDocument struct {
	SchemaVersion  int                 `json:"schema_version"`
	BaselineCommit string              `json:"baseline_commit"`
	Compatibility  map[string][]string `json:"compatibility"`
	Cases          []goldenCase        `json:"cases"`
}

type goldenCase struct {
	ID       string         `json:"id"`
	Filter   goldenFilter   `json:"filter"`
	Routable goldenRoutable `json:"routable"`
	Expected goldenExpected `json:"expected"`
}

type goldenFilter struct {
	Type        string          `json:"type"`
	ConnectorID string          `json:"connector_id"`
	UserID      int64           `json:"user_id"`
	GroupID     int64           `json:"group_id"`
	Pattern     string          `json:"pattern"`
	Start       string          `json:"start"`
	End         string          `json:"end"`
	Tag         json.RawMessage `json:"tag"`
}

type goldenRoutable struct {
	Direction       string       `json:"direction"`
	ConnectorID     string       `json:"connector_id"`
	UserID          int64        `json:"user_id"`
	GroupID         int64        `json:"group_id"`
	SourceAddr      *goldenBytes `json:"source_addr"`
	DestinationAddr *goldenBytes `json:"destination_addr"`
	ShortMessage    *goldenBytes `json:"short_message"`
	MessagePayload  *goldenBytes `json:"message_payload"`
	Timestamp       string       `json:"timestamp"`
	Tags            []string     `json:"tags"`
}

type goldenBytes struct {
	Base64 string `json:"base64"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

type goldenExpected struct {
	Matched   *bool   `json:"matched"`
	ErrorType *string `json:"error_type"`
}

func TestGoldenRoutingFilterCompatibility(t *testing.T) {
	document := loadGolden(t)
	if len(document.Cases) != 36 {
		t.Fatalf("case count = %d, want 36", len(document.Cases))
	}
	seen := make(map[string]struct{}, len(document.Cases))
	for _, fixture := range document.Cases {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			if _, duplicate := seen[fixture.ID]; duplicate {
				t.Fatalf("duplicate fixture %q", fixture.ID)
			}
			seen[fixture.ID] = struct{}{}
			filter := buildFilter(t, fixture.Filter)
			routable := buildRoutable(t, fixture.Routable)
			matched, err := filter.Match(routable)
			if fixture.Expected.ErrorType == nil {
				if err != nil {
					t.Fatal(err)
				}
				if fixture.Expected.Matched == nil || matched != *fixture.Expected.Matched {
					t.Fatalf("matched = %v, want %v", matched, fixture.Expected.Matched)
				}
			} else {
				if *fixture.Expected.ErrorType != "KeyError" || !errors.Is(err, routingfilter.ErrMissingField) {
					t.Fatalf("error = %v, want %s", err, *fixture.Expected.ErrorType)
				}
			}
		})
	}
	if len(seen) != 36 {
		t.Fatalf("seen = %d, want 36", len(seen))
	}

	for kind, want := range document.Compatibility {
		filter := buildFilter(t, minimalFilter(kind))
		gotDirections := filter.Directions()
		got := make([]string, len(gotDirections))
		for index, direction := range gotDirections {
			got[index] = string(direction)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s directions = %v, want %v", kind, got, want)
		}
	}
}

func TestRoutableDefensiveCopies(t *testing.T) {
	source := []byte("abc")
	tags := []string{"blue"}
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:  routingfilter.MT,
		SourceAddr: routingfilter.BytesField{Present: true, Value: source},
		Tags:       tags,
	})
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'x'
	tags[0] = "red"
	filter, err := routingfilter.NewSourceAddrFilter("^abc$")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := filter.Match(routable)
	if err != nil || !matched {
		t.Fatalf("match = (%v,%v)", matched, err)
	}
	tag, err := routingfilter.NewTagFilter("blue")
	if err != nil {
		t.Fatal(err)
	}
	matched, err = tag.Match(routable)
	if err != nil || !matched {
		t.Fatalf("tag match = (%v,%v)", matched, err)
	}
}

func TestValidationAndCaps(t *testing.T) {
	if _, err := routingfilter.NewSourceAddrFilter("(?=unsupported)"); !errors.Is(err, routingfilter.ErrInvalidPattern) {
		t.Fatalf("lookahead error = %v", err)
	}
	if _, err := routingfilter.NewSourceAddrFilter(string(make([]byte, routingfilter.MaxPatternBytes+1))); !errors.Is(err, routingfilter.ErrValueTooLarge) {
		t.Fatalf("pattern cap error = %v", err)
	}
	if _, err := routingfilter.NewRoutable(routingfilter.RoutableInput{Direction: "invalid"}); !errors.Is(err, routingfilter.ErrInvalidDirection) {
		t.Fatalf("direction error = %v", err)
	}
	if _, err := routingfilter.NewRoutable(routingfilter.RoutableInput{Direction: routingfilter.MT, SourceAddr: routingfilter.BytesField{Present: true, Value: make([]byte, routingfilter.MaxFieldBytes+1)}}); !errors.Is(err, routingfilter.ErrValueTooLarge) {
		t.Fatalf("field cap error = %v", err)
	}
	if _, err := routingfilter.NewDateIntervalFilter("2024-02-30", "2024-03-01"); !errors.Is(err, routingfilter.ErrInvalidInterval) {
		t.Fatalf("date error = %v", err)
	}
	if _, err := routingfilter.NewTagFilter(string([]byte{0xff})); !errors.Is(err, routingfilter.ErrInvalidValue) {
		t.Fatalf("tag UTF-8 error = %v", err)
	}
	if _, err := routingfilter.NewRoutable(routingfilter.RoutableInput{Direction: routingfilter.MT, ConnectorID: string([]byte{0xff})}); !errors.Is(err, routingfilter.ErrInvalidValue) {
		t.Fatalf("connector UTF-8 error = %v", err)
	}
}

func TestRegexUsesPythonMatchStartPosition(t *testing.T) {
	filter, err := routingfilter.NewSourceAddrFilter("20")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value string
		want  bool
	}{{"2020", true}, {"x20", false}} {
		routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{Direction: routingfilter.MT, SourceAddr: routingfilter.BytesField{Present: true, Value: []byte(tc.value)}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := filter.Match(routable)
		if err != nil || got != tc.want {
			t.Fatalf("match(%q) = (%v,%v), want %v", tc.value, got, err, tc.want)
		}
	}
}

func FuzzRegexFilterNeverPanics(f *testing.F) {
	f.Add("^hello.*$", []byte("hello world"))
	f.Add("^A�B$", []byte{'A', 0xff, 'B'})
	f.Fuzz(func(t *testing.T, pattern string, value []byte) {
		filter, err := routingfilter.NewSourceAddrFilter(pattern)
		if err != nil {
			return
		}
		routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{Direction: routingfilter.MT, SourceAddr: routingfilter.BytesField{Present: true, Value: value}})
		if err != nil {
			return
		}
		_, _ = filter.Match(routable)
	})
}

func buildFilter(t *testing.T, fixture goldenFilter) routingfilter.Filter {
	t.Helper()
	var filter routingfilter.Filter
	var err error
	switch fixture.Type {
	case "transparent":
		filter = routingfilter.NewTransparentFilter()
	case "connector":
		filter, err = routingfilter.NewConnectorFilter(fixture.ConnectorID)
	case "user":
		filter = routingfilter.NewUserFilter(fixture.UserID)
	case "group":
		filter = routingfilter.NewGroupFilter(fixture.GroupID)
	case "source_addr":
		filter, err = routingfilter.NewSourceAddrFilter(fixture.Pattern)
	case "destination_addr":
		filter, err = routingfilter.NewDestinationAddrFilter(fixture.Pattern)
	case "short_message":
		filter, err = routingfilter.NewShortMessageFilter(fixture.Pattern)
	case "date_interval":
		filter, err = routingfilter.NewDateIntervalFilter(fixture.Start, fixture.End)
	case "time_interval":
		filter, err = routingfilter.NewTimeIntervalFilter(fixture.Start, fixture.End)
	case "tag":
		var value any
		decoder := json.NewDecoder(bytes.NewReader(fixture.Tag))
		decoder.UseNumber()
		if err = decoder.Decode(&value); err == nil {
			switch typed := value.(type) {
			case string:
				filter, err = routingfilter.NewTagFilter(typed)
			case json.Number:
				filter, err = routingfilter.NewTagFilter(typed.String())
			default:
				err = errors.New("unsupported fixture tag")
			}
		}
	default:
		t.Fatalf("unknown filter type %q", fixture.Type)
	}
	if err != nil {
		t.Fatal(err)
	}
	return filter
}

func minimalFilter(kind string) goldenFilter {
	fixture := goldenFilter{Type: kind, ConnectorID: "abc", UserID: 1, GroupID: 100, Pattern: ".*", Start: "2024-01-01", End: "2024-01-02", Tag: json.RawMessage(`"x"`)}
	if kind == "time_interval" {
		fixture.Start, fixture.End = "00:00:00", "23:59:59"
	}
	return fixture
}

func buildRoutable(t *testing.T, fixture goldenRoutable) routingfilter.Routable {
	t.Helper()
	timestamp, err := time.Parse("2006-01-02T15:04:05", fixture.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.Direction(fixture.Direction),
		ConnectorID:     fixture.ConnectorID,
		UserID:          fixture.UserID,
		GroupID:         fixture.GroupID,
		SourceAddr:      decodeField(t, fixture.SourceAddr),
		DestinationAddr: decodeField(t, fixture.DestinationAddr),
		ShortMessage:    decodeField(t, fixture.ShortMessage),
		MessagePayload:  decodeField(t, fixture.MessagePayload),
		Timestamp:       timestamp,
		Tags:            fixture.Tags,
	})
	if err != nil {
		t.Fatal(err)
	}
	return routable
}

func decodeField(t *testing.T, descriptor *goldenBytes) routingfilter.BytesField {
	t.Helper()
	if descriptor == nil {
		return routingfilter.BytesField{}
	}
	raw, err := base64.StdEncoding.DecodeString(descriptor.Base64)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != descriptor.Length {
		t.Fatalf("fixture length = %d, want %d", len(raw), descriptor.Length)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != descriptor.SHA256 {
		t.Fatal("fixture SHA-256 mismatch")
	}
	return routingfilter.BytesField{Present: true, Value: raw}
}

func loadGolden(t *testing.T) goldenDocument {
	t.Helper()
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "routing-filters", "baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document goldenDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || document.BaselineCommit != baselineCommit {
		t.Fatalf("fixture provenance = (%d,%q)", document.SchemaVersion, document.BaselineCommit)
	}
	return document
}
