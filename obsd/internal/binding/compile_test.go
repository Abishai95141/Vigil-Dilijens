package binding

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

const (
	kgPath     = "../../../ontology/graph/k8s_signal_kg.json"
	overlayDir = "../../../ontology/graph/overlays"
	cluster    = "test-cluster-uid"
)

var compileAt = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

func loadGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	if len(g.Rules) != 8 {
		t.Fatalf("rules = %d, want 8", len(g.Rules))
	}
	return g
}

func pod(t *testing.T, ns, name, uid, roleKind, roleName string) identity.InstanceRecord {
	t.Helper()
	cei, err := identity.MintInstance(identity.InstanceCoords{Cluster: cluster, Namespace: ns, Kind: "Pod", Name: name, UID: uid}, compileAt)
	if err != nil {
		t.Fatal(err)
	}
	role, err := identity.MintRole(identity.RoleCoords{Cluster: cluster, Namespace: ns, Kind: roleKind, RoleKey: roleKind + "/" + roleName}, compileAt)
	if err != nil {
		t.Fatal(err)
	}
	return identity.InstanceRecord{CEI: cei, RoleCEI: role, Kind: "Pod", Namespace: ns, Name: name, UID: uid}
}

func node(t *testing.T, name, uid string) identity.InstanceRecord {
	t.Helper()
	cei, err := identity.MintInstance(identity.InstanceCoords{Cluster: cluster, Kind: "Node", Name: name, UID: uid}, compileAt)
	if err != nil {
		t.Fatal(err)
	}
	return identity.InstanceRecord{CEI: cei, Kind: "Node", Name: name, UID: uid}
}

// fakeConfig is a deterministic EntityConfig backed by maps.
type fakeConfig struct {
	pods  map[string]PodConfig // ns/name
	nodes map[string]NodeConfig
	pvcs  map[string]PVCConfig // ns/name
}

func (f fakeConfig) Pod(ns, name string) (PodConfig, bool) {
	c, ok := f.pods[ns+"/"+name]
	return c, ok
}
func (f fakeConfig) Node(name string) (NodeConfig, bool) { c, ok := f.nodes[name]; return c, ok }
func (f fakeConfig) PVC(ns, name string) (PVCConfig, bool) {
	c, ok := f.pvcs[ns+"/"+name]
	return c, ok
}
func (f fakeConfig) PVCs() []PVCRef {
	var out []PVCRef
	for k := range f.pvcs {
		parts := strings.SplitN(k, "/", 2)
		out = append(out, PVCRef{Namespace: parts[0], Name: parts[1]})
	}
	return out
}

const (
	mi256 = int64(256 << 20) // 268435456
	gi8   = int64(8 << 30)
	gi10  = int64(10 << 30)
)

// boutiqueFixture mirrors the live demo shape: two pods of one role with declared
// limits, one limitless pod (the deliberate resolvability hole), one pod whose
// config row is unreadable, a node, and a PVC.
func boutiqueFixture(t *testing.T) ([]identity.InstanceRecord, fakeConfig) {
	inv := []identity.InstanceRecord{
		pod(t, "shop", "web-a", "uid-a", "Deployment", "web"),
		pod(t, "shop", "web-b", "uid-b", "Deployment", "web"),
		pod(t, "shop", "payment-x", "uid-p", "Deployment", "payment"),
		pod(t, "shop", "ghost-x", "uid-g", "Deployment", "ghost"),
		node(t, "worker-1", "node-uid-1"),
	}
	cfg := fakeConfig{
		pods: map[string]PodConfig{
			"shop/web-a":     {Containers: []ContainerConfig{{Name: "server", MemLimitBytes: mi256, CPULimitMilli: 200}}},
			"shop/web-b":     {Containers: []ContainerConfig{{Name: "server", MemLimitBytes: mi256, CPULimitMilli: 200}}},
			"shop/payment-x": {Containers: []ContainerConfig{{Name: "server"}}}, // NO limits: the hole
			// shop/ghost-x deliberately missing: unresolved pair.
		},
		nodes: map[string]NodeConfig{"worker-1": {AllocatableMemoryBytes: gi8}},
		pvcs:  map[string]PVCConfig{"shop/data-0": {RequestedStorageBytes: gi10}},
	}
	return inv, cfg
}

