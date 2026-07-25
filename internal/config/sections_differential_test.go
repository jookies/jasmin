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
import json, sys, tempfile, os
from jasmin.protocols.smpp.configs import SMPPServerConfig
from jasmin.protocols.http.configs import HTTPApiConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
s = SMPPServerConfig(path)
h = HTTPApiConfig(path)
os.remove(path)
print(json.dumps({
    "smpp": {"id": s.id, "bind": s.bind, "port": s.port, "billing": s.billing_feature,
             "session": s.sessionInitTimerSecs, "elink": s.enquireLinkTimerSecs,
             "inactivity": s.inactivityTimerSecs, "response": s.responseTimerSecs,
             "pduread": s.pduReadTimerSecs},
    "http": {"bind": h.bind, "port": h.port, "billing": h.billing_feature,
             "privacy": h.log_privacy, "split": h.long_content_split},
}))
`

func TestListenerSectionsDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	text := "[smpp-server]\nid = smpps_prod\nbind = 127.0.0.1\nport = 2776\nbilling_feature = no\n" +
		"enquireLinkTimerSecs = 45\ninactivityTimerSecs = 600\npduReadTimerSecs = 15\n" +
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
		} `json:"smpp"`
		HTTP struct {
			Bind    string `json:"bind"`
			Port    int    `json:"port"`
			Billing bool   `json:"billing"`
			Privacy bool   `json:"privacy"`
			Split   string `json:"split"`
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
	api, err := config.LoadHTTPAPI(file)
	if err != nil {
		t.Fatal(err)
	}
	if api.Bind != oracle.HTTP.Bind || api.Port != oracle.HTTP.Port || api.BillingFeature != oracle.HTTP.Billing ||
		api.LogPrivacy != oracle.HTTP.Privacy || api.LongContentSplit != oracle.HTTP.Split {
		t.Fatalf("http-api diverges:\n  go %+v\n  py %+v", api, oracle.HTTP)
	}
}
