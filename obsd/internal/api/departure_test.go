package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/departure"
)

var depNow = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

func TestDepartureViewOff(t *testing.T) {
	v := BuildDepartures(nil, false, false, depNow)
	if v.Enabled || v.Active || len(v.Departures) != 0 {
		t.Fatalf("OFF wrong: enabled=%v active=%v deps=%d", v.Enabled, v.Active, len(v.Departures))
	}
	if !strings.Contains(v.Note, "OFF") {
		t.Errorf("OFF note must say so: %q", v.Note)
	}
}

// Enabled but gate-pending: the lane is on yet the departures are WITHHELD (a new PROJECTED
// class is not operator-visible before its gate) — even with departures present.
func TestDepartureViewGatePending(t *testing.T) {
	deps := []departure.Departure{{EntityCEI: "i|x", Side: "above", Class: "PROJECTED band ⋈ MEASURED sample (joined, never fused)"}}
	v := BuildDepartures(deps, true, false, depNow) // gate NOT passed
	if !v.Enabled || v.Active || len(v.Departures) != 0 {
		t.Fatalf("gate-pending must WITHHOLD departures, got active=%v deps=%d", v.Active, len(v.Departures))
	}
	// When the gate passes, the departures surface.
	v2 := BuildDepartures(deps, true, true, depNow)
	if !v2.Active || len(v2.Departures) != 1 {
		t.Errorf("gate-passed must surface departures, got active=%v deps=%d", v2.Active, len(v2.Departures))
	}
	// Charter: no causal/anomaly-score token in the rendered surface.
	b, _ := json.Marshal(v2)
	low := strings.ToLower(string(b))
	for _, tok := range []string{"caused", "anomaly score", "because of", "due to"} {
		if strings.Contains(low, tok) {
			t.Errorf("forbidden token %q in departure surface:\n%s", tok, b)
		}
	}
}
