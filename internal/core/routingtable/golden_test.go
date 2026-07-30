package routingtable_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

type document struct {
	SchemaVersion  int       `json:"schema_version"`
	BaselineCommit string    `json:"baseline_commit"`
	Cases          []fixture `json:"cases"`
}
type fixture struct {
	ID         string      `json:"id"`
	Direction  string      `json:"direction"`
	Operations []operation `json:"operations"`
	Query      *query      `json:"query"`
	Expected   expected    `json:"expected"`
}
type operation struct {
	Op    string    `json:"op"`
	Order int       `json:"order"`
	Route routeSpec `json:"route"`
}
type routeSpec struct {
	Kind      string        `json:"kind"`
	Direction string        `json:"direction"`
	Connector connectorSpec `json:"connector"`
	Filters   []filterSpec  `json:"filters"`
	Rate      float64       `json:"rate"`
}
type connectorSpec struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}
type filterSpec struct {
	Type    string `json:"type"`
	UserID  int64  `json:"user_id"`
	Pattern string `json:"pattern"`
}
type query struct {
	Direction   string `json:"direction"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	UserID      int64  `json:"user_id"`
}
type selected struct {
	ConnectorID string  `json:"connector_id"`
	Rate        float64 `json:"rate"`
}
type expected struct {
	Orders        []int     `json:"orders"`
	RemoveResults []bool    `json:"remove_results"`
	Selected      *selected `json:"selected"`
	ErrorType     *string   `json:"error_type"`
}

func TestGoldenStaticRoutingTables(t *testing.T) {
	doc := load(t)
	if len(doc.Cases) != 23 {
		t.Fatalf("cases=%d", len(doc.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range doc.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			if seen[tc.ID] {
				t.Fatalf("duplicate %s", tc.ID)
			}
			seen[tc.ID] = true
			builder, err := routingtable.NewBuilder(routingfilter.Direction(tc.Direction))
			if err != nil {
				t.Fatal(err)
			}
			removes := []bool{}
			var gotErr error
			for _, op := range tc.Operations {
				switch op.Op {
				case "add":
					r, err := buildRoute(op.Route)
					if err != nil {
						t.Fatal(err)
					}
					gotErr = builder.Add(op.Order, r)
				case "remove":
					removes = append(removes, builder.Remove(op.Order))
				case "flush":
					builder.Flush()
				default:
					t.Fatalf("unknown op %q", op.Op)
				}
				if gotErr != nil {
					break
				}
			}
			if tc.Expected.ErrorType != nil {
				if *tc.Expected.ErrorType != "InvalidRoutingTableParameterError" || !errors.Is(gotErr, routingtable.ErrInvalidTableParameter) {
					t.Fatalf("error=%v want=%v", gotErr, *tc.Expected.ErrorType)
				}
			} else if gotErr != nil {
				t.Fatal(gotErr)
			}
			if got := builder.Orders(); !reflect.DeepEqual(got, tc.Expected.Orders) {
				t.Fatalf("orders=%v want=%v", got, tc.Expected.Orders)
			}
			if !reflect.DeepEqual(removes, tc.Expected.RemoveResults) {
				t.Fatalf("removes=%v want=%v", removes, tc.Expected.RemoveResults)
			}
			if gotErr == nil && tc.Query != nil {
				table := builder.Build()
				route, found, err := table.Select(buildRoutable(t, *tc.Query))
				if err != nil {
					t.Fatal(err)
				}
				if tc.Expected.Selected == nil {
					if found {
						t.Fatalf("unexpected route %+v", route)
					}
				} else {
					if !found || route.Connector().ID() != tc.Expected.Selected.ConnectorID || route.Rate() != tc.Expected.Selected.Rate {
						t.Fatalf("selected=(%+v,%v) want=%+v", route, found, *tc.Expected.Selected)
					}
				}
			}
		})
	}
	if len(seen) != 23 {
		t.Fatalf("seen=%d", len(seen))
	}
}

func TestBuiltTableIsSnapshot(t *testing.T) {
	b, _ := routingtable.NewBuilder(routingfilter.MT)
	f := routingfilter.NewTransparentFilter()
	r, _ := routingtable.NewStaticRoute(routingfilter.MT, routingtable.Connector{IDValue: "a", TypeValue: routingtable.SMPPC}, 0, f)
	if err := b.Add(1, r); err != nil {
		t.Fatal(err)
	}
	table := b.Build()
	b.Remove(1)
	_, found, err := table.Select(buildRoutable(t, query{Direction: "mt"}))
	if err != nil || !found {
		t.Fatalf("snapshot=(%v,%v)", found, err)
	}
}

func TestCapsAndValidation(t *testing.T) {
	b, _ := routingtable.NewBuilder(routingfilter.MT)
	f := routingfilter.NewTransparentFilter()
	filters := make([]routingfilter.Filter, routingtable.MaxFiltersPerRoute+1)
	for i := range filters {
		filters[i] = f
	}
	if _, err := routingtable.NewStaticRoute(routingfilter.MT, routingtable.Connector{IDValue: "a", TypeValue: routingtable.SMPPC}, 0, filters...); !errors.Is(err, routingtable.ErrTooManyFilters) {
		t.Fatalf("filters err=%v", err)
	}
	r, _ := routingtable.NewStaticRoute(routingfilter.MT, routingtable.Connector{IDValue: "a", TypeValue: routingtable.SMPPC}, 0, f)
	if err := b.Add(-1, r); !errors.Is(err, routingtable.ErrInvalidTableParameter) {
		t.Fatalf("order err=%v", err)
	}
}

func FuzzBuilderNeverPanics(f *testing.F) {
	f.Add(1, "a")
	f.Add(-1, "b")
	f.Fuzz(func(t *testing.T, order int, id string) {
		b, _ := routingtable.NewBuilder(routingfilter.MT)
		r, err := routingtable.NewStaticRoute(routingfilter.MT, routingtable.Connector{IDValue: id, TypeValue: routingtable.SMPPC}, 0, routingfilter.NewTransparentFilter())
		if err == nil {
			_ = b.Add(order, r)
			_ = b.Build()
		}
	})
}

func buildRoute(s routeSpec) (routingtable.Route, error) {
	c := routingtable.Connector{IDValue: s.Connector.ID, TypeValue: routingtable.ConnectorType(s.Connector.Type)}
	if s.Kind == "default" {
		return routingtable.NewDefaultRoute(c, s.Rate)
	}
	fs := make([]routingfilter.Filter, 0, len(s.Filters))
	for _, x := range s.Filters {
		var f routingfilter.Filter
		var err error
		switch x.Type {
		case "transparent":
			f = routingfilter.NewTransparentFilter()
		case "user":
			f = routingfilter.NewUserFilter(x.UserID)
		case "source":
			f, err = routingfilter.NewSourceAddrFilter(x.Pattern)
		case "destination":
			f, err = routingfilter.NewDestinationAddrFilter(x.Pattern)
		}
		if err != nil {
			return routingtable.Route{}, err
		}
		fs = append(fs, f)
	}
	return routingtable.NewStaticRoute(routingfilter.Direction(s.Direction), c, s.Rate, fs...)
}
func buildRoutable(t *testing.T, q query) routingfilter.Routable {
	t.Helper()
	r, err := routingfilter.NewRoutable(routingfilter.RoutableInput{Direction: routingfilter.Direction(q.Direction), UserID: q.UserID, SourceAddr: routingfilter.BytesField{Present: true, Value: []byte(q.Source)}, DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(q.Destination)}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func load(t *testing.T) document {
	t.Helper()
	p := filepath.Join("..", "..", "..", "compat", "fixtures", "routing-tables", "baseline.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d.SchemaVersion != 1 || d.BaselineCommit != "0aac58e466d583d0f0436df7b8afa3dc96191263" {
		t.Fatal("provenance")
	}
	return d
}
