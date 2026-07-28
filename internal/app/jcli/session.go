package jcli

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"strings"
	"time"
)

// Transcript literals, taken verbatim from the frozen oracle
// (jasmin/protocols/cli/protocol.py and jcli.py) and proven against captured
// fixtures. Changing any of these breaks scripted clients, so they are
// constants rather than formatting decisions.
const (
	promptMain     = "jcli : "
	promptUsername = "Username: "
	promptPassword = "Password: "
	// promptSession is what an interactive key/value session prompts with.
	promptSession = "> "

	authRequiredText = "Authentication required.\n\n"
	authFailedText   = "Incorrect Username/Password.\n"
	unknownCommand   = "Incorrect command: %s, type help for a list of commands"
)

// motd is the banner drawn after a successful login. The trailing newline is
// part of the oracle's string and produces the blank line before "Session ref".
func motd() string {
	return fmt.Sprintf("Welcome to Jasmin %s console\nType help or ? to list commands.\n", LegacyRelease)
}

// session is one console connection.
type session struct {
	server *Server
	conn   net.Conn
	term   *terminal
	ref    int64

	// prompt is the string drawn by sendData; the oracle swaps it as the
	// session changes state (Username → Password → jcli : → > ).
	prompt string

	authenticated bool
	username      string

	// interactive is non-nil while an add/update key/value session is open,
	// mirroring the oracle's sessionLineCallback.
	interactive *interactiveSession
}

func newSession(server *Server, conn net.Conn, ref int64) *session {
	return &session{
		server: server,
		conn:   conn,
		term:   newTerminal(conn),
		ref:    ref,
		prompt: promptUsername,
	}
}

// sendData mirrors the legacy sendData(): optionally write a body followed by
// a line break, then optionally redraw the prompt.
func (s *session) sendData(body string, writeBody, drawPrompt bool) {
	if writeBody {
		s.term.writeRaw(strings.ReplaceAll(body, "\n", lineBreak))
		s.term.writeRaw(lineBreak)
	}
	if drawPrompt {
		s.term.writeRaw(s.prompt)
	}
}

// reply is the common case: a command's output, then the prompt.
func (s *session) reply(body string) { s.sendData(body, true, true) }

// promptOnly redraws the prompt with no output, like sendData() with no args.
func (s *session) promptOnly() { s.sendData("", false, true) }

// drawMotd mirrors CmdProtocol.drawMotd(): banner and session ref, each written
// without a prompt (the caller draws it).
func (s *session) drawMotd() {
	s.sendData(motd(), true, false)
	s.sendData(fmt.Sprintf("Session ref: %d", s.ref), true, false)
}

// run drives the connection: the screen setup, the auth exchange, then the
// command loop.
func (s *session) run(ctx context.Context) {
	s.server.logger.Info("jcli: session opened", "session", s.ref, "peer", s.conn.RemoteAddr().String())
	defer s.server.logger.Info("jcli: session closed", "session", s.ref)

	s.term.initializeScreen()
	if s.server.deps.AuthenticationRequired() {
		// initializeScreen() deliberately draws no prompt, so the operator sees
		// "Authentication required." and types blind until the Password prompt.
		s.term.writeRaw(strings.ReplaceAll(authRequiredText, "\n", lineBreak))
	} else {
		s.authenticated = true
		s.prompt = promptMain
		s.term.complete = s.completeLine
		s.drawMotd()
	}

	for {
		if ctx.Err() != nil {
			return
		}
		if s.server.deps.IdleTimeout > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.server.deps.IdleTimeout))
		}
		line, err := s.term.readLine()
		if err != nil {
			return
		}

		if !s.authenticated {
			s.handleAuthLine(line)
			continue
		}
		if quit := s.handleCommandLine(line); quit {
			// The oracle resets the terminal and drops the connection; it emits
			// no goodbye text.
			s.term.writeRaw(terminalReset)
			return
		}
	}
}

// handleAuthLine advances the username → password exchange, mirroring
// auth_username()/auth_password().
func (s *session) handleAuthLine(line string) {
	if s.username == "" {
		// A blank username just re-prompts, matching auth_username().
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			s.username = trimmed
			s.prompt = promptPassword
			s.term.echo = false // the oracle stops echoing while a password is typed
		}
		s.promptOnly()
		return
	}

	candidate := strings.TrimSpace(line)
	s.term.echo = true
	userDigest := sha256.Sum256([]byte(s.username))
	passDigest := sha256.Sum256([]byte(candidate))
	wantUser := sha256.Sum256([]byte(s.server.deps.Username))
	wantPass := sha256.Sum256([]byte(s.server.deps.Password))
	userOK := subtle.ConstantTimeCompare(userDigest[:], wantUser[:])
	passOK := subtle.ConstantTimeCompare(passDigest[:], wantPass[:])

	if userOK&passOK != 1 {
		s.server.logger.Warn("jcli: authentication failed",
			"session", s.ref, "peer", s.conn.RemoteAddr().String(), "username", s.username)
		s.username = ""
		s.prompt = promptUsername
		s.reply(authFailedText)
		return
	}

	s.authenticated = true
	s.prompt = promptMain
	s.term.complete = s.completeLine
	s.server.logger.Info("jcli: authenticated", "session", s.ref, "username", s.username)
	s.drawMotd()
	s.promptOnly()
}

// handleCommandLine dispatches one command. It reports whether to close.
func (s *session) handleCommandLine(line string) bool {
	// Inside an interactive session every line is key/value input, including
	// what would otherwise be a command.
	if s.interactive != nil {
		s.interactive.line(s, line)
		return false
	}

	command, argument := parseLine(line)
	if command == "" {
		s.promptOnly()
		return false
	}
	if command == "quit" {
		return true
	}
	handler, known := commandTable[command]
	if !known {
		s.reply(fmt.Sprintf(unknownCommand, strings.TrimSpace(line)))
		return false
	}
	s.reply(handler(s, argument))
	return false
}

// parseLine splits a line into its command word and the remainder, mirroring
// the legacy parseline(): "?" is an alias for help.
func parseLine(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "?") {
		return "help", strings.TrimSpace(trimmed[1:])
	}
	command, argument, _ := strings.Cut(trimmed, " ")
	return command, strings.TrimSpace(argument)
}
