package logging_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/logging"
)

func TestHandlerRendersJasminLineFormat(t *testing.T) {
	fixed := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	message := "SMS-MT [cid:x] [status:ESME_ROK]"
	cases := []struct {
		level slog.Level
		name  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
		{logging.LevelCritical, "CRITICAL"},
	}
	for _, testCase := range cases {
		var buffer bytes.Buffer
		handler := logging.NewHandler(logging.Config{Level: "DEBUG", Writer: &buffer})
		record := slog.NewRecord(fixed, testCase.level, message, 0)
		if err := handler.Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
		// %(asctime)s %(levelname)-8s %(process)d %(message)s — pid is this process.
		want := fmt.Sprintf("2026-07-26 12:00:00 %-8s %d %s\n", testCase.name, os.Getpid(), message)
		if buffer.String() != want {
			t.Errorf("level %s line = %q, want %q", testCase.name, buffer.String(), want)
		}
	}
}

func TestLoggerLevelGating(t *testing.T) {
	var buffer bytes.Buffer
	logger := logging.Logger("smpp.client.c1", logging.Config{Level: "INFO", Writer: &buffer})
	logger.Debug("below threshold")
	if buffer.Len() != 0 {
		t.Fatalf("DEBUG emitted at INFO level: %q", buffer.String())
	}
	logger.Info("at threshold")
	if !bytes.Contains(buffer.Bytes(), []byte("INFO")) || !bytes.Contains(buffer.Bytes(), []byte("at threshold")) {
		t.Fatalf("INFO not emitted: %q", buffer.String())
	}
	// Default (empty) level is INFO.
	var second bytes.Buffer
	logging.Logger("x", logging.Config{Writer: &second}).Debug("hidden")
	if second.Len() != 0 {
		t.Fatalf("default level should gate DEBUG: %q", second.String())
	}
}

func TestLoggerWritesToFileSink(t *testing.T) {
	base := filepath.Join(t.TempDir(), "messages.log")
	logger := logging.Logger("jasmin-sm-listener", logging.Config{File: base, Rotate: "midnight"})
	logger.Info("SMS-MT [cid:x] hello")
	data, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "SMS-MT [cid:x] hello") || !strings.Contains(string(data), "INFO") {
		t.Errorf("file sink content = %q", data)
	}
}

func TestRedactHonoursPrivacy(t *testing.T) {
	content := []byte("secret message body")
	if got := logging.Redact(true, content); got != fmt.Sprintf("** %d byte content **", len(content)) {
		t.Errorf("privacy redaction = %q", got)
	}
	if got := logging.Redact(false, content); got != string(content) {
		t.Errorf("non-privacy = %q", got)
	}
}
