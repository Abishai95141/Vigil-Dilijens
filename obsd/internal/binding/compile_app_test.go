package binding

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// appQueueRule is an application-signal SLO rule (doc 15 cap. A): Pod-scoped,
// config-relative, its bar resolved from the workload's declared slo.queue.max_depth.
func appQueueRule(factor float64) *graph.ThresholdRule {
	return &graph.ThresholdRule{
		ID: "THR_APP_QUEUE_DEPTH", Signal: "SIG_app_queue", Metric: "app_queue_depth",
		Kind: graph.RuleConfigRelative, ConfigPath: "slo.queue.max_depth", Factor: factor,
		Direction: "above", EntityScope: "Pod", Window: "5m",
	}
}

// appQueueRuleGated is appQueueRule with the app-SLO regime eligibility gate (the real
// overlay carries it): the rule applies only to a workload that declares >=1 vigil.io/slo.*.
func appQueueRuleGated(factor float64) *graph.ThresholdRule {
	r := appQueueRule(factor)
	r.Eligibility = graph.EligibilityAppSLODeclared
	return r
}

// The keystone borrowed-normativity test: a DECLARED app SLO resolves to a config-sourced
// bar (not flagged, not defaulted), the factor is applied, and it lives on the Pod CEI.
func TestAppSLOBindsDeclaredBar(t *testing.T) {
	p := pod(t, "shop", "agg-1", "uid-agg", "Deployment", "aggregation")
	cfg := fakeConfig{pods: map[string]PodConfig{
		"shop/agg-1": {SLOs: map[string]float64{"slo.queue.max_depth": 1000}},
	}}
	res := &Result{}
	cov := &RuleCoverage{}
	bindPod(res, cov, appQueueRule(0.9), Emission{}, p, cfg, compileAt)

	if len(res.Bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(res.Bindings))
	}
	b := res.Bindings[0]
	if b.Entity != "Pod" || b.CEIKey != p.CEI.Key() {
		t.Errorf("binding entity/key = %s/%s, want Pod/%s", b.Entity, b.CEIKey, p.CEI.Key())
	}
	if b.Bar == nil {
		t.Fatalf("bar is nil; want a declared config bar (%s)", b.Reason)
	}
	if b.Bar.Source != SourceConfig || b.Bar.Flagged {
		t.Errorf("bar source/flagged = %s/%v, want config/false (borrowed normativity, not a default)", b.Bar.Source, b.Bar.Flagged)
	}
	if b.Bar.Value != 900 { // 1000 declared * 0.9 factor
		t.Errorf("bar value = %v, want 900 (declared 1000 x factor 0.9)", b.Bar.Value)
	}
	if cov.ConfigBound != 1 || cov.Unbounded != 0 {
		t.Errorf("coverage configBound/unbounded = %d/%d, want 1/0", cov.ConfigBound, cov.Unbounded)
	}
}

// The charter floor: an UNDECLARED SLO is unbounded/listed — NEVER a learned or default
// capacity bar. This is the test the architect demanded ("fabricating a bar fails").
func TestAppSLOUndeclaredIsUnboundedNeverFabricated(t *testing.T) {
	p := pod(t, "shop", "agg-1", "uid-agg", "Deployment", "aggregation")
	// The pod exists in config but declares NO SLO annotation.
	cfg := fakeConfig{pods: map[string]PodConfig{"shop/agg-1": {SLOs: nil}}}
	res := &Result{}
	cov := &RuleCoverage{}
	bindPod(res, cov, appQueueRule(1.0), Emission{}, p, cfg, compileAt)

	b := res.Bindings[0]
	if b.Bar != nil {
		t.Fatalf("bar = %+v, want NIL (undeclared SLO must never fabricate a capacity bar)", b.Bar)
	}
	if cov.Unbounded != 1 || cov.ConfigBound != 0 || cov.DefaultBound != 0 {
		t.Errorf("coverage = unbounded %d / config %d / default %d, want 1/0/0 (no default capacity)", cov.Unbounded, cov.ConfigBound, cov.DefaultBound)
	}
	if b.Reason == "" {
		t.Errorf("an undeclared SLO must STATE its unboundedness, not be silent")
	}
}

// A pod absent from config (observe lag) is also unbounded — never guessed.
func TestAppSLONoConfigRowIsUnbounded(t *testing.T) {
	p := pod(t, "shop", "agg-1", "uid-agg", "Deployment", "aggregation")
	cfg := fakeConfig{pods: map[string]PodConfig{}} // no row for agg-1
	res := &Result{}
	cov := &RuleCoverage{}
	bindPod(res, cov, appQueueRule(1.0), Emission{}, p, cfg, compileAt)
	if res.Bindings[0].Bar != nil || cov.Unbounded != 1 {
		t.Errorf("missing config row must be unbounded, got bar=%+v unbounded=%d", res.Bindings[0].Bar, cov.Unbounded)
	}
}

