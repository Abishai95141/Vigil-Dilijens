package api

import "time"

// RightSizingView is the off-digest right-sizing advisory surface (docs/31 §6): per-workload,
// per-resource recommendations comparing SUSTAINED measured usage (a high percentile over a
// declared window) to the workload's OWN declared request/limit. It is an ADVISORY — a human
// reads the recommendation; the system NEVER auto-applies it, never writes to the cluster,
// never gates detection, and authors nothing in the graph. The percentiles are MEASURED; the
// request/limit are DECLARED (borrowed normativity); the recommended value is the suggestion.
type RightSizingView struct {
	GeneratedAt   time.Time          `json:"generatedAt"`
	Class         string             `json:"class"`
	Enabled       bool               `json:"enabled"`
	WindowSeconds int64              `json:"windowSeconds"` // the EFFECTIVE sustained window the percentile covers (hot-store-limited)
	Rules         string             `json:"rules"`         // the DECLARED advisory thresholds, surfaced so an operator sees the fixed rules applied
	Note          string             `json:"note"`
	Summary       RightSizingSummary `json:"summary"`
	Advisories    []RightSizingRow   `json:"advisories,omitempty"`
}

// RightSizingSummary is the at-a-glance breakdown over all analyzed (workload,resource) pairs.
type RightSizingSummary struct {
	Analyzed       int `json:"analyzed"`
	Reclaim        int `json:"reclaim"`
	ResizeUp       int `json:"resizeUp"`
	WithinHeadroom int `json:"withinHeadroom"`
	OutOfScope     int `json:"outOfScope"`
	Unstable       int `json:"unstable"`
}

// RightSizingRow is one (workload, resource) advisory. P95/CV/Samples are MEASURED; Request/
// Limit are DECLARED; Action/Recommended are the ADVISORY suggestion (never an action). Unit
// is "millicores" (cpu) or "bytes" (memory/storage) for display.
type RightSizingRow struct {
	WorkloadRef  string  `json:"workloadRef"`
	Namespace    string  `json:"namespace,omitempty"`
	Name         string  `json:"name,omitempty"`
	WorkloadKind string  `json:"workloadKind,omitempty"`
	Container    string  `json:"container,omitempty"`
	Resource     string  `json:"resource"` // "cpu" | "memory" | "storage"
	Unit         string  `json:"unit"`     // "millicores" | "bytes"
	QoS          string  `json:"qos"`      // Guaranteed | Burstable | BestEffort
	P95          float64 `json:"p95"`
	CV           float64 `json:"cv"`
	Samples      int     `json:"samples"`
	Request      int64   `json:"request,omitempty"`
	Limit        int64   `json:"limit,omitempty"`
	Action       string  `json:"action"` // reclaim | resize-up | within-headroom | out-of-scope | unstable
	Recommended  int64   `json:"recommended,omitempty"`
	Stable       bool    `json:"stable"`
	Reason       string  `json:"reason"`
}

const rightSizingClass = "MEASURED percentile advisory — recommendation, never auto-applied (a human acts; the system never does)"

// rightSizingRules surfaces the DECLARED advisory thresholds (mirrors rightsizing.DefaultParams)
// so the operator can see exactly which fixed rules were applied — never auto-tuned.
const rightSizingRules = "p95 vs declared request/limit · reclaim when p95 < 50% of request · resize-up when p95 > 85% of limit · headroom ×1.3 · stable only when CoV ≤ 0.5 with no active onset"

const (
	rightSizingOffNote = "The right-sizing advisory lane is OFF (needs --rightsizing-enabled): per-workload " +
		"recommendations comparing sustained usage to declared requests/limits are not being computed."
	rightSizingQuietNote = "Right-sizing lane ON; no workload has enough stable, declared-resource history in the window " +
		"yet to size against — nothing to advise. (A recommendation needs a stable usage window AND a declared request or limit.)"
	rightSizingActiveNote = "Each row compares a workload's SUSTAINED measured usage (p95 over the window) to its OWN " +
		"declared request/limit. Recommendations are ADVISORY — a human decides whether to apply them; the system never does. " +
		"A reclaim never touches a Guaranteed resource (it would change the QoS class); a BestEffort resource is out of scope; " +
		"a churny/ramping/mid-rollout workload yields no recommendation (honest silence), not a noisy number."
)

// BuildRightSizing renders the advisory surface. OFF, quiet, and active are distinct, stated
// states — it never invents a recommendation and never upgrades a class. windowSeconds is the
// EFFECTIVE window the percentile covered (hot-store-limited), surfaced honestly.
func BuildRightSizing(rows []RightSizingRow, enabled bool, windowSeconds int64, now time.Time) *RightSizingView {
	v := &RightSizingView{GeneratedAt: now, Class: rightSizingClass, Rules: rightSizingRules, Enabled: enabled, WindowSeconds: windowSeconds}
	for _, r := range rows {
		v.Summary.Analyzed++
		switch r.Action {
		case "reclaim":
			v.Summary.Reclaim++
		case "resize-up":
			v.Summary.ResizeUp++
		case "within-headroom":
			v.Summary.WithinHeadroom++
		case "out-of-scope":
			v.Summary.OutOfScope++
		case "unstable":
			v.Summary.Unstable++
		}
	}
	switch {
	case !enabled:
		v.Note = rightSizingOffNote
	case len(rows) == 0:
		v.Note = rightSizingQuietNote
	default:
		v.Note = rightSizingActiveNote
		v.Advisories = rows
	}
	return v
}
