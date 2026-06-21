package candidate

import (
	"strings"
	"testing"
)

var inv = []EntityRef{
	{Key: "i|c1|shop|Pod|web-a|pod-x", Kind: "Pod", Namespace: "shop", Name: "web-a", UID: "pod-x"},
	{Key: "i|c1|shop|Pod|web-b|pod-y", Kind: "Pod", Namespace: "shop", Name: "web-b", UID: "pod-y"},
	{Key: "i|c1||Node|worker-1|node-u1", Kind: "Node", Name: "worker-1", UID: "node-u1"},
}

func kinds(cs []Candidate) (nodes, edges int) {
	for _, c := range cs {
		switch c.Kind {
		case KindNode:
			nodes++
		case KindEdge:
			edges++
		}
	}
	return
}

// Two shared coordinates (namespace + name) ⇒ one associated-with edge to that pod.
func TestResolveTwoCoordinateMatch(t *testing.T) {
	stray := StrayObservation{
		Family: "node-exporter", Metric: "weird_widget_total",
		Labels: map[string]string{"namespace": "shop", "pod": "web-a", "endpoint": "/x"},
		Node:   "worker-1", Reason: "unknown-exporter-family", StreamRef: "weird@worker-1",
	}
	got := Resolve(stray, inv)
	nodes, edges := kinds(got)
	if nodes != 1 || edges != 1 {
		t.Fatalf("nodes=%d edges=%d, want 1/1 (%+v)", nodes, edges, got)
	}
	edge := got[1]
	if edge.Relation != "associated-with" {
		t.Errorf("edge relation = %q, want associated-with (never causal)", edge.Relation)
	}
	if edge.Payload["entityKey"] != "i|c1|shop|Pod|web-a|pod-x" {
		t.Errorf("edge points at %v, want the web-a pod", edge.Payload["entityKey"])
	}
	if len(edge.Evidence) != 2 {
		t.Errorf("edge evidence = %+v, want 2 coordinate matches (namespace, name)", edge.Evidence)
	}
	// NO score anywhere — evidence is a discrete set, payload carries a COUNT only.
	if edge.Payload["sharedCoordinates"] != 2 {
		t.Errorf("sharedCoordinates = %v, want 2", edge.Payload["sharedCoordinates"])
	}
}

// Degenerate self-match: an entity whose name == its namespace (e.g. a PVC named
// "erpnext" in namespace "erpnext") agrees with a SINGLE stray value "erpnext" on TWO
// dimensions (namespace + name). That is one identifying fact, not two — it must NOT
// clear the ≥2-coordinate floor and mint a spurious association. Regression for the
// namespace-value/name coordinate collision the proposal audit flagged.
func TestResolveDegenerateSameValueNoEdge(t *testing.T) {
	degen := []EntityRef{
		{Key: "i|c1|erpnext|PersistentVolumeClaim|erpnext|pvc-7c", Kind: "PersistentVolumeClaim", Namespace: "erpnext", Name: "erpnext", UID: "pvc-7c"},
	}
	stray := StrayObservation{
		Family: "ksm", Metric: "kube_persistentvolume_claim_ref",
		Labels: map[string]string{"claim_namespace": "erpnext", "name": "erpnext"},
		Reason: "unmapped-metric-class", StreamRef: "ref@ksm",
	}
	got := Resolve(stray, degen)
	nodes, edges := kinds(got)
	if nodes != 1 || edges != 0 {
		t.Fatalf("nodes=%d edges=%d, want 1/0 — one coincidental value must not satisfy the floor (%+v)", nodes, edges, got)
	}
	// A TRUE two-value match against the same entity still binds (sanity: the fix doesn't
	// over-suppress). Distinct values "data-mariadb-0" (name) + "erpnext" (namespace).
	twoVal := []EntityRef{
		{Key: "i|c1|erpnext|PersistentVolumeClaim|data-mariadb-0|pvc-f6", Kind: "PersistentVolumeClaim", Namespace: "erpnext", Name: "data-mariadb-0", UID: "pvc-f6"},
	}
	stray2 := StrayObservation{
		Family: "ksm", Metric: "kube_persistentvolume_claim_ref",
		Labels: map[string]string{"claim_namespace": "erpnext", "name": "data-mariadb-0"},
		Reason: "unmapped-metric-class", StreamRef: "ref2@ksm",
	}
	got2 := Resolve(stray2, twoVal)
	if _, e2 := kinds(got2); e2 != 1 {
		t.Fatalf("two DISTINCT shared values should still bind: edges=%d, want 1 (%+v)", e2, got2)
	}
	if got2[1].Payload["sharedCoordinates"] != 2 {
		t.Errorf("sharedCoordinates = %v, want 2 distinct values", got2[1].Payload["sharedCoordinates"])
	}
}

