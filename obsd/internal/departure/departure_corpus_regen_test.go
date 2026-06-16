package departure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The departure gate corpus (doc 15 cap. C / task #75 — the near-miss/decoy corpus): a
// STANDALONE deterministic gate folding the REAL Detect over recorded (forecast band,
// realized sample) pairs, graded against a LABEL ORACLE. The CARDINAL rule is the anti-FP
// floor: a DECOY (a noisy-but-stationary series whose clock band is WIDE) must produce ZERO
// departures, while a TRUE STEP must fire. Detect is a pure function of (observations,
// params), so the frozen corpus reproducing byte-identically is the off-digest determinism
// proof ("same recorded band + sample + params ⇒ same flags", NOT the tick digest).
//
// Regenerate: REGEN_DEPARTURE_CORPUS=1 go test ./obsd/internal/departure -run RegenDepartureCorpus

const departureCorpusDir = "../../../corpus/departure"

// depParams pins the structural margin used to build the frozen corpus (0.1 of band width).
func depParams() Params { return Params{MinExceedanceFraction: 0.1} }

type dLabel struct {
	Bundle          string `json:"bundle"`
	Scenario        string `json:"scenario"`
	Kind            string `json:"kind"` // step | decoy | healthy
	ExpectDeparture bool   `json:"expectDeparture"`
	ExpectSide      string `json:"expectSide"` // above | below | ""
	Note            string `json:"note"`
}

type dScenario struct {
	lbl dLabel
	obs []BandObservation
}

func departureScenarios() []dScenario {
	return []dScenario{
		{
			lbl: dLabel{Scenario: "step-above", Kind: "step", ExpectDeparture: true, ExpectSide: "above",
				Note: "a stationary series (band [10,20]) STEPS to 50 — well past its forecast band → a PROJECTED departure (recall)."},
			obs: []BandObservation{obs("i|cl|ns|Pod|inference|u1", 10, 20, 50, "moderate")},
		},
		{
			lbl: dLabel{Scenario: "step-below", Kind: "step", ExpectDeparture: true, ExpectSide: "below",
				Note: "a series (band [40,60]) DROPS to 5 — far below its forecast band → a 'below' departure."},
			obs: []BandObservation{obs("i|cl|ns|Pod|db|u2", 40, 60, 5, "tight")},
		},
		{
			lbl: dLabel{Scenario: "noisy-decoy", Kind: "decoy", ExpectDeparture: false,
				Note: "THE CARDINAL FP DEFENSE: a noisy-but-stationary series ⇒ the clock returns a WIDE band [0,40]; a noisy sample 35 stays INSIDE → NO departure."},
			obs: []BandObservation{obs("i|cl|ns|Pod|noisy|u3", 0, 40, 35, "wide")},
		},
		{
			lbl: dLabel{Scenario: "wide-band-absorbs", Kind: "decoy", ExpectDeparture: false,
				Note: "the clock is UNSURE (very wide band [5,95]); even a real bump to 80 is absorbed → NO departure. The detector never out-claims the forecast's own confidence."},
			obs: []BandObservation{obs("i|cl|ns|Pod|bursty|u4", 5, 95, 80, "wide")},
		},
		{
			lbl: dLabel{Scenario: "near-miss-margin", Kind: "decoy", ExpectDeparture: false,
				Note: "a sample 20.8 a hair past a tight band [10,20] is ABSORBED by the structural margin (0.1×10=1 ⇒ edge 21) → NO departure (no flag on jitter)."},
			obs: []BandObservation{obs("i|cl|ns|Pod|jitter|u5", 10, 20, 20.8, "tight")},
		},
		{
			lbl: dLabel{Scenario: "healthy-inside", Kind: "healthy", ExpectDeparture: false,
				Note: "a calm series (band [18,22]) realizes 20 — squarely inside → no departure, never invented."},
			obs: []BandObservation{obs("i|cl|ns|Pod|calm|u6", 18, 22, 20, "tight")},
		},
		{
			lbl: dLabel{Scenario: "edge-inside", Kind: "healthy", ExpectDeparture: false,
				Note: "a sample exactly ON the upper edge (20 of [10,20]) is INSIDE (strict) → no departure."},
			obs: []BandObservation{obs("i|cl|ns|Pod|onedge|u7", 10, 20, 20, "tight")},
		},
		{
			lbl: dLabel{Scenario: "zero-width-band", Kind: "decoy", ExpectDeparture: false,
				Note: "a COLLAPSED forecast band [5,5] (upper==lower) is degenerate — a band must NEVER collapse to a line (doc 01); even a far-away sample 50 produces NO departure, because the structural margin would be a zero-ULP hair-trigger. Degrade-never-fabricate at the model-adjacent edge."},
			obs: []BandObservation{obs("i|cl|ns|Pod|collapsed|u8", 5, 5, 50, "tight")},
		},
	}
}

func marshalDepartures(d []Departure) []byte {
	var buf []byte
	for i := range d {
		b, _ := json.Marshal(d[i])
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	return buf
}

func TestRegenDepartureCorpus(t *testing.T) {
	if os.Getenv("REGEN_DEPARTURE_CORPUS") != "1" {
		t.Skip("set REGEN_DEPARTURE_CORPUS=1 to regenerate corpus/departure")
	}
	if err := os.MkdirAll(departureCorpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sc := range departureScenarios() {
		deps := Detect(sc.obs, depParams())
		if err := os.WriteFile(filepath.Join(departureCorpusDir, "departures-"+sc.lbl.Scenario+".jsonl"), marshalDepartures(deps), 0o644); err != nil {
			t.Fatal(err)
		}
		lbl := sc.lbl
		lbl.Bundle = "departure-" + lbl.Scenario
		b, _ := json.MarshalIndent(lbl, "", "  ")
		if err := os.WriteFile(filepath.Join(departureCorpusDir, "label-"+sc.lbl.Scenario+".json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("regenerated %d departure scenarios into %s", len(departureScenarios()), departureCorpusDir)
}

// TestDepartureCorpusFrozenConsistent is the always-on determinism guard.
func TestDepartureCorpusFrozenConsistent(t *testing.T) {
	for _, sc := range departureScenarios() {
		got := marshalDepartures(Detect(sc.obs, depParams()))
		want, err := os.ReadFile(filepath.Join(departureCorpusDir, "departures-"+sc.lbl.Scenario+".jsonl"))
		if err != nil {
			t.Fatalf("frozen corpus missing for %s: %v (REGEN to create)", sc.lbl.Scenario, err)
		}
		if string(got) != string(want) {
			t.Errorf("departure corpus drift for %s — re-run differs from frozen (REGEN to update)", sc.lbl.Scenario)
		}
	}
}
