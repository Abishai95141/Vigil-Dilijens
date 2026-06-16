package binding

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
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
