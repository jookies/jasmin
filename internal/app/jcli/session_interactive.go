package jcli

import (
	"fmt"
	"sort"
	"strings"
)

// interactiveSession is the Go form of the oracle's @Session decorator plus
// Manager.startSession/stopSession (jasmin/protocols/cli/managers.py): while
// one is open the prompt is "> " and every typed line is key/value input for
// the entity being built, not a console command.
type interactiveSession struct {
	kind *entityKind

	// updating distinguishes `-u <id>` from `-a`; the oracle's announcements,
	// required-key checks and save messages all differ between them.
	updating bool
	targetID string

	// values holds what has been typed so far, keyed by the console key.
	values map[string]string
	// typed preserves entry order for renderers that care.
	typed []string
}

// entityKind describes one manager's interactive contract. Every string here is
// transcript, captured from the oracle -- see spec/compatibility/fixtures/jcli.
type entityKind struct {
	// keyLabel is the noun in "Unknown %s key: %s" (e.g. "User", "Group",
	// "Httpcc", "Interceptor", "SMPPClientConfig", "Filter").
	keyLabel string
	// addAnnouncement is printed when `-a` opens a session, e.g.
	// "Adding a new User: (ok: save, ko: exit)".
	addAnnouncement string
	// updateAnnouncement takes the target id, e.g.
	// "Updating User id [uid1]: (ok: save, ko: exit)".
	updateAnnouncement func(id string) string
	// required lists keys that must be set before `ok` succeeds on an add.
	required []string
	// validate checks one key/value pair, returning the oracle's error text
	// (empty when accepted). It may normalise the value it stores.
	validate func(session *interactiveSession, key, value string) (string, bool)
	// save applies the collected values, returning the reply text and whether
	// the session should close. "Nothing to save" keeps a session open, which
	// is why closing is a separate decision from the message.
	save func(s *session, is *interactiveSession) (string, bool)
}

// startInteractive opens a session, mirroring Manager.startSession().
func (s *session) startInteractive(kind *entityKind, updating bool, targetID string) string {
	s.interactive = &interactiveSession{
		kind:     kind,
		updating: updating,
		targetID: targetID,
		values:   map[string]string{},
	}
	s.prompt = promptSession
	if updating {
		return kind.updateAnnouncement(targetID)
	}
	return kind.addAnnouncement
}

// stopInteractive mirrors Manager.stopSession(): restore the prompt and draw it
// with no message.
func (s *session) stopInteractive() {
	s.interactive = nil
	s.prompt = promptMain
}

// line consumes one line of interactive input.
func (is *interactiveSession) line(s *session, raw string) {
	command, argument := parseLine(raw)

	switch command {
	case "":
		s.promptOnly()
		return
	case "ko":
		// stopSession() restores the prompt and sends no text.
		s.stopInteractive()
		s.promptOnly()
		return
	case "quit":
		// The oracle refuses to quit out of an open session.
		s.reply("Exit session before quitting")
		return
	case "ok":
		if missing := is.missingRequired(); len(missing) > 0 {
			s.reply(fmt.Sprintf("You must set these options before saving: %s",
				strings.Join(missing, ", ")))
			return
		}
		message, close := is.kind.save(s, is)
		if close {
			s.stopInteractive()
		}
		s.reply(message)
		return
	}

	if is.kind.validate == nil {
		s.reply(fmt.Sprintf("Unknown %s key: %s", is.kind.keyLabel, command))
		return
	}
	message, accepted := is.kind.validate(is, command, argument)
	if !accepted {
		s.reply(message)
		return
	}
	if message != "" {
		s.reply(message)
		return
	}
	s.promptOnly()
}

// set records a key/value pair in entry order.
func (is *interactiveSession) set(key, value string) {
	if _, seen := is.values[key]; !seen {
		is.typed = append(is.typed, key)
	}
	is.values[key] = value
}

func (is *interactiveSession) missingRequired() []string {
	if is.updating {
		// Updates only carry the changed keys; the stored entity supplies the
		// rest, so requiring them here would reject every legal update.
		return nil
	}
	var missing []string
	for _, key := range is.kind.required {
		if _, ok := is.values[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}
