package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// These tests drive the REAL binding compiler (binding.Compile over the real
// ontology) so the silence ledger reconciles against the compiler's own per-rule
// coverage counts — not a re-statement of its own logic. Network-free (file reads
// only); run with -race.

const (
	silKGPath     = "../../../ontology/graph/k8s_signal_kg.json"
	silOverlayDir = "../../../ontology/graph/overlays"
	silCluster    = "silence-test-cluster"
	silMi256      = int64(256 << 20)
	silGi8        = int64(8 << 30)
	silGi10       = int64(10 << 30)
)

var silAt = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// silFakeConfig is a deterministic binding.EntityConfig.
type silFakeConfig struct {
	pods  map[string]binding.PodConfig
	nodes map[string]binding.NodeConfig
	pvcs  map[string]binding.PVCConfig
}

func (f silFakeConfig) Pod(ns, name string) (binding.PodConfig, bool) {
	c, ok := f.pods[ns+"/"+name]
	return c, ok
}
func (f silFakeConfig) Node(name string) (binding.NodeConfig, bool) {
	c, ok := f.nodes[name]
	return c, ok
}
func (f silFakeConfig) PVC(ns, name string) (binding.PVCConfig, bool) {
	c, ok := f.pvcs[ns+"/"+name]
	return c, ok
}
func (f silFakeConfig) PVCs() []binding.PVCRef {
	return []binding.PVCRef{{Namespace: "shop", Name: "data-0"}}
}

func silPod(t *testing.T, ns, name, uid, roleName string) identity.InstanceRecord {
	t.Helper()
	cei, err := identity.MintInstance(identity.InstanceCoords{Cluster: silCluster, Namespace: ns, Kind: "Pod", Name: name, UID: uid}, silAt)
	if err != nil {
		t.Fatal(err)
	}
	role, err := identity.MintRole(identity.RoleCoords{Cluster: silCluster, Namespace: ns, Kind: "Deployment", RoleKey: "Deployment/" + roleName}, silAt)
	if err != nil {
		t.Fatal(err)
	}
	return identity.InstanceRecord{CEI: cei, RoleCEI: role, Kind: "Pod", Namespace: ns, Name: name, UID: uid}
}

func silNode(t *testing.T, name, uid string) identity.InstanceRecord {
	t.Helper()
	cei, err := identity.MintInstance(identity.InstanceCoords{Cluster: silCluster, Kind: "Node", Name: name, UID: uid}, silAt)
	if err != nil {
		t.Fatal(err)
	}
	return identity.InstanceRecord{CEI: cei, Kind: "Node", Name: name, UID: uid}
}

