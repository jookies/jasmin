package config

import "testing"

func TestLoadAMQPDefaultsAndValues(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nhost = broker-a\nusername = app\npassword = secret\nvhost = /prod\nport = 5673\nheartbeat = 30\nconnection_loss_retry = no\n")
	amqp, err := LoadAMQP(file)
	if err != nil {
		t.Fatal(err)
	}
	if amqp.Host != "broker-a" || amqp.Username != "app" || amqp.Password != "secret" || amqp.Vhost != "/prod" {
		t.Fatalf("amqp fields = %+v", amqp)
	}
	if amqp.Port != 5673 || amqp.Heartbeat != 30 {
		t.Fatalf("amqp ints = %+v", amqp)
	}
	if amqp.ReconnectOnLoss || !amqp.ReconnectOnFailure {
		t.Fatalf("reconnect flags = %+v", amqp)
	}
	if amqp.ReconnectLossDelay != 10 || amqp.ReconnectFailureDelay != 10 {
		t.Fatalf("reconnect delays = %+v", amqp)
	}
}

func TestLoadAMQPDefaultsWhenAbsent(t *testing.T) {
	file := mustParse(t, "[other]\nx = 1\n")
	amqp, err := LoadAMQP(file)
	if err != nil {
		t.Fatal(err)
	}
	if amqp.Host != "127.0.0.1" || amqp.Username != "guest" || amqp.Password != "guest" ||
		amqp.Vhost != "/" || amqp.Port != 5672 {
		t.Fatalf("amqp defaults = %+v", amqp)
	}
}

func TestAMQPURLBuilding(t *testing.T) {
	base := AMQP{Host: "h", Username: "u", Password: "p", Port: 5672, Vhost: "/"}
	if got := base.URL(); got != "amqp://u:p@h:5672/" {
		t.Fatalf("default vhost url = %q", got)
	}
	named := AMQP{Host: "h", Username: "u", Password: "p", Port: 5672, Vhost: "/prod"}
	if got := named.URL(); got != "amqp://u:p@h:5672/prod" {
		t.Fatalf("named vhost url = %q", got)
	}
}

func TestLoadAMQPCloudURLOverride(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nhost = ignored\n")
	file.getenv = withEnv(map[string]string{"CLOUDAMQP_URL": "amqps://user:pa_ss-1@rabbit.example.com/vh"})
	amqp, err := LoadAMQP(file)
	if err != nil {
		t.Fatal(err)
	}
	if amqp.Username != "user" || amqp.Password != "pa_ss-1" || amqp.Host != "rabbit.example.com" || amqp.Vhost != "vh" {
		t.Fatalf("cloud override = %+v", amqp)
	}
	// A malformed CLOUDAMQP_URL errors.
	file.getenv = withEnv(map[string]string{"CLOUDAMQP_URL": "not-a-url"})
	if _, err := LoadAMQP(file); err == nil {
		t.Fatal("malformed CLOUDAMQP_URL must error")
	}
}

func TestLoadRedisDefaultsAndValues(t *testing.T) {
	file := mustParse(t, "[redis-client]\nhost = redis-a\nport = 6380\npassword = pw\ndbid = 3\npoolsize = 20\n")
	redis, err := LoadRedis(file)
	if err != nil {
		t.Fatal(err)
	}
	if redis.Host != "redis-a" || redis.Port != 6380 || redis.Password != "pw" || redis.DBID != 3 || redis.PoolSize != 20 {
		t.Fatalf("redis fields = %+v", redis)
	}
}

func TestLoadRedisDefaults(t *testing.T) {
	file := mustParse(t, "[x]\ny=1\n")
	redis, err := LoadRedis(file)
	if err != nil {
		t.Fatal(err)
	}
	if redis.Host != "127.0.0.1" || redis.Port != 6379 || redis.Password != "" || redis.DBID != 0 || redis.PoolSize != 10 {
		t.Fatalf("redis defaults = %+v", redis)
	}
}

