package governance

import (
	"reflect"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// liveGraph builds a tiny graph with one existing equivalence group whose pattern matches
// "container_memory_working_set_bytes" only.
func liveGraph() *graph.Graph {
	return &graph.Graph{
		EquivalenceGroups: map[string]*graph.EquivalenceGroup{
			"EQG_MEM_WORKING_SET": {
				ID:            "EQG_MEM_WORKING_SET",
				CanonicalOTel: "k8s.container.memory.working_set",
				Patterns:      []string{`^container_memory_working_set_bytes$`},
			},
		},
	}
}

func TestPreviewNewGroupResolvesStray(t *testing.T) {
	g := liveGraph()
	in := EquivGroupPreviewInput{
		NewGroupID:   "EQG_REDIS_MEM",
		NewCanonical: "redis.memory.used",
		NewLabel:     "Redis memory used",
		Pattern:      `^redis_memory_used_bytes$`,
		ScopeMetrics: []string{"redis_memory_used_bytes"},
	}
	pv, err := PreviewEquivGroupPromotion(g, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !pv.DefinesNewGroup || pv.GroupID != "EQG_REDIS_MEM" {
		t.Fatalf("expected new group EQG_REDIS_MEM, got group=%q new=%v", pv.GroupID, pv.DefinesNewGroup)
	}
	if !reflect.DeepEqual(pv.NewlyResolved, []string{"redis_memory_used_bytes"}) {
		t.Fatalf("NewlyResolved = %v, want [redis_memory_used_bytes]", pv.NewlyResolved)
	}
	if len(pv.AlreadyResolved) != 0 || len(pv.StillUnresolved) != 0 {
		t.Fatalf("expected clean delta, got already=%v still=%v", pv.AlreadyResolved, pv.StillUnresolved)
	}
	if pv.GroupsBefore != 1 || pv.GroupsAfter != 2 {
		t.Fatalf("groups before/after = %d/%d, want 1/2", pv.GroupsBefore, pv.GroupsAfter)
	}
}

func TestPreviewExtendExistingGroupCapturesSample(t *testing.T) {
	g := liveGraph()
	// A pattern that captures TWO strays neither of which resolves now.
	in := EquivGroupPreviewInput{
		TargetGroupID: "EQG_MEM_WORKING_SET",
		Pattern:       `_memory_rss_bytes$`,
		ScopeMetrics:  []string{"app_memory_rss_bytes", "sidecar_memory_rss_bytes"},
	}
	pv, err := PreviewEquivGroupPromotion(g, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if pv.DefinesNewGroup {
		t.Fatalf("extending an existing group must not define a new one")
	}
	want := []string{"app_memory_rss_bytes", "sidecar_memory_rss_bytes"}
	if !reflect.DeepEqual(pv.NewlyResolved, want) {
		t.Fatalf("NewlyResolved = %v, want %v", pv.NewlyResolved, want)
	}
	if pv.GroupsBefore != 1 || pv.GroupsAfter != 1 {
		t.Fatalf("extending must not add a group: before/after = %d/%d", pv.GroupsBefore, pv.GroupsAfter)
	}
}

func TestPreviewAlreadyResolvedAndStillUnresolved(t *testing.T) {
	g := liveGraph()
	in := EquivGroupPreviewInput{
		NewGroupID:   "EQG_REDIS_MEM",
		NewCanonical: "redis.memory.used",
		NewLabel:     "Redis memory used",
		Pattern:      `^redis_memory_used_bytes$`,
		ScopeMetrics: []string{
			"redis_memory_used_bytes",                  // newly resolved by the pattern
			"container_memory_working_set_bytes",       // already resolves via the live group
			"some_unrelated_metric_the_pattern_misses", // pattern doesn't match → still unresolved
		},
	}
	pv, err := PreviewEquivGroupPromotion(g, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !reflect.DeepEqual(pv.NewlyResolved, []string{"redis_memory_used_bytes"}) {
		t.Fatalf("NewlyResolved = %v", pv.NewlyResolved)
	}
	if !reflect.DeepEqual(pv.AlreadyResolved, []string{"container_memory_working_set_bytes"}) {
		t.Fatalf("AlreadyResolved = %v", pv.AlreadyResolved)
	}
	if !reflect.DeepEqual(pv.StillUnresolved, []string{"some_unrelated_metric_the_pattern_misses"}) {
		t.Fatalf("StillUnresolved = %v", pv.StillUnresolved)
	}
}

func TestPreviewDoesNotMutateLiveGraph(t *testing.T) {
	g := liveGraph()
	before := append([]string(nil), g.EquivalenceGroups["EQG_MEM_WORKING_SET"].Patterns...)
	beforeGroups := len(g.EquivalenceGroups)
	in := EquivGroupPreviewInput{
		TargetGroupID: "EQG_MEM_WORKING_SET",
		Pattern:       `_memory_rss_bytes$`,
		ScopeMetrics:  []string{"app_memory_rss_bytes"},
	}
	if _, err := PreviewEquivGroupPromotion(g, in); err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got := g.EquivalenceGroups["EQG_MEM_WORKING_SET"].Patterns; !reflect.DeepEqual(got, before) {
		t.Fatalf("live group patterns MUTATED: %v (was %v) — preview must be read-only", got, before)
	}
	if len(g.EquivalenceGroups) != beforeGroups {
		t.Fatalf("live group COUNT changed: %d (was %d)", len(g.EquivalenceGroups), beforeGroups)
	}
}

func TestPreviewDeterministic(t *testing.T) {
	g := liveGraph()
	in := EquivGroupPreviewInput{
		NewGroupID:   "EQG_REDIS_MEM",
		NewCanonical: "redis.memory.used",
		Pattern:      `_bytes$`,
		ScopeMetrics: []string{"b_bytes", "a_bytes", "c_total"},
	}
	a, err := PreviewEquivGroupPromotion(g, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PreviewEquivGroupPromotion(g, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("preview not deterministic:\n a=%+v\n b=%+v", a, b)
	}
	// sorted, deterministic order
	if !reflect.DeepEqual(a.NewlyResolved, []string{"a_bytes", "b_bytes"}) {
		t.Fatalf("NewlyResolved not sorted: %v", a.NewlyResolved)
	}
}

func TestPreviewRejectsBadInput(t *testing.T) {
	g := liveGraph()
	cases := []EquivGroupPreviewInput{
		{Pattern: ""},  // empty pattern
		{Pattern: "("}, // non-compiling
		{Pattern: "x", TargetGroupID: "A", NewGroupID: "B"}, // both targets
		{Pattern: "x"}, // neither target
	}
	for i, c := range cases {
		if _, err := PreviewEquivGroupPromotion(g, c); err == nil {
			t.Errorf("case %d: expected error for %+v", i, c)
		}
	}
	if _, err := PreviewEquivGroupPromotion(nil, EquivGroupPreviewInput{Pattern: "x", NewGroupID: "A"}); err == nil {
		t.Errorf("expected error for nil graph")
	}
}