// silCompile compiles a fixture exercising every silence class: two fully-declared
// pods (watched), one pod with no limits (unbounded), one pod missing config
// (unresolved), a pod whose CPU limit is absent (eligibility out-of-scope), a node
// (watched), and a declared PVC (bar resolves but no stream key — the dark-bar).
func silCompile(t *testing.T) *binding.Result {
	t.Helper()
	g, err := graph.LoadWithOverlays(silKGPath, silOverlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	inv := []identity.InstanceRecord{
		silPod(t, "shop", "web-a", "uid-a", "web"),
		silPod(t, "shop", "web-b", "uid-b", "web"),
		silPod(t, "shop", "payment-x", "uid-p", "payment"),
		silPod(t, "shop", "ghost-x", "uid-g", "ghost"),
		silNode(t, "worker-1", "node-uid-1"),
	}
	cfg := silFakeConfig{
		pods: map[string]binding.PodConfig{
			"shop/web-a":     {Containers: []binding.ContainerConfig{{Name: "server", MemLimitBytes: silMi256, CPULimitMilli: 200}}},
			"shop/web-b":     {Containers: []binding.ContainerConfig{{Name: "server", MemLimitBytes: silMi256, CPULimitMilli: 200}}},
			"shop/payment-x": {Containers: []binding.ContainerConfig{{Name: "server"}}}, // no limits
			// shop/ghost-x deliberately absent => unresolved
		},
		nodes: map[string]binding.NodeConfig{"worker-1": {AllocatableMemoryBytes: silGi8}},
		pvcs:  map[string]binding.PVCConfig{"shop/data-0": {RequestedStorageBytes: silGi10}},
	}
	return binding.Compile(g, inv, cfg, nil, silAt)
}

// TestSilenceLedgerReconcilesWithCoverage is the anti-shallow core: the ledger's
// silence partition must reconcile EXACTLY with the per-rule coverage counts the
// compiler computed independently. A ledger that dropped or invented pairs fails.
func TestSilenceLedgerReconcilesWithCoverage(t *testing.T) {
	res := silCompile(t)
	v := BuildSilenceLedger("gv-test", "v-test", silAt, res)

	if !v.Available {
		t.Fatal("expected Available=true for a compiled result")
	}
	if v.Class != "MEASURED" {
		t.Fatalf("class = %q, want MEASURED", v.Class)
	}

	// 1. Every binding is accounted: total == len(bindings) == watched + silent.
	if v.Summary.TotalPairs != len(res.Bindings) {
		t.Fatalf("TotalPairs = %d, want %d (len bindings)", v.Summary.TotalPairs, len(res.Bindings))
	}
	if v.Summary.Watched+v.Summary.Silent != v.Summary.TotalPairs {
		t.Fatalf("watched(%d)+silent(%d) != total(%d)", v.Summary.Watched, v.Summary.Silent, v.Summary.TotalPairs)
	}
	if len(v.Silent) != v.Summary.Silent {
		t.Fatalf("len(Silent)=%d != Summary.Silent=%d", len(v.Silent), v.Summary.Silent)
	}

	// 2. Reconcile the three silence classes the per-rule coverage also counts.
	var unbounded, outOfScope, unresolved, configBound, defaultBound, instantiated int
	for _, rc := range res.Coverage.PerRule {
		unbounded += rc.Unbounded
		outOfScope += rc.OutOfScope
		unresolved += rc.Unresolved
		configBound += rc.ConfigBound
		defaultBound += rc.DefaultBound
		instantiated += rc.Instantiated
	}
	if got := v.Summary.ByReason[SilenceUnbounded]; got != unbounded {
		t.Errorf("ByReason[unbounded] = %d, want %d (sum PerRule.Unbounded)", got, unbounded)
	}
	if got := v.Summary.ByReason[SilenceOutOfScope]; got != outOfScope {
		t.Errorf("ByReason[out-of-scope] = %d, want %d (sum PerRule.OutOfScope)", got, outOfScope)
	}
	if got := v.Summary.ByReason[SilenceUnresolved]; got != unresolved {
		t.Errorf("ByReason[unresolved] = %d, want %d (sum PerRule.Unresolved)", got, unresolved)
	}
	// 3. Watched == bound-with-bar minus the no-stream-key (dark-bar) pairs.
	noStream := v.Summary.ByReason[SilenceNoStreamKey]
	if want := configBound + defaultBound - noStream; v.Summary.Watched != want {
		t.Errorf("Watched = %d, want %d (configBound %d + defaultBound %d - noStreamKey %d)",
			v.Summary.Watched, want, configBound, defaultBound, noStream)
	}
	// 4. Cross-check the total against the compiler's instantiated+out+unresolved.
	if want := instantiated + outOfScope + unresolved; v.Summary.TotalPairs != want {
		t.Errorf("TotalPairs = %d, want %d (instantiated+outOfScope+unresolved)", v.Summary.TotalPairs, want)
	}

	// 5. Every silent row carries a non-empty reason + a known class.
	known := map[string]bool{SilenceUnbounded: true, SilenceNoStreamKey: true, SilenceUnresolved: true, SilenceOutOfScope: true}
	for _, row := range v.Silent {
		if row.Reason == "" {
			t.Errorf("silent row %s/%s has empty reason", row.EntityCEI, row.RuleID)
		}
		if !known[row.ReasonClass] {
			t.Errorf("silent row %s/%s has unknown reasonClass %q", row.EntityCEI, row.RuleID, row.ReasonClass)
		}
	}
}

// TestSilenceLedgerSurfacesPVCDarkBar proves the declared PVC (bar resolves) is
// surfaced as a no-stream-key silence — the honest dark-bar the harness leads with.
func TestSilenceLedgerSurfacesPVCDarkBar(t *testing.T) {
	res := silCompile(t)
	v := BuildSilenceLedger("gv", "v", silAt, res)
	found := false
	for _, row := range v.Silent {
		if row.Entity == "PVC" && row.ReasonClass == SilenceNoStreamKey {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a PVC no-stream-key silence (the dark-bar) in the ledger")
	}
	if v.Summary.ByReason[SilenceNoStreamKey] < 1 {
		t.Fatal("expected at least one no-stream-key silence")
	}
}

// TestSilenceLedgerCompletenessNoPairDropped recomputes the partition independently
// per binding and asserts the ledger neither drops a silent pair nor mislabels a
// watched one. This is the "every pair accounted" floor the gate also enforces.
func TestSilenceLedgerCompletenessNoPairDropped(t *testing.T) {
	res := silCompile(t)
	v := BuildSilenceLedger("gv", "v", silAt, res)

	// Independent reference partition.
	type key struct{ cei, rule, container, metric string }
	wantSilent := map[key]string{}
	watched := 0
	for i := range res.Bindings {
		b := &res.Bindings[i]
		isWatched := b.State == binding.StateBound && b.Bar != nil && b.StreamUID() != ""
		if isWatched {
			watched++
			continue
		}
		wantSilent[key{b.CEIKey, b.RuleID, b.Container, b.Metric}] = string(b.State)
	}
	if v.Summary.Watched != watched {
		t.Fatalf("watched = %d, reference = %d", v.Summary.Watched, watched)
	}
	gotSilent := map[key]bool{}
	for _, row := range v.Silent {
		gotSilent[key{row.EntityCEI, row.RuleID, row.Container, row.Metric}] = true
	}
	for k := range wantSilent {
		if !gotSilent[k] {
			t.Errorf("silent pair dropped from ledger: %+v", k)
		}
	}
	if len(gotSilent) != len(wantSilent) {
		t.Errorf("ledger has %d silent pairs, reference %d (no inventing pairs either)", len(gotSilent), len(wantSilent))
	}
}

// TestSilenceLedgerDeterministic asserts the same Result yields a byte-identical
// ledger JSON across two independent builds (the determinism floor).
func TestSilenceLedgerDeterministic(t *testing.T) {
	res := silCompile(t)
	a, err := json.Marshal(BuildSilenceLedger("gv", "v", silAt, res))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(BuildSilenceLedger("gv", "v", silAt, res))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("silence ledger JSON is not deterministic across two builds")
	}
}

// TestSilenceLedgerNilResultHonest asserts the honest unavailable state.
func TestSilenceLedgerNilResultHonest(t *testing.T) {
	v := BuildSilenceLedger("", "", silAt, nil)
	if v.Available {
		t.Fatal("nil result must be Available=false")
	}
	if v.Summary.TotalPairs != 0 || len(v.Silent) != 0 {
		t.Fatal("nil result must have no pairs")
	}
	if v.Note == "" {
		t.Fatal("nil result must state why it is unavailable")
	}
}

// TestSilenceLedgerCharterClean asserts the ledger payload carries no banned
// causal/forecast register — it is MEASURED about coverage, nothing more.
func TestSilenceLedgerCharterClean(t *testing.T) {
	res := silCompile(t)
	payload, err := json.Marshal(BuildSilenceLedger("gv", "v", silAt, res))
	if err != nil {
		t.Fatal(err)
	}
	if vs := CharterViolations("silence-ledger", payload); len(vs) > 0 {
		t.Fatalf("silence ledger carried a banned register: %v", vs)
	}
}
