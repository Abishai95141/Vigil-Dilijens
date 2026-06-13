package governance

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

const repoRoot = "../../.."

func loadRel(t *testing.T, name string) *graph.Graph {
	t.Helper()
	g, _, err := graph.LoadReleasePinned(repoRoot+"/ontology/releases/"+name+".yaml", repoRoot)
	if err != nil {
		t.Fatalf("load release %s: %v", name, err)
	}
	return g
}

// TestClassifyRealReleases is the headline real-data test: the change-class engine
// diffs the ACTUAL committed releases and derives the class each one declared. v0.2.0
// and v0.3.0 add detection conditions + flagged threshold rules — behavioural-class,
// not normative (they change no existing normativity).
func TestClassifyRealReleases(t *testing.T) {
	v1 := loadRel(t, "v0.1.0")
	v2 := loadRel(t, "v0.2.0")
	v3 := loadRel(t, "v0.3.0")

	cases := []struct {
		name     string
		from, to *graph.Graph
		want     ChangeClass
	}{
		{"v0.1.0 first release", nil, v1, ClassBehaviouralMedium},
		{"v0.1.0→v0.2.0", v1, v2, ClassBehaviouralMedium},
		{"v0.2.0→v0.3.0", v2, v3, ClassBehaviouralMedium},
		{"identical (no change)", v3, v3, ClassNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, items := Classify(tc.from, tc.to)
			if got != tc.want {
				t.Errorf("Classify class = %s, want %s", got, tc.want)
				for _, it := range items {
					if it.Class == got {
						t.Logf("  widest: [%s] %s", it.Class, it.Detail)
					}
				}
			}
			// The diff must be non-empty where a change exists, and items must be
			// sorted widest-first.
			if tc.want != ClassNone && len(items) == 0 {
				t.Errorf("expected change items, got none")
			}
			for i := 1; i < len(items); i++ {
				if items[i-1].Class < items[i].Class {
					t.Errorf("items not sorted widest-first at %d: %s before %s", i, items[i-1].Class, items[i].Class)
				}
			}
		})
	}
}

// TestClassifyDeterministic: the diff is a pure function — same inputs, identical
// item list (governance audit must be reproducible).
func TestClassifyDeterministic(t *testing.T) {
	v1 := loadRel(t, "v0.1.0")
	v2 := loadRel(t, "v0.2.0")
	_, a := Classify(v1, v2)
	_, b := Classify(v1, v2)
	if len(a) != len(b) {
		t.Fatalf("non-deterministic item count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("item %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// --- synthetic graphs exercise the class boundaries precisely ---------------

func sig(id string) *graph.Signal { return &graph.Signal{ID: id, Modality: "Metric"} }

func f64(v float64) *float64 { return &v }

func baseGraph() *graph.Graph {
	return &graph.Graph{
		Version:           "sha256:base",
		Signals:           map[string]*graph.Signal{"SIG_A": sig("SIG_A")},
		Phenomena:         map[string]*graph.Phenomenon{},
		EquivalenceGroups: map[string]*graph.EquivalenceGroup{},
		Gotchas:           map[string]*graph.Gotcha{},
		Entities:          map[string]*graph.Node{},
		Tools:             map[string]*graph.Node{},
		Modalities:        map[string]*graph.Node{},
		Capabilities:      map[string]*graph.Node{},
		DistroGates:       map[string]*graph.Node{},
		Agents:            map[string]*graph.Node{},
		Checks:            map[string][]*graph.MemberCheck{},
	}
}

func TestClassifyAdditiveLow(t *testing.T) {
	from := baseGraph()
	to := baseGraph()
	to.Signals["SIG_B"] = sig("SIG_B") // new signal — additive
	to.EquivalenceGroups["EG_NEW"] = &graph.EquivalenceGroup{ID: "EG_NEW"}
	got, items := Classify(from, to)
	if got != ClassAdditiveLow {
		t.Fatalf("class = %s, want additive-low; items: %+v", got, items)
	}
}

func TestClassifyNormativeDefaultChange(t *testing.T) {
	from := baseGraph()
	from.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(100), Direction: "above"}}
	to := baseGraph()
	to.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(50), Direction: "above"}}
	got, items := Classify(from, to)
	if got != ClassNormativeHigh {
		t.Fatalf("default-change class = %s, want normative-high; items: %+v", got, items)
	}
	// The change item must name the default move (operator-readable).
	found := false
	for _, it := range items {
		if it.Kind == "ThresholdRule" && it.Class == ClassNormativeHigh {
			found = true
		}
	}
	if !found {
		t.Errorf("no normative ThresholdRule item recorded")
	}
}

