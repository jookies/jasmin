package jcli

import (
	"fmt"
	"strings"
)

// commandHandler runs one console command and returns the text to emit before
// the next prompt. An empty return means "just re-prompt".
type commandHandler func(*session, string) string

// commandOrder is the order the legacy console registers its commands, which is
// also the order `help` lists them. Do not sort it.
var commandOrder = []string{
	"persist", "load", "user", "group", "filter",
	"mointerceptor", "mtinterceptor", "morouter", "mtrouter",
	"smppccm", "httpccm", "stats",
}

// commandDocs are the legacy docstrings, rendered by `help`.
var commandDocs = map[string]string{
	"persist":       "Persist current configuration profile to disk in PROFILE",
	"load":          "Load configuration PROFILE profile from disk",
	"user":          "User management",
	"group":         "Group management",
	"filter":        "Filter management",
	"mointerceptor": "MO Interceptor management",
	"mtinterceptor": "MT Interceptor management",
	"morouter":      "MO Router management",
	"mtrouter":      "MT Router management",
	"smppccm":       "SMPP connector management",
	"httpccm":       "HTTP client connector management",
	"stats":         "Stats management",
}

var baseCommandOrder = []string{"quit", "help"}

var baseCommandDocs = map[string]string{
	"quit": "Disconnect from console",
	"help": `List available commands with "help" or detailed help with "help cmd".`,
}

// commandTable maps a command word to its handler. Commands not yet implemented
// are absent, so they report the legacy unknown-command text rather than
// pretending to work — an operator must never think a management action
// succeeded when nothing happened.
var commandTable = map[string]commandHandler{
	"help":    (*session).handleHelp,
	"smppccm": (*session).handleSMPPCCM,
	"mtrouter": func(s *session, argument string) string {
		return s.handleMTRouter(argument)
	},
	"morouter": func(s *session, argument string) string {
		return s.handleMORouter(argument)
	},
	"user": func(s *session, argument string) string {
		return s.handleUser(argument)
	},
}

const nohelpFormat = "*** No help on %s"

// handleHelp renders either the command list or one command's documentation,
// byte-compatible with the legacy do_help().
func (s *session) handleHelp(argument string) string {
	if argument != "" {
		if doc, ok := commandDocs[argument]; ok {
			return doc
		}
		if doc, ok := baseCommandDocs[argument]; ok {
			return doc
		}
		return fmt.Sprintf(nohelpFormat, argument)
	}

	var builder strings.Builder
	writeSection := func(header string, order []string, docs map[string]string) {
		builder.WriteString(header + "\n")
		builder.WriteString(strings.Repeat("=", len(header)))
		for _, command := range order {
			builder.WriteString("\n")
			builder.WriteString(padRight(command, 20))
			if doc, ok := docs[command]; ok {
				builder.WriteString(doc)
			} else {
				builder.WriteString(fmt.Sprintf(nohelpFormat, command))
			}
		}
	}
	writeSection("Available commands:", commandOrder, commandDocs)
	builder.WriteString("\n\n")
	writeSection("Control commands:", baseCommandOrder, baseCommandDocs)
	return builder.String()
}

// padRight mirrors Python's str.ljust(width).
func padRight(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(value))
}
