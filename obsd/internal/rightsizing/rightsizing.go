// Package rightsizing is the OFF-DIGEST right-sizing advisory producer (docs/31 §6): the
// charter-clean form of the ABB_Accelerator_Proto competitor's _rightsize(). It compares a
// workload's SUSTAINED resource usage (a high percentile over a declared window) to its OWN
// DECLARED requests/limits and emits a RECOMMENDATION — reclaim an over-provisioned request,
// grow an under-provisioned limit, or stay put — that an operator acts on. It NEVER auto-
// applies, NEVER writes to the cluster, NEVER gates detection, and authors nothing in the KG.
//
// It fixes every flaw in the competitor's version (docs/31 §6.2/§9):
//   - SUSTAINED percentile over a window, not a single-shot p95.
//   - a STABILITY gate: a churny / ramping / mid-rollout workload yields NO advice (honest
//     silence), not a noisy number — the discipline the competitor lacked.
//   - QoS awareness: a Guaranteed resource (request==limit) is never advised into Burstable
//     by a reclaim; a BestEffort resource (no request AND no limit) is OUT OF SCOPE, never a
//     fabricated number.
//   - cluster-agnostic: no namespace/service hardcoding (the competitor's factory-.* sin).
//
// CHARTER: the recommendation is grounded in MEASURED percentiles + DECLARED config
// (borrowed normativity), so it is a CLASSED fact a human reads — never a cause, never an
// action. Compute is a PURE, DETERMINISTIC function (sort-based percentile, no RNG, no time
// dependence in the logic) so the lane is replay-safe; it is off-digest regardless.
package rightsizing

import (
	"math"
	"sort"
	"time"
)

// ResourceKind names the resource a recommendation is about.
type ResourceKind string

const (
	CPU     ResourceKind = "cpu"     // milli-cores (request/limit in millicores; usage in millicores)
	Memory  ResourceKind = "memory"  // bytes
	Storage ResourceKind = "storage" // bytes (PVC used vs requested)
)

// Action is the recommendation verdict — a recommendation or an honest non-recommendation,
// never an executed action.
type Action string

const (
	Reclaim        Action = "reclaim"         // sustained usage well below the declared request → propose a smaller request
	ResizeUp       Action = "resize-up"       // sustained usage near/over the declared limit → propose a larger limit
	WithinHeadroom Action = "within-headroom" // usage fits the declared envelope → no change advised
	OutOfScope     Action = "out-of-scope"    // no declared bar to size against (BestEffort), no data, or QoS-pinned
	Unstable       Action = "unstable"        // the window is churny/ramping/mid-rollout → honest silence (no number)
)

// Params are the DECLARED constants of the advisory (docs/31 §6.2), never fit to the data.
type Params struct {
	Percentile float64 // the sustained percentile (e.g. 0.95)
	FLow       float64 // reclaim when p < FLow × request (e.g. 0.5)
	FHigh      float64 // resize-up when p > FHigh × limit (e.g. 0.85)
	Headroom   float64 // recommended value = p × Headroom (e.g. 1.3)
	CVCeiling  float64 // STABILITY: advise only when coefficient-of-variation ≤ this (e.g. 0.5)
	MinSamples int     // need at least this many finite samples in the window (else out of scope)
}

// DefaultParams are the declared defaults (docs/31 §6.2: f_low 0.5, f_high 0.85, h 1.3).
func DefaultParams() Params {
	return Params{Percentile: 0.95, FLow: 0.5, FHigh: 0.85, Headroom: 1.3, CVCeiling: 0.5, MinSamples: 20}
}

// valid reports whether the Params form a coherent advisory contract: a percentile in (0,1], a
// reclaim band below the resize-up band (both fractions in (0,1)), headroom that never shrinks
// the recommendation below usage (>=1), and positive stability/sample floors.
func (p Params) valid() bool {
	return p.Percentile > 0 && p.Percentile <= 1 &&
		p.FLow > 0 && p.FLow < 1 && p.FHigh > 0 && p.FHigh < 1 && p.FLow < p.FHigh &&
		p.Headroom >= 1 && p.CVCeiling > 0 && p.MinSamples > 0
}

