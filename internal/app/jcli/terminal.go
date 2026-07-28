package jcli

import (
	"bufio"
	"io"
	"net"
	"strings"
)

// The legacy console is a Twisted telnet terminal (recvline.HistoricRecvLine
// behind a TelnetTransport), not a plain line server, and scripted clients read
// its raw byte stream. Every constant below is transcribed from a transcript
// captured off the frozen oracle (spec/compatibility/fixtures/jcli/), not from
// reading the source — the byte sequences are emergent behaviour of the Twisted
// stack, so the recording is the only trustworthy source.
const (
	iac  = 0xff // interpret as command
	dont = 0xfe
	do   = 0xfd
	wont = 0xfc
	will = 0xfb
	sb   = 0xfa // subnegotiation begin
	se   = 0xf0 // subnegotiation end

	optEcho     = 0x01
	optSGA      = 0x03 // suppress go-ahead
	optNAWS     = 0x1f // negotiate about window size
	optLinemode = 0x22
)

// telnetHandshake is what the oracle emits the instant a connection lands:
// DO LINEMODE, DO NAWS, DO SUPPRESS-GO-AHEAD, WILL ECHO.
var telnetHandshake = []byte{
	iac, do, optLinemode,
	iac, do, optNAWS,
	iac, do, optSGA,
	iac, will, optEcho,
}

const (
	// terminalReset is ESC c. Twisted's initializeScreen() sends it on connect
	// and the console sends it again when a session quits.
	terminalReset = "\x1bc"
	// insertMode is ESC [ 4 h, the second half of initializeScreen().
	insertMode = "\x1b[4h"

	// lineBreak is what ends every emitted line. It is four bytes, not two:
	// the terminal's nextLine() and the telnet transport's newline translation
	// compose into "\r\r\r\n". Scripts that split on it depend on the exact form.
	lineBreak = "\r\r\r\n"
)

// terminal is the byte-level half of a console connection: telnet option
// filtering and input echo on the way in, line-break translation on the way out.
type terminal struct {
	conn   net.Conn
	reader *bufio.Reader

	// echo mirrors typed characters back, which the oracle does for every line
	// except a password (jcli.py switches it off around the password prompt).
	echo bool
}

func newTerminal(conn net.Conn) *terminal {
	return &terminal{conn: conn, reader: bufio.NewReader(conn), echo: true}
}

// initializeScreen sends the handshake and the screen setup, in the oracle's
// order. It writes no prompt: CmdProtocol.initializeScreen() overrides the
// Twisted default precisely to suppress it, which is why a fresh connection
// never shows "Username: ".
func (t *terminal) initializeScreen() {
	_, _ = t.conn.Write(telnetHandshake)
	t.writeRaw(terminalReset)
	t.writeRaw(insertMode)
}

func (t *terminal) writeRaw(text string) {
	if text == "" {
		return
	}
	_, _ = io.WriteString(t.conn, text)
}

// writeLine writes text followed by the line break. A multi-line body is split
// so every line gets the same terminator, matching terminal.write() +
// nextLine() over the oracle's "\n"-joined strings.
func (t *terminal) writeLine(text string) {
	t.writeRaw(strings.ReplaceAll(text, "\n", lineBreak) + lineBreak)
}

// readLine assembles one input line, filtering telnet negotiation and echoing
// what it keeps. It returns io.EOF-style errors from the underlying connection.
func (t *terminal) readLine() (string, error) {
	var line []byte
	for {
		b, err := t.reader.ReadByte()
		if err != nil {
			return "", err
		}

		switch b {
		case iac:
			if err := t.consumeCommand(); err != nil {
				return "", err
			}
			continue
		case '\r':
			// A client may send CR, CRLF or CRNUL; swallow the pair's tail.
			next, err := t.reader.Peek(1)
			if err == nil && (next[0] == '\n' || next[0] == 0) {
				_, _ = t.reader.ReadByte()
			}
			t.echoBytes(lineBreak)
			return string(line), nil
		case '\n':
			t.echoBytes(lineBreak)
			return string(line), nil
		case 0x7f, 0x08: // DEL / BS
			// Line editing is not part of any captured transcript (scripted
			// clients send whole lines). Erasing the last character keeps an
			// interactive operator sane without inventing a wire contract.
			if len(line) > 0 {
				line = line[:len(line)-1]
				t.echoBytes("\b \b")
			}
			continue
		default:
			if b < 0x20 {
				continue // ignore other control bytes, as recvline does
			}
			line = append(line, b)
			t.echoBytes(string(b))
		}
	}
}

func (t *terminal) echoBytes(text string) {
	if t.echo {
		t.writeRaw(text)
	}
}

// consumeCommand reads the remainder of an IAC sequence and discards it. The
// console negotiates nothing beyond its opening offer, so a client's replies
// only need to be kept out of the command stream.
func (t *terminal) consumeCommand() error {
	verb, err := t.reader.ReadByte()
	if err != nil {
		return err
	}
	switch verb {
	case will, wont, do, dont:
		_, err = t.reader.ReadByte() // the option byte
		return err
	case sb:
		for {
			b, err := t.reader.ReadByte()
			if err != nil {
				return err
			}
			if b != iac {
				continue
			}
			next, err := t.reader.ReadByte()
			if err != nil {
				return err
			}
			if next == se {
				return nil
			}
		}
	default:
		return nil // two-byte command (NOP, AYT, ...)
	}
}
