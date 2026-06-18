package observe

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// quarantineBufferCap bounds the off-digest stray buffer (doc 20 P1). The candidate
// ER drains it each cycle; on overflow the OLDEST strays are dropped (recent strays
// win) so memory stays bounded if the drain stalls.
const quarantineBufferCap = 2048

// QuarantinedSeries is one stray series the identity normalizer could not join — the
// input to the candidate ER's stray-metric fallback (doc 20 §2.4). It is MEASURED
// (the readings exist) but unmapped; it never entered the hot store or the digest.
type QuarantinedSeries struct {
	Family identity.Family
	Metric string
	Labels map[string]string
	Node   string
	Reason string // the typed quarantine reason (e.g. "unknown-exporter-family")
	At     time.Time
}

// EnableQuarantineCapture turns on the off-digest stray tap. Off by default; capturing
// changes no fingerprint (quarantined series are never in the digest), so this is safe
// to enable independently. Idempotent.
func (in *Ingestor) EnableQuarantineCapture() {
	in.mu.Lock()
	in.quarantineCapture = true
	in.mu.Unlock()
}

// captureQuarantine buffers a stray when capture is on. The labels are copied so the
// buffered entry never aliases the ingest loop's per-metric map.
func (in *Ingestor) captureQuarantine(metric string, family identity.Family, labels map[string]string, node, reason string, at time.Time) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.quarantineCapture {
		return
	}
	in.quarantined = append(in.quarantined, QuarantinedSeries{
		Family: family, Metric: metric, Labels: copyLabels(labels), Node: node, Reason: reason, At: at.UTC(),
	})
	if over := len(in.quarantined) - quarantineBufferCap; over > 0 {
		in.quarantined = in.quarantined[over:]
	}
}

// DrainQuarantined returns and clears the buffered strays (nil when empty).
func (in *Ingestor) DrainQuarantined() []QuarantinedSeries {
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.quarantined) == 0 {
		return nil
	}
	out := in.quarantined
	in.quarantined = nil
	return out
}

func copyLabels(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
