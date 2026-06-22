package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const overlayDir = "../../../ontology/graph/overlays"

func loadKGWithOverlays(t *testing.T) *Graph {
	t.Helper()
	g, err := LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	return g
}

// With the authored overlays applied, every one of the 38 phenomena must declare a
// span from the doc 02 §3.5 vocabulary, and non-entity-local spans must declare
// their traversal edge types — the doc 14 A14 gap, closed by authored content.
func TestOverlaysCloseTheSpanGap(t *testing.T) {
	g := loadKGWithOverlays(t)
	if len(g.Phenomena) != 45 {
		t.Fatalf("phenomena = %d, want 45 (+ 3 PSI saturation, psi-pressure-v1 / v0.14.0; + 1 PHEN_PVC_FILLING, pvc-filling-v1 / v0.15.0)", len(g.Phenomena))
	}
	counts := map[string]int{}
	for id, p := range g.Phenomena {
		if !p.HasSpan() {
			t.Errorf("%s: still missing a span after overlays", id)
			continue
		}
		counts[p.Span]++
		if p.Span != SpanEntityLocal && len(p.TraversalEdgeTypes) == 0 {
			t.Errorf("%s: span %s without traversal edge types", id, p.Span)
		}
		if p.Span == SpanEntityLocal && len(p.TraversalEdgeTypes) != 0 {
			t.Errorf("%s: entity-local with traversal edges %v", id, p.TraversalEdgeTypes)
		}
	}
	// The authored distribution (22 + 17 + 5 = 44) — pinned so an accidental edit
	// to the overlay shows up as a deliberate diff here too. v0.4.0 (doc 15 Phase C)
	// added PHEN_UPSTREAM_DEGRADATION (entity-local) + PHEN_DOWNSTREAM_IMPACT
	// (first-order over the new "flow" traversal edge). v0.14.0 (psi-pressure-v1)
	// added 3 entity-local PSI saturation phenomena (19 -> 22); v0.15.0 (pvc-filling-v1)
	// added PHEN_PVC_FILLING (entity-local, 22 -> 23).
	if counts[SpanEntityLocal] != 23 || counts[SpanFirstOrder] != 17 || counts[SpanSecondOrder] != 5 {
		t.Errorf("span distribution = %v, want entity-local:23 first-order:17 second-order:5", counts)
	}
}

// The worked example of doc 02 §3.5: the OOM phenomenon (whose KG members include
// the node-carried dmesg line) is declared first-order over runs-on.
func TestOOMSpanAuthored(t *testing.T) {
	g := loadKGWithOverlays(t)
	p := g.Phenomena["PHEN_OOM_KILL_CGROUP"]
	if p.Span != SpanFirstOrder {
		t.Errorf("OOM span = %q, want first-order", p.Span)
	}
	if len(p.TraversalEdgeTypes) != 1 || p.TraversalEdgeTypes[0] != "runs-on" {
		t.Errorf("OOM traversal = %v, want [runs-on]", p.TraversalEdgeTypes)
	}
}

// The threshold rules attach, are sorted, referentially valid, and carry the
// doc 02 §3.4 coherence properties (config-relative ⇒ known path + factor;
// absolute/rate ⇒ flagged default).
func TestThresholdRulesAttached(t *testing.T) {
	g := loadKGWithOverlays(t)
	if len(g.Rules) != 21 {
		t.Fatalf("rules = %d, want 21 (... + 1 v5: THR_NODE_DISK_PRESSURE + 1 v6: THR_PVC_PENDING + 1 disk-filling: THR_CONTAINER_FS_USAGE_VS_EPHEMERAL_LIMIT + 1 init-container: THR_INIT_CONTAINER_RESTARTS_RATE + 3 psi-pressure: THR_CONTAINER_PSI_{CPU_WAITING,MEMORY_STALLED,IO_STALLED}_RATE + 1 pvc-filling: THR_PVC_USED_VS_REQUESTED_STORAGE)", len(g.Rules))
	}
	for i := 1; i < len(g.Rules); i++ {
		if g.Rules[i-1].ID >= g.Rules[i].ID {
			t.Errorf("rules not sorted: %s >= %s", g.Rules[i-1].ID, g.Rules[i].ID)
		}
	}
	for _, r := range g.Rules {
		if g.Signals[r.Signal] == nil {
			t.Errorf("%s: dangling signal %s", r.ID, r.Signal)
		}
		if _, err := r.WindowDuration(); err != nil {
			t.Errorf("%s: bad window: %v", r.ID, err)
		}
		switch r.Kind {
		case RuleConfigRelative:
			if !knownConfigPaths[r.ConfigPath] || r.Factor <= 0 {
				t.Errorf("%s: incoherent config-relative rule: path=%q factor=%v", r.ID, r.ConfigPath, r.Factor)
			}
		case RuleAbsolute, RuleRateOfChange:
			if r.Default == nil {
				t.Errorf("%s: %s rule without flagged default", r.ID, r.Kind)
			}
		}
	}
	// The doc 04 §3.3 worked example rule is present with the documented shape.
	r, ok := g.RuleByID("THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT")
	if !ok {
		t.Fatal("expected THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT")
	}
	if r.ConfigPath != PathContainerLimitsMemory || r.Factor != 0.95 || r.EntityScope != "Container" || r.Direction != "above" {
		t.Errorf("working-set rule shape wrong: %+v", r)
	}
}

