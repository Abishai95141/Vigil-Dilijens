package rightsizing

import (
	"math/rand"
	"testing"
	"time"
)

var rsNow = time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)

// stable returns n samples jittering tightly around mean (low CoV → stable).
func stable(n int, mean float64, r *rand.Rand) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + 0.02*mean*r.NormFloat64() // ~2% jitter ⇒ CoV ≈ 0.02
	}
	return out
}

func one(t *testing.T, in Input) Advice {
	t.Helper()
	adv := Compute([]Input{in}, DefaultParams(), rsNow)
	if len(adv) != 1 {
		t.Fatalf("want 1 advice, got %d", len(adv))
	}
	return adv[0]
}

// TestRightSizing_ReclaimWhenOverProvisioned: a Burstable container using a steady ~100m of
// CPU but requesting 500m → p95 < 0.5×request → RECLAIM, recommended ≈ p95×1.3, below request.
func TestRightSizing_ReclaimWhenOverProvisioned(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	a := one(t, Input{WorkloadRef: "ns/web", Resource: CPU, Usage: stable(60, 100, r), Request: 500, Limit: 1000})
	if a.Action != Reclaim {
		t.Fatalf("action = %q, want reclaim (p95≈100 < 0.5×500); reason=%s", a.Action, a.Reason)
	}
	if a.Recommended <= 0 || a.Recommended >= a.Request {
		t.Errorf("recommended request = %d, want a positive value below the current request %d", a.Recommended, a.Request)
	}
	if !a.Stable {
		t.Errorf("a tight ~100m series should be stable (CV=%v)", a.CV)
	}
}

// TestRightSizing_ResizeUpWhenNearLimit: steady ~950 bytes against a 1000-byte limit →
// p95 > 0.85×limit → RESIZE-UP with a larger recommended limit.
func TestRightSizing_ResizeUpWhenNearLimit(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	a := one(t, Input{WorkloadRef: "ns/cache", Resource: Memory, Usage: stable(60, 950, r), Request: 800, Limit: 1000})
	if a.Action != ResizeUp {
		t.Fatalf("action = %q, want resize-up (p95≈950 > 0.85×1000); reason=%s", a.Action, a.Reason)
	}
	if a.Recommended <= a.Limit {
		t.Errorf("recommended limit = %d, want greater than the current limit %d", a.Recommended, a.Limit)
	}
}

// TestRightSizing_GuaranteedNeverReclaimed: a Guaranteed resource (request==limit) using far
// below its request must NOT be advised to reclaim (it would break the QoS class) → OUT OF SCOPE.
func TestRightSizing_GuaranteedNeverReclaimed(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	a := one(t, Input{WorkloadRef: "ns/db", Resource: Memory, Usage: stable(60, 100, r), Request: 500, Limit: 500})
	if a.QoS != "Guaranteed" {
		t.Fatalf("qos = %q, want Guaranteed (request==limit)", a.QoS)
	}
	if a.Action == Reclaim {
		t.Errorf("a Guaranteed resource was advised to reclaim — would change its QoS class (forbidden); reason=%s", a.Reason)
	}
	if a.Action != OutOfScope {
		t.Errorf("action = %q, want out-of-scope for a Guaranteed reclaim candidate", a.Action)
	}
}

// TestRightSizing_BestEffortOutOfScope: no request AND no limit ⇒ no bar to size against.
func TestRightSizing_BestEffortOutOfScope(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	a := one(t, Input{WorkloadRef: "ns/job", Resource: CPU, Usage: stable(60, 100, r), Request: 0, Limit: 0})
	if a.QoS != "BestEffort" {
		t.Fatalf("qos = %q, want BestEffort (no request/limit)", a.QoS)
	}
	if a.Action != OutOfScope {
		t.Errorf("action = %q, want out-of-scope (BestEffort has no declared bar — never a fabricated number)", a.Action)
	}
}