// The eligibility gate (the Pod analogue of the container CPU-limit gate): a workload that
// declares NO application SLO is OUT-OF-SCOPE for an app rule — the rule cannot apply, so
// the pair LEAVES the resolvability denominator — distinct from unbounded, and with a
// stated reason. This is the fix for the 57 infrastructure pairs that dragged resolvability
// to 0.319.
func TestAppSLOEligibilityGateInfraOutOfScope(t *testing.T) {
	// An infrastructure pod (kube-system/coredns) that carries no application SLO.
	p := pod(t, "kube-system", "coredns-x", "uid-dns", "Deployment", "coredns")
	cfg := fakeConfig{pods: map[string]PodConfig{"kube-system/coredns-x": {SLOs: nil}}}
	res := &Result{}
	cov := &RuleCoverage{}
	bindPod(res, cov, appQueueRuleGated(1.0), Emission{}, p, cfg, compileAt)

	b := res.Bindings[0]
	if b.State != StateOutOfScope {
		t.Fatalf("infra pod with no SLO must be OUT-OF-SCOPE for an app rule, got %s (%+v)", b.State, b)
	}
	if b.Bar != nil {
		t.Errorf("out-of-scope binding must carry no bar, got %+v", b.Bar)
	}
	if !strings.Contains(b.Reason, "declares no application SLO") || !strings.Contains(b.Reason, "slo.*") {
		t.Errorf("out-of-scope reason must STATE the gate (regime + predicate): %q", b.Reason)
	}
	// Coverage: out-of-scope leaves the resolvability accounting entirely.
	if cov.OutOfScope != 1 || cov.Unbounded != 0 || cov.Instantiated != 0 || cov.ConfigBound != 0 {
		t.Errorf("coverage outOfScope/unbounded/instantiated/configBound = %d/%d/%d/%d, want 1/0/0/0 (gate removes the pair from resolvability)",
			cov.OutOfScope, cov.Unbounded, cov.Instantiated, cov.ConfigBound)
	}
}

// Honest partial coverage: a workload INSIDE the regime (declares some SLO) that has NOT
// declared THIS particular bar stays in scope and is listed UNBOUNDED — the gap is visible,
// never silently scoped away. The gate distinguishes "rule does not apply" (out-of-scope)
// from "rule applies but you declared no bar" (unbounded).
func TestAppSLOEligibilityInRegimeUndeclaredStaysUnbounded(t *testing.T) {
	// The pod declares a freshness SLO but NOT the queue SLO the gated queue rule reads.
	p := pod(t, "shop", "agg-1", "uid-agg", "Deployment", "aggregation")
	cfg := fakeConfig{pods: map[string]PodConfig{
		"shop/agg-1": {SLOs: map[string]float64{"slo.freshness.max_age": 30}},
	}}
	res := &Result{}
	cov := &RuleCoverage{}
	bindPod(res, cov, appQueueRuleGated(1.0), Emission{}, p, cfg, compileAt)

	b := res.Bindings[0]
	if b.State != StateBound || b.Bar != nil {
		t.Fatalf("in-regime workload missing THIS bar must be bound-but-unbounded, got %s bar=%+v", b.State, b.Bar)
	}
	if cov.Unbounded != 1 || cov.OutOfScope != 0 || cov.Instantiated != 1 {
		t.Errorf("coverage unbounded/outOfScope/instantiated = %d/%d/%d, want 1/0/1 (in regime, gap stays in the denominator)",
			cov.Unbounded, cov.OutOfScope, cov.Instantiated)
	}
	if !strings.Contains(b.Reason, "no declared SLO") {
		t.Errorf("unbounded reason should state the missing declaration, got %q", b.Reason)
	}
}

// The gate passes for a workload that declares the rule's own bar: config-bound exactly as
// before (the gate must not change the resolved bar — borrowed normativity intact).
func TestAppSLOEligibilityDeclaredStillBindsBar(t *testing.T) {
	p := pod(t, "shop", "agg-1", "uid-agg", "Deployment", "aggregation")
	cfg := fakeConfig{pods: map[string]PodConfig{
		"shop/agg-1": {SLOs: map[string]float64{"slo.queue.max_depth": 1000}},
	}}
	res := &Result{}
	cov := &RuleCoverage{}
	bindPod(res, cov, appQueueRuleGated(0.9), Emission{}, p, cfg, compileAt)

	b := res.Bindings[0]
	if b.Bar == nil || b.State != StateBound {
		t.Fatalf("declared workload must bind a bar through the gate, got %s (%s)", b.State, b.Reason)
	}
	if b.Bar.Source != SourceConfig || b.Bar.Flagged || b.Bar.Value != 900 {
		t.Errorf("bar = %+v, want config-sourced unflagged 900 (declared 1000 x 0.9), unchanged by the gate", b.Bar)
	}
	if cov.ConfigBound != 1 || cov.OutOfScope != 0 || cov.Unbounded != 0 {
		t.Errorf("coverage configBound/outOfScope/unbounded = %d/%d/%d, want 1/0/0", cov.ConfigBound, cov.OutOfScope, cov.Unbounded)
	}
}

