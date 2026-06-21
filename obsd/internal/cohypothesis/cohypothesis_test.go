package cohypothesis

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/onset"
)

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
	got := Hypothesize(onsets, pairs, 90*time.Second, "v1")
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
	got := Hypothesize(onsets, pairs, 90*time.Second, "v1")
	if len(got) != 1 || got[0].Subject != "podA|mem ~ podB|mem" {
		t.Fatalf("subject must be sorted/stable regardless of input order, got %+v", got)
	}
}

func TestCoOnset_NoHypothesisWhenStepsTooFarApart(t *testing.T) {
	onsets := []onset.Onset{ons("podA|mem", 0, "up"), ons("podB|mem", 300, "up")} // 300s apart
	pairs := []CoupledPair{{A: "podA|mem", B: "podB|mem", Coefficient: 0.91}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1"); len(got) != 0 {
		t.Fatalf("steps 300s apart with a 90s window must NOT co-onset, got %d", len(got))
	}
}

func TestCoOnset_NoHypothesisWhenOnlyOneStepped(t *testing.T) {
	onsets := []onset.Onset{ons("podA|mem", 0, "up")} // podB never stepped
	pairs := []CoupledPair{{A: "podA|mem", B: "podB|mem", Coefficient: 0.91}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1"); len(got) != 0 {
		t.Fatalf("only one side stepped ⇒ no co-onset, got %d", len(got))
	}
}

func TestCoOnset_UsesMostRecentOnsetPerSeries(t *testing.T) {
	// podA stepped long ago AND recently; podB stepped recently. The recent pair co-onsets.
	onsets := []onset.Onset{ons("podA|mem", -1000, "up"), ons("podA|mem", 10, "up"), ons("podB|mem", 0, "up")}
	pairs := []CoupledPair{{A: "podA|mem", B: "podB|mem", Coefficient: 0.8}}
	if got := Hypothesize(onsets, pairs, 90*time.Second, "v1"); len(got) != 1 {
		t.Fatalf("most-recent onsets (10s vs 0s) are within window ⇒ 1 hypothesis, got %d", len(got))
	}
}
