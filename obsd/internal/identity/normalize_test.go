package identity

import (
	"testing"
	"time"
)

// --- time-aware fake lookup ------------------------------------------------

// interval is one identity's lifetime: [from, to). Zero `to` = still alive.
type interval struct {
	uid  string
	from time.Time
	to   time.Time
}

type fakeLookup struct {
	pods  map[string][]interval // key: ns + "/" + name
	nodes map[string][]interval // key: node name
}

func lookupIn(ivs []interval, at time.Time) (string, bool) {
	for _, iv := range ivs {
		if !at.Before(iv.from) && (iv.to.IsZero() || at.Before(iv.to)) {
			return iv.uid, true
		}
	}
	return "", false
}

func (f *fakeLookup) PodUID(ns, name string, at time.Time) (string, bool) {
	return lookupIn(f.pods[ns+"/"+name], at)
}

func (f *fakeLookup) NodeUID(name string, at time.Time) (string, bool) {
	return lookupIn(f.nodes[name], at)
}

// --- fixtures ----------------------------------------------------------------

var (
	tBase = time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)

	stdLookup = &fakeLookup{
		pods: map[string][]interval{
			"shop/currencyservice-x": {{uid: "uid-pod-1", from: tBase}},
			"shop/cart-1":            {{uid: "uid-cart", from: tBase}},
		},
		nodes: map[string][]interval{
			"worker-1": {{uid: "uid-node-1", from: tBase}},
		},
	}
)

func newNorm() *Normalizer { return NewNormalizer(cluster, stdLookup) }

func at(d time.Duration) time.Time { return tBase.Add(d) }

// --- cAdvisor: the trap rows (doc 14 §3.2) -------------------------------------

func TestCAdvisorNamedContainerBindsContainerCEI(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{
			"namespace": "shop", "pod": "currencyservice-x", "container": "server",
			"id": "/kubepods/pod-uid-pod-1/abc", "image": "img:v1",
		},
		SourceNode: "worker-1",
		At:         at(time.Minute),
	})
	if r.Outcome != OutcomeResolved {
		t.Fatalf("outcome = %s (reason %q), want resolved", r.Outcome, r.Reason)
	}
	if r.CEI.Kind != "Container" || r.CEI.UID != "uid-pod-1/server" {
		t.Errorf("container CEI = kind %q uid %q, want Container / uid-pod-1/server", r.CEI.Kind, r.CEI.UID)
	}
}

// container="" rows are pod-level cgroup AGGREGATES → pod CEI, never container.
func TestCAdvisorEmptyContainerIsPodAggregate(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"namespace": "shop", "pod": "currencyservice-x", "container": "", "image": ""},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeResolved {
		t.Fatalf("outcome = %s (reason %q), want resolved", r.Outcome, r.Reason)
	}
	if r.CEI.Kind != "Pod" || r.CEI.UID != "uid-pod-1" {
		t.Errorf("aggregate row bound to kind %q uid %q, want the Pod CEI uid-pod-1", r.CEI.Kind, r.CEI.UID)
	}
}

