package binding

import (
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// fixtureEvidence is a seeded stream-evidence world: the harness-style fixture
// bed for QA (doc 04 M3 exit: seeded false-equivalence fixtures are caught).
type fixtureEvidence struct {
	streams map[string][]string        // uid|metric -> stream ids
	info    map[string][2]string       // streamID -> {kind, expoType}
	history map[string][]EvidencePoint // streamID -> samples
}

func (f fixtureEvidence) StreamsFor(uid, metric string) []string { return f.streams[uid+"|"+metric] }
func (f fixtureEvidence) StreamInfo(id string) (string, string, bool) {
	i, ok := f.info[id]
	return i[0], i[1], ok
}
func (f fixtureEvidence) History(id string, n int) []EvidencePoint {
	h := f.history[id]
	if len(h) > n {
		h = h[len(h)-n:]
	}
	return h
}

func qaT(i int) time.Time { return compileAt.Add(time.Duration(i) * 15 * time.Second) }

func points(vals ...float64) []EvidencePoint {
	out := make([]EvidencePoint, len(vals))
	for i, v := range vals {
		out[i] = EvidencePoint{At: qaT(i), Value: v}
	}
	return out
}

// compiledFixture builds a real compile over the boutique fixture so QA mutates
// genuine binding records.
func rulesFixture(t *testing.T) map[string]*graph.ThresholdRule {
	t.Helper()
	g := loadGraph(t)
	out := make(map[string]*graph.ThresholdRule, len(g.Rules))
	for _, r := range g.Rules {
		out[r.ID] = r
	}
	return out
}

func compiledFixture(t *testing.T) *Result {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	return Compile(g, inv, cfg, nil, compileAt)
}

// wsKey is the evidence key of web-a's working-set binding (podUID/container).
const wsKey = "uid-a/server|container_memory_working_set_bytes"

// Healthy evidence: plausible gauge values under a real binding ⇒ VERIFIED.
func TestQAHealthyEvidenceVerifies(t *testing.T) {
	res := compiledFixture(t)
	ev := fixtureEvidence{
		streams: map[string][]string{wsKey: {"s1"}},
		info:    map[string][2]string{"s1": {"Container", "gauge"}},
		history: map[string][]EvidencePoint{"s1": points(1.2e8, 1.25e8, 1.3e8)},
	}
	sum := ValidateBindings(res, nil, rulesFixture(t), ev, RangeBounds{MaxMemoryBytes: 16 << 30})
	if sum.Verified != 1 {
		t.Fatalf("verified = %d, want exactly the evidenced binding (summary %+v)", sum.Verified, sum)
	}
	b := find(t, res, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "web-a")
	if b.Validation != ValidationVerified {
		t.Errorf("web-a working set = %s (%s), want verified", b.Validation, b.Reason)
	}
	// Everything without evidence stays suspect — never silently promoted.
	other := find(t, res, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "web-b")
	if other.Validation != ValidationSuspect || !strings.Contains(other.Reason, "no stream evidence") {
		t.Errorf("web-b = %s (%s), want suspect/no-evidence", other.Validation, other.Reason)
	}
}

// THE false-equivalence catcher (doc 04 M3 exit): seeded fixtures where the name
// matched but the semantics lie — each must FAIL, with the contradiction stated.
func TestQASeededFalseEquivalencesCaught(t *testing.T) {
	cases := []struct {
		name    string
		info    [2]string
		history []EvidencePoint
		want    string // substring of the failure reason
	}{
		{
			// A cumulative counter stream masquerading under the gauge-charactered
			// working-set variable: monotone growth + counter TYPE.
			"counter masquerading as gauge",
			[2]string{"Container", "counter"},
			points(100, 200, 300, 400),
			"character mismatch: instantaneous variable exposed as counter",
		},
		{
			// Scope lie: a node-scoped stream bound under a container-scoped rule.
			"scope mismatch",
			[2]string{"Node", "gauge"},
			points(1.2e8, 1.3e8),
			"scope mismatch",
		},
		{
			// Physically implausible: working set far beyond any node's capacity
			// (classic unit error — KiB exported as bytes upstream, or a wrong join).
			"range implausible",
			[2]string{"Container", "gauge"},
			points(1e15, 1.1e15),
			"exceeds plausible capacity",
		},
		{
			// Negative memory: impossible for the quantity.
			"negative value",
			[2]string{"Container", "gauge"},
			points(-5, 10),
			"negative value",
		},
		{
			// Identity/naming ambiguity: two streams claim the same (entity, variable).
			"ambiguous duplicate streams",
			[2]string{"Container", "gauge"},
			points(1, 2),
			"ambiguous evidence",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := compiledFixture(t)
			ev := fixtureEvidence{
				streams: map[string][]string{wsKey: {"s1"}},
				info:    map[string][2]string{"s1": c.info, "s2": c.info},
				history: map[string][]EvidencePoint{"s1": c.history, "s2": c.history},
			}
			if c.name == "ambiguous duplicate streams" {
				ev.streams[wsKey] = []string{"s1", "s2"}
			}
			sum := ValidateBindings(res, nil, rulesFixture(t), ev, RangeBounds{MaxMemoryBytes: 16 << 30})
			if sum.Failed != 1 {
				t.Fatalf("failed = %d, want exactly 1 (summary %+v)", sum.Failed, sum)
			}
			b := find(t, res, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "web-a")
			if b.Validation != ValidationFailed || !strings.Contains(b.Reason, c.want) {
				t.Errorf("verdict = %s (%q), want failed with %q", b.Validation, b.Reason, c.want)
			}
			if len(sum.Findings) != 1 || !strings.Contains(sum.Findings[0], "FAILED") {
				t.Errorf("findings = %v, want the failure surfaced", sum.Findings)
			}
		})
	}
}

