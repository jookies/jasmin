package jcli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/modispatch"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
)

// MT and MO route management (J-009, J-008).
//
// The console names filters and HTTP connectors; the routing engine takes them
// inline. Resolution happens here, at save time, which is what the frozen
// console does too (it pickles the filter object into the route), so a filter
// removed later cannot orphan a route that already embeds it.

// routeTypes describes each route class the console accepts.
var mtRouteTypes = map[string][]string{
	"DefaultRoute":            {"connector", "rate"},
	"StaticMTRoute":           {"filters", "connector", "rate"},
	"RandomRoundrobinMTRoute": {"filters", "connectors", "rate"},
	"FailoverMTRoute":         {"filters", "connectors", "rate"},
}

var moRouteTypes = map[string][]string{
	"DefaultRoute":            {"connector"},
	"StaticMORoute":           {"filters", "connector"},
	"RandomRoundrobinMORoute": {"filters", "connectors"},
	"FailoverMORoute":         {"filters", "connectors"},
}

// connectorReference matches the console's connector syntax: smppc(cid) or
// http(cid).
var connectorReference = regexp.MustCompile(`^(smppc|http)\(([^)]+)\)$`)

// routeDirection distinguishes the two managers where their contracts differ:
// the noun in messages, which route classes are legal, and whether a rate is
// part of the shape at all.
type routeDirection struct {
	// label is "MT" or "MO", used in "Successfully added MTRoute [...]".
	label string
	// noun is what remove/flush call the table ("MT Route").
	noun  string
	types map[string][]string
	rated bool
}

var mtDirection = routeDirection{label: "MT", noun: "MT Route", types: mtRouteTypes, rated: true}
var moDirection = routeDirection{label: "MO", noun: "MO Route", types: moRouteTypes, rated: false}

func routeKindFor(direction routeDirection) *entityKind {
	return &entityKind{
		keyLabel:        "Route",
		addAnnouncement: fmt.Sprintf("Adding a new %s Route: (ok: save, ko: exit)", direction.label),
		updateAnnouncement: func(order string) string {
			return fmt.Sprintf("Updating %s Route with order [%s]: (ok: save, ko: exit)", direction.label, order)
		},
		required: []string{"type"},
		validate: func(is *interactiveSession, key, value string) (string, bool) {
			return validateRouteKey(direction, is, key, value)
		},
		save: func(s *session, is *interactiveSession) (string, bool) {
			return saveRoute(direction, s, is)
		},
	}
}

var mtRouteKind = routeKindFor(mtDirection)
var moRouteKind = routeKindFor(moDirection)

func validateRouteKey(direction routeDirection, is *interactiveSession, key, value string) (string, bool) {
	switch key {
	case "type":
		arguments, known := direction.types[value]
		if !known {
			return fmt.Sprintf("Unknown Route type: %s", value), false
		}
		is.set(key, value)
		return fmt.Sprintf("<class 'jasmin.routing.Routes.%s'> arguments:\n%s",
			value, strings.Join(arguments, ", ")), true
	case "order":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Sprintf("Error: invalid literal for int() with base 10: '%s'", value), false
		}
	case "rate":
		if !direction.rated {
			return fmt.Sprintf("Unknown Route key: %s", key), false
		}
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Sprintf("Error: could not convert string to float: '%s'", value), false
		}
	case "filters", "connector", "connectors":
	default:
		return fmt.Sprintf("Unknown Route key: %s", key), false
	}
	is.set(key, value)
	return "", true
}

// resolveFilters turns "f1;f2" (or "f1,f2") into the stored filter entries.
func (s *session) resolveFilters(names string) ([]namedFilter, string, bool) {
	var resolved []namedFilter
	for _, name := range strings.FieldsFunc(names, func(r rune) bool { return r == ';' || r == ',' }) {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		ctx, cancel := s.context()
		spec, err := s.server.deps.Filters.Get(ctx, name)
		cancel()
		if err != nil {
			return nil, fmt.Sprintf("Unknown Filter: %s", name), false
		}
		var entry namedFilter
		if err := json.Unmarshal([]byte(spec.SpecJSON), &entry); err != nil {
			return nil, fmt.Sprintf("filter %q: stored spec is not valid JSON: %v", name, err), false
		}
		resolved = append(resolved, entry)
	}
	return resolved, "", true
}