// container="POD" is the pause sandbox → deliberate drop, NEVER a container CEI.
func TestCAdvisorPauseSandboxDroppedDeliberately(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_cpu_usage_seconds_total",
		Labels: map[string]string{"namespace": "shop", "pod": "currencyservice-x", "container": "POD"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeDropped || r.Reason != ReasonPauseSandbox {
		t.Errorf("sandbox row: outcome=%s reason=%q, want dropped/%s", r.Outcome, r.Reason, ReasonPauseSandbox)
	}
	if r.CEI.Kind != "" {
		t.Errorf("sandbox row must carry no CEI, got %q", r.CEI.Key())
	}
}

// System-cgroup rows (no namespace/pod) are recognized and deliberately dropped.
func TestCAdvisorSystemCgroupDropped(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"id": "/system.slice/containerd.service"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeDropped || r.Reason != ReasonNonPodCgroup {
		t.Errorf("system cgroup: outcome=%s reason=%q, want dropped/%s", r.Outcome, r.Reason, ReasonNonPodCgroup)
	}
}

// An unknown pod quarantines — never guessed into an identity.
func TestCAdvisorUnknownPodQuarantines(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"namespace": "shop", "pod": "ghost", "container": "server"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonUnknownPod {
		t.Errorf("unknown pod: outcome=%s reason=%q, want quarantined/%s", r.Outcome, r.Reason, ReasonUnknownPod)
	}
}

// machine_* rows describe the node itself.
func TestCAdvisorMachineMetricsBindNode(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family:     FamilyCAdvisor,
		Metric:     "machine_cpu_cores",
		Labels:     map[string]string{},
		SourceNode: "worker-1",
		At:         at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.Kind != "Node" || r.CEI.UID != "uid-node-1" {
		t.Errorf("machine_*: outcome=%s kind=%q uid=%q, want resolved Node uid-node-1", r.Outcome, r.CEI.Kind, r.CEI.UID)
	}
}

// Partial identity labels (ns without pod) quarantine as missing labels.
func TestCAdvisorPartialLabelsQuarantine(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"namespace": "shop"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonMissingLabels {
		t.Errorf("partial labels: outcome=%s reason=%q, want quarantined/%s", r.Outcome, r.Reason, ReasonMissingLabels)
	}
}

// Legacy kubelet label dialect (pod_name/container_name) normalizes identically.
func TestCAdvisorLegacyLabelAliases(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"namespace": "shop", "pod_name": "currencyservice-x", "container_name": "server"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.UID != "uid-pod-1/server" {
		t.Errorf("legacy labels: outcome=%s uid=%q, want resolved uid-pod-1/server", r.Outcome, r.CEI.UID)
	}
}

// THE MANDATORY FIXTURE (doc 14 §3.2): a pod deleted and recreated with the SAME
// name. cAdvisor carries no UID, so the join must be time-aware: a late sample
// stamped during the old pod's lifetime joins the OLD pod's CEI; a fresh sample
// joins the NEW pod's. The two CEIs are distinct; nothing mis-joins.
func TestCAdvisorSameNameRecreateRace(t *testing.T) {
	died := tBase.Add(10 * time.Minute)
	reborn := tBase.Add(10*time.Minute + 5*time.Second)
	lk := &fakeLookup{
		pods: map[string][]interval{
			"shop/web-x": {
				{uid: "uid-OLD", from: tBase, to: died},
				{uid: "uid-NEW", from: reborn},
			},
		},
	}
	n := NewNormalizer(cluster, lk)
	series := func(ts time.Time) Series {
		return Series{
			Family: FamilyCAdvisor,
			Metric: "container_memory_working_set_bytes",
			Labels: map[string]string{"namespace": "shop", "pod": "web-x", "container": "server"},
			At:     ts,
		}
	}

	onTime := n.Normalize(series(died.Add(-30 * time.Second))) // received while OLD lived
	fresh := n.Normalize(series(reborn.Add(time.Minute)))      // received after NEW born

	if onTime.Outcome != OutcomeResolved || onTime.CEI.UID != "uid-OLD/server" {
		t.Errorf("on-time sample: outcome=%s uid=%q, want resolved uid-OLD/server", onTime.Outcome, onTime.CEI.UID)
	}
	if fresh.Outcome != OutcomeResolved || fresh.CEI.UID != "uid-NEW/server" {
		t.Errorf("fresh sample: outcome=%s uid=%q, want resolved uid-NEW/server", fresh.Outcome, fresh.CEI.UID)
	}
	if onTime.CEI.Same(fresh.CEI) {
		t.Fatalf("MIS-JOIN: same-name successor shares a CEI with the dead pod: %q", onTime.CEI.Key())
	}

	// THE GENUINELY LATE SAMPLE: collected (EventTime) while uid-OLD lived, but
	// RECEIVED (At) after uid-NEW's birth. The join must key off the sample's own
	// time and land on the dead pod — joining the successor would be the silent
	// mis-join doc 14 §1.4's tombstones exist to prevent.
	lateSeries := series(reborn.Add(time.Minute)) // received post-rebirth...
	lateSeries.EventTime = died.Add(-10 * time.Second)
	late := n.Normalize(lateSeries)
	if late.Outcome != OutcomeResolved || late.CEI.UID != "uid-OLD/server" {
		t.Errorf("genuinely late sample: outcome=%s uid=%q, want resolved uid-OLD/server (MIS-JOIN to successor if uid-NEW)",
			late.Outcome, late.CEI.UID)
	}

	// In the gap between death and rebirth, (ns,name) maps to nothing → quarantine.
	gap := n.Normalize(series(died.Add(2 * time.Second)))
	if gap.Outcome != OutcomeQuarantined || gap.Reason != ReasonUnknownPod {
		t.Errorf("gap sample: outcome=%s reason=%q, want quarantined/%s", gap.Outcome, gap.Reason, ReasonUnknownPod)
	}
}

// --- kube-state-metrics ---------------------------------------------------------

// kube_pod_info carries the uid label → direct minting, no lookup needed.
func TestKSMPodInfoUsesUIDLabel(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyKSM,
		Metric: "kube_pod_info",
		Labels: map[string]string{"namespace": "shop", "pod": "currencyservice-x", "uid": "uid-pod-1", "node": "worker-1"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.Kind != "Pod" || r.CEI.UID != "uid-pod-1" {
		t.Errorf("kube_pod_info: outcome=%s kind=%q uid=%q", r.Outcome, r.CEI.Kind, r.CEI.UID)
	}
}

// The threshold-resolution source (kube_pod_container_resource_limits) joins to
// the right CONTAINER CEI — this is what borrowed normativity (04) hangs off.
func TestKSMContainerLimitsBindContainer(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyKSM,
		Metric: "kube_pod_container_resource_limits",
		Labels: map[string]string{
			"namespace": "shop", "pod": "currencyservice-x", "uid": "uid-pod-1",
			"container": "server", "resource": "memory", "unit": "byte",
		},
		At: at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.Kind != "Container" || r.CEI.UID != "uid-pod-1/server" {
		t.Errorf("limits row: outcome=%s kind=%q uid=%q, want Container uid-pod-1/server", r.Outcome, r.CEI.Kind, r.CEI.UID)
	}
}

// A container-CLASS row (kube_pod_container_* / kube_pod_init_container_*) that
// lost its container label is malformed: it must QUARANTINE, never silently
// re-scope to the pod CEI — identity scope is decided by metric class, not label
// presence (doc 03 §3.3: never guessed). This matters most for the threshold-
// resolution source, kube_pod_container_resource_limits.
func TestKSMContainerClassWithoutContainerLabelQuarantines(t *testing.T) {
	for _, metric := range []string{
		"kube_pod_container_resource_limits",
		"kube_pod_container_status_restarts_total",
		"kube_pod_init_container_status_ready",
	} {
		r := newNorm().Normalize(Series{
			Family: FamilyKSM,
			Metric: metric,
			Labels: map[string]string{"namespace": "shop", "pod": "currencyservice-x", "uid": "uid-pod-1", "resource": "memory"},
			At:     at(time.Minute),
		})
		if r.Outcome != OutcomeQuarantined || r.Reason != ReasonMissingLabels {
			t.Errorf("%s without container label: outcome=%s reason=%q kind=%q, want quarantined/%s",
				metric, r.Outcome, r.Reason, r.CEI.Kind, ReasonMissingLabels)
		}
	}
}

// Rows without a uid label fall back to the time-aware mapping.
func TestKSMPodWithoutUIDFallsBackToLookup(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyKSM,
		Metric: "kube_pod_status_phase",
		Labels: map[string]string{"namespace": "shop", "pod": "cart-1", "phase": "Running"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.UID != "uid-cart" {
		t.Errorf("no-uid row: outcome=%s uid=%q, want resolved uid-cart", r.Outcome, r.CEI.UID)
	}
}

// THE KSM TRAP (doc 14 §3.2): state metrics persist briefly for terminated pods.
// A uid-carrying row for a dead pod still resolves — to the DEAD pod's own CEI —
// even when the live mapping no longer knows the name. Lifecycle gates binding;
// normalization never re-points history at a successor.
func TestKSMTerminatedPodRowStillResolvesToItsOwnCEI(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyKSM,
		Metric: "kube_pod_status_phase",
		Labels: map[string]string{"namespace": "shop", "pod": "departed", "uid": "uid-DEAD", "phase": "Succeeded"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.UID != "uid-DEAD" {
		t.Errorf("terminated-pod row: outcome=%s uid=%q, want resolved uid-DEAD", r.Outcome, r.CEI.UID)
	}
}

func TestKSMNodeBindsNode(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyKSM,
		Metric: "kube_node_status_condition",
		Labels: map[string]string{"node": "worker-1", "condition": "Ready", "status": "true"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.Kind != "Node" || r.CEI.UID != "uid-node-1" {
		t.Errorf("kube_node_*: outcome=%s kind=%q uid=%q", r.Outcome, r.CEI.Kind, r.CEI.UID)
	}
}

// Workload metrics bind ROLE CEIs whose keys match DeriveRole's anchors exactly.
func TestKSMDeploymentBindsRoleMatchingDeriveRole(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyKSM,
		Metric: "kube_deployment_status_replicas_available",
		Labels: map[string]string{"namespace": "shop", "deployment": "currencyservice"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.Layer != LayerRole {
		t.Fatalf("deployment row: outcome=%s layer=%s, want resolved role", r.Outcome, r.CEI.Layer)
	}
	// The exact-join check: the workload metric's role CEI equals the role CEI
	// derived from a member pod's ownership chain.
	chain := []OwnerRef{
		{Kind: "ReplicaSet", Name: "currencyservice-abc", UID: "rs", Controller: true},
		{Kind: "Deployment", Name: "currencyservice", UID: "d", Controller: true},
	}
	fromPods, _ := MintRole(DeriveRole(cluster, "shop", "currencyservice-1", chain), at(time.Minute))
	if !r.CEI.Same(fromPods) {
		t.Errorf("workload metric role %q != pod-derived role %q", r.CEI.Key(), fromPods.Key())
	}
}

// Unmapped KSM classes (e.g. ReplicaSet — needs the owner chain) quarantine with a
// stated reason rather than guessing an anchor.
func TestKSMUnmappedClassQuarantines(t *testing.T) {
	for _, metric := range []string{"kube_replicaset_status_replicas", "kube_persistentvolumeclaim_info", "kube_service_info"} {
		r := newNorm().Normalize(Series{
			Family: FamilyKSM, Metric: metric,
			Labels: map[string]string{"namespace": "shop"},
			At:     at(time.Minute),
		})
		if r.Outcome != OutcomeQuarantined || r.Reason != ReasonUnmappedMetric {
			t.Errorf("%s: outcome=%s reason=%q, want quarantined/%s", metric, r.Outcome, r.Reason, ReasonUnmappedMetric)
		}
	}
}

// --- node-exporter ---------------------------------------------------------------

func TestNodeExporterBindsViaScrapeTarget(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family:     FamilyNodeExporter,
		Metric:     "node_memory_MemAvailable_bytes",
		Labels:     map[string]string{"instance": "10.0.0.5:9100"}, // identity ignores this
		SourceNode: "worker-1",
		At:         at(time.Minute),
	})
	if r.Outcome != OutcomeResolved || r.CEI.Kind != "Node" || r.CEI.UID != "uid-node-1" {
		t.Errorf("node-exporter: outcome=%s kind=%q uid=%q", r.Outcome, r.CEI.Kind, r.CEI.UID)
	}
}

func TestNodeExporterUnknownNodeQuarantines(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family:     FamilyNodeExporter,
		Metric:     "node_cpu_seconds_total",
		SourceNode: "gone-node",
		At:         at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonUnknownNode {
		t.Errorf("unknown node: outcome=%s reason=%q, want quarantined/%s", r.Outcome, r.Reason, ReasonUnknownNode)
	}
}

func TestNodeExporterMissingSourceQuarantines(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyNodeExporter,
		Metric: "node_cpu_seconds_total",
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonMissingSource {
		t.Errorf("missing source: outcome=%s reason=%q, want quarantined/%s", r.Outcome, r.Reason, ReasonMissingSource)
	}
}

// --- cross-cutting ---------------------------------------------------------------

func TestUnknownFamilyQuarantines(t *testing.T) {
	r := newNorm().Normalize(Series{Family: "mystery-exporter", Metric: "x", At: at(0)})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonUnknownFamily {
		t.Errorf("unknown family: outcome=%s reason=%q", r.Outcome, r.Reason)
	}
}

// Label values that would corrupt the CEI key (separator injection) quarantine as
// malformed rather than minting an ambiguous identity. Tested on the paths where
// the tainted value actually enters the key: the pod name on the pod-aggregate
// path, and the container name on the container path. (On the container path the
// pod NAME is not a coordinate — only the pod UID anchors it — so a tainted pod
// name there is harmless by construction.)
func TestMalformedCoordinatesQuarantine(t *testing.T) {
	lk := &fakeLookup{pods: map[string][]interval{
		"shop/bad|pod": {{uid: "u1", from: tBase}},
		"shop/goodpod": {{uid: "u2", from: tBase}},
	}}
	n := NewNormalizer(cluster, lk)

	// Pod-aggregate row: pod name enters the key → must quarantine.
	r := n.Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"namespace": "shop", "pod": "bad|pod", "container": ""},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonMalformed {
		t.Errorf("separator in pod name (aggregate row): outcome=%s reason=%q, want quarantined/%s", r.Outcome, r.Reason, ReasonMalformed)
	}

	// Container row: container name enters the key → must quarantine.
	r = n.Normalize(Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"namespace": "shop", "pod": "goodpod", "container": "bad|name"},
		At:     at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonMalformed {
		t.Errorf("separator in container name: outcome=%s reason=%q, want quarantined/%s", r.Outcome, r.Reason, ReasonMalformed)
	}
}

// Determinism: the same series normalizes identically every time.
func TestNormalizeDeterministic(t *testing.T) {
	s := Series{
		Family: FamilyCAdvisor,
		Metric: "container_memory_working_set_bytes",
		Labels: map[string]string{"namespace": "shop", "pod": "currencyservice-x", "container": "server"},
		At:     at(time.Minute),
	}
	n := newNorm()
	a, b := n.Normalize(s), n.Normalize(s)
	if a != b {
		t.Errorf("normalization not deterministic:\n a=%+v\n b=%+v", a, b)
	}
}

// Audit records carry the stream key, outcome, CEI-or-reason, and ingest time
// (doc 03 §3.6), and the stream key is deterministic under map iteration order.
func TestAuditRecords(t *testing.T) {
	n := newNorm()
	ts := at(time.Minute)

	src := Series{
		Family: FamilyCAdvisor, Metric: "container_memory_working_set_bytes",
		Labels:     map[string]string{"namespace": "shop", "pod": "currencyservice-x", "container": "server"},
		SourceNode: "worker-1",
		At:         ts,
	}
	ok := n.Normalize(src)
	rec := ok.Audit(src)
	if rec.CEIKey == "" || rec.Outcome != OutcomeResolved || !rec.At.Equal(ts) {
		t.Errorf("resolved audit record incomplete: %+v", rec)
	}
	want := `cadvisor://worker-1/container_memory_working_set_bytes{container="server",namespace="shop",pod="currencyservice-x"}`
	if rec.StreamKey != want {
		t.Errorf("stream key = %q, want %q", rec.StreamKey, want)
	}

	q := n.Normalize(Series{Family: FamilyKSM, Metric: "kube_service_info", Labels: map[string]string{}, At: ts})
	qrec := q.Audit(Series{Family: FamilyKSM, Metric: "kube_service_info", Labels: map[string]string{}, At: ts})
	if qrec.CEIKey != "" || qrec.Reason != ReasonUnmappedMetric {
		t.Errorf("quarantine audit record incomplete: %+v", qrec)
	}
}

// The stream fingerprint must distinguish streams whose identity comes from
// scrape-target metadata, not labels: machine_cpu_cores from two different nodes
// is two streams, never one (doc 03 §3.6 — the audit must identify the stream).
func TestStreamKeyDistinguishesSourceNodes(t *testing.T) {
	a := StreamKey(Series{Family: FamilyCAdvisor, Metric: "machine_cpu_cores", SourceNode: "worker-1"})
	b := StreamKey(Series{Family: FamilyCAdvisor, Metric: "machine_cpu_cores", SourceNode: "worker-2"})
	if a == b {
		t.Errorf("streams from different nodes collapsed to one fingerprint: %q", a)
	}
	// Same series twice → same key (determinism under map iteration order).
	s := Series{Family: FamilyKSM, Metric: "kube_pod_info",
		Labels: map[string]string{"namespace": "shop", "pod": "x", "uid": "u"}}
	if StreamKey(s) != StreamKey(s) {
		t.Error("StreamKey not deterministic")
	}
}

// Map versions exist for every shipped family (regression hook, doc 03 §6).
func TestMapVersionsPresent(t *testing.T) {
	for _, f := range []Family{FamilyCAdvisor, FamilyKSM, FamilyNodeExporter} {
		if MapVersion(f) == "" {
			t.Errorf("family %s has no map version", f)
		}
	}
}
