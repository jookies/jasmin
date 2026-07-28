package jcli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Transcript literals, taken verbatim from the frozen oracle
// (jasmin/protocols/cli/protocol.py and jcli.py). Changing any of these breaks
// scripted clients, so they are constants rather than formatting decisions.
const (
	promptMain     = "jcli : "
	promptUsername = "Username: "
	promptPassword = "Password: "

	authRequiredText = "Authentication required.\n\n"
	authFailedText   = "Incorrect Username/Password."
	unknownCommand   = "Incorrect command: %s, type help for a list of commands"

	// The terminal emits CRLF between lines.
	newline = "\r\n"
)

// motd is the banner drawn after a successful login.
func motd() string {
	return fmt.Sprintf("Welcome to Jasmin %s console\nType help or ? to list commands.\n", LegacyRelease)
}

// session is one console connection.
type session struct {
	server *Server
	conn   net.Conn
	reader *bufio.Reader
	ref    int64

	authenticated bool
	username      string
}

func newSession(server *Server, conn net.Conn, ref int64) *session {
	return &session{
		server: server,
		conn:   conn,
		reader: bufio.NewReader(conn),
		ref:    ref,
	}
}

// write emits raw bytes, translating the oracle's "\n" line breaks into the
// CRLF a terminal client expects.
func (s *session) write(text string) {
	if text == "" {
		return
	}
	_, _ = io.WriteString(s.conn, strings.ReplaceAll(text, "\n", newline))
}

// sendData mirrors the legacy sendData(): optionally write a line of output,
// then write the current prompt. Passing an empty body just re-prompts.
func (s *session) sendData(body string) {
	if body != "" {
		s.write(body + "\n")
	}
	s.write(s.prompt())
}

func (s *session) prompt() string {
	if s.authenticated {
		return promptMain
	}
	if s.username == "" {
		return promptUsername
	}
	return promptPassword
}

// run drives the connection: the auth exchange, then the command loop.
func (s *session) run(ctx context.Context) {
	s.server.logger.Info("jcli: session opened", "session", s.ref, "peer", s.conn.RemoteAddr().String())
	defer s.server.logger.Info("jcli: session closed", "session", s.ref)

	// Authentication is always required: unlike the legacy console this has no
	// "anonymous" mode, because the console can mint credentials.
	s.write(authRequiredText)
	s.write(s.prompt())

	for {
		if ctx.Err() != nil {
			return
		}
		if s.server.deps.IdleTimeout > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.server.deps.IdleTimeout))
		}
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if !s.authenticated {
			if s.handleAuthLine(line) {
				continue
			}
			return
		}
		if quit := s.handleCommandLine(line); quit {
			return
		}
	}
}

// handleAuthLine advances the username → password exchange. It reports whether
// the session should continue.
func (s *session) handleAuthLine(line string) bool {
	if s.username == "" {
		// A blank username just re-prompts, matching auth_username().
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			s.username = trimmed
		}
		s.write(s.prompt())
		return true
	}

	candidate := strings.TrimSpace(line)
	userDigest := sha256.Sum256([]byte(s.username))
	passDigest := sha256.Sum256([]byte(candidate))
	wantUser := sha256.Sum256([]byte(s.server.deps.Username))
	wantPass := sha256.Sum256([]byte(s.server.deps.Password))
	userOK := subtle.ConstantTimeCompare(userDigest[:], wantUser[:])
	passOK := subtle.ConstantTimeCompare(passDigest[:], wantPass[:])

	if userOK&passOK != 1 {
		s.server.logger.Warn("jcli: authentication failed",
			"session", s.ref, "peer", s.conn.RemoteAddr().String(), "username", s.username)
		// The legacy resets to the username prompt and reports the failure.
		s.username = ""
		s.sendData(authFailedText)
		return true
	}

	s.authenticated = true
	s.server.logger.Info("jcli: authenticated", "session", s.ref, "username", s.username)
	// drawMotd(): banner, then the session ref, then the prompt.
	s.write(motd() + "\n")
	s.write(fmt.Sprintf("Session ref: %d\n", s.ref))
	s.write(s.prompt())
	return true
}

// handleCommandLine dispatches one command. It reports whether to close.
func (s *session) handleCommandLine(line string) bool {
	command, argument := parseLine(line)
	if command == "" {
		s.sendData("")
		return false
	}
	if command == "quit" {
		return true
	}
	handler, known := commandTable[command]
	if !known {
		s.sendData(fmt.Sprintf(unknownCommand, line))
		return false
	}
	s.sendData(handler(s, argument))
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
