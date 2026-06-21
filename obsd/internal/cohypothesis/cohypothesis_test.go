package cohypothesis

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/onset"
)

// The same pair re-staged across cycles (with LATER onsets + a slightly different coefficient)
// must DEDUP to one row — the content id is the PAIR, not the per-cycle timestamps (the
// live-found unbounded-growth fix).
func TestCoOnset_DedupsSamePairAcrossCycles(t *testing.T) {
	s, err := candidate.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, b := "i|cl|ns|Pod|x|u1|m", "i|cl|ns|Pod|y|u2|m"
	if _, err := HypothesizeAndStage(s, t0, []onset.Onset{ons(a, 0, "up"), ons(b, 5, "up")},
		[]CoupledPair{{A: a, B: b, Coefficient: 0.90}}, 90*time.Second, "v1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := HypothesizeAndStage(s, t0.Add(time.Minute), []onset.Onset{ons(a, 1000, "up"), ons(b, 1005, "up")},
		[]CoupledPair{{A: a, B: b, Coefficient: 0.92}}, 90*time.Second, "v1", 0); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(candidate.Filter{Kind: candidate.KindCausalHypothesis})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("same pair across cycles must dedup to 1 row (pair-stable id), got %d", len(rows))
	}
}

var t0 = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

func ons(key string, sec int, dir string) onset.Onset {
	// split "cei|metric" back into the onset's two fields (last '|' = metric boundary here).
	cei, metric := key, ""
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '|' {
			cei, metric = key[:i], key[i+1:]
			break
		}
	}
	return onset.Onset{EntityCEI: cei, Metric: metric, At: t0.Add(time.Duration(sec) * time.Second), Direction: dir, StepZ: 12}
}

func TestCoOnset_StagesDirectionFreeHypothesis(t *testing.T) {
	onsets := []onset.Onset{ons("podA|mem", 0, "up"), ons("podB|mem", 30, "up")}
	pairs := []CoupledPair{{A: "podA|mem", B: "podB|mem", Coefficient: 0.91}}
	got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 co-onset hypothesis, got %d", len(got))
	}
	c := got[0]
	if c.Kind != candidate.KindCausalHypothesis || c.Relation != "co-occurrence" {
		t.Fatalf("must be a direction-free causal_hypothesis/co-occurrence, got %s/%s", c.Kind, c.Relation)
	}
	if c.Subject != "podA|mem ~ podB|mem" {
		t.Errorf("subject = %q, want direction-free sorted pair", c.Subject)
	}
	// observedFirst is the MEASURED order (podA stepped at 0, podB at 30).
	if c.Payload["observedFirst"] != "podA|mem" {
		t.Errorf("observedFirst = %v, want podA|mem (the earlier onset)", c.Payload["observedFirst"])
	}
	// CHARTER: no causal/direction field may leak into the payload.
	for k := range c.Payload {
		switch k {
		case "cause", "causes", "direction", "from", "to", "source", "effect":
			t.Errorf("payload leaked a direction/cause field %q — the hypothesis must be direction-free", k)
		}
	}
}

func TestCoOnset_DirectionFreeRegardlessOfInputOrder(t *testing.T) {
	onsets := []onset.Onset{ons("podB|mem", 30, "up"), ons("podA|mem", 0, "up")}
	pairs := []CoupledPair{{A: "podB|mem", B: "podA|mem", Coefficient: 0.91}} // B,A order
	got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 0)
	if len(got) != 1 || got[0].Subject != "podA|mem ~ podB|mem" {
		t.Fatalf("subject must be sorted/stable regardless of input order, got %+v", got)
	}
}

