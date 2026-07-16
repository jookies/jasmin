package routingfilter

import (
	"fmt"
	"strconv"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

type evalPyFilter struct {
	filterBase
	pyCode  string
	program *starlark.Program
}

func NewEvalPyFilter(pyCode string) (Filter, error) {
	// Parse and compile the code with top-level control flow allowed
	opts := &syntax.FileOptions{
		TopLevelControl: true,
		GlobalReassign:  true,
	}
	isPredeclared := func(name string) bool {
		return name == "routable" || name == "result"
	}
	_, program, err := starlark.SourceProgramOptions(opts, "evalpy", pyCode, isPredeclared)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}

	return &evalPyFilter{
		filterBase: commonBase(KindEvalPy),
		pyCode:     pyCode,
		program:    program,
	}, nil
}

func (f *evalPyFilter) Match(routable Routable) (bool, error) {
	thread := &starlark.Thread{Name: "evalpy"}

	// Predeclared globals
	predeclared := starlark.StringDict{
		"routable": newStarlarkRoutable(&routable),
		"result":   starlark.False,
	}

	// Execute the program
	globals, err := f.program.Init(thread, predeclared)
	if err != nil {
		return false, fmt.Errorf("evalpy execution error: %v", err)
	}

	// Extract result from the updated globals
	resultVal, ok := globals["result"]
	if !ok {
		resultVal = predeclared["result"]
	}

	if b, ok := resultVal.(starlark.Bool); ok {
		return bool(b), nil
	}

	return false, nil
}

// starlarkRoutable is a wrapper around Routable to expose it to Starlark
type starlarkRoutable struct {
	r *Routable
}

func newStarlarkRoutable(r *Routable) *starlarkRoutable {
	return &starlarkRoutable{r: r}
}

func (sr *starlarkRoutable) String() string        { return "<Routable>" }
func (sr *starlarkRoutable) Type() string          { return "routable" }
func (sr *starlarkRoutable) Freeze()               {}
func (sr *starlarkRoutable) Truth() starlark.Bool  { return starlark.True }
func (sr *starlarkRoutable) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable") }

func (sr *starlarkRoutable) Attr(name string) (starlark.Value, error) {
	switch name {
	case "connector":
		return &starlarkConnector{cid: sr.r.connectorID}, nil
	case "user":
		return &starlarkUser{uid: strconv.FormatInt(sr.r.userID, 10), gid: strconv.FormatInt(sr.r.groupID, 10)}, nil
	case "pdu":
		return &starlarkPDU{r: sr.r}, nil
	case "datetime":
		return &starlarkDatetime{t: sr.r.timestamp}, nil
	case "hasTag":
		return starlark.NewBuiltin("hasTag", sr.AttrHasTag), nil
	case "addTag":
		return starlark.NewBuiltin("addTag", sr.AttrAddTag), nil
	}
	return nil, nil
}

func (sr *starlarkRoutable) AttrNames() []string {
	return []string{"connector", "user", "pdu", "datetime", "hasTag", "addTag"}
}

// Implement hasTag and addTag as methods
func (sr *starlarkRoutable) AttrHasTag(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var tag string
	if err := starlark.UnpackArgs("hasTag", args, kwargs, "tag", &tag); err != nil {
		return nil, err
	}
	_, ok := sr.r.tags[tag]
	return starlark.Bool(ok), nil
}

func (sr *starlarkRoutable) AttrAddTag(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var tag string
	if err := starlark.UnpackArgs("addTag", args, kwargs, "tag", &tag); err != nil {
		return nil, err
	}
	_ = sr.r.AddTag(tag)
	return starlark.None, nil
}

// Helper types for Starlark
type starlarkConnector struct {
	cid string
}

