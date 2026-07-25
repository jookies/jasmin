package config_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/config"
)

// jasminFileOracleScript loads every config class from the same jasmin.cfg the
// Go aggregator parses and dumps a flat "section.field" map. It covers the
// sections present in the shipped misc/config/jasmin.cfg.
const jasminFileOracleScript = `
import json, sys, tempfile, os
from jasmin.queues.configs import AmqpConfig
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.protocols.smpp.configs import SMPPServerConfig, SMPPServerPBConfig
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.managers.configs import DLRLookupConfig, SMPPClientSMListenerConfig, SMPPClientPBConfig
from jasmin.routing.configs import RouterPBConfig, deliverSmThrowerConfig, DLRThrowerConfig
from jasmin.protocols.cli.configs import JCliConfig
from jasmin.interceptor.configs import InterceptorPBClientConfig

text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
a = AmqpConfig(path); r = RedisForJasminConfig(path)
s = SMPPServerConfig(path); h = HTTPApiConfig(path)
d = DLRLookupConfig(path); sml = SMPPClientSMListenerConfig(path)
cm = SMPPClientPBConfig(path); rt = RouterPBConfig(path)
dst = deliverSmThrowerConfig(path); dlt = DLRThrowerConfig(path)
spb = SMPPServerPBConfig(path); jc = JCliConfig(path)
itc = InterceptorPBClientConfig(path)
os.remove(path)

out = {
    "amqp.host": a.host, "amqp.port": a.port, "amqp.username": a.username, "amqp.vhost": a.vhost,
    "redis.host": r.host, "redis.port": r.port, "redis.dbid": r.dbid,
    "smpp_server.bind": s.bind, "smpp_server.port": s.port, "smpp_server.billing": s.billing_feature,
    "http.bind": h.bind, "http.port": h.port, "http.split": h.long_content_split,
    "dlr.pid": d.pid, "dlr.max_retries": d.dlr_lookup_max_retries, "dlr.retry_delay": d.dlr_lookup_retry_delay,
    "sml.publish": sml.publish_submit_sm_resp, "sml.max_age": sml.submit_max_age_smppc_not_ready,
    "sml.quirk": sml.dlr_lookup_retry_delay,
    "cm.bind": cm.bind, "cm.port": cm.port, "cm.admin": cm.admin_username,
    "router.bind": rt.bind, "router.port": rt.port, "router.persistence": rt.persistence_timer_secs,
    "router.admin": rt.admin_username,
    "dst.timeout": dst.timeout, "dst.retry_delay": dst.retry_delay, "dst.max_retries": dst.max_retries,
    "dlt.timeout": dlt.timeout, "dlt.max_retries": dlt.max_retries, "dlt.dlr_pdu": dlt.dlr_pdu,
    "spb.bind": spb.bind, "spb.port": spb.port, "spb.admin": spb.admin_username,
    "jcli.bind": jc.bind, "jcli.port": jc.port, "jcli.admin": jc.admin_username,
    "itc.host": itc.host, "itc.port": itc.port, "itc.username": itc.username,
}
print(json.dumps(out))
`

// TestJasminFileDifferentialAgainstLegacy parses the real shipped
// misc/config/jasmin.cfg with the Go aggregator and verifies every section
// matches the Python config classes loaded from the same file. This is the
// strongest parity check: the default operator config parses identically.
func TestJasminFileDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	const cfgPath = "../../misc/config/jasmin.cfg"
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", jasminFileOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader(content)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	jasmin, err := config.LoadJasmin(cfgPath)
	if err != nil {
		t.Fatalf("LoadJasmin: %v", err)
	}
	// Build the same flat map from the Go aggregator. Everything is formatted
	// with %v so ints (float64 from JSON), bools and strings compare uniformly.
	got := map[string]any{
		"amqp.host": jasmin.AMQP.Host, "amqp.port": jasmin.AMQP.Port,
		"amqp.username": jasmin.AMQP.Username, "amqp.vhost": jasmin.AMQP.Vhost,
		"redis.host": jasmin.Redis.Host, "redis.port": jasmin.Redis.Port, "redis.dbid": jasmin.Redis.DBID,
		"smpp_server.bind": jasmin.SMPPServer.Bind, "smpp_server.port": jasmin.SMPPServer.Port,
		"smpp_server.billing": jasmin.SMPPServer.BillingFeature,
		"http.bind":           jasmin.HTTPAPI.Bind, "http.port": jasmin.HTTPAPI.Port,
		"http.split": jasmin.HTTPAPI.LongContentSplit,
		"dlr.pid":    jasmin.DLR.PID, "dlr.max_retries": jasmin.DLR.LookupMaxRetries,
		"dlr.retry_delay": jasmin.DLR.LookupRetryDelay,
		"sml.publish":     jasmin.SMListener.PublishSubmitSMResp,
		"sml.max_age":     jasmin.SMListener.SubmitMaxAgeSMPPcNotReady,
		"sml.quirk":       jasmin.SMListener.DLRLookupRetryDelayQuirk,
		"cm.bind":         jasmin.ClientManagement.Bind, "cm.port": jasmin.ClientManagement.Port,
		"cm.admin":    jasmin.ClientManagement.AdminUsername,
		"router.bind": jasmin.Router.Bind, "router.port": jasmin.Router.Port,
		"router.persistence": jasmin.Router.PersistenceTimerSecs, "router.admin": jasmin.Router.AdminUsername,
		"dst.timeout": jasmin.DeliverSMThrower.TimeoutSecs, "dst.retry_delay": jasmin.DeliverSMThrower.RetryDelaySecs,
		"dst.max_retries": jasmin.DeliverSMThrower.MaxRetries,
		"dlt.timeout":     jasmin.DLRThrower.TimeoutSecs, "dlt.max_retries": jasmin.DLRThrower.MaxRetries,
		"dlt.dlr_pdu": jasmin.DLRThrower.DLRPDU,
		"spb.bind":    jasmin.SMPPServerPB.Bind, "spb.port": jasmin.SMPPServerPB.Port,
		"spb.admin": jasmin.SMPPServerPB.AdminUsername,
		"jcli.bind": jasmin.JCli.Bind, "jcli.port": jasmin.JCli.Port, "jcli.admin": jasmin.JCli.AdminUsername,
		"itc.host": jasmin.InterceptorClient.Host, "itc.port": jasmin.InterceptorClient.Port,
		"itc.username": jasmin.InterceptorClient.Username,
	}

	if len(got) != len(oracle) {
		t.Fatalf("key count: go=%d oracle=%d", len(got), len(oracle))
	}
	for key, goValue := range got {
		oracleValue, ok := oracle[key]
		if !ok {
			t.Errorf("%s: missing from oracle", key)
			continue
		}
		if fmt.Sprintf("%v", goValue) != fmt.Sprintf("%v", oracleValue) {
			t.Errorf("%s: go=%v oracle=%v", key, goValue, oracleValue)
		}
	}
}
