package jcli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Named filter management (J-007).
//
// The routing engine only ever sees filters inlined on a route (ADR-003 /
// deviation D-001), and that is not in tension with a named registry: the frozen
// console pickles the *filter object* into the route at save time, so removing a
// named filter afterwards leaves existing routes untouched. This registry names
// filters for the operator; route commands copy the resolved filter into the
// route spec, which reproduces the legacy semantics exactly.

// namedFilter is one registry entry.
type namedFilter struct {
	Type string `json:"type"`
	// Args holds the type's parameters under the console's own argument names
	// (destination_addr, short_message, cid, uid, gid, tag, ...).
	Args map[string]string `json:"args,omitempty"`
}

// filterTypes maps each console filter type to the argument names it takes, in
// the order the console prints them, and the directions it is valid for.
var filterTypes = []struct {
	Name       string
	Args       []string
	MO, MT     bool
	ReprFormat func(args map[string]string) string
	StrFormat  func(args map[string]string) string
}{
	{
		Name: "TransparentFilter", MO: true, MT: true,
		ReprFormat: func(map[string]string) string { return "<T>" },
		StrFormat:  func(map[string]string) string { return "TransparentFilter" },
	},
	{
		Name: "ConnectorFilter", Args: []string{"cid"}, MO: true,
		ReprFormat: func(a map[string]string) string { return fmt.Sprintf("<C (cid=%s)>", a["cid"]) },
		StrFormat:  func(a map[string]string) string { return "ConnectorFilter:\ncid = " + a["cid"] },
	},
	{
		Name: "UserFilter", Args: []string{"uid"}, MT: true,
		ReprFormat: func(a map[string]string) string { return fmt.Sprintf("<U (uid=%s)>", a["uid"]) },
		StrFormat:  func(a map[string]string) string { return "UserFilter:\nuid = " + a["uid"] },
	},
	{
		Name: "GroupFilter", Args: []string{"gid"}, MT: true,
		ReprFormat: func(a map[string]string) string { return fmt.Sprintf("<G (gid=%s)>", a["gid"]) },
		StrFormat:  func(a map[string]string) string { return "GroupFilter:\ngid = " + a["gid"] },
	},
	{
		Name: "SourceAddrFilter", Args: []string{"source_addr"}, MO: true, MT: true,
		ReprFormat: func(a map[string]string) string {
			return fmt.Sprintf("<SA (src_addr=%s)>", a["source_addr"])
		},
		StrFormat: func(a map[string]string) string {
			return "SourceAddrFilter:\nsource_addr = " + a["source_addr"]
		},
	},
	{
		Name: "DestinationAddrFilter", Args: []string{"destination_addr"}, MO: true, MT: true,
		ReprFormat: func(a map[string]string) string {
			return fmt.Sprintf("<DA (dst_addr=%s)>", a["destination_addr"])
		},
		StrFormat: func(a map[string]string) string {
			return "DestinationAddrFilter:\ndestination_addr = " + a["destination_addr"]
		},
	},
	{
		Name: "ShortMessageFilter", Args: []string{"short_message"}, MO: true, MT: true,
		ReprFormat: func(a map[string]string) string {
			return fmt.Sprintf("<SM (msg=%s)>", a["short_message"])
		},
		StrFormat: func(a map[string]string) string {
			return "ShortMessageFilter:\nshort_message = " + a["short_message"]
		},
	},
	{
		Name: "DateIntervalFilter", Args: []string{"dateInterval"}, MO: true, MT: true,
		ReprFormat: func(a map[string]string) string {
			left, right := splitInterval(a["dateInterval"])
			return fmt.Sprintf("<DI (%s,%s)>", left, right)
		},
		StrFormat: func(a map[string]string) string {
			left, right := splitInterval(a["dateInterval"])
			return fmt.Sprintf("DateIntervalFilter:\nLeft border = %s\nRight border = %s", left, right)
		},
	},
	{
		Name: "TimeIntervalFilter", Args: []string{"timeInterval"}, MO: true, MT: true,
		ReprFormat: func(a map[string]string) string {
			left, right := splitInterval(a["timeInterval"])
			return fmt.Sprintf("<TI (%s,%s)>", left, right)
		},
		StrFormat: func(a map[string]string) string {
			left, right := splitInterval(a["timeInterval"])
			return fmt.Sprintf("TimeIntervalFilter:\nLeft border = %s\nRight border = %s", left, right)
		},
	},
	{
		Name: "EvalPyFilter", Args: []string{"pyCode"}, MO: true, MT: true,
		ReprFormat: func(a map[string]string) string {
			// Slice first, strip newlines second (Filters.py:255). Ten bytes
			// here, not the interceptors' thirty.
			code := a["pyCode"]
			if len(code) > 10 {
				code = code[:10]
			}
			return fmt.Sprintf("<Ev (pyCode=%s ..)>", strings.ReplaceAll(code, "\n", ""))
		},
		StrFormat: func(a map[string]string) string { return "EvalPyFilter:\n" + a["pyCode"] },
	},
	{
		Name: "TagFilter", Args: []string{"tag"}, MO: true, MT: true,
		ReprFormat: func(a map[string]string) string { return fmt.Sprintf("<TG (tag=%s)>", a["tag"]) },
		StrFormat:  func(a map[string]string) string { return "TagFilter:\nhas tag = " + a["tag"] },
	},
}

