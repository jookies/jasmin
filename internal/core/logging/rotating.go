package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const secondsPerDay int64 = 24 * 60 * 60

// rotatingFileWriter is an io.WriteCloser reproducing Python's
// logging.handlers.TimedRotatingFileHandler for the when values Jasmin uses
// (midnight, W0..W6) with the library defaults it relies on: interval=1,
// backupCount=0 (never deletes), local time, atTime=None. It renames the base
// file to base.<YYYY-MM-DD> at each roll boundary and reopens a fresh base,
// matching the legacy suffix and roll instant so operators can rotate/ship by
// filename. Time is injected for testability.
type rotatingFileWriter struct {
	mu         sync.Mutex
	baseName   string
	when       string // "MIDNIGHT" or "W0".."W6"
	interval   int64  // seconds
	dayOfWeek  int    // 0=Monday..6=Sunday (Python tm_wday), for W*
	rolloverAt int64  // unix seconds
	file       *os.File
	now        func() time.Time
	loc        *time.Location // wall-clock zone (Python uses local time); default time.Local
}

// at renders a unix time in the writer's wall-clock zone, matching the library's
// use of local time for roll-boundary and suffix computation.
func (w *rotatingFileWriter) at(unixSecs int64) time.Time {
	return time.Unix(unixSecs, 0).In(w.loc)
}

// newRotatingFileWriter opens (creating/appending) baseName and seeds the first
// roll boundary from the base file mtime if it exists, else now — exactly as the
// library does. when must be "midnight" or "W0".."W6" (case-insensitive); other
// forms return an error so the caller can fall back to stderr.
func newRotatingFileWriter(baseName, when string, now func() time.Time) (*rotatingFileWriter, error) {
	if now == nil {
		now = time.Now
	}
	writer := &rotatingFileWriter{baseName: baseName, now: now, loc: time.Local}
	switch upper := strings.ToUpper(strings.TrimSpace(when)); {
	case upper == "MIDNIGHT":
		writer.when = "MIDNIGHT"
		writer.interval = secondsPerDay
	case len(upper) == 2 && upper[0] == 'W' && upper[1] >= '0' && upper[1] <= '6':
		writer.when = upper
		writer.interval = secondsPerDay * 7
		writer.dayOfWeek = int(upper[1] - '0')
	default:
		return nil, fmt.Errorf("logging: unsupported log_rotate %q (want midnight or W0..W6)", when)
	}
	if err := writer.openFile(); err != nil {
		return nil, err
	}
	seed := writer.now().Unix()
	if info, err := os.Stat(baseName); err == nil {
		seed = info.ModTime().Unix()
	}
	writer.rolloverAt = writer.computeRollover(seed)
	return writer, nil
}

func (w *rotatingFileWriter) openFile() error {
	file, err := os.OpenFile(w.baseName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.file = file
	return nil
}

// pyWeekday returns the Python tm_wday (Monday=0..Sunday=6) for t, converting from
// Go's time.Weekday (Sunday=0..Saturday=6).
func pyWeekday(t time.Time) int {
	return (int(t.Weekday()) + 6) % 7
}

// computeRollover ports TimedRotatingFileHandler.computeRollover for MIDNIGHT/W:
// the next local midnight, plus (for W) the days until the target weekday and the
// library's ±3600s DST adjustment. MIDNIGHT gets no DST adjustment, matching the
// library's asymmetry.
func (w *rotatingFileWriter) computeRollover(currentTime int64) int64 {
	t := w.at(currentTime)
	secondsIntoDay := int64((t.Hour()*60+t.Minute())*60 + t.Second())
	result := currentTime + (secondsPerDay - secondsIntoDay)
	if strings.HasPrefix(w.when, "W") {
		day := pyWeekday(t)
		if day != w.dayOfWeek {
			var daysToWait int
			if day < w.dayOfWeek {
				daysToWait = w.dayOfWeek - day
			} else {
				daysToWait = 6 - day + w.dayOfWeek + 1
			}
			newRolloverAt := result + int64(daysToWait)*secondsPerDay
			dstNow := t.IsDST()
			dstAtRollover := w.at(newRolloverAt).IsDST()
			if dstNow != dstAtRollover {
				if !dstNow {
					newRolloverAt -= 3600
				} else {
					newRolloverAt += 3600
				}
			}
			result = newRolloverAt
		}
	}
	return result
}

// rolloverSuffix ports doRollover's dated-suffix computation: strftime("%Y-%m-%d")
// of localtime(rolloverAt - interval), with the library's ±3600s DST adjustment
// relative to the current time.
func (w *rotatingFileWriter) rolloverSuffix(currentTime int64) string {
	dstNow := w.at(currentTime).IsDST()
	target := w.rolloverAt - w.interval
	suffixTime := w.at(target)
	if dstThen := suffixTime.IsDST(); dstNow != dstThen {
		if dstNow {
			target += 3600
		} else {
			target -= 3600
		}
		suffixTime = w.at(target)
	}
	return suffixTime.Format("2006-01-02")
}

// doRollover closes the base file, renames it to base.<suffix>, reopens a fresh
// base, and recomputes the next roll boundary (with the library's post-roll
// MIDNIGHT/W DST re-adjust).
func (w *rotatingFileWriter) doRollover(currentTime int64) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	target := w.baseName + "." + w.rolloverSuffix(currentTime)
	if _, err := os.Stat(target); err == nil {
		_ = os.Remove(target)
	}
	if _, err := os.Stat(w.baseName); err == nil {
		if err := os.Rename(w.baseName, target); err != nil {
			return err
		}
	}
	if err := w.openFile(); err != nil {
		return err
	}
	newRolloverAt := w.computeRollover(currentTime)
	for newRolloverAt <= currentTime {
		newRolloverAt += w.interval
	}
	dstNow := w.at(currentTime).IsDST()
	if dstAtRollover := w.at(newRolloverAt).IsDST(); dstNow != dstAtRollover {
		if !dstNow {
			newRolloverAt -= 3600
		} else {
			newRolloverAt += 3600
		}
	}
	w.rolloverAt = newRolloverAt
	return nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	current := w.now().Unix()
	if current >= w.rolloverAt {
		if err := w.doRollover(current); err != nil {
			return 0, err
		}
	}
	if w.file == nil {
		if err := w.openFile(); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *rotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

var _ io.WriteCloser = (*rotatingFileWriter)(nil)
