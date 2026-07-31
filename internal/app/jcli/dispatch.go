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
	"smppccm", "httpccm", "msgconsumer", "stats",
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
	"msgconsumer":   "Message read-token management",
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
	"group": func(s *session, argument string) string {
		return s.handleGroup(argument)
	},
	"filter": func(s *session, argument string) string {
		return s.handleFilter(argument)
	},
	"httpccm": func(s *session, argument string) string {
		return s.handleHTTPCCM(argument)
	},
	"msgconsumer": func(s *session, argument string) string {
		return s.handleMsgConsumer(argument)
	},
	"stats": func(s *session, argument string) string {
		return s.handleStats(argument)
	},
	"persist": func(s *session, argument string) string {
		return s.handlePersist(argument)
	},
	"load": func(s *session, argument string) string {
		return s.handleLoad(argument)
	},
	"mointerceptor": func(s *session, argument string) string {
		return s.handleInterceptor(moInterceptorDirection, argument)
	},
	"mtinterceptor": func(s *session, argument string) string {
		return s.handleInterceptor(mtInterceptorDirection, argument)
	},
}

const nohelpFormat = "*** No help on %s"

// handleHelp renders either the command list or one command's documentation,
// byte-compatible with the legacy do_help().
func (s *session) handleHelp(argument string) string {
	if argument != "" {
		// The per-command body is optparse's rendering, captured verbatim.
		if doc, ok := commandHelp[argument]; ok {
			// The trailing newline is optparse's own, and produces the blank
			// line the oracle emits before the prompt. Do not trim it.
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

// findCommands lists the commands matching a prefix, in registration order:
// the manager commands first, then the control commands. An empty prefix lists
// everything, which is what a bare TAB shows.
func findCommands(prefix string) []string {
	var found []string
	for _, command := range append(append([]string{}, commandOrder...), baseCommandOrder...) {
		if strings.HasPrefix(command, prefix) {
			found = append(found, command)
		}
	}
	return found
}

// completeLine handles a TAB, mirroring CmdProtocol.handle_TAB(). It returns
// the line buffer to keep typing from.
//
// The oracle only completes the *command word*: a TAB after an argument has
// begun does nothing at all, and neither does a prefix that matches nothing --
// not even a bell. Both silences are contract.
func (s *session) completeLine(line string) string {
	command, argument := parseLine(line)

	if command == "" {
		// A bare TAB lists every command.
		s.sendData("\n"+strings.Join(findCommands(""), " "), true, true)
		return line
	}
	if argument != "" {
		return line
	}

	completions := findCommands(command)
	switch {
	case len(completions) > 1:
		// List the candidates and redraw the prompt with what was typed.
		s.sendData("\n"+strings.Join(completions, " "), true, false)
		s.term.writeRaw(s.prompt + command)
		return line
	case len(completions) == 1 && completions[0] != command:
		// Unique match: finish the word, with the trailing space the oracle adds.
		completed := completions[0] + " "
		s.term.writeRaw(completed[len(command):])
		return completed
	default:
		return line
	}
}