func TestRedisURLBuilding(t *testing.T) {
	withPass := Redis{Host: "h", Port: 6379, Password: "pw", DBID: 2}
	if got := withPass.URL(); got != "redis://:pw@h:6379/2" {
		t.Fatalf("redis url with pass = %q", got)
	}
	noPass := Redis{Host: "h", Port: 6379, DBID: 0}
	if got := noPass.URL(); got != "redis://h:6379/0" {
		t.Fatalf("redis url no pass = %q", got)
	}
}

func TestLoadRedisURLOverride(t *testing.T) {
	file := mustParse(t, "[redis-client]\nhost = ignored\n")
	file.getenv = withEnv(map[string]string{"REDIS_URL": "redis://:pass123@cache.example.com:6381"})
	redis, err := LoadRedis(file)
	if err != nil {
		t.Fatal(err)
	}
	if redis.Password != "pass123" || redis.Host != "cache.example.com" || redis.Port != 6381 {
		t.Fatalf("redis url override = %+v", redis)
	}
}

func TestLoadSMPPServer(t *testing.T) {
	file := mustParse(t, "[smpp-server]\nid = smpps_prod\nbind = 127.0.0.1\nport = 2776\nenquireLinkTimerSecs = 45\npduReadTimerSecs = 15\nbilling_feature = no\n")
	server, err := LoadSMPPServer(file)
	if err != nil {
		t.Fatal(err)
	}
	if server.ID != "smpps_prod" || server.Bind != "127.0.0.1" || server.Port != 2776 {
		t.Fatalf("smpp-server fields = %+v", server)
	}
	if server.EnquireLinkTimerSecs != 45 || server.PDUReadTimerSecs != 15 {
		t.Fatalf("smpp-server timers = %+v", server)
	}
	if server.BillingFeature {
		t.Fatal("billing_feature should be false")
	}
	if server.InactivityTimerSecs != 300 || server.ResponseTimerSecs != 60 || server.SessionInitTimerSecs != 30 {
		t.Fatalf("smpp-server timer defaults = %+v", server)
	}
	if server.BindAddr() != "127.0.0.1:2776" {
		t.Fatalf("bind addr = %q", server.BindAddr())
	}
}

func TestLoadSMPPServerDefaults(t *testing.T) {
	file := mustParse(t, "[x]\ny=1\n")
	server, err := LoadSMPPServer(file)
	if err != nil {
		t.Fatal(err)
	}
	if server.ID != "smpps_01" || server.Bind != "0.0.0.0" || server.Port != 2775 || !server.BillingFeature {
		t.Fatalf("smpp-server defaults = %+v", server)
	}
}

func TestLoadHTTPAPI(t *testing.T) {
	file := mustParse(t, "[http-api]\nbind = 127.0.0.1\nport = 8080\nlong_content_split = sar\nlog_privacy = yes\n")
	api, err := LoadHTTPAPI(file)
	if err != nil {
		t.Fatal(err)
	}
	if api.Bind != "127.0.0.1" || api.Port != 8080 || api.LongContentSplit != "sar" || !api.LogPrivacy {
		t.Fatalf("http-api fields = %+v", api)
	}
	if api.BindAddr() != "127.0.0.1:8080" {
		t.Fatalf("bind addr = %q", api.BindAddr())
	}
}