func find(t *testing.T, res *Result, ruleID, ceiSubstr string) *Binding {
	t.Helper()
	for i := range res.Bindings {
		b := &res.Bindings[i]
		if b.RuleID == ruleID && strings.Contains(b.CEIKey, ceiSubstr) {
			return b
		}
	}
	t.Fatalf("no binding for rule %s entity ~%s", ruleID, ceiSubstr)
	return nil
}

// The doc 04 §3.3 worked example, exact: one authored line (limit x 0.95) becomes
// per-instance bars auto-calibrated from each entity's own config.
func TestPerInstanceConfigRelativeBars(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	res := Compile(g, inv, cfg, nil, compileAt)

	web := find(t, res, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "web-a")
	if web.Bar == nil || web.State != StateBound {
		t.Fatalf("web-a should have a bar: %+v", web)
	}
	wantBar := float64(mi256) * 0.95 // 255013683.2
	if web.Bar.Value != wantBar || web.Bar.Unit != "bytes" || web.Bar.Source != SourceConfig || web.Bar.Flagged {
		t.Errorf("web-a bar = %+v, want value %v bytes from config, unflagged", web.Bar, wantBar)
	}
	if web.Bar.ConfigPath != graph.PathContainerLimitsMemory || web.Bar.Direction != "above" {
		t.Errorf("bar provenance wrong: %+v", web.Bar)
	}

	cpu := find(t, res, "THR_CONTAINER_CPU_USAGE_VS_LIMIT", "web-a")
	if cpu.Bar == nil || cpu.Bar.Value != 180 || cpu.Bar.Unit != "millicores" {
		t.Errorf("cpu bar = %+v, want 180 millicores (200m x 0.90)", cpu.Bar)
	}

	nodeBar := find(t, res, "THR_NODE_MEMAVAILABLE_VS_ALLOCATABLE", "worker-1")
	if nodeBar.Bar == nil || nodeBar.Bar.Value != float64(gi8)*0.10 || nodeBar.Bar.Direction != "below" {
		t.Errorf("node bar = %+v, want %v below", nodeBar.Bar, float64(gi8)*0.10)
	}

	pvc := find(t, res, "THR_PVC_USED_VS_REQUESTED", "data-0")
	if pvc.Bar == nil || pvc.Bar.Value != float64(gi10)*0.85 {
		t.Errorf("pvc bar = %+v, want %v", pvc.Bar, float64(gi10)*0.85)
	}
}

// The resolvability hole (doc 04 §3.4): a limitless container is instantiated,
// gets NO bar, is listed unbounded — never silently skipped, never defaulted.
func TestResolvabilityHoleListedNeverSilent(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	res := Compile(g, inv, cfg, nil, compileAt)

	pay := find(t, res, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "payment-x")
	if pay.State != StateBound || pay.Bar != nil {
		t.Errorf("payment should be bound-but-unbounded: %+v", pay)
	}
	if !strings.Contains(pay.Reason, "unbounded: no early-warning eligibility") {
		t.Errorf("unbounded reason missing: %q", pay.Reason)
	}
	joined := strings.Join(res.Coverage.UnboundedWorkloads, "\n")
	if !strings.Contains(joined, "Deployment/payment") || !strings.Contains(joined, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT") {
		t.Errorf("unbounded list must name the payment role:\n%s", joined)
	}

	// Resolvability metric: config-eligible pairs = mem(3) + cpu(3) + node(1) + pvc(1) = 8;
	// config-bound = web x2 x2rules + node + pvc = 6.
	if res.Coverage.ConfigEligible != 8 || res.Coverage.ConfigBound != 6 {
		t.Errorf("eligible/bound = %d/%d, want 8/6", res.Coverage.ConfigEligible, res.Coverage.ConfigBound)
	}
	if res.Coverage.Resolvability != 0.75 {
		t.Errorf("resolvability = %v, want 0.75", res.Coverage.Resolvability)
	}
}