// A gated rule on a pod whose config row is unreadable is UNRESOLVED — we cannot read its
// declarations to judge eligibility, so we state that rather than guess it in or out.
func TestAppSLOEligibilityUnreadableConfigIsUnresolved(t *testing.T) {
	p := pod(t, "shop", "agg-1", "uid-agg", "Deployment", "aggregation")
	cfg := fakeConfig{pods: map[string]PodConfig{}} // no row for agg-1
	res := &Result{}
	cov := &RuleCoverage{}
	bindPod(res, cov, appQueueRuleGated(1.0), Emission{}, p, cfg, compileAt)

	b := res.Bindings[0]
	if b.State != StateUnresolved || b.Reason == "" {
		t.Fatalf("unreadable config under a gated rule must be unresolved with a reason, got %s (%q)", b.State, b.Reason)
	}
	if cov.Unresolved != 1 || cov.OutOfScope != 0 || cov.Unbounded != 0 || cov.Instantiated != 0 {
		t.Errorf("coverage unresolved/outOfScope/unbounded/instantiated = %d/%d/%d/%d, want 1/0/0/0",
			cov.Unresolved, cov.OutOfScope, cov.Unbounded, cov.Instantiated)
	}
}

// End-to-end against the REAL app overlay (the YAML the runtime loads behind
// --app-metrics-enabled): the eligibility wiring validates at load AND gates at compile.
// An infra pod is out-of-scope for ALL THREE app rules and leaves the resolvability
// denominator; the app pod's declared bar resolves while its undeclared bars stay unbounded.
func TestAppSLOEligibilityEndToEndRealOverlay(t *testing.T) {
	g, err := graph.LoadWithOverlayPaths(kgPath, []string{"../../../ontology/graph/overlays/experimental/app-conditions-v1.yaml"})
	if err != nil {
		t.Fatalf("load app overlay: %v", err)
	}
	inv := []identity.InstanceRecord{
		pod(t, "kube-system", "coredns-x", "uid-dns", "Deployment", "coredns"), // infra, no SLO
		pod(t, "shop", "agg-1", "uid-agg", "Deployment", "aggregation"),        // app, declares queue SLO
	}
	cfg := fakeConfig{pods: map[string]PodConfig{
		"kube-system/coredns-x": {SLOs: nil},
		"shop/agg-1":            {SLOs: map[string]float64{"slo.queue.max_depth": 1000}},
	}}
	res := Compile(g, inv, cfg, nil, compileAt)

	appRules := []string{"THR_APP_QUEUE_DEPTH", "THR_APP_DATA_AGE", "THR_APP_REQUEST_RATE"}
	for _, rid := range appRules {
		dns := find(t, res, rid, "coredns-x")
		if dns.State != StateOutOfScope {
			t.Errorf("%s on coredns must be out-of-scope (no app SLO), got %s", rid, dns.State)
		}
	}
	q := find(t, res, "THR_APP_QUEUE_DEPTH", "agg-1")
	if q.Bar == nil || q.Bar.Source != SourceConfig || q.Bar.Value != 1000 {
		t.Errorf("agg-1 queue bar = %+v, want config-sourced 1000", q.Bar)
	}
	for _, rid := range []string{"THR_APP_DATA_AGE", "THR_APP_REQUEST_RATE"} {
		u := find(t, res, rid, "agg-1")
		if u.State != StateBound || u.Bar != nil {
			t.Errorf("%s on agg-1 must be in-regime unbounded (declared queue, not this bar), got %s bar=%+v", rid, u.State, u.Bar)
		}
	}
	// Resolvability denominator: only agg-1's 3 app pairs are config-eligible (1 bound + 2
	// unbounded); coredns's 3 out-of-scope pairs are excluded. Without the gate the
	// denominator would be 6 and resolvability 1/6 — the gate lifts it to 1/3.
	if res.Coverage.ConfigEligible != 3 || res.Coverage.ConfigBound != 1 {
		t.Fatalf("configEligible/configBound = %d/%d, want 3/1 (infra pairs left the denominator)",
			res.Coverage.ConfigEligible, res.Coverage.ConfigBound)
	}
	if res.Coverage.Resolvability != 1.0/3.0 {
		t.Errorf("resolvability = %v, want %v (gate removed the infra pairs)", res.Coverage.Resolvability, 1.0/3.0)
	}

	// Determinism: identical inputs -> byte-identical result (the replay floor).
	if !reflect.DeepEqual(res, Compile(g, inv, cfg, nil, compileAt)) {
		t.Error("gated app compile is not deterministic for identical inputs")
	}
}
