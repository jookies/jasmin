package config_test

import (
	"os"
	"testing"

	"github.com/pumpitspace/jasmin/internal/config"
)

// withLogEnv saves ROOT_PATH/LOG_PATH/KUBERNETES_SERVICE_HOST/HOSTNAME, applies
// the given values (empty string => unset), and restores on cleanup. Not
// parallel-safe (mutates process env), so tests using it must not call t.Parallel.
func withLogEnv(t *testing.T, values map[string]string) {
	t.Helper()
	keys := []string{"ROOT_PATH", "LOG_PATH", "KUBERNETES_SERVICE_HOST", "HOSTNAME"}
	saved := make(map[string]*string, len(keys))
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			s := v
			saved[k] = &s
		} else {
			saved[k] = nil
		}
	}
	t.Cleanup(func() {
		for _, k := range keys {
			if saved[k] == nil {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, *saved[k])
			}
		}
	})
	for _, k := range keys {
		if v, ok := values[k]; ok && v != "" {
			os.Setenv(k, v)
		} else {
			os.Unsetenv(k)
		}
	}
}

func TestLogPath(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "bare default is double-slashed (ROOT_PATH defaults to /)",
			env:  nil,
			want: "//var/log/jasmin",
		},
		{
			name: "ROOT_PATH prefixes the default log dir",
			env:  map[string]string{"ROOT_PATH": "/opt/jasmin"},
			want: "/opt/jasmin/var/log/jasmin",
		},
		{
			name: "explicit LOG_PATH wins over the ROOT_PATH default",
			env:  map[string]string{"ROOT_PATH": "/opt", "LOG_PATH": "/custom/logs"},
			want: "/custom/logs",
		},
		{
			name: "kubernetes appends HOSTNAME and ignores LOG_PATH",
			env:  map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1", "ROOT_PATH": "/k8s", "HOSTNAME": "pod-7", "LOG_PATH": "/ignored"},
			want: "/k8s/var/log/jasmin/pod-7",
		},
		{
			name: "kubernetes without HOSTNAME uses default-hostname",
			env:  map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"},
			want: "//var/log/jasmin/default-hostname",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			withLogEnv(t, testCase.env)
			if got := config.LogPath(); got != testCase.want {
				t.Errorf("LogPath() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestSectionLogDefaults(t *testing.T) {
	withLogEnv(t, map[string]string{"LOG_PATH": "/var/log/jasmin"})
	file, err := config.ParseString("")
	if err != nil {
		t.Fatal(err)
	}
	// Per-section defaults: file name under LOG_PATH, and the midnight/W6 rotate split.
	server, err := config.LoadSMPPServer(file)
	if err != nil {
		t.Fatal(err)
	}
	wantServer := config.LogConfig{
		File: "/var/log/jasmin/default-smpps_01.log", Rotate: "midnight", Level: "INFO",
		Format: config.DefaultLogFormat, DateFormat: config.DefaultLogDateFormat,
	}
	if server.Log != wantServer {
		t.Errorf("smpp-server Log = %+v, want %+v", server.Log, wantServer)
	}
	api, err := config.LoadHTTPAPI(file)
	if err != nil {
		t.Fatal(err)
	}
	if api.Log.File != "/var/log/jasmin/http-api.log" || api.Log.Rotate != "W6" {
		t.Errorf("http-api Log = %+v", api.Log)
	}
	dlr, err := config.LoadDLR(file)
	if err != nil {
		t.Fatal(err)
	}
	if dlr.Log.File != "/var/log/jasmin/messages.log" || dlr.Log.Rotate != "midnight" {
		t.Errorf("dlr Log = %+v", dlr.Log)
	}
	sml, err := config.LoadSMListener(file)
	if err != nil {
		t.Fatal(err)
	}
	if sml.Log.File != "/var/log/jasmin/messages.log" || sml.Log.Rotate != "midnight" {
		t.Errorf("sm-listener Log = %+v", sml.Log)
	}
}

func TestSectionLogExplicitValues(t *testing.T) {
	text := "[smpp-server]\nid = smpps_prod\nlog_file = /srv/logs/s.log\nlog_rotate = W0\n" +
		"log_level = DEBUG\nlog_format = %(message)s\nlog_date_format = %H:%M:%S\n"
	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	server, err := config.LoadSMPPServer(file)
	if err != nil {
		t.Fatal(err)
	}
	want := config.LogConfig{
		File: "/srv/logs/s.log", Rotate: "W0", Level: "DEBUG",
		Format: "%(message)s", DateFormat: "%H:%M:%S",
	}
	if server.Log != want {
		t.Errorf("explicit Log = %+v, want %+v", server.Log, want)
	}
}