// A declared counter that decreases repeatedly is not cumulative — the observed
// shape contradicts the declared character even when the TYPE matches the name.
func TestQACounterShapeContradiction(t *testing.T) {
	res := compiledFixture(t)
	cpuKey := "uid-a/server|container_cpu_usage_seconds_total"
	ev := fixtureEvidence{
		streams: map[string][]string{cpuKey: {"c1"}},
		info:    map[string][2]string{"c1": {"Container", "counter"}},
		history: map[string][]EvidencePoint{"c1": points(100, 90, 95, 80, 85)},
	}
	ValidateBindings(res, nil, rulesFixture(t), ev, RangeBounds{})
	b := find(t, res, "THR_CONTAINER_CPU_USAGE_VS_LIMIT", "web-a")
	if b.Validation != ValidationFailed || !strings.Contains(b.Reason, "decreased") {
		t.Errorf("verdict = %s (%q), want failed on repeated decreases", b.Validation, b.Reason)
	}
	// And a single decrease (a legitimate restart reset) does NOT fail.
	res2 := compiledFixture(t)
	ev2 := fixtureEvidence{
		streams: map[string][]string{cpuKey: {"c1"}},
		info:    map[string][2]string{"c1": {"Container", "counter"}},
		history: map[string][]EvidencePoint{"c1": points(100, 110, 5, 20, 30)},
	}
	ValidateBindings(res2, nil, rulesFixture(t), ev2, RangeBounds{})
	b2 := find(t, res2, "THR_CONTAINER_CPU_USAGE_VS_LIMIT", "web-a")
	if b2.Validation != ValidationVerified {
		t.Errorf("single counter reset should verify, got %s (%q)", b2.Validation, b2.Reason)
	}
}

