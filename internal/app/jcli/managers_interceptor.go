package jcli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

// MO and MT interceptor management (J-010, J-011).
//
// Editing interceptors is opt-in (`admin.allow_interceptor_editing`) because
// the scripts are arbitrary Python executed on the gateway host: an operator who
// can add one has the same reach as a shell. When the capability is off the
// console says so rather than pretending the command does not exist.

// typedScript matches the console's script syntax, python3(/path/to/script).
var typedScript = regexp.MustCompile(`(?i)^python3\((.*)\)$`)

// interceptorDirection carries the two managers' differing nouns.
type interceptorDirection struct {
	label     string // "MO" or "MT"
	noun      string // "MO Interceptor"
	scriptTag string // the repr prefix: MOIS or MTIS
	types     map[string][]string
}

var moInterceptorDirection = interceptorDirection{
	label: "MO", noun: "MO Interceptor", scriptTag: "MOIS",
	types: map[string][]string{
		"DefaultInterceptor":  {"script"},
		"StaticMOInterceptor": {"filters", "script"},
	},
}

var mtInterceptorDirection = interceptorDirection{
	label: "MT", noun: "MT Interceptor", scriptTag: "MTIS",
	types: map[string][]string{
		"DefaultInterceptor":  {"script"},
		"StaticMTInterceptor": {"filters", "script"},
	},
}

func interceptorKindFor(direction interceptorDirection) *entityKind {
	return &entityKind{
		keyLabel:        "Interceptor",
		addAnnouncement: fmt.Sprintf("Adding a new %s Interceptor: (ok: save, ko: exit)", direction.label),
		updateAnnouncement: func(order string) string {
			return fmt.Sprintf("Updating %s Interceptor with order [%s]: (ok: save, ko: exit)", direction.label, order)
		},
		required: []string{"type"},
		validate: func(is *interactiveSession, key, value string) (string, bool) {
			return validateInterceptorKey(direction, is, key, value)
		},
		save: func(s *session, is *interactiveSession) (string, bool) {
			return saveInterceptor(direction, s, is)
		},
	}
}

var moInterceptorKind = interceptorKindFor(moInterceptorDirection)
var mtInterceptorKind = interceptorKindFor(mtInterceptorDirection)

func validateInterceptorKey(direction interceptorDirection, is *interactiveSession, key, value string) (string, bool) {
	switch key {
	case "type":
		arguments, known := direction.types[value]
		if !known {
			return fmt.Sprintf("Unknown Interceptor type: %s", value), false
		}
		is.set(key, value)
		return fmt.Sprintf("<class 'jasmin.routing.Interceptors.%s'> arguments:\n%s",
			value, strings.Join(arguments, ", ")), true
	case "order":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Sprintf("Error: invalid literal for int() with base 10: '%s'", value), false
		}
	case "script":
		match := typedScript.FindStringSubmatch(strings.TrimSpace(value))
		if match == nil {
			return "Error: Invalid syntax for script, must be python3(/path/to/script).", false
		}
		// The oracle reads and compiles the script at add time, so a path that
		// does not exist fails here rather than at the first intercepted
		// message. Reading it now also means the stored spec carries the code,
		// not a path that could change underneath the gateway.
		if _, err := os.Stat(match[1]); err != nil {
			return fmt.Sprintf("Error: [Errno 2] No such file or directory: '%s'", match[1]), false
		}
	case "filters":
	default:
		return fmt.Sprintf("Unknown Interceptor key: %s", key), false
	}
	is.set(key, value)
	return "", true
}

func saveInterceptor(direction interceptorDirection, s *session, is *interactiveSession) (string, bool) {
	if s.server.deps.Interceptors == nil {
		return "Error: interceptor editing is disabled on this gateway (admin.allow_interceptor_editing)", false
	}
	interceptorType := is.values["type"]
	order := 0
	if text, set := is.values["order"]; set {
		order, _ = strconv.Atoi(text)
	}
	if interceptorType == "DefaultInterceptor" {
		order = 0
	}

	match := typedScript.FindStringSubmatch(strings.TrimSpace(is.values["script"]))
	if match == nil {
		return "Error: Invalid syntax for script, must be python3(/path/to/script).", false
	}
	code, err := os.ReadFile(match[1])
	if err != nil {
		return fmt.Sprintf("Error: [Errno 2] No such file or directory: '%s'", match[1]), false
	}

	filters, message, ok := s.resolveFilters(is.values["filters"])
	if !ok {
		return message, false
	}
	inline, message, ok := toOutboundFilters(filters)
	if !ok {
		return message, false
	}

	spec, err := json.Marshal(outbound.InterceptorConfig{
		Order:   order,
		Filters: inline,
		PyCode:  string(code),
	})
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Interceptors.PutInterceptor(ctx, admin.InterceptorDirection(strings.ToLower(direction.label)), order, string(spec)); err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return fmt.Sprintf("Successfully added %sInterceptor [%s] with order:%d",
		direction.label, interceptorType, order), true
}

// interceptorScriptRepr renders the oracle's script repr (jasminApi.py:470):
// the class tag plus the first 30 bytes of the code, newlines stripped.
//
// The order matters and is not the obvious one: the oracle slices first and
// removes newlines second, so a newline inside the first 30 bytes shortens the
// result. Stripping first would produce a longer, different string.
func interceptorScriptRepr(tag, code string) string {
	head := code
	if len(head) > 30 {
		head = head[:30]
	}
	return fmt.Sprintf("<%s (pyCode=%s ..)>", tag, strings.ReplaceAll(head, "\n", ""))
}