func TestLoadHTTPAPIEnvDefaults(t *testing.T) {
	// The bind/port DEFAULTS read API_BIND/API_PORT when the section omits them.
	file := mustParse(t, "[other]\nx=1\n")
	file.getenv = withEnv(map[string]string{"API_BIND": "10.0.0.5", "API_PORT": "1500"})
	api, err := LoadHTTPAPI(file)
	if err != nil {
		t.Fatal(err)
	}
	if api.Bind != "10.0.0.5" || api.Port != 1500 {
		t.Fatalf("env-default http-api = %+v", api)
	}
	// The section value still wins over the env default.
	file2 := mustParse(t, "[http-api]\nbind = 192.168.1.1\n")
	file2.getenv = withEnv(map[string]string{"API_BIND": "10.0.0.5"})
	api2, _ := LoadHTTPAPI(file2)
	if api2.Bind != "192.168.1.1" {
		t.Fatalf("section value should win over API_BIND default: %q", api2.Bind)
	}
	// And the HTTP_API_BIND section override wins over both.
	file3 := mustParse(t, "[http-api]\nbind = 192.168.1.1\n")
	file3.getenv = withEnv(map[string]string{"API_BIND": "10.0.0.5", "HTTP_API_BIND": "172.16.0.1"})
	api3, _ := LoadHTTPAPI(file3)
	if api3.Bind != "172.16.0.1" {
		t.Fatalf("HTTP_API_BIND override should win: %q", api3.Bind)
	}
}

func TestLoadRESTAPIBatchQoS(t *testing.T) {
	file := mustParse(t, "[rest-api]\nhttp_throughput_per_worker = 21\nsmart_qos = no\n")
	rest, err := LoadRESTAPI(file)
	if err != nil {
		t.Fatal(err)
	}
	if rest.HTTPThroughputPerWorker != 21 || rest.SmartQoS {
		t.Fatalf("rest-api fields = %+v", rest)
	}
	defaults, err := LoadRESTAPI(mustParse(t, "[other]\nx=1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.HTTPThroughputPerWorker != 8 || !defaults.SmartQoS {
		t.Fatalf("rest-api defaults = %+v", defaults)
	}
	if _, err = LoadRESTAPI(mustParse(t,
		"[rest-api]\nhttp_throughput_per_worker = -1\n")); err == nil {
		t.Fatal("negative REST throughput accepted")
	}
}

func TestLoadDLR(t *testing.T) {
	file := mustParse(t, "[dlr]\npid = worker2\ndlr_lookup_retry_delay = 15\ndlr_lookup_max_retries = 5\nsmpp_receipt_on_success_submit_sm_resp = yes\n")
	dlr, err := LoadDLR(file)
	if err != nil {
		t.Fatal(err)
	}
	if dlr.PID != "worker2" || dlr.LookupRetryDelay != 15 || dlr.LookupMaxRetries != 5 || !dlr.SMPPReceiptOnSuccessSubmitSMResp {
		t.Fatalf("dlr fields = %+v", dlr)
	}
}

func TestLoadDLRDefaults(t *testing.T) {
	file := mustParse(t, "[x]\ny=1\n")
	dlr, _ := LoadDLR(file)
	if dlr.PID != "main" || dlr.LookupRetryDelay != 10 || dlr.LookupMaxRetries != 2 || dlr.SMPPReceiptOnSuccessSubmitSMResp {
		t.Fatalf("dlr defaults = %+v", dlr)
	}
}

func TestLoadSMListenerQuirk(t *testing.T) {
	// Q-020: sm-listener's retry-delay quirk holds the max_retries value.
	file := mustParse(t, "[sm-listener]\npublish_submit_sm_resp = yes\ndlr_lookup_retry_delay = 10\ndlr_lookup_max_retries = 7\nsubmit_max_age_smppc_not_ready = 900\n")
	listener, err := LoadSMListener(file)
	if err != nil {
		t.Fatal(err)
	}
	if !listener.PublishSubmitSMResp || listener.SubmitMaxAgeSMPPcNotReady != 900 {
		t.Fatalf("sm-listener fields = %+v", listener)
	}
	if listener.DLRLookupRetryDelayQuirk != 7 {
		t.Fatalf("Q-020: quirk field = %d, want 7 (the max_retries value)", listener.DLRLookupRetryDelayQuirk)
	}
	if listener.SubmitRetrialDelaySMPPcNotReady != 30 {
		t.Fatalf("submit retrial delay default = %d", listener.SubmitRetrialDelaySMPPcNotReady)
	}
}
