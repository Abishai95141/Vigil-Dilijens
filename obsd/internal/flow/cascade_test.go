package flow

import (
	"os"
	"testing"
	"time"
)

// boutiquePods is the frozen identity snapshot for the test (real live pod IPs).
func boutiquePods() []PodInfo {
	mk := func(wl, ip string) PodInfo {
		return PodInfo{Namespace: "online-boutique", Name: wl + "-x", IP: ip, Workload: wl, UID: "uid-" + wl}
	}
	return []PodInfo{
		mk("loadgenerator", "10.244.1.2"),
		mk("frontend", "10.244.1.6"),
		mk("productcatalogservice", "10.244.1.3"),
		mk("cartservice", "10.244.1.7"),
		mk("redis-cart", "10.244.1.4"),
		mk("paymentservice", "10.244.1.8"),
		mk("currencyservice", "10.244.3.2"),
		mk("checkoutservice", "10.244.3.3"),
		mk("shippingservice", "10.244.3.4"),
		mk("emailservice", "10.244.3.6"),
		mk("recommendationservice", "10.244.3.7"),
		mk("adservice", "10.244.3.8"),
	}
}

func loadSampleGraph(t *testing.T, at time.Time) *Graph {
	t.Helper()
	f, err := os.Open("testdata/nf_conntrack_sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conns, err := ParseConntrack(f)
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return at }
	g := NewGraph(NewResolver("test-cluster", boutiquePods()), clock)
	g.Observe(conns, at)
	return g
}

// TestReconstructFanIn pins the SIGNAL: the recovered graph contains the real
// 3-way fan-in to productcatalog and the other boutique edges, with no self-loops.
func TestReconstructFanIn(t *testing.T) {
	at := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	g := loadSampleGraph(t, at)

	want := map[string]bool{
		"online-boutique/frontend->online-boutique/productcatalogservice":              true,
		"online-boutique/checkoutservice->online-boutique/productcatalogservice":       true,
		"online-boutique/recommendationservice->online-boutique/productcatalogservice": true,
		"online-boutique/loadgenerator->online-boutique/frontend":                      true,
		"online-boutique/cartservice->online-boutique/redis-cart":                      true,
		"online-boutique/checkoutservice->online-boutique/emailservice":                true,
		"online-boutique/checkoutservice->online-boutique/cartservice":                 true,
	}
	got := map[string]bool{}
	for _, e := range g.Edges() {
		got[e.FromLabel+"->"+e.ToLabel] = true
		if e.FromLabel == e.ToLabel {
			t.Errorf("self-loop edge emitted: %s", e.FromLabel)
		}
	}
	for w := range want {
		if !got[w] {
			t.Errorf("missing reconstructed edge: %s", w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d edges, want %d: %v", len(got), len(want), got)
	}
	// productcatalog fan-in == 3 distinct callers
	var fanIn int
	for _, e := range g.Edges() {
		if e.ToLabel == "online-boutique/productcatalogservice" {
			fanIn++
		}
	}
	if fanIn != 3 {
		t.Errorf("productcatalog fan-in = %d, want 3", fanIn)
	}
}

// TestWalkNamesHubRoot pins the WALK + the CHARTER: a degraded hub is named as the
// most-upstream node, all three callers appear as impacted (never the root), and the
// rendered output carries no causal-claim token.
func TestWalkNamesHubRoot(t *testing.T) {
	at := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	g := loadSampleGraph(t, at)
	rel, err := LoadRelation("../../../ontology/graph/overlays/experimental/flow-relation-v0.yaml")
	if err != nil {
		t.Fatal(err)
	}

	res := NewResolver("test-cluster", boutiquePods())
	root, _ := res.Role("10.244.1.3") // productcatalog
	rootLabel, _ := res.Label("10.244.1.3")
	symptoms := []Symptom{{
		Workload: root, Label: rootLabel, Phenomenon: rel.Trigger, Class: "MEASURED",
		Detail: "cpu throttle ratio 0.42 > 0.25 bar (50m limit)",
	}}

	chain := Walk(g, symptoms, rel, at)

	if chain.MostUpstreamDegradedNode != "online-boutique/productcatalogservice" {
		t.Fatalf("root = %q, want online-boutique/productcatalogservice", chain.MostUpstreamDegradedNode)
	}
	if len(chain.Links) != 3 {
		t.Fatalf("got %d impacted links, want 3", len(chain.Links))
	}
	impacted := map[string]bool{}
	for _, l := range chain.Links {
		impacted[l.Impacted] = true
		if l.Impacted == chain.MostUpstreamDegradedNode {
			t.Errorf("symptom-as-root: %s appears as its own impacted caller", l.Impacted)
		}
		if l.Degraded != "online-boutique/productcatalogservice" {
			t.Errorf("link degraded = %s, want productcatalog", l.Degraded)
		}
		if l.EdgeClass != "MEASURED observed flow" || l.WhyClass != "AUTHORED" {
			t.Errorf("class labels wrong: edge=%q why=%q", l.EdgeClass, l.WhyClass)
		}
	}
	for _, w := range []string{"online-boutique/frontend", "online-boutique/checkoutservice", "online-boutique/recommendationservice"} {
		if !impacted[w] {
			t.Errorf("expected impacted caller missing: %s", w)
		}
	}

	// Charter: no causal-claim token anywhere in the rendered chain.
	b, err := chain.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if tok, bad := HasForbiddenToken(string(b)); bad {
		t.Errorf("forbidden causal token %q in rendered chain:\n%s", tok, b)
	}
	// Determinism: a second identical walk renders byte-identical.
	chain2 := Walk(g, symptoms, rel, at)
	b2, _ := chain2.JSON()
	if string(b) != string(b2) {
		t.Errorf("walk not deterministic")
	}
}