func (s *session) handleInterceptor(direction interceptorDirection, argument string) string {
	verb, operand := verbOf(argument)
	kind := moInterceptorKind
	if direction.label == "MT" {
		kind = mtInterceptorKind
	}
	switch {
	case isListVerb(verb):
		return s.listInterceptors(direction)
	case isShowVerb(verb):
		return s.showInterceptor(direction, operand)
	case verb == "-a" || verb == "--add":
		return s.startInteractive(kind, false, "")
	case verb == "-r" || verb == "--remove":
		return s.removeInterceptor(direction, operand)
	case verb == "-f" || verb == "--flush":
		return s.flushInterceptors(direction)
	case verb == "":
		return "Missing required option"
	default:
		return unsupportedVerb(strings.ToLower(direction.label)+"interceptor", verb)
	}
}

func (s *session) storedInterceptors(direction interceptorDirection) ([]admin.StoredSpec, []outbound.InterceptorConfig, error) {
	if s.server.deps.Interceptors == nil {
		return nil, nil, nil
	}
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Interceptors.ListInterceptors(ctx, admin.InterceptorDirection(strings.ToLower(direction.label)))
	if err != nil {
		return nil, nil, err
	}
	configs := make([]outbound.InterceptorConfig, 0, len(stored))
	for _, entry := range stored {
		var config outbound.InterceptorConfig
		if err := json.Unmarshal([]byte(entry.SpecJSON), &config); err != nil {
			return nil, nil, fmt.Errorf("%s %d: stored spec is not valid JSON: %w", direction.noun, entry.Order, err)
		}
		config.Order = entry.Order
		configs = append(configs, config)
	}
	return stored, configs, nil
}

func (s *session) listInterceptors(direction interceptorDirection) string {
	_, configs, err := s.storedInterceptors(direction)
	if err != nil {
		return err.Error()
	}
	rows := make([]routeRow, 0, len(configs))
	for _, config := range configs {
		types := make([]string, 0, len(config.Filters))
		args := make([]map[string]string, 0, len(config.Filters))
		for _, filter := range config.Filters {
			types = append(types, filter.Type)
			args = append(args, outboundFilterArgs(filter))
		}
		rows = append(rows, routeRow{
			order:   config.Order,
			class:   interceptorClassName(direction, config.Order),
			targets: interceptorScriptRepr(direction.scriptTag, config.PyCode),
			filters: filterDescriptions(types, args),
		})
	}
	return renderInterceptorRows(rows, len(configs), direction.noun)
}

// interceptorClassName names the legacy class. Order 0 is the default
// interceptor; anything else is the direction's static class.
func interceptorClassName(direction interceptorDirection, order int) string {
	if order == 0 {
		return "DefaultInterceptor"
	}
	return "Static" + direction.label + "Interceptor"
}

func renderInterceptorRows(rows []routeRow, total int, noun string) string {
	sortRouteRowsDescending(rows)
	var lines []string
	if len(rows) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("Order", 5),
			padRight("Type", 20),
			padRight("Script", 47),
			padRight("Filter(s)", 64),
		}, " "))
		for _, row := range rows {
			lines = append(lines, "#"+strings.Join([]string{
				padRight(strconv.Itoa(row.order), 5),
				padRight(row.class, 20),
				padRight(row.targets, 47),
				padRight(row.filters, 64),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total %ss: %d", noun, total))
	return strings.Join(lines, "\n")
}

func (s *session) showInterceptor(direction interceptorDirection, orderText string) string {
	if orderText == "" {
		return "Missing required option"
	}
	order, err := strconv.Atoi(orderText)
	if err != nil {
		return fmt.Sprintf("Unknown %s: %s", direction.noun, orderText)
	}
	_, configs, listErr := s.storedInterceptors(direction)
	if listErr != nil {
		return listErr.Error()
	}
	for _, config := range configs {
		if config.Order == order {
			return fmt.Sprintf("%s/%s", interceptorClassName(direction, order),
				interceptorScriptRepr(direction.scriptTag, config.PyCode))
		}
	}
	return fmt.Sprintf("Unknown %s: %s", direction.noun, orderText)
}

func (s *session) removeInterceptor(direction interceptorDirection, orderText string) string {
	if orderText == "" {
		return "Missing required option"
	}
	order, err := strconv.Atoi(orderText)
	if err != nil {
		return fmt.Sprintf("Unknown %s: %s", direction.noun, orderText)
	}
	if s.server.deps.Interceptors == nil {
		return "Error: interceptor editing is disabled on this gateway (admin.allow_interceptor_editing)"
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Interceptors.DeleteInterceptor(ctx, admin.InterceptorDirection(strings.ToLower(direction.label)), order); err != nil {
		return fmt.Sprintf("Unknown %s: %s", direction.noun, orderText)
	}
	return fmt.Sprintf("Successfully removed %s with order:%d", direction.noun, order)
}

func (s *session) flushInterceptors(direction interceptorDirection) string {
	stored, _, err := s.storedInterceptors(direction)
	if err != nil {
		return err.Error()
	}
	if s.server.deps.Interceptors == nil {
		return "Error: interceptor editing is disabled on this gateway (admin.allow_interceptor_editing)"
	}
	ctx, cancel := s.context()
	defer cancel()
	for _, entry := range stored {
		if err := s.server.deps.Interceptors.DeleteInterceptor(ctx, admin.InterceptorDirection(strings.ToLower(direction.label)), entry.Order); err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
	}
	return fmt.Sprintf("Successfully flushed %s table (%d flushed entries)", direction.noun, len(stored))
}

// sortRouteRowsDescending orders rows highest-first, the way both the route and
// interceptor tables list: the default entry (order 0) lands last.
func sortRouteRowsDescending(rows []routeRow) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].order > rows[j].order })
}