// Input is one analyzable (workload, resource): the usage samples over the window plus the
// DECLARED request/limit and the stability signals the caller measured (active onset / pod
// churn). Request/Limit 0 = undeclared. Usage is in the resource's native unit (CPU millicores,
// memory/storage bytes).
type Input struct {
	WorkloadRef  string // "namespace/name" for display
	Namespace    string
	Name         string
	WorkloadKind string // Deployment | StatefulSet | DaemonSet | PVC …
	Container    string // "" for a PVC/storage input
	Resource     ResourceKind
	Usage        []float64 // finite-or-not; Compute filters non-finite
	Request      int64
	Limit        int64
	ActiveOnset  bool // a changepoint onset is active on this series in the window → unstable
	Churned      bool // the pod's identity churned in the window (mid-rollout) → unstable
}

// Advice is one recommendation row (or an honest non-recommendation with a stated reason).
// The percentiles + CoV are MEASURED; Request/Limit are DECLARED; Action/Recommended* are the
// ADVISORY suggestion. Nothing here is a cause or an executed action.
type Advice struct {
	WorkloadRef  string       `json:"workloadRef"`
	Namespace    string       `json:"namespace"`
	Name         string       `json:"name"`
	WorkloadKind string       `json:"workloadKind"`
	Container    string       `json:"container,omitempty"`
	Resource     ResourceKind `json:"resource"`
	QoS          string       `json:"qos"` // Guaranteed | Burstable | BestEffort (per-resource: request==limit ⇒ Guaranteed)
	P95          float64      `json:"p95"` // the MEASURED sustained percentile (native unit)
	CV           float64      `json:"cv"`  // the MEASURED coefficient of variation over the window
	Samples      int          `json:"samples"`
	Request      int64        `json:"request,omitempty"`
	Limit        int64        `json:"limit,omitempty"`
	Action       Action       `json:"action"`
	Recommended  int64        `json:"recommended,omitempty"` // proposed request (reclaim) or limit (resize-up), native unit
	Stable       bool         `json:"stable"`
	Reason       string       `json:"reason"`
}

// QoSClass derives the per-resource QoS interpretation from the declared request/limit:
// request==limit (both > 0) ⇒ Guaranteed; neither declared ⇒ BestEffort; else Burstable.
// (Kubernetes QoS is pod-level; per-resource is the right granularity for the reclaim guard —
// reclaiming a request that equals its limit is exactly what would break a Guaranteed pod.)
func QoSClass(request, limit int64) string {
	switch {
	case request > 0 && limit > 0 && request == limit:
		return "Guaranteed"
	case request == 0 && limit == 0:
		return "BestEffort"
	default:
		return "Burstable"
	}
}

// Compute is the pure, deterministic advisory: one Advice per Input, in the SAME order as the
// inputs (the caller sorts for a stable surface). It NEVER fabricates a number — every row is
// either a recommendation grounded in the measured percentile + declared config, or an honest
// non-recommendation (out-of-scope / unstable) with a stated reason. now stamps nothing in the
// logic (determinism); it is the caller's GeneratedAt.
func Compute(inputs []Input, p Params, now time.Time) []Advice {
	// Guard against a malformed Params (e.g. Headroom<1 would recommend BELOW usage, FLow>=FHigh
	// would make the bands nonsensical): fall back to the validated declared defaults rather than
	// emit garbage. The defaults are the contract; this only catches a caller bug.
	if !p.valid() {
		p = DefaultParams()
	}
	out := make([]Advice, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, adviseOne(in, p))
	}
	return out
}