// TestRightSizing_ChurnyWorkloadNoAdvice (the stability gate): a ramping / high-variance series
// yields NO recommendation (honest silence as an "unstable" row), even though it is over-provisioned.
func TestRightSizing_ChurnyWorkloadNoAdvice(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	// a steep ramp 0→600 over the window: high coefficient-of-variation.
	ramp := make([]float64, 60)
	for i := range ramp {
		ramp[i] = float64(i)*10 + 5*r.NormFloat64()
	}
	a := one(t, Input{WorkloadRef: "ns/ramp", Resource: CPU, Usage: ramp, Request: 1000, Limit: 2000})
	if a.Action != Unstable {
		t.Errorf("action = %q, want unstable (a ramping workload must yield no recommendation); CV=%v reason=%s", a.Action, a.CV, a.Reason)
	}
	// also: an explicit active onset must force unstable even on an otherwise tight series.
	rr := rand.New(rand.NewSource(6))
	b := one(t, Input{WorkloadRef: "ns/onset", Resource: CPU, Usage: stable(60, 100, rr), Request: 1000, Limit: 2000, ActiveOnset: true})
	if b.Action != Unstable {
		t.Errorf("active-onset series: action = %q, want unstable", b.Action)
	}
	// and pod churn (mid-rollout) must force unstable.
	rc := rand.New(rand.NewSource(7))
	c := one(t, Input{WorkloadRef: "ns/churn", Resource: CPU, Usage: stable(60, 100, rc), Request: 1000, Limit: 2000, Churned: true})
	if c.Action != Unstable {
		t.Errorf("churned series: action = %q, want unstable", c.Action)
	}
}

// TestRightSizing_ZeroValueOutOfScope (docs/31 §2.5): an all-zero never-moving series is
// NO-DATA, never read as "healthy" → out of scope.
func TestRightSizing_ZeroValueOutOfScope(t *testing.T) {
	zeros := make([]float64, 60)
	a := one(t, Input{WorkloadRef: "ns/idle", Resource: CPU, Usage: zeros, Request: 500, Limit: 1000})
	if a.Action != OutOfScope {
		t.Errorf("action = %q, want out-of-scope for an all-zero series (the zero-value trap)", a.Action)
	}
}

// TestRightSizing_WithinHeadroom: usage sits comfortably inside the declared envelope → no change.
func TestRightSizing_WithinHeadroom(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	a := one(t, Input{WorkloadRef: "ns/ok", Resource: CPU, Usage: stable(60, 400, r), Request: 500, Limit: 1000})
	if a.Action != WithinHeadroom {
		t.Errorf("action = %q, want within-headroom (p95≈400 in [0.5×500, 0.85×1000]); reason=%s", a.Action, a.Reason)
	}
}

// TestRightSizing_InsufficientSamplesOutOfScope: fewer than MinSamples ⇒ out of scope (window-limited).
func TestRightSizing_InsufficientSamplesOutOfScope(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	a := one(t, Input{WorkloadRef: "ns/new", Resource: CPU, Usage: stable(5, 100, r), Request: 500, Limit: 1000})
	if a.Action != OutOfScope {
		t.Errorf("action = %q, want out-of-scope (only 5 samples < MinSamples)", a.Action)
	}
}

// TestRightSizing_Deterministic: Compute is a pure function — same inputs+params ⇒ identical output.
func TestRightSizing_Deterministic(t *testing.T) {
	r := rand.New(rand.NewSource(10))
	in := []Input{
		{WorkloadRef: "ns/a", Resource: CPU, Usage: stable(60, 100, r), Request: 500, Limit: 1000},
		{WorkloadRef: "ns/b", Resource: Memory, Usage: stable(60, 950, r), Request: 800, Limit: 1000},
	}
	x := Compute(in, DefaultParams(), rsNow)
	y := Compute(in, DefaultParams(), rsNow)
	if len(x) != len(y) {
		t.Fatalf("length differs")
	}
	for i := range x {
		if x[i] != y[i] {
			t.Errorf("non-deterministic at %d: %+v vs %+v", i, x[i], y[i])
		}
	}
}

// TestRightSizing_StorageReclaim: a PVC using ~1GiB of a 10GiB request, stable → reclaim.
func TestRightSizing_StorageReclaim(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	const gib = 1 << 30
	a := one(t, Input{WorkloadRef: "ns/data-pvc", WorkloadKind: "PVC", Resource: Storage,
		Usage: stable(60, gib, r), Request: 10 * gib, Limit: 0})
	// storage has no "limit"; request==10GiB, usage~1GiB ⇒ p95 < 0.5×request ⇒ reclaim.
	if a.Action != Reclaim {
		t.Errorf("action = %q, want reclaim for an over-provisioned PVC; reason=%s", a.Action, a.Reason)
	}
}
