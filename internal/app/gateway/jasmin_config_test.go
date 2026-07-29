package gateway

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/app/dlrlookup"
	"github.com/pumpitspace/jasmin/internal/app/dlrthrower"
	"github.com/pumpitspace/jasmin/internal/app/mothrower"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/config"
)

func mustJasmin(t *testing.T, text string) *config.Jasmin {
	t.Helper()
	file, err := config.ParseString(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	jasmin, err := config.LoadJasminFile(file)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return jasmin
}

func TestApplyJasminOverlaysInfraAndEnabledWorkers(t *testing.T) {
	jasmin := mustJasmin(t, "[amqp-broker]\nhost = broker-x\nport = 5673\n"+
		"log_level = ERROR\nlog_file = /srv/log/amqp.log\nlog_rotate = W0\n"+
		"[redis-client]\nhost = redis-x\nport = 6380\n"+
		"[http-api]\nbind = 10.0.0.5\nport = 8080\nlog_file = /srv/log/http.log\naccess_log = /srv/log/access.log\n"+
		"[router]\nlog_file = /srv/log/router.log\n"+
		"[dlr]\nlog_file = /srv/log/dlr.log\n"+
		"[dlr-thrower]\nhttp_timeout = 12\nmax_retries = 7\nlog_file = /srv/log/dlrt.log\n"+
		"[deliversm-thrower]\nlog_file = /srv/log/mot.log\n"+
		"[sm-listener]\nlog_level = DEBUG\nlog_privacy = yes\nlog_file = /srv/log/messages.log\nlog_rotate = W6\n"+
		"[smpp-server]\nbind = 0.0.0.0\nport = 2776\nenquireLinkTimerSecs = 45\n"+
		"log_level = DEBUG\nlog_file = /srv/log/default-smpps_01.log\nlog_rotate = midnight\n")

	cfg := Config{
		Role:                 RoleHTTPAndSMPPc,
		RequiredConnectorIDs: []string{"keep-me"},
		Outbound:             outbound.Config{PostgresDSN: "sentinel-dsn"},
		DLRThrower:           &dlrthrower.Config{},
		SMPPS:                &smppsserver.Config{},
		// MOThrower and DLRLookup left nil: they must stay nil (not enabled here).
	}
	ApplyJasmin(&cfg, jasmin)

	if cfg.Outbound.AMQPURL != jasmin.AMQP.URL() {
		t.Errorf("AMQPURL = %q, want %q", cfg.Outbound.AMQPURL, jasmin.AMQP.URL())
	}
	if cfg.Outbound.ListenAddress != "10.0.0.5:8080" {
		t.Errorf("ListenAddress = %q", cfg.Outbound.ListenAddress)
	}
	if cfg.DLRThrower.HTTPTimeoutSeconds != 12 || cfg.DLRThrower.MaxRetries != 7 {
		t.Errorf("DLRThrower overlay diverges: %+v", cfg.DLRThrower)
	}
	if cfg.SMPPS.BindAddr != "0.0.0.0:2776" || cfg.SMPPS.EnquireLinkTimeoutSeconds != 45 {
		t.Errorf("SMPPS overlay diverges: %+v", cfg.SMPPS)
	}
	if cfg.SMPPServerLog.Level != "DEBUG" || cfg.SMPPServerLog.File != "/srv/log/default-smpps_01.log" ||
		cfg.SMPPServerLog.Rotate != "midnight" {
		t.Errorf("SMPPServerLog overlay diverges: %+v", cfg.SMPPServerLog)
	}
	// The SMS-MT audit line config is overlaid from [sm-listener].
	if cfg.SubmitAuditLog.Level != "DEBUG" || !cfg.SubmitAuditLog.Privacy ||
		cfg.SubmitAuditLog.File != "/srv/log/messages.log" || cfg.SubmitAuditLog.Rotate != "W6" {
		t.Errorf("SubmitAuditLog overlay diverges: %+v", cfg.SubmitAuditLog)
	}
	logs := map[string]struct {
		got  ComponentLogConfig
		file string
	}{
		"router":      {cfg.RouterLog, "/srv/log/router.log"},
		"http-api":    {cfg.HTTPAPILog, "/srv/log/http.log"},
		"http-access": {cfg.HTTPAccessLog, "/srv/log/access.log"},
		"dlr":         {cfg.DLRLog, "/srv/log/dlr.log"},
		"amqp":        {cfg.AMQPLog, "/srv/log/amqp.log"},
		"dlr-thrower": {cfg.DLRThrowerLog, "/srv/log/dlrt.log"},
		"mo-thrower":  {cfg.DeliverSMThrowerLog, "/srv/log/mot.log"},
	}
	for name, expected := range logs {
		if expected.got.File != expected.file {
			t.Errorf("%s logger = %+v, want file %q", name, expected.got, expected.file)
		}
	}
	// Disabled workers stay nil; non-infra fields are untouched.
	if cfg.MOThrower != nil || cfg.DLRLookup != nil {
		t.Error("nil workers must not be created by the overlay")
	}
	if cfg.Role != RoleHTTPAndSMPPc || len(cfg.RequiredConnectorIDs) != 1 || cfg.RequiredConnectorIDs[0] != "keep-me" {
		t.Error("overlay must not touch role or connectors")
	}
	// The Postgres DSN (JSON-only) must survive the overlay.
	if cfg.Outbound.PostgresDSN != "sentinel-dsn" {
		t.Errorf("PostgresDSN clobbered: %q", cfg.Outbound.PostgresDSN)
	}
}

func TestApplyJasminEnabledDLRLookupAndMOThrower(t *testing.T) {
	jasmin := mustJasmin(t, "[redis-client]\nhost = r\nport = 6399\n"+
		"[dlr]\npid = worker-9\ndlr_lookup_max_retries = 4\n"+
		"[deliversm-thrower]\nhttp_timeout = 8\nretry_delay = 3\n")
	cfg := Config{DLRLookup: &dlrlookup.Config{}, MOThrower: &mothrower.Config{}}
	ApplyJasmin(&cfg, jasmin)

	if cfg.DLRLookup.RedisURL != jasmin.Redis.URL() || cfg.DLRLookup.PID != "worker-9" ||
		cfg.DLRLookup.MaxRetries != 4 {
		t.Errorf("DLRLookup overlay diverges: %+v", cfg.DLRLookup)
	}
	if cfg.MOThrower.HTTPTimeoutSeconds != 8 || cfg.MOThrower.RetryDelaySeconds != 3 {
		t.Errorf("MOThrower overlay diverges: %+v", cfg.MOThrower)
	}
}