// Availability gating (the M1<->M2 join): a rule whose signal is unobtainable on
// this cluster lands EVERY pair out-of-scope with the availability reason — and
// the resolvability metric only counts pairs that could ever be evaluated.
func TestCompileAvailabilityGating(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	avail := GateSignals(g, kindFacts()) // kind: no KSM, no node-exporter

	res := Compile(g, inv, cfg, avail, compileAt)

	// The restarts rule rides on KSM — absent here: all its pairs out-of-scope.
	restarts := find(t, res, "THR_CONTAINER_RESTARTS_RATE", "web-a")
	if restarts.State != StateOutOfScope || !strings.Contains(restarts.Reason, "kube-state-metrics") {
		t.Errorf("restarts binding = %s (%q), want out-of-scope naming KSM", restarts.State, restarts.Reason)
	}
	// Node memory rides on node-exporter — absent: out-of-scope. The PVC rule's
	// signal carries the AUTHORED capability CAP_CSI_DRIVER, and this cluster has
	// no CSI driver (local-path) — also out-of-scope, honoring the authored claim.
	// Config-eligible therefore shrinks 8 -> 6 (mem 3 + cpu 3), bound 6 -> 4.
	nodeMem := find(t, res, "THR_NODE_MEMAVAILABLE_VS_ALLOCATABLE", "worker-1")
	if nodeMem.State != StateOutOfScope {
		t.Errorf("node mem binding = %s, want out-of-scope (node-exporter absent)", nodeMem.State)
	}
	pvc := find(t, res, "THR_PVC_USED_VS_REQUESTED", "data-0")
	if pvc.State != StateOutOfScope || !strings.Contains(pvc.Reason, "CAP_CSI_DRIVER") {
		t.Errorf("pvc binding = %s (%q), want out-of-scope on CAP_CSI_DRIVER", pvc.State, pvc.Reason)
	}
	if res.Coverage.ConfigEligible != 6 || res.Coverage.ConfigBound != 4 {
		t.Errorf("eligible/bound = %d/%d, want 6/4 (availability-gated pairs excluded)", res.Coverage.ConfigEligible, res.Coverage.ConfigBound)
	}
	// The cAdvisor-backed rules still instantiate normally.
	ws := find(t, res, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "web-a")
	if ws.State != StateBound || ws.Bar == nil {
		t.Errorf("working-set binding should be unaffected: %+v", ws)
	}
}

// Regression (Phase-1 live finding): a divisor-less COUNTER under a ratio bar
// (PSI stall-seconds — the evaluated quantity is its RATE, a fraction of wall
// time) must be range-checked on the observed rate, never on the cumulative
// level. The live cluster's ~1000 stall-seconds was range-FAILED against 1.5
// before this distinction, silently excluding the cascade's node-side member.
func TestQARateEvaluatedRatioRangeUsesRate(t *testing.T) {
	mkPSI := func() *Result {
		return &Result{Bindings: []Binding{{
			CEIKey: "i|cl||Node|w1|uid-n", Entity: "Node",
			RuleID: "THR_NODE_CPU_PSI_STALL", Metric: "node_pressure_cpu_waiting_seconds_total",
			State: StateBound, Validation: ValidationSuspect,
			Bar: &ResolvedBar{Kind: "absolute", Source: SourceDefault, Flagged: true,
				Value: 0.10, Unit: "ratio", Direction: "above", Window: "5m"},
		}}}
	}
	key := "uid-n|node_pressure_cpu_waiting_seconds_total"

	// Plausible: cumulative ~1000s rising 3s per 15s scrape (rate 0.2/s) ⇒ VERIFIED.
	res := mkPSI()
	ev := fixtureEvidence{
		streams: map[string][]string{key: {"p1"}},
		info:    map[string][2]string{"p1": {"Node", "counter"}},
		history: map[string][]EvidencePoint{"p1": points(1000, 1003, 1006, 1009)},
	}
	ValidateBindings(res, nil, rulesFixture(t), ev, RangeBounds{})
	if v := res.Bindings[0].Validation; v != ValidationVerified {
		t.Errorf("plausible PSI counter under a ratio bar = %s (%q), want verified (the level is not the evaluated quantity)",
			v, res.Bindings[0].Reason)
	}

	// Implausible RATE: +30 stall-seconds per 15s of wall time (rate 2.0) ⇒ FAILED.
	res2 := mkPSI()
	ev2 := fixtureEvidence{
		streams: map[string][]string{key: {"p1"}},
		info:    map[string][2]string{"p1": {"Node", "counter"}},
		history: map[string][]EvidencePoint{"p1": points(1000, 1030, 1060, 1090)},
	}
	ValidateBindings(res2, nil, rulesFixture(t), ev2, RangeBounds{})
	if v := res2.Bindings[0].Validation; v != ValidationFailed {
		t.Errorf("a stall rate beating wall time = %s (%q), want failed", v, res2.Bindings[0].Reason)
	}
}