func TestCoOnset_SkipsSameEntityPairs(t *testing.T) {
	// two memory facets of the SAME container (same 6-field CEI) co-step — a trivial
	// within-workload pair, NOT a cross-workload lead. Must be skipped.
	a := "i|cl|ns|Pod|influx|uid|container_memory_mapped_file"
	b := "i|cl|ns|Pod|influx|uid|container_memory_active_file"
	onsets := []onset.Onset{ons(a, 0, "up"), ons(b, 10, "up")}
	pairs := []CoupledPair{{A: a, B: b, Coefficient: 0.95}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 0); len(got) != 0 {
		t.Fatalf("same-entity pair must be skipped (not a cross-workload lead), got %d", len(got))
	}
	// a CROSS-entity pair (different pods) co-stepping IS staged.
	c := "i|cl|ns|Pod|other|uid2|container_memory_working_set_bytes"
	onsets = append(onsets, ons(c, 5, "up"))
	pairs = []CoupledPair{{A: a, B: c, Coefficient: 0.9}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 0); len(got) != 1 {
		t.Fatalf("cross-entity co-onset must be staged, got %d", len(got))
	}
	// the SAME physical pod as both a Container CEI and a Pod CEI (shared pod UID) is NOT a
	// cross-workload lead (the live-found self-correlation leak) — must be skipped.
	pc := "i|cl|ns|Container|s|POD123/s|container_memory_working_set_bytes"
	pp := "i|cl|ns|Pod|costep-y|POD123|container_memory_working_set_bytes"
	on2 := []onset.Onset{ons(pc, 0, "up"), ons(pp, 5, "up")}
	if got := Hypothesize(on2, []CoupledPair{{A: pc, B: pp, Coefficient: 1.0}}, 90*time.Second, "v1", 0); len(got) != 0 {
		t.Fatalf("same-pod Container~Pod views (shared UID) must be skipped, got %d", len(got))
	}
}

func TestCoOnset_CapsToStrongestLeads(t *testing.T) {
	// 5 cross-entity co-onset pairs of varying correlation; maxOut=2 keeps the 2 strongest.
	var onsets []onset.Onset
	var pairs []CoupledPair
	for i, coef := range []float64{0.70, 0.99, 0.80, 0.95, 0.60} {
		c := string(rune('a' + i))
		a := "i|cl|ns|Pod|p" + c + "|u" + c + "|m" // distinct pod UIDs (different physical pods)
		b := "i|cl|ns|Pod|q" + c + "|v" + c + "|m"
		onsets = append(onsets, ons(a, 0, "up"), ons(b, 5, "up"))
		pairs = append(pairs, CoupledPair{A: a, B: b, Coefficient: coef})
	}
	got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 2)
	if len(got) != 2 {
		t.Fatalf("maxOut=2 must keep 2, got %d", len(got))
	}
	for _, c := range got { // the kept pairs must be the strongest two (coef 0.99 and 0.95)
		coef := c.Payload["coefficient"].(float64)
		if coef < 0.94 {
			t.Errorf("cap kept a weak lead coef=%.2f — must keep the strongest", coef)
		}
	}
}

func TestCoOnset_NoHypothesisWhenStepsTooFarApart(t *testing.T) {
	onsets := []onset.Onset{ons("podA|mem", 0, "up"), ons("podB|mem", 300, "up")} // 300s apart
	pairs := []CoupledPair{{A: "podA|mem", B: "podB|mem", Coefficient: 0.91}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 0); len(got) != 0 {
		t.Fatalf("steps 300s apart with a 90s window must NOT co-onset, got %d", len(got))
	}
}

func TestCoOnset_NoHypothesisWhenOnlyOneStepped(t *testing.T) {
	onsets := []onset.Onset{ons("podA|mem", 0, "up")} // podB never stepped
	pairs := []CoupledPair{{A: "podA|mem", B: "podB|mem", Coefficient: 0.91}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 0); len(got) != 0 {
		t.Fatalf("only one side stepped ⇒ no co-onset, got %d", len(got))
	}
}

func TestCoOnset_UsesMostRecentOnsetPerSeries(t *testing.T) {
	// podA stepped long ago AND recently; podB stepped recently. The recent pair co-onsets.
	onsets := []onset.Onset{ons("podA|mem", -1000, "up"), ons("podA|mem", 10, "up"), ons("podB|mem", 0, "up")}
	pairs := []CoupledPair{{A: "podA|mem", B: "podB|mem", Coefficient: 0.8}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1", 0); len(got) != 1 {
		t.Fatalf("most-recent onsets (10s vs 0s) are within window ⇒ 1 hypothesis, got %d", len(got))
	}
}