// toOutboundFilters projects named filters into the MT route's inline shape.
func toOutboundFilters(filters []namedFilter) ([]outbound.FilterConfig, string, bool) {
	var configs []outbound.FilterConfig
	for _, entry := range filters {
		config := outbound.FilterConfig{}
		switch entry.Type {
		case "TransparentFilter":
			config.Type = "transparent"
		case "DestinationAddrFilter":
			config.Type, config.Pattern = "destination_addr", entry.Args["destination_addr"]
		case "SourceAddrFilter":
			config.Type, config.Pattern = "source_addr", entry.Args["source_addr"]
		case "ShortMessageFilter":
			config.Type, config.Pattern = "short_message", entry.Args["short_message"]
		case "TagFilter":
			config.Type, config.Value = "tag", entry.Args["tag"]
		case "UserFilter":
			config.Type, config.Username = "user", entry.Args["uid"]
		case "DateIntervalFilter":
			config.Type = "date_interval"
			config.Start, config.End = splitInterval(entry.Args["dateInterval"])
		case "TimeIntervalFilter":
			config.Type = "time_interval"
			config.Start, config.End = splitInterval(entry.Args["timeInterval"])
		default:
			return nil, fmt.Sprintf("Error: filter type %s is not supported on an MT route", entry.Type), false
		}
		configs = append(configs, config)
	}
	return configs, "", true
}

// toMOFilters projects named filters into the MO route's inline shape.
func toMOFilters(filters []namedFilter) ([]modispatch.FilterConfig, string, bool) {
	var configs []modispatch.FilterConfig
	for _, entry := range filters {
		config := modispatch.FilterConfig{}
		switch entry.Type {
		case "TransparentFilter":
			config.Type = "transparent"
		case "DestinationAddrFilter":
			config.Type, config.Pattern = "destination_addr", entry.Args["destination_addr"]
		case "SourceAddrFilter":
			config.Type, config.Pattern = "source_addr", entry.Args["source_addr"]
		case "ShortMessageFilter":
			config.Type, config.Pattern = "short_message", entry.Args["short_message"]
		case "TagFilter":
			config.Type, config.Value = "tag", entry.Args["tag"]
		case "ConnectorFilter":
			config.Type, config.Value = "connector", entry.Args["cid"]
		case "DateIntervalFilter":
			config.Type = "date_interval"
			config.Start, config.End = splitInterval(entry.Args["dateInterval"])
		case "TimeIntervalFilter":
			config.Type = "time_interval"
			config.Start, config.End = splitInterval(entry.Args["timeInterval"])
		default:
			return nil, fmt.Sprintf("Error: filter type %s is not supported on an MO route", entry.Type), false
		}
		configs = append(configs, config)
	}
	return configs, "", true
}

// parseConnectors reads one or more connector references.
func parseConnectors(value string) ([]string, []string, bool) {
	var kinds, cids []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ',' }) {
		match := connectorReference.FindStringSubmatch(strings.TrimSpace(item))
		if match == nil {
			return nil, nil, false
		}
		kinds = append(kinds, match[1])
		cids = append(cids, match[2])
	}
	return kinds, cids, len(cids) > 0
}

func saveRoute(direction routeDirection, s *session, is *interactiveSession) (string, bool) {
	routeType := is.values["type"]
	order := 0
	if text, set := is.values["order"]; set {
		order, _ = strconv.Atoi(text)
	}
	if routeType == "DefaultRoute" {
		// The default route is always order 0, whatever was typed.
		order = 0
	}

	filters, message, ok := s.resolveFilters(is.values["filters"])
	if !ok {
		return message, false
	}
	connectorValue := is.values["connector"]
	if connectorValue == "" {
		connectorValue = is.values["connectors"]
	}
	kinds, cids, parsed := parseConnectors(connectorValue)
	if !parsed {
		return "Error: Invalid connector syntax, must be smppc(cid) or http(cid)", false
	}

	ctx, cancel := s.context()
	defer cancel()

	if direction.rated {
		rate := 0.0
		if text, set := is.values["rate"]; set {
			rate, _ = strconv.ParseFloat(text, 64)
		}
		for _, kind := range kinds {
			if kind != "smppc" {
				return "Error: an MT route must point at an smppc connector", false
			}
		}
		inline, message, ok := toOutboundFilters(filters)
		if !ok {
			return message, false
		}
		route := outbound.RouteConfig{
			Order:   order,
			Rate:    rate,
			Default: routeType == "DefaultRoute",
			Filters: inline,
		}
		if len(cids) == 1 {
			route.ConnectorID = cids[0]
		} else {
			route.ConnectorIDs = cids
		}
		spec, err := json.Marshal(route)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		if err := s.server.deps.Routes.PutRoute(ctx, order, string(spec)); err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		return fmt.Sprintf("Successfully added MTRoute [%s] with order:%d", routeType, order), true
	}

	// A legacy ConnectorFilter selects the *inbound* connector, which the Go MO
	// route expresses as FilterConnectorID rather than as a content filter.
	var connectorFilterCID string
	var contentFilters []namedFilter
	for _, entry := range filters {
		if entry.Type == "ConnectorFilter" {
			connectorFilterCID = entry.Args["cid"]
			continue
		}
		contentFilters = append(contentFilters, entry)
	}
	inline, message, ok := toMOFilters(contentFilters)
	if !ok {
		return message, false
	}
	route := modispatch.RouteConfig{
		Order:             order,
		Default:           routeType == "DefaultRoute",
		FilterConnectorID: connectorFilterCID,
		Filters:           inline,
	}
	switch kinds[0] {
	case "http":
		connector, err := s.lookupHTTPConnector(cids[0])
		if err != nil {
			return fmt.Sprintf("Unknown Httpcc: %s", cids[0]), false
		}
		route.Connector = modispatch.ConnectorConfig{
			Type:   "http",
			CID:    cids[0],
			URL:    connector.BaseURL,
			Method: strings.ToUpper(connector.Method),
		}
	case "smppc":
		route.Connector = modispatch.ConnectorConfig{Type: "smpps", CID: cids[0], SystemID: cids[0]}
	}
	spec, err := json.Marshal(route)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	if err := s.server.deps.MORoutes.PutRoute(ctx, order, string(spec)); err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return fmt.Sprintf("Successfully added MORoute [%s] with order:%d", routeType, order), true
}