// splitInterval splits a "left,right" interval argument.
func splitInterval(value string) (string, string) {
	left, right, _ := strings.Cut(value, ",")
	return strings.TrimSpace(left), strings.TrimSpace(right)
}

func filterTypeByName(name string) (int, bool) {
	for index, entry := range filterTypes {
		if entry.Name == name {
			return index, true
		}
	}
	return 0, false
}

var filterKind = &entityKind{
	keyLabel:        "Filter",
	addAnnouncement: "Adding a new Filter: (ok: save, ko: exit)",
	updateAnnouncement: func(fid string) string {
		return fmt.Sprintf("Updating Filter id [%s]: (ok: save, ko: exit)", fid)
	},
	required: []string{"fid", "type"},
	validate: validateFilterKey,
	save:     saveFilter,
}

func validateFilterKey(is *interactiveSession, key, value string) (string, bool) {
	switch key {
	case "fid":
		if !legacyFilterID.MatchString(value) {
			return "Error: Filter fid syntax is invalid", false
		}
		is.set(key, value)
		return "", true
	case "type":
		index, known := filterTypeByName(value)
		if !known {
			return fmt.Sprintf("Unknown Filter type: %s", value), false
		}
		is.set(key, value)
		// Setting the type makes the console announce the arguments that type
		// takes; a type with none announces nothing.
		if args := filterTypes[index].Args; len(args) > 0 {
			return fmt.Sprintf("<class 'jasmin.routing.Filters.%s'> arguments:\n%s",
				value, strings.Join(args, ", ")), true
		}
		return "", true
	}

	// Anything else must be an argument of the already-chosen type.
	chosen, set := is.values["type"]
	if !set {
		return fmt.Sprintf("Unknown Filter key: %s", key), false
	}
	index, _ := filterTypeByName(chosen)
	if !contains(filterTypes[index].Args, key) {
		return fmt.Sprintf("Unknown Filter key: %s", key), false
	}
	is.set(key, value)
	return "", true
}

var legacyFilterID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)

func saveFilter(s *session, is *interactiveSession) (string, bool) {
	fid := is.values["fid"]
	filterType := is.values["type"]
	index, known := filterTypeByName(filterType)
	if !known {
		return fmt.Sprintf("Unknown Filter type: %s", filterType), false
	}
	entry := namedFilter{Type: filterType, Args: map[string]string{}}
	for _, argument := range filterTypes[index].Args {
		value, provided := is.values[argument]
		if !provided {
			return fmt.Sprintf("You must set these options before saving: %s", argument), false
		}
		entry.Args[argument] = value
	}

	spec, err := json.Marshal(entry)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Filters.Put(ctx, fid, string(spec)); err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return fmt.Sprintf("Successfully added Filter [%s] with fid:%s", filterType, fid), true
}

func (s *session) handleFilter(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listFilters()
	case isShowVerb(verb):
		return s.showFilter(operand)
	case verb == "-a" || verb == "--add":
		return s.startInteractive(filterKind, false, "")
	case verb == "-r" || verb == "--remove":
		return s.removeFilter(operand)
	case verb == "":
		return "Missing required option"
	default:
		return unsupportedVerb("filter", verb)
	}
}

func (s *session) listFilters() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Filters.List(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(stored) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("Filter id", 16),
			padRight("Type", 22),
			padRight("Routes", 6),
			padRight("Description", 32),
		}, " "))
		for _, spec := range stored {
			var entry namedFilter
			if err := json.Unmarshal([]byte(spec.SpecJSON), &entry); err != nil {
				return fmt.Sprintf("filter %q: stored spec is not valid JSON: %v", spec.ID, err)
			}
			index, known := filterTypeByName(entry.Type)
			if !known {
				continue
			}
			lines = append(lines, "#"+strings.Join([]string{
				padRight(spec.ID, 16),
				padRight(entry.Type, 22),
				padRight(filterRoutes(index), 6),
				padRight(filterTypes[index].ReprFormat(entry.Args), 32),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total Filters: %d", len(stored)))
	return strings.Join(lines, "\n")
}

// filterRoutes renders the "Routes" column: which directions accept the type.
func filterRoutes(index int) string {
	entry := filterTypes[index]
	switch {
	case entry.MO && entry.MT:
		return "MO MT"
	case entry.MO:
		return "MO"
	case entry.MT:
		return "MT"
	default:
		return ""
	}
}

func (s *session) showFilter(fid string) string {
	if fid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	spec, err := s.server.deps.Filters.Get(ctx, fid)
	if err != nil {
		return fmt.Sprintf("Unknown Filter: %s", fid)
	}
	var entry namedFilter
	if err := json.Unmarshal([]byte(spec.SpecJSON), &entry); err != nil {
		return fmt.Sprintf("filter %q: stored spec is not valid JSON: %v", fid, err)
	}
	index, known := filterTypeByName(entry.Type)
	if !known {
		return fmt.Sprintf("Unknown Filter type: %s", entry.Type)
	}
	return filterTypes[index].StrFormat(entry.Args)
}

func (s *session) removeFilter(fid string) string {
	if fid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Filters.Delete(ctx, fid); err != nil {
		return fmt.Sprintf("Unknown Filter: %s", fid)
	}
	return fmt.Sprintf("Successfully removed Filter id:%s", fid)
}
