package jcli

import (
	"fmt"
	"strings"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// Message read-token management.
//
// FORK-LOCAL COMMAND. Every other verb in this console is a byte-for-byte
// reimplementation of legacy Jasmin's; this one has no counterpart there,
// because legacy Jasmin has no message spool to read. Adding it changes the
// output of bare `help`, so the J-002-help fixture is a fork baseline rather
// than an oracle recording — see docs/deviations.md D-005.
//
// A read token authenticates the pull API (GET /messages on the REST listener)
// for a downstream application. It is deliberately NOT a gateway user and NOT
// the admin token: it can read spooled messages for the connectors in its scope
// and nothing else. Handing an application the admin token so it could read its
// own traffic would also let it mint users and start connectors.
//
// The secret is printed exactly once, by `-a`. The service stores a SHA-256
// proof and not the secret, so no later command can show it again — which is
// why `ok` prints it inline instead of a "created" acknowledgement.

// msgConsumerKind drives the interactive add session.
var msgConsumerKind = &entityKind{
	keyLabel:        "MsgConsumer",
	addAnnouncement: "Adding a new MsgConsumer: (ok: save, ko: exit)",
	updateAnnouncement: func(id string) string {
		return fmt.Sprintf("Updating MsgConsumer id [%s]: (ok: save, ko: exit)", id)
	},
	// connectors is required, not optional-defaulting-to-all: a token whose
	// scope is empty would read every connector's traffic, and that must be an
	// explicit act, never the result of forgetting a key.
	required: []string{"cid", "connectors"},
	validate: func(is *interactiveSession, key, value string) (string, bool) {
		switch key {
		case "cid":
			if err := msgspool.ValidateConsumerID(value); err != nil {
				return "Error: MsgConsumer cid syntax is invalid", false
			}
		case "label":
		case "connectors":
			if strings.TrimSpace(value) == "" {
				return "Error: connectors must name at least one termination connector", false
			}
		case "include_text":
			switch strings.ToLower(value) {
			case "yes", "no", "true", "false", "1", "0":
			default:
				return fmt.Sprintf("Error: include_text must be yes or no, got %s", value), false
			}
		default:
			return fmt.Sprintf("Unknown MsgConsumer key: %s", key), false
		}
		is.set(key, value)
		return "", true
	},
	save: func(s *session, is *interactiveSession) (string, bool) {
		consumers := s.server.deps.MessageConsumers
		if consumers == nil {
			return msgConsumerUnavailable, false
		}
		id := is.values["cid"]
		scope := msgspool.Scope{
			Connectors:  splitConnectors(is.values["connectors"]),
			IncludeText: parseYesNo(is.values["include_text"], true),
		}
		ctx, cancel := s.context()
		defer cancel()
		view, token, err := consumers.CreateConsumer(ctx, id, is.values["label"], scope)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		// The token is on its own line so a copy-paste cannot pick up the
		// surrounding prose, and the warning follows it rather than preceding
		// it — an operator scrolling back looks at the last line, not the first.
		return fmt.Sprintf(
			"Successfully added MsgConsumer [%s]\ntoken = %s\nThis token is shown once and cannot be recovered.",
			view.Consumer.ID, token), true
	},
}

const msgConsumerUnavailable = "Error: this gateway does not spool messages (no termination connector configured)"

// splitConnectors accepts the comma- or space-separated list an operator types.
func splitConnectors(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseYesNo(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "true", "1":
		return true
	case "no", "false", "0":
		return false
	default:
		return fallback
	}
}

func (s *session) handleMsgConsumer(argument string) string {
	if s.server.deps.MessageConsumers == nil {
		return msgConsumerUnavailable
	}
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listMsgConsumers()
	case isShowVerb(verb):
		return s.showMsgConsumer(operand)
	case verb == "-a" || verb == "--add":
		return s.startInteractive(msgConsumerKind, false, "")
	case verb == "-r" || verb == "--remove":
		return s.removeMsgConsumer(operand)
	case verb == "-x" || verb == "--revoke":
		return s.setMsgConsumerRevoked(operand, true)
	case verb == "-e" || verb == "--enable":
		return s.setMsgConsumerRevoked(operand, false)
	case verb == "":
		return "Missing required option"
	default:
		return unsupportedVerb("msgconsumer", verb)
	}
}

func (s *session) listMsgConsumers() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.MessageConsumers.ListConsumers(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(stored) > 0 {
		lines = append(lines, "#"+strings.Join([]string{
			padRight("MsgConsumer id", 24),
			padRight("State", 9),
			padRight("Text", 5),
			padRight("Last used", 21),
			padRight("Connectors", 40),
		}, " "))
		for _, view := range stored {
			consumer := view.Consumer
			state := "active"
			if consumer.Revoked {
				state = "revoked"
			}
			// "never" is the row worth noticing: a token issued and never used
			// is either an integration that never shipped or a credential
			// sitting somewhere nobody watches.
			lastUsed := "never"
			if consumer.LastUsedAt != nil {
				lastUsed = consumer.LastUsedAt.UTC().Format("2006-01-02 15:04:05")
			}
			lines = append(lines, "#"+strings.Join([]string{
				padRight(consumer.ID, 24),
				padRight(state, 9),
				padRight(yesNo(consumer.Scope.IncludeText), 5),
				padRight(lastUsed, 21),
				padRight(strings.Join(consumer.Scope.Connectors, ","), 40),
			}, " "))
		}
	}
	lines = append(lines, fmt.Sprintf("Total MsgConsumers: %d", len(stored)))
	return strings.Join(lines, "\n")
}

func (s *session) showMsgConsumer(id string) string {
	if id == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	view, err := s.server.deps.MessageConsumers.GetConsumer(ctx, id)
	if err != nil {
		return fmt.Sprintf("Unknown MsgConsumer: %s", id)
	}
	consumer := view.Consumer
	lastUsed := "never"
	if consumer.LastUsedAt != nil {
		lastUsed = consumer.LastUsedAt.UTC().Format("2006-01-02 15:04:05")
	}
	state := "active"
	if consumer.Revoked {
		state = "revoked"
	}
	// No token field: it is unrecoverable by construction, and printing an
	// empty or masked one would suggest otherwise.
	return fmt.Sprintf(
		"MsgConsumer:\ncid = %s\nlabel = %s\nstate = %s\nconnectors = %s\ninclude_text = %s\nlast_used = %s",
		consumer.ID, consumer.Label, state,
		strings.Join(consumer.Scope.Connectors, ","),
		yesNo(consumer.Scope.IncludeText), lastUsed)
}

func (s *session) removeMsgConsumer(id string) string {
	if id == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.MessageConsumers.DeleteConsumer(ctx, id); err != nil {
		return fmt.Sprintf("Unknown MsgConsumer: %s", id)
	}
	return fmt.Sprintf("Successfully removed MsgConsumer id:%s", id)
}

// setMsgConsumerRevoked is separate from remove because they answer different
// questions: revoke stops an application immediately but keeps the row, so the
// audit trail and the "who was this" answer survive; remove erases both.
func (s *session) setMsgConsumerRevoked(id string, revoked bool) string {
	if id == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if _, err := s.server.deps.MessageConsumers.SetRevoked(ctx, id, revoked); err != nil {
		return fmt.Sprintf("Unknown MsgConsumer: %s", id)
	}
	if revoked {
		return fmt.Sprintf("Successfully revoked MsgConsumer id:%s", id)
	}
	return fmt.Sprintf("Successfully re-enabled MsgConsumer id:%s", id)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