// ---------------------------------------------------------------- rendering ---

// routeRow is one rendered row, kept sortable by order (descending, as the
// oracle lists them: highest order first, default last).
type routeRow struct {
	order    int
	class    string
	rate     string
	targets  string
	filters  string
	hasRate  bool
	rateZero bool
}

func renderRouteRows(rows []routeRow, rated bool, total int, noun string) string {
	sortRouteRowsDescending(rows)
	var lines []string
	if len(rows) > 0 {
		header := []string{padRight("Order", 5), padRight("Type", 23)}
		if rated {
			header = append(header, padRight("Rate", 10))
		}
		header = append(header, padRight("Connector ID(s)", 48), padRight("Filter(s)", 64))
		lines = append(lines, "#"+strings.Join(header, " "))
		for _, row := range rows {
			columns := []string{padRight(strconv.Itoa(row.order), 5), padRight(row.class, 23)}
			if rated {
				columns = append(columns, padRight(row.rate, 10))
			}
			columns = append(columns, padRight(row.targets, 48), padRight(row.filters, 64))
			lines = append(lines, "#"+strings.Join(columns, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total %ss: %d", noun, total))
	return strings.Join(lines, "\n")
}

// renderRate prints the Rate column: five decimals, or "0 (!)" for a free route
// — the oracle's way of flagging traffic that is not being charged.
func renderRate(rate float64) string {
	if rate == 0 {
		return "0 (!)"
	}
	return strconv.FormatFloat(rate, 'f', 5, 64)
}

// filterDescriptions renders the Filter(s) column from inline filters, using the
// same repr the named registry shows.
func filterDescriptions(types []string, args []map[string]string) string {
	parts := make([]string, 0, len(types))
	for index, name := range types {
		className, ok := consoleFilterClass(name)
		if !ok {
			continue
		}
		typeIndex, known := filterTypeByName(className)
		if !known {
			continue
		}
		parts = append(parts, filterTypes[typeIndex].ReprFormat(args[index]))
	}
	return strings.Join(parts, ", ")
}

// consoleFilterClass maps an inline filter type back to its legacy class name.
func consoleFilterClass(inline string) (string, bool) {
	switch inline {
	case "transparent":
		return "TransparentFilter", true
	case "destination_addr":
		return "DestinationAddrFilter", true
	case "source_addr":
		return "SourceAddrFilter", true
	case "short_message":
		return "ShortMessageFilter", true
	case "tag":
		return "TagFilter", true
	case "user":
		return "UserFilter", true
	case "connector":
		return "ConnectorFilter", true
	case "date_interval":
		return "DateIntervalFilter", true
	case "time_interval":
		return "TimeIntervalFilter", true
	default:
		return "", false
	}
}

// routeClassName names the legacy route class for a stored route.
func routeClassName(direction routeDirection, isDefault bool, connectors int) string {
	if isDefault {
		return "DefaultRoute"
	}
	if connectors > 1 {
		return "RandomRoundrobin" + direction.label + "Route"
	}
	return "Static" + direction.label + "Route"
}

// ------------------------------------------------------------------ MT ---

func (s *session) handleMTRouter(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listMTRoutes()
	case isShowVerb(verb):
		return s.showMTRoute(operand)
	case verb == "-a" || verb == "--add":
		return s.startInteractive(mtRouteKind, false, "")
	case verb == "-r" || verb == "--remove":
		return s.removeMTRoute(operand)
	case verb == "-f" || verb == "--flush":
		return s.flushMTRoutes()
	case verb == "":
		return "Missing required option"
	default:
		return unsupportedVerb("mtrouter", verb)
	}
}

func (s *session) storedMTRoutes() ([]admin.StoredRoute, []outbound.RouteConfig, error) {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Routes.ListRoutes(ctx)
	if err != nil {
		return nil, nil, err
	}
	routes := make([]outbound.RouteConfig, 0, len(stored))
	for _, entry := range stored {
		var route outbound.RouteConfig
		if err := json.Unmarshal([]byte(entry.SpecJSON), &route); err != nil {
			return nil, nil, fmt.Errorf("MT route %d: stored spec is not valid JSON: %w", entry.Order, err)
		}
		route.Order = entry.Order
		routes = append(routes, route)
	}
	return stored, routes, nil
}

func (s *session) listMTRoutes() string {
	_, routes, err := s.storedMTRoutes()
	if err != nil {
		return err.Error()
	}
	rows := make([]routeRow, 0, len(routes))
	for _, route := range routes {
		types := make([]string, 0, len(route.Filters))
		args := make([]map[string]string, 0, len(route.Filters))
		for _, filter := range route.Filters {
			types = append(types, filter.Type)
			args = append(args, outboundFilterArgs(filter))
		}
		rows = append(rows, routeRow{
			order:   route.Order,
			class:   routeClassName(mtDirection, route.Default, len(route.ConnectorCandidates())),
			rate:    renderRate(route.Rate),
			targets: connectorList("smppc", route.ConnectorCandidates()),
			filters: filterDescriptions(types, args),
		})
	}
	return renderRouteRows(rows, true, len(routes), "MT Route")
}

// outboundFilterArgs projects an inline MT filter back into the argument map the
// repr formatters read.
func outboundFilterArgs(filter outbound.FilterConfig) map[string]string {
	return map[string]string{
		"destination_addr": filter.Pattern,
		"source_addr":      filter.Pattern,
		"short_message":    filter.Pattern,
		"tag":              filter.Value,
		"cid":              filter.Value,
		"uid":              filter.Username,
		"dateInterval":     filter.Start + "," + filter.End,
		"timeInterval":     filter.Start + "," + filter.End,
	}
}

func moFilterArgs(filter modispatch.FilterConfig) map[string]string {
	return map[string]string{
		"destination_addr": filter.Pattern,
		"source_addr":      filter.Pattern,
		"short_message":    filter.Pattern,
		"tag":              filter.Value,
		"cid":              filter.Value,
		"dateInterval":     filter.Start + "," + filter.End,
		"timeInterval":     filter.Start + "," + filter.End,
	}
}

func connectorList(kind string, cids []string) string {
	parts := make([]string, 0, len(cids))
	for _, cid := range cids {
		parts = append(parts, fmt.Sprintf("%s(%s)", kind, cid))
	}
	return strings.Join(parts, ", ")
}

func (s *session) showMTRoute(orderText string) string {
	if orderText == "" {
		return "Missing required option"
	}
	order, err := strconv.Atoi(orderText)
	if err != nil {
		return fmt.Sprintf("Unknown MT Route: %s", orderText)
	}
	ctx, cancel := s.context()
	defer cancel()
	entry, err := s.server.deps.Routes.GetRoute(ctx, order)
	if err != nil {
		return fmt.Sprintf("Unknown MT Route: %s", orderText)
	}
	var route outbound.RouteConfig
	if err := json.Unmarshal([]byte(entry.SpecJSON), &route); err != nil {
		return fmt.Sprintf("MT route %d: stored spec is not valid JSON: %v", order, err)
	}
	class := routeClassName(mtDirection, route.Default, len(route.ConnectorCandidates()))
	return fmt.Sprintf("%s to %s rated %.2f", class,
		connectorList("smppc", route.ConnectorCandidates()), route.Rate)
}

func (s *session) removeMTRoute(orderText string) string {
	if orderText == "" {
		return "Missing required option"
	}
	order, err := strconv.Atoi(orderText)
	if err != nil {
		return fmt.Sprintf("Unknown MT Route: %s", orderText)
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Routes.DeleteRoute(ctx, order); err != nil {
		return fmt.Sprintf("Unknown MT Route: %s", orderText)
	}
	return fmt.Sprintf("Successfully removed MT Route with order:%d", order)
}

func (s *session) flushMTRoutes() string {
	stored, _, err := s.storedMTRoutes()
	if err != nil {
		return err.Error()
	}
	ctx, cancel := s.context()
	defer cancel()
	for _, entry := range stored {
		if err := s.server.deps.Routes.DeleteRoute(ctx, entry.Order); err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
	}
	return fmt.Sprintf("Successfully flushed MT Route table (%d flushed entries)", len(stored))
}

// ------------------------------------------------------------------ MO ---

func (s *session) handleMORouter(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listMORoutes()
	case isShowVerb(verb):
		return s.showMORoute(operand)
	case verb == "-a" || verb == "--add":
		return s.startInteractive(moRouteKind, false, "")
	case verb == "-r" || verb == "--remove":
		return s.removeMORoute(operand)
	case verb == "-f" || verb == "--flush":
		return s.flushMORoutes()
	case verb == "":
		return "Missing required option"
	default:
		return unsupportedVerb("morouter", verb)
	}
}

func (s *session) storedMORoutes() ([]admin.StoredSpec, []modispatch.RouteConfig, error) {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.MORoutes.ListRoutes(ctx)
	if err != nil {
		return nil, nil, err
	}
	routes := make([]modispatch.RouteConfig, 0, len(stored))
	for _, entry := range stored {
		var route modispatch.RouteConfig
		if err := json.Unmarshal([]byte(entry.SpecJSON), &route); err != nil {
			return nil, nil, fmt.Errorf("MO route %d: stored spec is not valid JSON: %w", entry.Order, err)
		}
		route.Order = entry.Order
		routes = append(routes, route)
	}
	return stored, routes, nil
}

func moConnectorLabel(route modispatch.RouteConfig) string {
	if route.Connector.Type == "http" {
		return fmt.Sprintf("http(%s)", route.Connector.CID)
	}
	return fmt.Sprintf("smppc(%s)", route.Connector.SystemID)
}

func (s *session) listMORoutes() string {
	_, routes, err := s.storedMORoutes()
	if err != nil {
		return err.Error()
	}
	rows := make([]routeRow, 0, len(routes))
	for _, route := range routes {
		types := make([]string, 0, len(route.Filters))
		args := make([]map[string]string, 0, len(route.Filters))
		for _, filter := range route.Filters {
			types = append(types, filter.Type)
			args = append(args, moFilterArgs(filter))
		}
		rows = append(rows, routeRow{
			order:   route.Order,
			class:   routeClassName(moDirection, route.Default, 1),
			targets: moConnectorLabel(route),
			filters: filterDescriptions(types, args),
		})
	}
	return renderRouteRows(rows, false, len(routes), "MO Route")
}

func (s *session) showMORoute(orderText string) string {
	if orderText == "" {
		return "Missing required option"
	}
	order, err := strconv.Atoi(orderText)
	if err != nil {
		return fmt.Sprintf("Unknown MO Route: %s", orderText)
	}
	ctx, cancel := s.context()
	defer cancel()
	entry, err := s.server.deps.MORoutes.GetRoute(ctx, order)
	if err != nil {
		return fmt.Sprintf("Unknown MO Route: %s", orderText)
	}
	var route modispatch.RouteConfig
	if err := json.Unmarshal([]byte(entry.SpecJSON), &route); err != nil {
		return fmt.Sprintf("MO route %d: stored spec is not valid JSON: %v", order, err)
	}
	// MO routes carry no rate, and the oracle says so in words.
	return fmt.Sprintf("%s to %s NOT RATED",
		routeClassName(moDirection, route.Default, 1), moConnectorLabel(route))
}

func (s *session) removeMORoute(orderText string) string {
	if orderText == "" {
		return "Missing required option"
	}
	order, err := strconv.Atoi(orderText)
	if err != nil {
		return fmt.Sprintf("Unknown MO Route: %s", orderText)
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.MORoutes.DeleteRoute(ctx, order); err != nil {
		return fmt.Sprintf("Unknown MO Route: %s", orderText)
	}
	return fmt.Sprintf("Successfully removed MO Route with order:%d", order)
}

func (s *session) flushMORoutes() string {
	stored, _, err := s.storedMORoutes()
	if err != nil {
		return err.Error()
	}
	ctx, cancel := s.context()
	defer cancel()
	for _, entry := range stored {
		if err := s.server.deps.MORoutes.DeleteRoute(ctx, entry.Order); err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
	}
	return fmt.Sprintf("Successfully flushed MO Route table (%d flushed entries)", len(stored))
}
