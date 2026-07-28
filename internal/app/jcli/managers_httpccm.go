package jcli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// HTTP client connector management (J-013). An httpcc is a destination for MO
// delivery: a base URL and a method. The Go MO route embeds the destination
// inline, so this registry names them for the operator and the route commands
// copy the resolved connector into the route spec.

// httpConnector is one registry entry.
type httpConnector struct {
	BaseURL string `json:"baseurl"`
	Method  string `json:"method"`
}

// legacyHTTPConnectorID is the frozen cid constraint
// (jasmin/routing/jasminApi.py:387) — note the 3-character minimum, which is
// why a two-character cid is rejected.
var legacyHTTPConnectorID = regexp.MustCompile(`^[A-Za-z0-9_-]{3,25}$`)

var httpConnectorKind = &entityKind{
	keyLabel:        "Httpcc",
	addAnnouncement: "Adding a new Httpcc: (ok: save, ko: exit)",
	updateAnnouncement: func(cid string) string {
		return fmt.Sprintf("Updating Httpcc id [%s]: (ok: save, ko: exit)", cid)
	},
	required: []string{"cid", "url"},
	validate: func(is *interactiveSession, key, value string) (string, bool) {
		switch key {
		case "cid":
			if !legacyHTTPConnectorID.MatchString(value) {
				return "Error: HttpConnector cid syntax is invalid", false
			}
		case "url":
		case "method":
			upper := strings.ToUpper(value)
			if upper != "GET" && upper != "POST" {
				return fmt.Sprintf("Error: Http method must be GET or POST, got %s", value), false
			}
		default:
			return fmt.Sprintf("Unknown Httpcc key: %s", key), false
		}
		is.set(key, value)
		return "", true
	},
	save: func(s *session, is *interactiveSession) (string, bool) {
		cid := is.values["cid"]
		method := is.values["method"]
		if method == "" {
			method = "GET"
		}
		spec, err := json.Marshal(httpConnector{BaseURL: is.values["url"], Method: strings.ToUpper(method)})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		ctx, cancel := s.context()
		defer cancel()
		if err := s.server.deps.HTTPConnectors.Put(ctx, cid, string(spec)); err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		return fmt.Sprintf("Successfully added Httpcc [HttpConnector] with cid:%s", cid), true
	},
}

func (s *session) handleHTTPCCM(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listHTTPConnectors()
	case isShowVerb(verb):
		return s.showHTTPConnector(operand)
	case verb == "-a" || verb == "--add":
		return s.startInteractive(httpConnectorKind, false, "")
	case verb == "-r" || verb == "--remove":
		return s.removeHTTPConnector(operand)
	case verb == "":
		return "Missing required option"
	default:
		return unsupportedVerb("httpccm", verb)
	}
}

func (s *session) listHTTPConnectors() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.HTTPConnectors.List(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(stored) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("Httpcc id", 16),
			padRight("Type", 22),
			padRight("Method", 6),
			padRight("URL", 64),
		}, " "))
		for _, spec := range stored {
			var entry httpConnector
			if err := json.Unmarshal([]byte(spec.SpecJSON), &entry); err != nil {
				return fmt.Sprintf("httpcc %q: stored spec is not valid JSON: %v", spec.ID, err)
			}
			lines = append(lines, "#"+strings.Join([]string{
				padRight(spec.ID, 16),
				padRight("HttpConnector", 22),
				padRight(strings.ToUpper(entry.Method), 6),
				padRight(entry.BaseURL, 64),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total Httpccs: %d", len(stored)))
	return strings.Join(lines, "\n")
}

func (s *session) showHTTPConnector(cid string) string {
	if cid == "" {
		return "Missing required option"
	}
	entry, err := s.lookupHTTPConnector(cid)
	if err != nil {
		return fmt.Sprintf("Unknown Httpcc: %s", cid)
	}
	return fmt.Sprintf("HttpConnector:\ncid = %s\nbaseurl = %s\nmethod = %s",
		cid, entry.BaseURL, strings.ToUpper(entry.Method))
}

func (s *session) removeHTTPConnector(cid string) string {
	if cid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.HTTPConnectors.Delete(ctx, cid); err != nil {
		return fmt.Sprintf("Unknown Httpcc: %s", cid)
	}
	return fmt.Sprintf("Successfully removed Httpcc id:%s", cid)
}

func (s *session) lookupHTTPConnector(cid string) (httpConnector, error) {
	ctx, cancel := s.context()
	defer cancel()
	spec, err := s.server.deps.HTTPConnectors.Get(ctx, cid)
	if err != nil {
		return httpConnector{}, err
	}
	var entry httpConnector
	if err := json.Unmarshal([]byte(spec.SpecJSON), &entry); err != nil {
		return httpConnector{}, err
	}
	return entry, nil
}