// The eligibility gate: a container with no CPU limit is OUT-OF-SCOPE for the
// throttle-ratio rule (throttling cannot occur), with a stated reason — distinct
// from unbounded.
func TestEligibilityGateOutOfScope(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	res := Compile(g, inv, cfg, nil, compileAt)

	throttled := find(t, res, "THR_CONTAINER_CPU_THROTTLE_RATIO", "payment-x")
	if throttled.State != StateOutOfScope {
		t.Errorf("limitless container should be out-of-scope for throttle rule: %+v", throttled)
	}
	if !strings.Contains(throttled.Reason, "CFS throttling cannot occur") {
		t.Errorf("out-of-scope reason missing: %q", throttled.Reason)
	}
	// And an eligible container gets the FLAGGED ontology default.
	web := find(t, res, "THR_CONTAINER_CPU_THROTTLE_RATIO", "web-a")
	if web.Bar == nil || web.Bar.Source != SourceDefault || !web.Bar.Flagged || web.Bar.Value != 0.25 {
		t.Errorf("throttle default bar = %+v, want flagged default 0.25", web.Bar)
	}
}

// Default-sourced bars are always flagged (doc 04 §3.4: a default is a visible,
// lower-trust bar) and counted in the coverage report.
func TestDefaultBarsAlwaysFlagged(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	res := Compile(g, inv, cfg, nil, compileAt)
	defaults := 0
	for _, b := range res.Bindings {
		if b.Bar != nil && b.Bar.Source == SourceDefault {
			defaults++
			if !b.Bar.Flagged {
				t.Errorf("unflagged default bar: %+v", b)
			}
		}
	}
	if defaults == 0 || res.Coverage.DefaultBars != defaults {
		t.Errorf("default bars: found %d, report says %d", defaults, res.Coverage.DefaultBars)
	}
}

// An unreadable pod config row is an UNRESOLVED pair with a stated reason — the
// pair exists in the report, hidden gaps are impossible (doc 04 §3.5).
func TestUnresolvedConfigRowStated(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	res := Compile(g, inv, cfg, nil, compileAt)
	ghost := find(t, res, "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "ghost-x")
	if ghost.State != StateUnresolved || ghost.Reason == "" {
		t.Errorf("ghost pod should be unresolved with reason: %+v", ghost)
	}
}

// Axis 3: role bindings aggregate the role's current member instances, so the
// durable role absorbs instance churn by construction.
func TestRoleLayerAggregation(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	res := Compile(g, inv, cfg, nil, compileAt)
	var webRole *RoleBinding
	for i := range res.Roles {
		r := &res.Roles[i]
		if strings.Contains(r.RoleKey, "Deployment/web") && r.RuleID == "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT" {
			webRole = r
		}
	}
	if webRole == nil || webRole.Members != 2 || webRole.Bound != 2 || webRole.Unbounded != 0 {
		t.Fatalf("web role binding = %+v, want 2 members both bound", webRole)
	}
	var payRole *RoleBinding
	for i := range res.Roles {
		r := &res.Roles[i]
		if strings.Contains(r.RoleKey, "Deployment/payment") && r.RuleID == "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT" {
			payRole = r
		}
	}
	if payRole == nil || payRole.Members != 1 || payRole.Unbounded != 1 {
		t.Fatalf("payment role binding = %+v, want 1 member unbounded", payRole)
	}
}

// Same graph + same inventory + same config => byte-identical result (replay
// guarantee at the binding layer; map-order independence).
func TestCompileDeterministic(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	a := Compile(g, inv, cfg, nil, compileAt)
	b := Compile(g, inv, cfg, nil, compileAt)
	if !reflect.DeepEqual(a, b) {
		t.Error("Compile is not deterministic for identical inputs")
	}
	// Shuffled inventory order must not change the result.
	rev := make([]identity.InstanceRecord, len(inv))
	for i, r := range inv {
		rev[len(inv)-1-i] = r
	}
	c := Compile(g, rev, cfg, nil, compileAt)
	if !reflect.DeepEqual(a, c) {
		t.Error("Compile depends on inventory order")
	}
}

// Every binding is suspect by construction until the semantic-validation suite
// (doc 04 M3) exists, and the coverage report says so.
func TestAllBindingsSuspectUntilSemanticQA(t *testing.T) {
	g := loadGraph(t)
	inv, cfg := boutiqueFixture(t)
	res := Compile(g, inv, cfg, nil, compileAt)
	for _, b := range res.Bindings {
		if b.Validation != ValidationSuspect {
			t.Errorf("binding %s/%s validation = %s, want suspect (M3 pending)", b.RuleID, b.CEIKey, b.Validation)
		}
	}
	notes := strings.Join(res.Coverage.Notes, "\n")
	if !strings.Contains(notes, "semantic QA") || !strings.Contains(notes, "hot-window") {
		t.Errorf("honesty notes incomplete:\n%s", notes)
	}
}
