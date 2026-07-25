package stats

import (
	"strings"
	"testing"
)

func TestRenderHTTPAPIFormat(t *testing.T) {
	http := &HTTPStats{}
	http.Inc("request_count")
	http.Inc("request_count")
	http.Inc("success_count")

	out := string(Render(http, nil, nil, nil))
	// Jasmin emits TYPE before HELP (reverse of the Prometheus convention),
	// then the value line, for each metric in the fixed order.
	head := "# TYPE httpapi_request_count counter\n" +
		"# HELP httpapi_request_count Http request count.\n" +
		"httpapi_request_count 2\n"
	if !strings.HasPrefix(out, head) {
		t.Fatalf("httpapi head mismatch:\n%q", out)
	}
	if !strings.Contains(out, "httpapi_success_count 1\n") {
		t.Fatalf("success_count value missing:\n%s", out)
	}
	// An untouched counter renders 0.
	if !strings.Contains(out, "httpapi_auth_error_count 0\n") {
		t.Fatalf("zero counter missing:\n%s", out)
	}
	// The payload ends with two blank lines (the legacy padding).
	if !strings.HasSuffix(out, "\n\n") {
		t.Fatalf("missing trailing padding: %q", out[len(out)-4:])
	}
}

func TestRenderSMPPcPerConnectorLabels(t *testing.T) {
	registry := NewSMPPcRegistry()
	registry.Inc("conn-a", "submit_sm_count")
	registry.Inc("conn-a", "submit_sm_count")
	registry.Inc("conn-b", "submit_sm_count")

	out := string(Render(nil, registry, []string{"conn-a", "conn-b"}, nil))
	// TYPE/HELP header emitted once, then one labelled value line per connector.
	if !strings.Contains(out, "# TYPE smppc_submit_sm_count counter\n# HELP smppc_submit_sm_count Complete SubmitSm transactions count.\n") {
		t.Fatalf("smppc header missing:\n%s", out)
	}
	if !strings.Contains(out, `smppc_submit_sm_count{cid="conn-a"} 2`) ||
		!strings.Contains(out, `smppc_submit_sm_count{cid="conn-b"} 1`) {
		t.Fatalf("smppc per-connector values missing:\n%s", out)
	}
}

func TestRenderSMPPcHeaderOnlyWithConnectors(t *testing.T) {
	// With no connectors, the smppc section emits nothing at all (not even
	// TYPE/HELP) — matching the legacy len(_connectors) > 0 guard.
	out := string(Render(&HTTPStats{}, NewSMPPcRegistry(), nil, nil))
	if strings.Contains(out, "smppc_") {
		t.Fatalf("smppc section should be absent with no connectors:\n%s", out)
	}
}

func TestRenderSMPPsAPIFormat(t *testing.T) {
	smpps := &SMPPsStats{}
	smpps.Inc("bound_trx_count")
	out := string(Render(nil, nil, nil, smpps))
	if !strings.Contains(out, "# TYPE smppsapi_bound_trx_count counter\n# HELP smppsapi_bound_trx_count Number of bound sessions in transceiver mode.\nsmppsapi_bound_trx_count 1\n") {
		t.Fatalf("smppsapi format mismatch:\n%s", out)
	}
}

func TestRenderEmpty(t *testing.T) {
	// All sections absent: only the trailing padding remains.
	if out := string(Render(nil, nil, nil, nil)); out != "\n" {
		t.Fatalf("empty render = %q, want a single newline (two joined blanks)", out)
	}
}

func TestCountersAreConcurrencySafe(t *testing.T) {
	http := &HTTPStats{}
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 1000; j++ {
				http.Inc("request_count")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if got := http.Get("request_count"); got != 8000 {
		t.Fatalf("request_count = %d, want 8000", got)
	}
}