func (c *starlarkConnector) String() string        { return "<Connector>" }
func (c *starlarkConnector) Type() string          { return "connector" }
func (c *starlarkConnector) Freeze()               {}
func (c *starlarkConnector) Truth() starlark.Bool  { return starlark.True }
func (c *starlarkConnector) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable") }
func (c *starlarkConnector) Attr(name string) (starlark.Value, error) {
	if name == "cid" {
		return starlark.String(c.cid), nil
	}
	return nil, nil
}
func (c *starlarkConnector) AttrNames() []string { return []string{"cid"} }

type starlarkUser struct {
	uid string
	gid string
}

func (u *starlarkUser) String() string        { return "<User>" }
func (u *starlarkUser) Type() string          { return "user" }
func (u *starlarkUser) Freeze()               {}
func (u *starlarkUser) Truth() starlark.Bool  { return starlark.True }
func (u *starlarkUser) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable") }
func (u *starlarkUser) Attr(name string) (starlark.Value, error) {
	switch name {
	case "uid":
		return starlark.String(u.uid), nil
	case "group":
		return &starlarkGroup{gid: u.gid}, nil
	}
	return nil, nil
}
func (u *starlarkUser) AttrNames() []string { return []string{"uid", "group"} }

type starlarkGroup struct {
	gid string
}

func (g *starlarkGroup) String() string        { return "<Group>" }
func (g *starlarkGroup) Type() string          { return "group" }
func (g *starlarkGroup) Freeze()               {}
func (g *starlarkGroup) Truth() starlark.Bool  { return starlark.True }
func (g *starlarkGroup) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable") }
func (g *starlarkGroup) Attr(name string) (starlark.Value, error) {
	if name == "gid" {
		return starlark.String(g.gid), nil
	}
	return nil, nil
}
func (g *starlarkGroup) AttrNames() []string { return []string{"gid"} }

type starlarkPDU struct {
	r *Routable
}

func (p *starlarkPDU) String() string        { return "<PDU>" }
func (p *starlarkPDU) Type() string          { return "pdu" }
func (p *starlarkPDU) Freeze()               {}
func (p *starlarkPDU) Truth() starlark.Bool  { return starlark.True }
func (p *starlarkPDU) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable") }
func (p *starlarkPDU) Attr(name string) (starlark.Value, error) {
	if name == "params" {
		dict := starlark.NewDict(4)
		if p.r.sourceAddr.Present {
			dict.SetKey(starlark.String("source_addr"), starlark.Bytes(p.r.sourceAddr.Value))
		}
		if p.r.destinationAddr.Present {
			dict.SetKey(starlark.String("destination_addr"), starlark.Bytes(p.r.destinationAddr.Value))
		}
		if p.r.shortMessage.Present {
			dict.SetKey(starlark.String("short_message"), starlark.Bytes(p.r.shortMessage.Value))
		}
		if p.r.messagePayload.Present {
			dict.SetKey(starlark.String("message_payload"), starlark.Bytes(p.r.messagePayload.Value))
		}
		return dict, nil
	}
	return nil, nil
}
func (p *starlarkPDU) AttrNames() []string { return []string{"params"} }

type starlarkDatetime struct {
	t time.Time
}

func (d *starlarkDatetime) String() string        { return d.t.Format(time.RFC3339) }
func (d *starlarkDatetime) Type() string          { return "datetime" }
func (d *starlarkDatetime) Freeze()               {}
func (d *starlarkDatetime) Truth() starlark.Bool  { return starlark.True }
func (d *starlarkDatetime) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable") }
func (d *starlarkDatetime) Attr(name string) (starlark.Value, error) {
	switch name {
	case "year":
		return starlark.MakeInt(d.t.Year()), nil
	case "month":
		return starlark.MakeInt(int(d.t.Month())), nil
	case "day":
		return starlark.MakeInt(d.t.Day()), nil
	case "hour":
		return starlark.MakeInt(d.t.Hour()), nil
	case "minute":
		return starlark.MakeInt(d.t.Minute()), nil
	case "second":
		return starlark.MakeInt(d.t.Second()), nil
	}
	return nil, nil
}
func (d *starlarkDatetime) AttrNames() []string {
	return []string{"year", "month", "day", "hour", "minute", "second"}
}
