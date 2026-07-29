package outbound

import (
	"time"

	"github.com/pumpitspace/jasmin/internal/core/throughput"
)

// throughputGate joins the provisioned per-user ceiling to the spacing state,
// satisfying core.ThroughputGate.
//
// The two halves are deliberately separate: the quota is provisioning data that
// admin/jCli rewrite freely, while the spacing is live per-process state that
// must survive a user being edited. Re-provisioning a user therefore changes
// their ceiling without granting them a free submit.
type throughputGate struct {
	directory *runtimeDirectory
	limiter   *throughput.Limiter
}

func newThroughputGate(directory *runtimeDirectory) *throughputGate {
	return &throughputGate{directory: directory, limiter: throughput.New()}
}

// AllowSubmit reports whether this submit is within the user's ceiling for the
// ingress it arrived through.
func (g *throughputGate) AllowSubmit(username, ingress string, now time.Time) bool {
	quota := g.directory.ThroughputQuota(username, ingress)
	return g.limiter.Allow(username+"\x00"+ingress, quota, now)
}