// The version pin must cover overlays: base-only and base+overlays are different
// releases, and the merged hash is deterministic.
func TestOverlayVersionPinning(t *testing.T) {
	base := loadKG(t)
	merged := loadKGWithOverlays(t)
	merged2 := loadKGWithOverlays(t)
	if base.Version == merged.Version {
		t.Error("overlays applied but Version pin unchanged — a changed overlay must be a changed release")
	}
	if merged.Version != merged2.Version {
		t.Errorf("merged hash not deterministic: %s vs %s", merged.Version, merged2.Version)
	}
	if !strings.HasPrefix(merged.Version, "sha256:") {
		t.Errorf("version %q not a sha256 pin", merged.Version)
	}
	// Provenance travels with the content. v0.5.0 (G2): + detect-conditions-v4 (KSM
	// restart check). v0.6.0 (G2b): + threshold-rules-v4 + detect-conditions-v5 (the
	// KSM-derived pod-eviction member of EVICTION_MEMORY). v0.7.0 (DISK): +
	// threshold-rules-v5 + detect-conditions-v6 (the KSM node-condition disk-pressure
	// member of DISK_PID_INODE_PRESSURE). v0.8.0 (PVC): + threshold-rules-v6 +
	// detect-conditions-v7 (the KSM stuck-Pending member of VOLUME_MOUNT_FAILURE), so 15.
	// v0.10.0 (Phase 5): + phenomenon-severity-v1 (the authored severity field), so 17.
	// v0.11.0 (INIT): + init-container-failure-v1 (the KSM init-restart member +
	// flagged rate bar that wires PHEN_INIT_CONTAINER_FAILURE), so 18.
	// v0.12.0 (STRUCTURING): + detection-status-v1 (the membership-structuring escape
	// hatch acknowledging the 6 metric-matcher-dark phenomena), so 19.
	// v0.13.0 (CORRECTIONS): + corrections-v1 (governed overrides: 2 non-causal probe-cascade
	// triggers downgraded, the EQG_OOM_EVENTS false-equivalence pattern removed), so 20.
	// v0.14.0 (PSI): + psi-pressure-v1 (the PSI saturation lane), so 21.
	// v0.15.0 (PVC-fill): + pvc-filling-v1 (the per-PVC fill lane), so 22.
	if len(merged.Overlays) != 22 {
		t.Fatalf("overlay provenance records = %d, want 22 (… + init-container-failure-v1, detection-status-v1, corrections-v1, psi-pressure-v1, pvc-filling-v1)", len(merged.Overlays))
	}
	if len(merged.ChecksFor("PHEN_MEMORY_LEAK")) != 1 {
		t.Errorf("expected the authored MEMORY_LEAK member check")
	}
	for _, o := range merged.Overlays {
		if o.Author == "" || o.Name == "" {
			t.Errorf("overlay %s missing provenance: %+v", o.File, o)
		}
	}
}

// A missing overlay dir yields the base graph unchanged — gaps stay VISIBLE, the
// loader never silently defaults a span (doc 02 §3.6).
func TestNoOverlayDirKeepsGapsVisible(t *testing.T) {
	g, err := LoadWithOverlays(kgPath, "does-not-exist")
	if err != nil {
		t.Fatalf("LoadWithOverlays(no dir): %v", err)
	}
	withSpan := 0
	for _, p := range g.Phenomena {
		if p.HasSpan() {
			withSpan++
		}
	}
	if withSpan != 0 {
		t.Errorf("base graph has %d spans without overlays; the gap must stay visible", withSpan)
	}
	if len(g.Rules) != 0 {
		t.Errorf("base graph has %d rules without overlays", len(g.Rules))
	}
}

