// Package stats holds Jasmin's process-local counters and renders them in the
// exact /metrics Prometheus text format (jasmin/protocols/http/endpoints/
// metrics.py). Counters are process-local and reset on restart — persistent
// state must never masquerade as a legacy metric (KNOWN_QUIRKS Q-011, O-008).
package stats

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// metric names a single counter and its /metrics HELP text, in emission order.
type metric struct {
	name string
	help string
}

// httpAPIMetrics is PROM_METRICS_HTTPAPI in the legacy emission order.
var httpAPIMetrics = []metric{
	{"request_count", "Http request count."},
	{"interceptor_count", "Successful http request count."},
	{"auth_error_count", "Authentication error count."},
	{"route_error_count", "Routing error count."},
	{"interceptor_error_count", "Interceptor error count."},
	{"throughput_error_count", "Throughput exceeded error count."},
	{"charging_error_count", "Charging error count."},
	{"server_error_count", "Server error count."},
	{"success_count", "Successful http request count."},
}

// smppcMetrics is PROM_METRICS_SMPPC in the legacy emission order.
var smppcMetrics = []metric{
	{"connected_count", "Cumulated number of successful connections."},
	{"disconnected_count", "Cumulated number of disconnections."},
	{"bound_count", "Number of bound sessions."},
	{"submit_sm_request_count", "SubmitSm pdu requests count."},
	{"submit_sm_count", "Complete SubmitSm transactions count."},
	{"deliver_sm_count", "DeliverSm pdu requests count."},
	{"data_sm_count", "Complete DataSm transactions count."},
	{"interceptor_count", "Interceptor calls count."},
	{"elink_count", "EnquireLinks count."},
	{"throttling_error_count", "Throttling errors count."},
	{"interceptor_error_count", "Interception errors count."},
	{"other_submit_error_count", "Other errors count."},
}

// smppsAPIMetrics is PROM_METRICS_SMPPS_API in the legacy emission order.
var smppsAPIMetrics = []metric{
	{"connected_count", "Number of connected sessions."},
	{"connect_count", "Cumulated number of connect requests."},
	{"disconnect_count", "Cumulated number of disconnect requests."},
	{"interceptor_count", "Interceptor calls count."},
	{"bound_trx_count", "Number of bound sessions in transceiver mode."},
	{"bound_rx_count", "Number of bound sessions in receiver mode."},
	{"bound_tx_count", "Number of bound sessions in transmitter mode."},
	{"bind_trx_count", "Number of bind requests in transceiver mode."},
	{"bind_rx_count", "Number of bind requests in receiver mode."},
	{"bind_tx_count", "Number of bind requests in transmitter mode."},
	{"unbind_count", "Cumulated number of unbind requests."},
	{"submit_sm_request_count", "SubmitSm pdu requests count."},
	{"submit_sm_count", "Complete SubmitSm transactions count."},
	{"deliver_sm_count", "DeliverSm pdu requests count."},
	{"data_sm_count", "Complete DataSm transactions count."},
	{"elink_count", "EnquireLinks count."},
	{"throttling_error_count", "Throttling errors count."},
	{"interceptor_error_count", "Interception errors count."},
	{"other_submit_error_count", "Other errors count."},
}

// counters is a concurrency-safe set of named int64 counters.
type counters struct {
	values sync.Map // string -> *int64
}

func (c *counters) inc(name string) {
	value, _ := c.values.LoadOrStore(name, new(int64))
	atomic.AddInt64(value.(*int64), 1)
}

func (c *counters) get(name string) int64 {
	value, ok := c.values.Load(name)
	if !ok {
		return 0
	}
	return atomic.LoadInt64(value.(*int64))
}

// HTTPStats holds the httpapi counters. The zero value is ready to use.
type HTTPStats struct{ c counters }

func (s *HTTPStats) Inc(name string) { s.c.inc(name) }
func (s *HTTPStats) Get(name string) int64 {
	return s.c.get(name)
}

// SMPPsStats holds the single smppsapi counter set.
type SMPPsStats struct{ c counters }

func (s *SMPPsStats) Inc(name string) { s.c.inc(name) }
func (s *SMPPsStats) Get(name string) int64 {
	return s.c.get(name)
}

// SMPPcRegistry holds per-connector counter sets, created on first use.
type SMPPcRegistry struct {
	mu         sync.Mutex
	connectors map[string]*counters
}

func NewSMPPcRegistry() *SMPPcRegistry {
	return &SMPPcRegistry{connectors: make(map[string]*counters)}
}

// Inc increments a counter for one connector id, creating its set if needed.
func (r *SMPPcRegistry) Inc(cid, name string) {
	r.mu.Lock()
	set, ok := r.connectors[cid]
	if !ok {
		set = &counters{}
		r.connectors[cid] = set
	}
	r.mu.Unlock()
	set.inc(name)
}

// Get returns a connector's counter value (0 when the connector is unknown).
func (r *SMPPcRegistry) Get(cid, name string) int64 {
	r.mu.Lock()
	set, ok := r.connectors[cid]
	r.mu.Unlock()
	if !ok {
		return 0
	}
	return set.get(name)
}

// Render produces the /metrics response bytes matching the legacy Metrics
// resource exactly: for each metric a `# TYPE` line then a `# HELP` line
// (Jasmin's order — the reverse of the Prometheus convention) then the value;
// smppc metrics carry a {cid="..."} label and their TYPE/HELP header is emitted
// only when at least one connector exists; the payload ends with two blank
// lines. connectorIDs are rendered in the given order (Jasmin uses the PB
// connector-list order); nil http/smpps sections render as absent.
func Render(http *HTTPStats, smppc *SMPPcRegistry, connectorIDs []string, smpps *SMPPsStats) []byte {
	var lines []string

	if http != nil {
		for _, m := range httpAPIMetrics {
			lines = append(lines,
				"# TYPE httpapi_"+m.name+" counter",
				"# HELP httpapi_"+m.name+" "+m.help,
				"httpapi_"+m.name+" "+strconv.FormatInt(http.Get(m.name), 10),
			)
		}
	}

	for _, m := range smppcMetrics {
		if len(connectorIDs) > 0 {
			lines = append(lines,
				"# TYPE smppc_"+m.name+" counter",
				"# HELP smppc_"+m.name+" "+m.help,
			)
		}
		for _, cid := range connectorIDs {
			var value int64
			if smppc != nil {
				value = smppc.Get(cid, m.name)
			}
			lines = append(lines,
				"smppc_"+m.name+`{cid="`+cid+`"} `+strconv.FormatInt(value, 10),
			)
		}
	}

	if smpps != nil {
		for _, m := range smppsAPIMetrics {
			lines = append(lines,
				"# TYPE smppsapi_"+m.name+" counter",
				"# HELP smppsapi_"+m.name+" "+m.help,
				"smppsapi_"+m.name+" "+strconv.FormatInt(smpps.Get(m.name), 10),
			)
		}
	}

	// The legacy renderer appends two empty strings before the newline join.
	lines = append(lines, "", "")
	return []byte(strings.Join(lines, "\n"))
}
