// Package logging renders Go logs in the exact Jasmin (Python logging) line
// format so operator tooling — greps, level filters, log shippers — keeps
// working when a component runs on Go. It is a thin layer over log/slog with a
// custom handler; call sites emit pre-formatted messages, mirroring the legacy
// `self.log.info("SMS-MT [cid:%s] ...", ...)` style.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
)

// dateLayout is the legacy log_date_format '%Y-%m-%d %H:%M:%S'.
const dateLayout = "2006-01-02 15:04:05"

// pid is captured once; unlike Python's per-record %(process)d there is no churn.
var pid = strconv.Itoa(os.Getpid())

// LevelCritical mirrors Python logging CRITICAL (50), above slog's ERROR (8).
const LevelCritical = slog.Level(12)

// levelName maps a slog level to the Python logging level name Jasmin emits
// (WARNING, not WARN; CRITICAL above ERROR).
func levelName(level slog.Level) string {
	switch {
	case level >= LevelCritical:
		return "CRITICAL"
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARNING"
	case level >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// parseLevel maps a config log_level string to a slog level (default INFO).
func parseLevel(name string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	case "CRITICAL", "FATAL":
		return LevelCritical
	default:
		return slog.LevelInfo
	}
}

// handler renders each record as the legacy line:
//
//	%(asctime)s %(levelname)-8s %(process)d %(message)s
//
// It carries no attrs or groups — Jasmin lines are a single formatted message.
type handler struct {
	mu    *sync.Mutex
	out   io.Writer
	level slog.Level
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	line := fmt.Sprintf("%s %-8s %s %s\n",
		record.Time.Format(dateLayout), levelName(record.Level), pid, record.Message)
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, line)
	return err
}

func (h *handler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *handler) WithGroup(_ string) slog.Handler      { return h }

// Config is a component's resolved logging settings (from the parsed log_*).
type Config struct {
	Level string // log_level; "" defaults to INFO
	// Writer overrides the output sink (tests, stderr). Phase 2 wires a
	// rotating file sink from log_file/log_rotate; nil defaults to stderr.
	Writer io.Writer
}

// NewHandler builds the Jasmin-format slog handler for a component.
func NewHandler(cfg Config) slog.Handler {
	out := cfg.Writer
	if out == nil {
		out = os.Stderr
	}
	return &handler{mu: &sync.Mutex{}, out: out, level: parseLevel(cfg.Level)}
}

// Logger builds a component logger. name identifies the component (the legacy
// logger name, e.g. "smpp.client.<cid>"); it drives per-component file/level
// selection by the caller and is intentionally absent from the rendered line,
// matching the legacy format which omits %(name)s.
func Logger(name string, cfg Config) *slog.Logger {
	_ = name
	return slog.New(NewHandler(cfg))
}

// Redact mirrors the legacy log_privacy content handling: with privacy on the
// message content is replaced by "** N byte content **"; off returns the
// content as-is for the call site to format.
func Redact(privacy bool, content []byte) string {
	if privacy {
		return fmt.Sprintf("** %d byte content **", len(content))
	}
	return string(content)
}
