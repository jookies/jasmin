package config

import "testing"

func TestLoadJasminFileThreadsEverySection(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nhost = broker-a\n"+
		"[redis-client]\nport = 6390\n"+
		"[smpp-server]\nid = smpps_x\n"+
		"[http-api]\nport = 8080\n"+
		"[dlr]\ndlr_lookup_max_retries = 5\n"+
		"[sm-listener]\nsubmit_max_age_smppc_not_ready = 900\n"+
		"[client-management]\nadmin_username = cm\n"+
		"[router]\npersistence_timer_secs = 42\n"+
		"[deliversm-thrower]\nmax_retries = 7\n"+
		"[dlr-thrower]\ndlr_pdu = data_sm\n"+
		"[smpp-server-pb]\nport = 14001\n"+
		"[jcli]\nport = 8991\n"+
		"[interceptor]\nlog_slow_script = 4\n"+
		"[smpp-server-pb-client]\nhost = pbs\n"+
		"[interceptor-client]\nusername = iu\n")
	jasmin, err := LoadJasminFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// One threaded field per section confirms the loader ran and mapped to the
	// right struct member.
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"amqp.host", jasmin.AMQP.Host, "broker-a"},
		{"redis.port", jasmin.Redis.Port, 6390},
		{"smpp-server.id", jasmin.SMPPServer.ID, "smpps_x"},
		{"http-api.port", jasmin.HTTPAPI.Port, 8080},
		{"dlr.max_retries", jasmin.DLR.LookupMaxRetries, 5},
		{"sm-listener.max_age", jasmin.SMListener.SubmitMaxAgeSMPPcNotReady, 900},
		{"client-management.admin", jasmin.ClientManagement.AdminUsername, "cm"},
		{"router.persistence", jasmin.Router.PersistenceTimerSecs, 42},
		{"deliversm-thrower.max_retries", jasmin.DeliverSMThrower.MaxRetries, 7},
		{"dlr-thrower.dlr_pdu", jasmin.DLRThrower.DLRPDU, "data_sm"},
		{"smpp-server-pb.port", jasmin.SMPPServerPB.Port, 14001},
		{"jcli.port", jasmin.JCli.Port, 8991},
		{"interceptor.log_slow_script", jasmin.Interceptor.LogSlowScript, 4},
		{"smpp-server-pb-client.host", jasmin.SMPPServerPBClient.Host, "pbs"},
		{"interceptor-client.username", jasmin.InterceptorClient.Username, "iu"},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
}

func TestLoadJasminFileDefaultsOnEmpty(t *testing.T) {
	jasmin, err := LoadJasminFile(mustParse(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	// Absent sections must fall back to legacy defaults, not zero values.
	if jasmin.AMQP.Port != 5672 || jasmin.Redis.Port != 6379 || jasmin.SMPPServer.Port != 2775 ||
		jasmin.ClientManagement.Port != 8989 || jasmin.Router.Port != 8988 || jasmin.JCli.Bind != "127.0.0.1" ||
		jasmin.DeliverSMThrower.MaxRetries != 3 {
		t.Fatalf("empty-config defaults diverge: %+v", jasmin)
	}
}

func TestLoadJasminFilePropagatesSectionError(t *testing.T) {
	// A malformed int in any one section aborts the whole aggregation.
	if _, err := LoadJasminFile(mustParse(t, "[jcli]\nport = notanint\n")); err == nil {
		t.Fatal("a bad section value must abort LoadJasminFile")
	}
}
