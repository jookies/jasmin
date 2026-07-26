package config

import "os"

// Default log line format and date format, matching the log_format /
// log_date_format defaults every Jasmin ConfigFile subclass applies.
const (
	DefaultLogFormat     = "%(asctime)s %(levelname)-8s %(process)d %(message)s"
	DefaultLogDateFormat = "%Y-%m-%d %H:%M:%S"
)

// LogConfig is a section's resolved logging directives (the log_* keys). It maps
// 1:1 to the log_file/log_rotate/log_level/log_format/log_date_format a Jasmin
// ConfigFile subclass exposes. Level is the level NAME (e.g. "INFO"); the legacy
// code stores logging.getLevelName() of it (an int), but the wire-visible input
// is the name, which is what the Go logger consumes. log_privacy stays a
// separate call-site flag on the owning section, not part of this struct.
type LogConfig struct {
	File       string // log_file, absolute; defaults to LogPath()/<name>
	Rotate     string // log_rotate: TimedRotatingFileHandler 'when' (midnight, W6, ...)
	Level      string // log_level name (INFO default)
	Format     string // log_format
	DateFormat string // log_date_format
}

// loadLogConfig reads the five log_* directives for a section, applying the
// per-section log_file/log_rotate defaults the legacy config uses. The log_file
// default is resolved under LogPath() exactly as the legacy '%s/<name>' % LOG_PATH;
// an explicit log_file (or its SECTION_LOG_FILE env override) wins via File.Get.
func loadLogConfig(file *File, section, defaultFileName, defaultRotate string) LogConfig {
	return LogConfig{
		File:       file.Get(section, "log_file", LogPath()+"/"+defaultFileName),
		Rotate:     file.Get(section, "log_rotate", defaultRotate),
		Level:      file.Get(section, "log_level", "INFO"),
		Format:     file.Get(section, "log_format", DefaultLogFormat),
		DateFormat: file.Get(section, "log_date_format", DefaultLogDateFormat),
	}
}

// LogPath reproduces jasmin.config.LOG_PATH resolution: under Kubernetes
// (KUBERNETES_SERVICE_HOST set) it is '<ROOT_PATH>/var/log/jasmin/<HOSTNAME>' and
// $LOG_PATH is ignored; otherwise it is $LOG_PATH, else '<ROOT_PATH>/var/log/jasmin'.
// ROOT_PATH defaults to '/', so the bare default is the double-slashed
// '//var/log/jasmin' — reproduced verbatim for path parity. Resolved per call
// (not cached at import like the legacy) so an env override is always honoured.
func LogPath() string {
	rootPath := getenvDefault("ROOT_PATH", "/")
	if _, underK8s := os.LookupEnv("KUBERNETES_SERVICE_HOST"); underK8s {
		return rootPath + "/var/log/jasmin/" + getenvDefault("HOSTNAME", "default-hostname")
	}
	return getenvDefault("LOG_PATH", rootPath+"/var/log/jasmin")
}

func getenvDefault(key, def string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return def
}
