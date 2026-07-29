package config_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/config"
)

// sectionOracleScript builds the real AmqpConfig and RedisForJasminConfig and
// dumps the fields the Go section parsers must reproduce.
const sectionOracleScript = `
import json, sys, tempfile, os
from jasmin.queues.configs import AmqpConfig
from jasmin.redis.configs import RedisForJasminConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
a = AmqpConfig(path)
r = RedisForJasminConfig(path)
os.remove(path)
print(json.dumps({
    "amqp": {"host": a.host, "username": a.username, "password": a.password, "vhost": a.vhost,
             "port": a.port, "heartbeat": a.heartbeat,
             "loss_delay": a.reconnectOnConnectionLossDelay,
             "failure_delay": a.reconnectOnConnectionFailureDelay,
             "loss_retry": a.reconnectOnConnectionLoss, "failure_retry": a.reconnectOnConnectionFailure},
    "redis": {"host": r.host, "port": r.port, "dbid": r.dbid, "poolsize": r.poolsize,
              "password": "" if r.password is None else r.password},
}))
`

func TestSectionsDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	text := "[amqp-broker]\nhost = broker-a\nusername = app\npassword = secret\nvhost = /prod\n" +
		"port = 5673\nheartbeat = 30\nconnection_loss_retry = no\nconnection_loss_retry_delay = 7\n" +
		"[redis-client]\nhost = redis-a\nport = 6380\npassword = pw\ndbid = 3\npoolsize = 20\n"

	command := exec.CommandContext(ctx, pythonPath, "-c", sectionOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle struct {
		AMQP struct {
			Host         string `json:"host"`
			Username     string `json:"username"`
			Password     string `json:"password"`
			Vhost        string `json:"vhost"`
			Port         int    `json:"port"`
			Heartbeat    int    `json:"heartbeat"`
			LossDelay    int    `json:"loss_delay"`
			FailureDelay int    `json:"failure_delay"`
			LossRetry    bool   `json:"loss_retry"`
			FailureRetry bool   `json:"failure_retry"`
		} `json:"amqp"`
		Redis struct {
			Host     string `json:"host"`
			Password string `json:"password"`
			Port     int    `json:"port"`
			DBID     int    `json:"dbid"`
			PoolSize int    `json:"poolsize"`
		} `json:"redis"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	amqp, err := config.LoadAMQP(file)
	if err != nil {
		t.Fatal(err)
	}
	if amqp.Host != oracle.AMQP.Host || amqp.Username != oracle.AMQP.Username ||
		amqp.Password != oracle.AMQP.Password || amqp.Vhost != oracle.AMQP.Vhost ||
		amqp.Port != oracle.AMQP.Port || amqp.Heartbeat != oracle.AMQP.Heartbeat ||
		amqp.ReconnectLossDelay != oracle.AMQP.LossDelay || amqp.ReconnectFailureDelay != oracle.AMQP.FailureDelay ||
		amqp.ReconnectOnLoss != oracle.AMQP.LossRetry || amqp.ReconnectOnFailure != oracle.AMQP.FailureRetry {
		t.Fatalf("amqp diverges:\n  go %+v\n  py %+v", amqp, oracle.AMQP)
	}
	redis, err := config.LoadRedis(file)
	if err != nil {
		t.Fatal(err)
	}
	if redis.Host != oracle.Redis.Host || redis.Port != oracle.Redis.Port ||
		redis.Password != oracle.Redis.Password || redis.DBID != oracle.Redis.DBID ||
		redis.PoolSize != oracle.Redis.PoolSize {
		t.Fatalf("redis diverges:\n  go %+v\n  py %+v", redis, oracle.Redis)
	}
}

const listenerOracleScript = `
import json, sys, tempfile, os, logging
from jasmin.protocols.smpp.configs import SMPPServerConfig
from jasmin.protocols.http.configs import HTTPApiConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
s = SMPPServerConfig(path)
h = HTTPApiConfig(path)
os.remove(path)
# log_level is stored as logging.getLevelName(name) (an int); convert back to the
# name for comparison with the Go LogConfig.Level string.
print(json.dumps({
    "smpp": {"id": s.id, "bind": s.bind, "port": s.port, "billing": s.billing_feature,
             "session": s.sessionInitTimerSecs, "elink": s.enquireLinkTimerSecs,
             "inactivity": s.inactivityTimerSecs, "response": s.responseTimerSecs,
             "pduread": s.pduReadTimerSecs,
             "log_file": s.log_file, "log_rotate": s.log_rotate,
             "log_level": logging.getLevelName(s.log_level),
             "log_format": s.log_format, "log_date_format": s.log_date_format},
    "http": {"bind": h.bind, "port": h.port, "billing": h.billing_feature,
             "privacy": h.log_privacy, "split": h.long_content_split,
             "log_file": h.log_file, "log_rotate": h.log_rotate,
             "log_level": logging.getLevelName(h.log_level),
             "log_format": h.log_format, "log_date_format": h.log_date_format},
}))
`

func TestListenerSectionsDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// smpp-server carries explicit log_* (parsing parity); http-api omits them so
	// its log_file default exercises LOG_PATH parity against the legacy resolution.
	text := "[smpp-server]\nid = smpps_prod\nbind = 127.0.0.1\nport = 2776\nbilling_feature = no\n" +
		"enquireLinkTimerSecs = 45\ninactivityTimerSecs = 600\npduReadTimerSecs = 15\n" +
		"log_file = /srv/log/smpps.log\nlog_rotate = W3\nlog_level = WARNING\n" +
		"log_format = %(message)s\nlog_date_format = %H:%M:%S\n" +
		"[http-api]\nbind = 10.0.0.9\nport = 8080\nlong_content_split = sar\nlog_privacy = yes\n"

	command := exec.CommandContext(ctx, pythonPath, "-c", listenerOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle struct {
		SMPP struct {
			ID         string `json:"id"`
			Bind       string `json:"bind"`
			Port       int    `json:"port"`
			Billing    bool   `json:"billing"`
			Session    int    `json:"session"`
			Elink      int    `json:"elink"`
			Inactivity int    `json:"inactivity"`
			Response   int    `json:"response"`
			PDURead    int    `json:"pduread"`
			LogFile    string `json:"log_file"`
			LogRotate  string `json:"log_rotate"`
			LogLevel   string `json:"log_level"`
			LogFormat  string `json:"log_format"`
			LogDateFmt string `json:"log_date_format"`
		} `json:"smpp"`
		HTTP struct {
			Bind       string `json:"bind"`
			Port       int    `json:"port"`
			Billing    bool   `json:"billing"`
			Privacy    bool   `json:"privacy"`
			Split      string `json:"split"`
			LogFile    string `json:"log_file"`
			LogRotate  string `json:"log_rotate"`
			LogLevel   string `json:"log_level"`
			LogFormat  string `json:"log_format"`
			LogDateFmt string `json:"log_date_format"`
		} `json:"http"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	smpp, err := config.LoadSMPPServer(file)
	if err != nil {
		t.Fatal(err)
	}
	if smpp.ID != oracle.SMPP.ID || smpp.Bind != oracle.SMPP.Bind || smpp.Port != oracle.SMPP.Port ||
		smpp.BillingFeature != oracle.SMPP.Billing || smpp.SessionInitTimerSecs != oracle.SMPP.Session ||
		smpp.EnquireLinkTimerSecs != oracle.SMPP.Elink || smpp.InactivityTimerSecs != oracle.SMPP.Inactivity ||
		smpp.ResponseTimerSecs != oracle.SMPP.Response || smpp.PDUReadTimerSecs != oracle.SMPP.PDURead {
		t.Fatalf("smpp-server diverges:\n  go %+v\n  py %+v", smpp, oracle.SMPP)
	}
	wantSMPPLog := config.LogConfig{
		File: oracle.SMPP.LogFile, Rotate: oracle.SMPP.LogRotate, Level: oracle.SMPP.LogLevel,
		Format: oracle.SMPP.LogFormat, DateFormat: oracle.SMPP.LogDateFmt,
	}
	if smpp.Log != wantSMPPLog {
		t.Fatalf("smpp-server log diverges:\n  go %+v\n  py %+v", smpp.Log, wantSMPPLog)
	}
	api, err := config.LoadHTTPAPI(file)
	if err != nil {
		t.Fatal(err)
	}
	if api.Bind != oracle.HTTP.Bind || api.Port != oracle.HTTP.Port || api.BillingFeature != oracle.HTTP.Billing ||
		api.LogPrivacy != oracle.HTTP.Privacy || api.LongContentSplit != oracle.HTTP.Split {
		t.Fatalf("http-api diverges:\n  go %+v\n  py %+v", api, oracle.HTTP)
	}
	// http-api omits log_* — this asserts the defaulted log_file (LOG_PATH parity).
	wantHTTPLog := config.LogConfig{
		File: oracle.HTTP.LogFile, Rotate: oracle.HTTP.LogRotate, Level: oracle.HTTP.LogLevel,
		Format: oracle.HTTP.LogFormat, DateFormat: oracle.HTTP.LogDateFmt,
	}
	if api.Log != wantHTTPLog {
		t.Fatalf("http-api log diverges:\n  go %+v\n  py %+v", api.Log, wantHTTPLog)
	}
}

const restAPIOracleScript = `
import json, sys, tempfile, os
from jasmin.protocols.rest.config import RestAPIForJasminConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
r = RestAPIForJasminConfig(path)
os.remove(path)
print(json.dumps({
    "throughput": r.http_throughput_per_worker,
    "smart_qos": r.smart_qos,
}))
`

func TestRESTAPIQoSDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	text := "[rest-api]\nhttp_throughput_per_worker = 17\nsmart_qos = no\nlog_file = stdout\n"
	command := exec.CommandContext(ctx, pythonPath, "-c", restAPIOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..", "LOG_PATH="+t.TempDir())
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle struct {
		Throughput int  `json:"throughput"`
		SmartQoS   bool `json:"smart_qos"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := config.LoadRESTAPI(file)
	if err != nil {
		t.Fatal(err)
	}
	if rest.HTTPThroughputPerWorker != oracle.Throughput || rest.SmartQoS != oracle.SmartQoS {
		t.Fatalf("rest-api diverges:\n  go %+v\n  py %+v", rest, oracle)
	}
}

const dlrListenerOracleScript = `
import json, sys, tempfile, os, logging
from jasmin.managers.configs import DLRLookupConfig, SMPPClientSMListenerConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
d = DLRLookupConfig(path)
s = SMPPClientSMListenerConfig(path)
os.remove(path)
print(json.dumps({
    "dlr": {"pid": d.pid, "retry_delay": d.dlr_lookup_retry_delay,
            "max_retries": d.dlr_lookup_max_retries,
            "receipt": d.smpp_receipt_on_success_submit_sm_resp,
            "log_file": d.log_file, "log_rotate": d.log_rotate,
            "log_level": logging.getLevelName(d.log_level),
            "log_format": d.log_format, "log_date_format": d.log_date_format},
    "sml": {"publish": s.publish_submit_sm_resp,
            "max_age": s.submit_max_age_smppc_not_ready,
            "retrial_delay": s.submit_retrial_delay_smppc_not_ready,
            "quirk": s.dlr_lookup_retry_delay,
            "has_max_retries": hasattr(s, "dlr_lookup_max_retries"),
            "log_file": s.log_file, "log_rotate": s.log_rotate,
            "log_level": logging.getLevelName(s.log_level),
            "log_format": s.log_format, "log_date_format": s.log_date_format},
}))
`

func TestDLRAndSMListenerDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// dlr carries explicit log_* (parsing parity); sm-listener omits them so its
	// log_file default exercises LOG_PATH parity against the legacy resolution.
	text := "[dlr]\npid = worker2\ndlr_lookup_retry_delay = 15\ndlr_lookup_max_retries = 5\n" +
		"smpp_receipt_on_success_submit_sm_resp = yes\n" +
		"log_file = /srv/log/messages.log\nlog_rotate = W1\nlog_level = ERROR\n" +
		"log_format = %(name)s %(message)s\nlog_date_format = %d/%m/%Y\n" +
		"[sm-listener]\npublish_submit_sm_resp = yes\nsubmit_max_age_smppc_not_ready = 900\n" +
		"dlr_lookup_retry_delay = 10\ndlr_lookup_max_retries = 7\n"

	command := exec.CommandContext(ctx, pythonPath, "-c", dlrListenerOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle struct {
		DLR struct {
			PID        string `json:"pid"`
			RetryDelay int    `json:"retry_delay"`
			MaxRetries int    `json:"max_retries"`
			Receipt    bool   `json:"receipt"`
			LogFile    string `json:"log_file"`
			LogRotate  string `json:"log_rotate"`
			LogLevel   string `json:"log_level"`
			LogFormat  string `json:"log_format"`
			LogDateFmt string `json:"log_date_format"`
		} `json:"dlr"`
		SML struct {
			Publish       bool   `json:"publish"`
			MaxAge        int    `json:"max_age"`
			RetrialDelay  int    `json:"retrial_delay"`
			Quirk         int    `json:"quirk"`
			HasMaxRetries bool   `json:"has_max_retries"`
			LogFile       string `json:"log_file"`
			LogRotate     string `json:"log_rotate"`
			LogLevel      string `json:"log_level"`
			LogFormat     string `json:"log_format"`
			LogDateFmt    string `json:"log_date_format"`
		} `json:"sml"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	dlr, err := config.LoadDLR(file)
	if err != nil {
		t.Fatal(err)
	}
	if dlr.PID != oracle.DLR.PID || dlr.LookupRetryDelay != oracle.DLR.RetryDelay ||
		dlr.LookupMaxRetries != oracle.DLR.MaxRetries || dlr.SMPPReceiptOnSuccessSubmitSMResp != oracle.DLR.Receipt {
		t.Fatalf("dlr diverges:\n  go %+v\n  py %+v", dlr, oracle.DLR)
	}
	wantDLRLog := config.LogConfig{
		File: oracle.DLR.LogFile, Rotate: oracle.DLR.LogRotate, Level: oracle.DLR.LogLevel,
		Format: oracle.DLR.LogFormat, DateFormat: oracle.DLR.LogDateFmt,
	}
	if dlr.Log != wantDLRLog {
		t.Fatalf("dlr log diverges:\n  go %+v\n  py %+v", dlr.Log, wantDLRLog)
	}
	sml, err := config.LoadSMListener(file)
	if err != nil {
		t.Fatal(err)
	}
	// The oracle confirms the Q-020 bug: quirk holds max_retries (7), no max_retries attr.
	if oracle.SML.Quirk != 7 || oracle.SML.HasMaxRetries {
		t.Fatalf("oracle does not exhibit Q-020: %+v", oracle.SML)
	}
	if sml.PublishSubmitSMResp != oracle.SML.Publish || sml.SubmitMaxAgeSMPPcNotReady != oracle.SML.MaxAge ||
		sml.SubmitRetrialDelaySMPPcNotReady != oracle.SML.RetrialDelay || sml.DLRLookupRetryDelayQuirk != oracle.SML.Quirk {
		t.Fatalf("sm-listener diverges:\n  go %+v\n  py %+v", sml, oracle.SML)
	}
	// sm-listener omits log_* — this asserts the defaulted log_file (LOG_PATH parity).
	wantSMLLog := config.LogConfig{
		File: oracle.SML.LogFile, Rotate: oracle.SML.LogRotate, Level: oracle.SML.LogLevel,
		Format: oracle.SML.LogFormat, DateFormat: oracle.SML.LogDateFmt,
	}
	if sml.Log != wantSMLLog {
		t.Fatalf("sm-listener log diverges:\n  go %+v\n  py %+v", sml.Log, wantSMLLog)
	}
}