// Defective overlays are rejected with a reason, never half-applied: unknown
// phenomenon, bad span vocab, walk without declared edges, missing rationale,
// missing author, dangling rule signal, incoherent rule kinds.
func TestOverlayRejection(t *testing.T) {
	cases := []struct {
		name, body, wantErr string
	}{
		{"unknown phenomenon", `
overlay: t
author: a
spans:
  PHEN_NOPE: {span: entity-local, rationale: r}
`, "unknown phenomenon"},
		{"bad span vocab", `
overlay: t
author: a
spans:
  PHEN_OOM_KILL_CGROUP: {span: galaxy-wide, rationale: r}
`, "invalid span"},
		{"walk without edges", `
overlay: t
author: a
spans:
  PHEN_OOM_KILL_CGROUP: {span: first-order, rationale: r}
`, "requires traversal edge types"},
		{"missing rationale", `
overlay: t
author: a
spans:
  PHEN_OOM_KILL_CGROUP: {span: entity-local}
`, "rationale"},
		{"missing author", `
overlay: t
spans:
  PHEN_OOM_KILL_CGROUP: {span: entity-local, rationale: r}
`, "author"},
		{"dangling rule signal", `
overlay: t
author: a
rules:
  - {id: R1, signal: SIG_NOPE, metric: m, kind: absolute, default: 1, direction: above, entity_scope: Node, window: 5m}
`, "unknown signal"},
		{"config-relative without path", `
overlay: t
author: a
rules:
  - {id: R1, signal: SIG_node_nf_conntrack_entries_6161e704, metric: m, kind: config-relative, factor: 0.9, direction: above, entity_scope: Node, window: 5m}
`, "config_path"},
		{"absolute without default", `
overlay: t
author: a
rules:
  - {id: R1, signal: SIG_node_nf_conntrack_entries_6161e704, metric: m, kind: absolute, direction: above, entity_scope: Node, window: 5m}
`, "default"},
		{"transform on a rate rule", `
overlay: t
author: a
rules:
  - {id: R1, signal: SIG_node_nf_conntrack_entries_6161e704, metric: m, kind: rate-of-change, default: 1, direction: above, entity_scope: Node, window: 5m, transform: age-from-timestamp}
`, "only meaningful on a gauge level rule"},
		{"transform combined with a divisor", `
overlay: t
author: a
rules:
  - {id: R1, signal: SIG_node_nf_conntrack_entries_6161e704, metric: m, divisor_metric: d, kind: config-relative, config_path: slo.freshness.max_age, factor: 1.0, direction: above, entity_scope: Node, window: 5m, transform: age-from-timestamp}
`, "cannot combine with divisor_metric"},
	}

	raw, err := os.ReadFile(kgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			err = g.applyOverlay("test.yaml", []byte(c.body))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

// The v2 first-order authoring (doc 07 M2): anchors + neighbour-scoped checks
// land on the merged graph with the declared shape.
func TestFirstOrderConditionsAuthored(t *testing.T) {
	g := loadKGWithOverlays(t)
	cases := map[string]struct {
		anchor string
		checks int
	}{
		"PHEN_THROTTLING_CASCADE":      {"Container", 2},
		"PHEN_EVICTION_MEMORY":         {"Node", 2}, // v2 node-memory anchor + v5 evicted-pod neighbour (G2b)
		"PHEN_CONNTRACK_EXHAUSTION":    {"Node", 2},
		"PHEN_OOM_KILL_SYSTEM":         {"Node", 1},
		"PHEN_DISK_PID_INODE_PRESSURE": {"Node", 1}, // v6 node-condition DiskPressure anchor (DISK)
	}
	for id, want := range cases {
		p := g.Phenomena[id]
		if p.Anchor != want.anchor {
			t.Errorf("%s anchor = %q, want %q", id, p.Anchor, want.anchor)
		}
		if got := len(g.ChecksFor(id)); got != want.checks {
			t.Errorf("%s checks = %d, want %d", id, got, want.checks)
		}
	}
	// The cascade's PSI member is neighbour-scoped; its ratio member is anchor-scoped.
	for _, c := range g.ChecksFor("PHEN_THROTTLING_CASCADE") {
		switch c.Metric {
		case "node_pressure_cpu_waiting_seconds_total":
			if c.OnAnchor() {
				t.Error("PSI check must be neighbour-scoped")
			}
		case "container_cpu_cfs_throttled_periods_total":
			if !c.OnAnchor() {
				t.Error("throttle-ratio check must be anchor-scoped")
			}
		}
	}
}

// Cross-overlay coherence (finalizeOverlays): span/anchor/neighbour constraints
// hold across files in either merge order.
func TestFinalizeOverlayRejections(t *testing.T) {
	raw, err := os.ReadFile(kgPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, body, want string }{
		{"anchor on entity-local", `
overlay: t
author: a
spans:
  PHEN_MEMORY_LEAK: {span: entity-local, rationale: r}
anchors:
  PHEN_MEMORY_LEAK: Container
`, "anchors are for spanned phenomena"},
		{"neighbour check on entity-local", `
overlay: t
author: a
spans:
  PHEN_MEMORY_LEAK: {span: entity-local, rationale: r}
checks:
  PHEN_MEMORY_LEAK:
    - {signal: SIG_container_memory_family_14_metrics_529498d3, metric: m, facet: level, expect: crossed, on: neighbour}
`, "neighbour-scoped check on a non-spanned"},
		{"spanned checks without anchor", `
overlay: t
author: a
spans:
  PHEN_OOM_KILL_SYSTEM: {span: first-order, traversal_edge_types: [runs-on], rationale: r}
checks:
  PHEN_OOM_KILL_SYSTEM:
    - {signal: SIG_node_vmstat_family_30_metrics_005633c0, metric: m, facet: rate-guard, expect: breached}
`, "must declare an anchor"},
		{"unknown anchor kind", `
overlay: t
author: a
anchors:
  PHEN_OOM_KILL_SYSTEM: Galaxy
`, "unknown anchor kind"},
		{"unknown on", `
overlay: t
author: a
checks:
  PHEN_MEMORY_LEAK:
    - {signal: SIG_container_memory_family_14_metrics_529498d3, metric: m, facet: level, expect: crossed, on: elsewhere}
`, "unknown on"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			err = g.applyOverlay("test.yaml", []byte(c.body))
			if err == nil {
				err = g.finalizeOverlays()
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

// Overlay files merge in sorted file-name order regardless of directory listing
// order, so the version pin is stable across platforms.
func TestOverlayPathOrderDeterministic(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.yaml", "a.yaml", "c.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := overlayPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"), filepath.Join(dir, "c.yml")}
	if len(paths) != 3 || paths[0] != want[0] || paths[1] != want[1] || paths[2] != want[2] {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

// Regression (07-M1 finding 4 & 9): defective detection-condition overlays are
// rejected — a duplicate check per signal, and min_state on a non-slope facet.
func TestDetectionConditionDefectsRejected(t *testing.T) {
	raw, err := os.ReadFile(kgPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, body, want string }{
		{"duplicate signal check", `
overlay: t
author: a
checks:
  PHEN_MEMORY_LEAK:
    - {signal: SIG_container_memory_family_14_metrics_529498d3, metric: m, facet: slope, expect: rising}
    - {signal: SIG_container_memory_family_14_metrics_529498d3, metric: m, facet: slope, expect: falling}
`, "duplicate check"},
		{"min_state on non-slope", `
overlay: t
author: a
checks:
  PHEN_MEMORY_LEAK:
    - {signal: SIG_container_memory_family_14_metrics_529498d3, metric: m, facet: level, expect: crossed, min_state: above}
`, "only meaningful on a slope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			err = g.applyOverlay("test.yaml", []byte(c.body))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

// TestDetectionLaneVocab locks the runtime detection-lane vocabulary to the canonical set.
// It MUST stay in sync with graphlint's ovDetectionLaneVocab (tools/graphlint, guarded by
// its own TestDetectionLaneVocabParity) — adding/removing a lane requires updating both
// copies and both lock tests.
func TestDetectionLaneVocab(t *testing.T) {
	canonical := []string{"events-only", "log-only", "needs-scrape-lane", "needs-entity-binding", "wireable-backlog"}
	if len(knownDetectionLanes) != len(canonical) {
		t.Fatalf("lane vocab size = %d, want %d (mirror graphlint ovDetectionLaneVocab)", len(knownDetectionLanes), len(canonical))
	}
	for _, l := range canonical {
		if !IsValidDetectionLane(l) {
			t.Errorf("lane %q should be valid", l)
		}
	}
	// Empty is NOT valid here (a detection_status must name a concrete lane), and an unknown
	// lane is rejected.
	if IsValidDetectionLane("") || IsValidDetectionLane("galaxy-wide") {
		t.Error("empty / unknown lane must be invalid")
	}
}

// Governed corrections (doc 12): an overlay `overrides:` block downgrades a non-causal
// relation and removes a false-equivalence pattern. A valid override applies; one that
// targets non-existent content or omits a rationale is a hard error.
func TestOverrideOverlay(t *testing.T) {
	raw, err := os.ReadFile(kgPath)
	if err != nil {
		t.Fatal(err)
	}

	// Valid: downgrade a real relation + remove a present pattern.
	g, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	valid := `
overlay: t
author: a
overrides:
  phenomenon_relations:
    - src: PHEN_PROBE_CASCADE_META
      dst: PHEN_KUBE_PROXY_SYNC_SLOW
      set_role: corroborating
      rationale: probes hit the pod IP directly and bypass kube-proxy
  equivalence_patterns:
    - id: EQG_OOM_EVENTS
      remove: [oom_kill]
      rationale: the unanchored pattern mis-binds node_vmstat_oom_kill as a container OOM
`
	if err := g.applyOverlay("test.yaml", []byte(valid)); err != nil {
		t.Fatalf("valid override rejected: %v", err)
	}
	rel := ""
	for _, r := range g.Phenomena["PHEN_PROBE_CASCADE_META"].Relations {
		if r.TargetID == "PHEN_KUBE_PROXY_SYNC_SLOW" {
			rel = r.Role
		}
	}
	if rel != "corroborating" {
		t.Errorf("relation role = %q, want corroborating", rel)
	}
	for _, p := range g.EquivalenceGroups["EQG_OOM_EVENTS"].Patterns {
		if p == "oom_kill" {
			t.Error("oom_kill pattern should have been removed")
		}
	}

	// Defects are hard errors.
	cases := []struct{ name, body, want string }{
		{"relation does not exist", `
overlay: t
author: a
overrides:
  phenomenon_relations:
    - {src: PHEN_MEMORY_LEAK, dst: PHEN_DNS_FAILURE, set_role: corroborating, rationale: x}
`, "no such relation exists"},
		{"relation missing rationale", `
overlay: t
author: a
overrides:
  phenomenon_relations:
    - {src: PHEN_PROBE_CASCADE_META, dst: PHEN_KUBE_PROXY_SYNC_SLOW, set_role: corroborating, rationale: ""}
`, "rationale is required"},
		{"bad set_role", `
overlay: t
author: a
overrides:
  phenomenon_relations:
    - {src: PHEN_PROBE_CASCADE_META, dst: PHEN_KUBE_PROXY_SYNC_SLOW, set_role: sideways, rationale: x}
`, "is not one of trigger|downstream|corroborating"},
		{"pattern not present", `
overlay: t
author: a
overrides:
  equivalence_patterns:
    - {id: EQG_OOM_EVENTS, remove: [not_a_real_pattern], rationale: x}
`, "is not present to remove"},
		{"unknown equivalence group", `
overlay: t
author: a
overrides:
  equivalence_patterns:
    - {id: EQG_DOES_NOT_EXIST, remove: [x], rationale: y}
`, "unknown equivalence group"},
		{"empties the group", `
overlay: t
author: a
overrides:
  equivalence_patterns:
    - {id: EQG_OOM_EVENTS, remove: ['^container_oom_events_total$', oom_kill], rationale: x}
`, "would remove the LAST pattern"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			err = g.applyOverlay("test.yaml", []byte(c.body))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

// detection_status (the membership-structuring escape hatch) is AUTHORED: a known
// phenomenon, a lane from the closed vocabulary, and a REQUIRED falsifiable rationale.
// A valid declaration applies; a defective one is a hard error.
func TestDetectionStatusOverlay(t *testing.T) {
	raw, err := os.ReadFile(kgPath)
	if err != nil {
		t.Fatal(err)
	}

	// Valid: applies onto an existing phenomenon.
	g, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	valid := `
overlay: t
author: a
detection_status:
  PHEN_CERT_EXPIRY:
    lane: needs-scrape-lane
    rationale: no kube-apiserver scrape lane exists in obsd
`
	if err := g.applyOverlay("test.yaml", []byte(valid)); err != nil {
		t.Fatalf("valid detection_status rejected: %v", err)
	}
	ds := g.Phenomena["PHEN_CERT_EXPIRY"].DetectionStatus
	if ds == nil || ds.Lane != "needs-scrape-lane" || ds.Rationale == "" {
		t.Fatalf("detection_status not applied: %+v", ds)
	}

	// Defects are hard errors.
	cases := []struct{ name, body, want string }{
		{"unknown lane", `
overlay: t
author: a
detection_status:
  PHEN_CERT_EXPIRY: {lane: galaxy-wide, rationale: x}
`, "is not one of"},
		{"empty rationale", `
overlay: t
author: a
detection_status:
  PHEN_CERT_EXPIRY: {lane: needs-scrape-lane, rationale: ""}
`, "needs a rationale"},
		{"unknown phenomenon", `
overlay: t
author: a
detection_status:
  PHEN_DOES_NOT_EXIST: {lane: events-only, rationale: x}
`, "unknown phenomenon"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			err = g.applyOverlay("test.yaml", []byte(c.body))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}