// A single shared coordinate (namespace only) is co-occurrence, not identity:
// no edge, the stray surfaces alone with the ambiguity stated.
func TestResolveSingleCoordinateNoEdge(t *testing.T) {
	stray := StrayObservation{
		Family: "app", Metric: "q_depth",
		Labels: map[string]string{"namespace": "shop"},
		Reason: "unmapped-metric-class", StreamRef: "q@n",
	}
	got := Resolve(stray, inv)
	nodes, edges := kinds(got)
	if nodes != 1 || edges != 0 {
		t.Fatalf("nodes=%d edges=%d, want 1/0 (identification floor)", nodes, edges)
	}
	if !strings.Contains(got[0].Evidence[0].Detail, "no identifying entity overlap") {
		t.Errorf("provisional node should state the ambiguity, got %q", got[0].Evidence[0].Detail)
	}
}

// A name+namespace match against TWO same-named entities of different kinds is a
// genuine tie — both surface as edges, sorted by key, no arbitrary pick.
func TestResolveTieSurfacesBoth(t *testing.T) {
	tieInv := []EntityRef{
		{Key: "i|c1|shop|PersistentVolumeClaim|shared|pvc-1", Kind: "PersistentVolumeClaim", Namespace: "shop", Name: "shared", UID: "pvc-1"},
		{Key: "i|c1|shop|Pod|shared|pod-1", Kind: "Pod", Namespace: "shop", Name: "shared", UID: "pod-1"},
	}
	stray := StrayObservation{
		Family: "app", Metric: "m",
		Labels: map[string]string{"namespace": "shop", "name": "shared"},
		Reason: "unmapped-metric-class", StreamRef: "m@n",
	}
	got := Resolve(stray, tieInv)
	nodes, edges := kinds(got)
	if nodes != 1 || edges != 2 {
		t.Fatalf("nodes=%d edges=%d, want 1/2 (tie surfaces both)", nodes, edges)
	}
	// sorted by entity key: Pod key < PVC key lexically? "i|c1|shop|Pod|..." < "i|c1|shop|PersistentVolumeClaim|..."
	if got[1].Payload["entityKey"].(string) > got[2].Payload["entityKey"].(string) {
		t.Errorf("tie edges not sorted by key: %v then %v", got[1].Payload["entityKey"], got[2].Payload["entityKey"])
	}
}

// uid + namespace + name = the strongest evidence (3 coords); still exactly one edge.
func TestResolveExactTriple(t *testing.T) {
	stray := StrayObservation{
		Family: "app", Metric: "m",
		Labels: map[string]string{"namespace": "shop", "pod": "web-a", "pod_uid": "pod-x"},
		Reason: "unknown-pod", StreamRef: "m@n",
	}
	got := Resolve(stray, inv)
	_, edges := kinds(got)
	if edges != 1 {
		t.Fatalf("edges=%d, want 1", edges)
	}
	if got[1].Payload["sharedCoordinates"] != 3 {
		t.Errorf("sharedCoordinates = %v, want 3", got[1].Payload["sharedCoordinates"])
	}
}

func TestResolveNoInventoryStillSurfacesNode(t *testing.T) {
	got := Resolve(StrayObservation{Metric: "lonely", Reason: "unknown-exporter-family", StreamRef: "l@n"}, nil)
	if len(got) != 1 || got[0].Kind != KindNode {
		t.Fatalf("want exactly one provisional node, got %+v", got)
	}
}

// Stage persists the ER output and it reads back through the store API.
func TestStageRoundTrip(t *testing.T) {
	s := newStore(t)
	stray := StrayObservation{
		Family: "node-exporter", Metric: "weird_widget_total",
		Labels: map[string]string{"namespace": "shop", "pod": "web-a"},
		Node:   "worker-1", Reason: "unknown-exporter-family", StreamRef: "w@worker-1", GraphVersion: "v0.8.0",
	}
	n, err := s.Stage(t0, Resolve(stray, inv))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("staged %d, want 2 (node + edge)", n)
	}
	all, _ := s.List(Filter{})
	if len(all) != 2 {
		t.Fatalf("store has %d rows, want 2", len(all))
	}
	edges, _ := s.List(Filter{Kind: KindEdge})
	if len(edges) != 1 || edges[0].Relation != "associated-with" || edges[0].Lineage.Source != "cei-fallback" {
		t.Errorf("staged edge wrong: %+v", edges)
	}
}
