package routepolicy_test

import (
	"encoding/json"
	"errors"
	"github.com/pumpitspace/synevyr/internal/core/routepolicy"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type doc struct {
	Cases []tc `json:"cases"`
}
type tc struct {
	ID        string  `json:"id"`
	Input     input   `json:"input"`
	Expected  any     `json:"expected"`
	ErrorType *string `json:"error_type"`
}
type input struct {
	Policy     string  `json:"policy"`
	Direction  string  `json:"direction"`
	Connectors []conn  `json:"connectors"`
	Index      int     `json:"index"`
	Rate       float64 `json:"rate"`
	Filter     string  `json:"filter"`
	Match      *bool   `json:"match"`
	Action     string  `json:"action"`
}
type conn struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func TestGolden(t *testing.T) {
	d := load(t)
	if len(d.Cases) != 16 {
		t.Fatal(len(d.Cases))
	}
	seen := map[string]bool{}
	for _, x := range d.Cases {
		x := x
		t.Run(x.ID, func(t *testing.T) {
			if seen[x.ID] {
				t.Fatal("duplicate")
			}
			seen[x.ID] = true
			r, e := build(x.Input)
			if x.ErrorType != nil {
				if e == nil {
					t.Fatal("expected error")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			var got any
			switch {
			case x.Input.Action == "sequence":
				a := r.NewAttempt()
				seq := []any{}
				for i := 0; i < 3; i++ {
					c, ok := a.Next()
					if ok {
						seq = append(seq, c.ID())
					} else {
						seq = append(seq, nil)
					}
				}
				got = map[string]any{"sequence": seq, "rate": r.Rate()}
			case x.Input.Action == "reset":
				a := r.NewAttempt()
				c, _ := a.Next()
				ok, e := r.Match(routable(t, "mo", 1, "http1"))
				if e != nil || !ok {
					t.Fatal(e)
				}
				a = r.NewAttempt()
				d, _ := a.Next()
				got = map[string]any{"before_reset": c.ID(), "after_reset": d.ID()}
			case x.Input.Action == "pool":
				ids := []string{}
				for _, c := range r.Connectors() {
					ids = append(ids, c.ID())
				}
				got = ids
			case x.Input.Match != nil:
				got, e = r.Match(routable(t, x.Input.Direction, map[bool]int64{true: 1, false: 2}[*x.Input.Match], map[bool]string{true: "http1", false: "other"}[*x.Input.Match]))
				if e != nil {
					t.Fatal(e)
				}
			case x.ID == "random_mo_hybrid_allowed":
				types := []string{}
				for _, c := range r.Connectors() {
					types = append(types, string(c.Type()))
				}
				got = map[string]any{"connector_types": types}
			default:
				c, e := r.Choose(x.Input.Index)
				if e != nil {
					t.Fatal(e)
				}
				got = map[string]any{"connector_id": c.ID(), "rate": r.Rate()}
			}
			if !equalJSON(got, x.Expected) {
				t.Fatalf("got=%#v want=%#v", got, x.Expected)
			}
		})
	}
	if len(seen) != 16 {
		t.Fatal(len(seen))
	}
}
func TestValidationBoundaries(t *testing.T) {
	badUTF8 := string([]byte{0xff})
	if _, err := routepolicy.New(routepolicy.Random, routingfilter.MT, []routingtable.Connector{{IDValue: badUTF8, TypeValue: routingtable.SMPPC}}, 0); !errors.Is(err, routepolicy.ErrInvalidPolicy) {
		t.Fatalf("utf8 err=%v", err)
	}
	if _, err := routepolicy.New(routepolicy.Random, routingfilter.MO, []routingtable.Connector{{IDValue: "http1", TypeValue: routingtable.HTTP}}, 0, routingfilter.NewUserFilter(1)); !errors.Is(err, routepolicy.ErrInvalidPolicy) {
		t.Fatalf("filter err=%v", err)
	}
}

func TestPoolDefensiveCopy(t *testing.T) {
	r, _ := routepolicy.New(routepolicy.Random, routingfilter.MT, []routingtable.Connector{{IDValue: "aaa", TypeValue: routingtable.SMPPC}}, 0, routingfilter.NewTransparentFilter())
	p := r.Connectors()
	p[0].IDValue = "bad"
	if r.Connectors()[0].ID() != "aaa" {
		t.Fatal("aliased")
	}
}
func TestAttemptConcurrent(t *testing.T) {
	r, _ := routepolicy.New(routepolicy.Failover, routingfilter.MT, []routingtable.Connector{{IDValue: "aaa", TypeValue: routingtable.SMPPC}, {IDValue: "bbb", TypeValue: routingtable.SMPPC}}, 0)
	a := r.NewAttempt()
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func() { _, ok := a.Next(); done <- ok }()
	}
	n := 0
	for i := 0; i < 8; i++ {
		if <-done {
			n++
		}
	}
	if n != 2 {
		t.Fatal(n)
	}
}
func FuzzPolicyNeverPanics(f *testing.F) {
	f.Add(0, "aaa")
	f.Add(5, "bbb")
	f.Fuzz(func(t *testing.T, index int, id string) {
		r, e := routepolicy.New(routepolicy.Random, routingfilter.MT, []routingtable.Connector{{IDValue: id, TypeValue: routingtable.SMPPC}}, 0)
		if e == nil {
			_, _ = r.Choose(index)
			_ = r.Connectors()
		}
	})
}
func build(i input) (routepolicy.Route, error) {
	cs := make([]routingtable.Connector, len(i.Connectors))
	for n, c := range i.Connectors {
		cs[n] = routingtable.Connector{IDValue: c.ID, TypeValue: routingtable.ConnectorType(c.Type)}
	}
	var f routingfilter.Filter
	switch i.Filter {
	case "transparent":
		f = routingfilter.NewTransparentFilter()
	case "user":
		f = routingfilter.NewUserFilter(1)
	case "connector":
		ff, _ := routingfilter.NewConnectorFilter("http1")
		f = ff
	}
	return routepolicy.New(routepolicy.Kind(i.Policy), routingfilter.Direction(i.Direction), cs, i.Rate, f)
}
func routable(t *testing.T, d string, uid int64, cid string) routingfilter.Routable {
	t.Helper()
	r, e := routingfilter.NewRoutable(routingfilter.RoutableInput{Direction: routingfilter.Direction(d), UserID: uid, ConnectorID: cid, SourceAddr: routingfilter.BytesField{Present: true}, DestinationAddr: routingfilter.BytesField{Present: true}})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	var c any
	_ = json.Unmarshal(x, &c)
	return reflect.DeepEqual(c, b)
}
func load(t *testing.T) doc {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("..", "..", "..", "compat", "fixtures", "multi-connector-routes", "baseline.json"))
	if e != nil {
		t.Fatal(e)
	}
	var d doc
	if e = json.Unmarshal(b, &d); e != nil {
		t.Fatal(e)
	}
	return d
}

var _ = errors.Is