func TestClassifyNormativeEquivalenceRedefinition(t *testing.T) {
	from := baseGraph()
	from.EquivalenceGroups["EG_M"] = &graph.EquivalenceGroup{ID: "EG_M", CanonicalOTel: "old.canonical", Patterns: []string{"p1", "p2"}}
	to := baseGraph()
	to.EquivalenceGroups["EG_M"] = &graph.EquivalenceGroup{ID: "EG_M", CanonicalOTel: "new.canonical", Patterns: []string{"p1"}} // canonical changed + pattern removed
	got, _ := Classify(from, to)
	if got != ClassNormativeHigh {
		t.Fatalf("equivalence redefinition class = %s, want normative-high", got)
	}
}

func TestClassifyNewRuleIsBehavioural(t *testing.T) {
	from := baseGraph()
	to := baseGraph()
	// A brand-new flagged default is behavioural (new disclosed detection), not
	// normative (no existing normativity changed).
	to.Rules = []*graph.ThresholdRule{{ID: "THR_NEW", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(10), Direction: "above"}}
	got, _ := Classify(from, to)
	if got != ClassBehaviouralMedium {
		t.Fatalf("new-rule class = %s, want behavioural-medium", got)
	}
}

func TestClassifyMaxClassWins(t *testing.T) {
	from := baseGraph()
	from.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(100), Direction: "above"}}
	to := baseGraph()
	to.Signals["SIG_B"] = sig("SIG_B")                                                                                                     // additive
	to.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(80), Direction: "above"}} // normative
	got, _ := Classify(from, to)
	if got != ClassNormativeHigh {
		t.Fatalf("max-class = %s, want normative-high (the widest change sets the rigor)", got)
	}
}

// TestClassifyRulePointerChanges pins the under-classification fixes: re-pointing a
// rule's Signal or Eligibility (both change which bar binds where) is NORMATIVE, not
// silently ClassNone (the dangerous direction, doc 12 §3.2).
func TestClassifyRulePointerChanges(t *testing.T) {
	mk := func(signal, elig string) *graph.Graph {
		g := baseGraph()
		g.Signals["SIG_B"] = sig("SIG_B")
		g.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: signal, Metric: "m", Kind: "config-relative",
			ConfigPath: "container.resources.limits.memory", Factor: 0.9, Direction: "above", Eligibility: elig}}
		return g
	}
	cases := []struct {
		name     string
		from, to *graph.Graph
	}{
		{"signal re-point", mk("SIG_A", ""), mk("SIG_B", "")},
		{"eligibility add", mk("SIG_A", ""), mk("SIG_A", "container.resources.limits.cpu")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, items := Classify(tc.from, tc.to)
			if got != ClassNormativeHigh {
				t.Fatalf("class = %s, want normative-high; items: %+v", got, items)
			}
		})
	}
}

func TestParseChangeClassRoundTrip(t *testing.T) {
	for _, c := range []ChangeClass{ClassNone, ClassAdditiveLow, ClassBehaviouralMedium, ClassNormativeHigh} {
		got, err := ParseChangeClass(c.String())
		if err != nil || got != c {
			t.Errorf("round-trip %s: got %s err %v", c, got, err)
		}
	}
	if _, err := ParseChangeClass("bogus"); err == nil {
		t.Errorf("expected error for bogus class")
	}
}