func adviseOne(in Input, p Params) Advice {
	a := Advice{
		WorkloadRef: in.WorkloadRef, Namespace: in.Namespace, Name: in.Name, WorkloadKind: in.WorkloadKind,
		Container: in.Container, Resource: in.Resource, Request: in.Request, Limit: in.Limit,
		QoS: QoSClass(in.Request, in.Limit),
	}

	// Filter to VALID samples: drop NaN/Inf and negatives (a resource usage — CPU millicores,
	// memory/storage bytes — is never < 0; a negative is bad data that would suppress the
	// percentile and invite a false reclaim). The zero-value trap (docs/31 §2.5): an all-zero
	// never-moving series is NO-DATA, not "healthy" — out of scope, never a fabricated number.
	vals := make([]float64, 0, len(in.Usage))
	allZero := true
	for _, v := range in.Usage {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		vals = append(vals, v)
		if v != 0 {
			allZero = false
		}
	}
	a.Samples = len(vals)
	if len(vals) < p.MinSamples {
		a.Action, a.Reason = OutOfScope, "insufficient samples in the window to size against (window-limited)"
		return a
	}
	if allZero {
		a.Action, a.Reason = OutOfScope, "no measured usage (all-zero series) — never read as healthy"
		return a
	}

	a.P95 = percentile(vals, p.Percentile)
	a.CV = coeffVar(vals)

	// BestEffort: no declared request AND no declared limit ⇒ no config bar to size against.
	if in.Request == 0 && in.Limit == 0 {
		a.Action, a.Reason = OutOfScope, "BestEffort — no declared request or limit to size against"
		return a
	}

	// Stability gate: a churny / ramping / mid-rollout workload yields NO recommendation.
	a.Stable = a.CV <= p.CVCeiling && !in.ActiveOnset && !in.Churned
	if !a.Stable {
		a.Action = Unstable
		switch {
		case in.Churned:
			a.Reason = "mid-rollout (workload identity churned in the window) — no recommendation"
		case in.ActiveOnset:
			a.Reason = "an active changepoint onset on this series — usage not yet settled, no recommendation"
		default:
			a.Reason = "usage too variable to size against (coefficient-of-variation above the stability ceiling) — no recommendation"
		}
		return a
	}

	// Resize-up: sustained usage near or over the declared limit ⇒ propose a larger limit
	// (headroom target). Checked before reclaim: a near-limit workload is the urgent case.
	if in.Limit > 0 && a.P95 > p.FHigh*float64(in.Limit) {
		rec := int64(math.Ceil(a.P95 * p.Headroom))
		if rec <= in.Limit {
			rec = in.Limit + 1
		}
		a.Action, a.Recommended = ResizeUp, rec
		a.Reason = "sustained usage is near or over the declared limit — consider a larger limit with headroom"
		return a
	}

	// Reclaim: sustained usage well below the declared request ⇒ propose a smaller request.
	// QoS guard: NEVER reclaim a Guaranteed resource (request==limit) — that would drop it to
	// Burstable, changing the QoS class the operator chose.
	if in.Request > 0 && a.P95 < p.FLow*float64(in.Request) {
		if a.QoS == "Guaranteed" {
			a.Action = OutOfScope
			a.Reason = "Guaranteed (request==limit) — not advised to reclaim, which would change the QoS class"
			return a
		}
		rec := int64(math.Ceil(a.P95 * p.Headroom))
		if rec < 1 {
			rec = 1 // floor at 1 in the native unit (1 millicore / 1 byte) — never advise a zero-valued request
		}
		if rec >= in.Request {
			// headroom already lands at/above the request → nothing to reclaim
			a.Action, a.Reason = WithinHeadroom, "sustained usage fits the declared request within headroom"
			return a
		}
		a.Action, a.Recommended = Reclaim, rec
		a.Reason = "sustained usage is well below the declared request — the request could be reclaimed (with headroom)"
		return a
	}

	a.Action, a.Reason = WithinHeadroom, "sustained usage fits the declared request/limit envelope — no change advised"
	return a
}

// percentile returns the phi-quantile of vals using the nearest-rank method (deterministic:
// sort, then the ceil(phi·n)-th element). vals need not be pre-sorted.
func percentile(vals []float64, phi float64) float64 {
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	if len(s) == 0 {
		return 0
	}
	k := int(math.Ceil(phi*float64(len(s)))) - 1
	if k < 0 {
		k = 0
	}
	if k >= len(s) {
		k = len(s) - 1
	}
	return s[k]
}

// coeffVar is the coefficient of variation (stddev / |mean|) — the stability measure. A
// near-zero mean returns +Inf (treated as unstable: a flat-at-zero or sign-flipping series
// has no meaningful sizing target).
func coeffVar(vals []float64) float64 {
	n := len(vals)
	if n == 0 {
		return math.Inf(1)
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(n)
	if math.Abs(mean) < 1e-12 {
		return math.Inf(1)
	}
	var ss float64
	for _, v := range vals {
		d := v - mean
		ss += d * d
	}
	return math.Sqrt(ss/float64(n)) / math.Abs(mean)
}
