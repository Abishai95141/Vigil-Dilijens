package departure

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

var depAt = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

func obs(entity string, lower, upper, realized float64, conf string) BandObservation {
	return BandObservation{EntityCEI: entity, Metric: "working_set", At: depAt, Lower: lower, Upper: upper, Realized: realized, Confidence: conf}
}

// forbidden checks the departure scaffolding never fabricates a causal/anomaly-score claim.
func forbiddenInDeparture(t *testing.T, d []Departure) {
	t.Helper()
	b, _ := json.Marshal(d)
	low := strings.ToLower(string(b))
	for _, tok := range []string{"caused", "because of", "due to", "root cause", "anomaly score", "is an anomaly"} {
		if strings.Contains(low, tok) {
			t.Errorf("forbidden token %q in departure output:\n%s", tok, b)
		}
	}
}

// A genuine STEP above the band fires a PROJECTED departure (recall).
func TestDepartureTrueStepAbove(t *testing.T) {
	out := Detect([]BandObservation{obs("i|svc", 10, 20, 50, "moderate")}, Params{})
	if len(out) != 1 {
		t.Fatalf("a sample 50 over band [10,20] must depart, got %d", len(out))
	}
	d := out[0]
	if d.Side != "above" || d.Exceedance != 30 {
		t.Errorf("side/exceedance wrong: %s / %v (want above / 30)", d.Side, d.Exceedance)
	}
	if !strings.HasPrefix(d.Class, "PROJECTED") {
		t.Errorf("a departure is PROJECTED (band ⋈ measured), got class %q", d.Class)
	}
	forbiddenInDeparture(t, out)
}

// THE CARDINAL FP DEFENSE: a noisy-but-stationary series gets a WIDE band from the clock,
// so a noisy sample stays INSIDE — no departure (no false anomaly).
func TestDepartureNoisyDecoyStaysInside(t *testing.T) {
	// the clock, unsure about a noisy series, returns a wide band [0,40]; realized 35 is
	// noisy but well within it.
	out := Detect([]BandObservation{obs("i|noisy", 0, 40, 35, "wide")}, Params{})
	if len(out) != 0 {
		t.Fatalf("a noisy sample inside a WIDE band must NOT depart (structural FP defense), got %+v", out)
	}
}

// A drop below the band fires a 'below' departure.
func TestDepartureTrueStepBelow(t *testing.T) {
	out := Detect([]BandObservation{obs("i|svc", 40, 60, 5, "tight")}, Params{})
	if len(out) != 1 || out[0].Side != "below" {
		t.Fatalf("a sample 5 below band [40,60] must depart below, got %+v", out)
	}
}

// A sample exactly ON the band edge is inside (strict — the edge is not a departure).
func TestDepartureEdgeIsInside(t *testing.T) {
	if out := Detect([]BandObservation{obs("i|svc", 10, 20, 20, "tight")}, Params{}); len(out) != 0 {
		t.Fatalf("a sample exactly at the upper edge is inside, got %+v", out)
	}
}

// The structural margin absorbs a near-miss a hair outside a tight band.
func TestDepartureStructuralMarginAbsorbsNearMiss(t *testing.T) {
	// band [10,20] width 10; margin 0.1*10 = 1.0 ⇒ upper+margin = 21. realized 20.5 < 21.
	p := Params{MinExceedanceFraction: 0.1}
	if out := Detect([]BandObservation{obs("i|svc", 10, 20, 20.5, "tight")}, p); len(out) != 0 {
		t.Fatalf("a near-miss within the structural margin must NOT depart, got %+v", out)
	}
	// but a clear step (50) still departs through the margin.
	if out := Detect([]BandObservation{obs("i|svc", 10, 20, 50, "tight")}, p); len(out) != 1 {
		t.Fatalf("a clear step must still depart through the margin, got %+v", out)
	}
}

// A malformed band (upper < lower, non-finite, or ZERO-WIDTH) NEVER fabricates a departure.
// A collapsed band (upper==lower) is degenerate — a band must NEVER collapse to a line
// (doc 01); comparing a sample to it would make the structural margin a zero-ULP hair-trigger,
// exactly where the FP defense must hold. So even a far-away sample never departs against it.
func TestDepartureMalformedBandNeverFires(t *testing.T) {
	cases := []BandObservation{
		obs("i|a", 20, 10, 50, "tight"),          // upper < lower
		obs("i|b", math.NaN(), 20, 50, "tight"),  // NaN lower
		obs("i|c", 10, math.Inf(1), 50, "tight"), // Inf upper
		obs("i|d", 10, 20, math.NaN(), "tight"),  // NaN realized
		obs("i|e", 5, 5, 50, "tight"),            // zero-width (collapsed) band — degenerate, never fires
	}
	if out := Detect(cases, Params{}); len(out) != 0 {
		t.Fatalf("malformed/collapsed bands must produce no departure, got %+v", out)
	}
}

// Determinism: same observations + params ⇒ byte-identical departures.
func TestDepartureDeterministic(t *testing.T) {
	in := []BandObservation{
		obs("i|c", 10, 20, 50, "moderate"), obs("i|a", 40, 60, 5, "tight"), obs("i|b", 0, 40, 35, "wide"),
	}
	a, _ := json.Marshal(Detect(in, Params{}))
	b, _ := json.Marshal(Detect(in, Params{}))
	if string(a) != string(b) {
		t.Error("Detect is non-deterministic across identical inputs")
	}
}
