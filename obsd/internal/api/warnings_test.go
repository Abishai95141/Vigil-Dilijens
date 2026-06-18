package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
)

var wAt = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

func cycleWithCandidate() forecast.CycleResult {
	return forecast.CycleResult{
		Candidates: []forecast.Candidate{{
			Class: forecast.ClassProjected, IsProjection: true,
			EntityCEI: "i|cl|shop|Pod|web-a|uid-a", Entity: "Container",
			Metric: "container_memory_working_set_bytes", SeriesKind: "gauge",
			BarValue: 486, BarUnit: "bytes", BarSource: "config", Direction: "above",
			PrecursorPhenomena: []string{"PHEN_OOM_KILL_CGROUP"}, GraphVersion: "v",
			GeneratedAt: wAt, BasisAt: wAt,
			CrossAt: wAt.Add(13 * time.Minute), EarliestAt: wAt.Add(9 * time.Minute),
			LatestAt: wAt.Add(22 * time.Minute), TimeToCross: 13 * time.Minute,
			Confidence: "moderate", ContextPoints: 240, HorizonSteps: 240,
		}},
		Silences: []forecast.Silence{{EntityCEI: "i|cl|shop|Pod|web-b|uid-b",
			Metric: "container_memory_working_set_bytes", Reason: forecast.SilenceFlat}},
	}
}

func TestBuildWarningsJoinsAdjacent(t *testing.T) {
	atRisk := func(phen, anchor string) []detect.AtRisk {
		return []detect.AtRisk{{CEIKey: "i|cl||Node|n1|uid-n", Phenomenon: phen,
			Related: "runs-on", Temporal: "T0+terminal", Why: "Eventual outcome"}}
	}
	v := BuildWarnings("v", "v0.3.0", wAt, true, cycleWithCandidate(), 3, ClockHealthRow{Ready: true}, nil, atRisk)
	if !v.Enabled || len(v.Warnings) != 1 {
		t.Fatalf("expected the enabled lane with one card: %+v", v)
	}
	c := v.Warnings[0]
	if c.Class != "PROJECTED" || !c.IsProjection {
		t.Error("the mandatory PROJECTED mark is missing")
	}
	if c.EarliestAt.IsZero() || c.LatestAt.IsZero() || c.EarliestAt.Equal(c.LatestAt) {
		t.Error("the band is mandatory and never collapses to a line")
	}
	if len(c.PrecursorPhenomena) != 1 || len(c.AtRisk) != 1 {
		t.Errorf("AUTHORED references must be CITED on the card: %+v", c)
	}
	if c.AtRisk[0].Why != "Eventual outcome" {
		t.Error("the authored note rides verbatim, never paraphrased")
	}
	if v.Unbudgeted != 3 {
		t.Error("eligible-but-unbudgeted must stay visible (doc 06 M5)")
	}
	if len(v.Silences) != 1 || v.Silences[0].Reason != forecast.SilenceFlat {
		t.Error("every silence carries its guardrail reason")
	}
}

// The regime-shift contamination flag (09 M5 companion) must surface as an adjacent,
// labelled caveat on the card — and be OMITTED entirely when the input was single-regime.
func TestBuildWarningsSurfacesContaminationFlag(t *testing.T) {
	res := forecast.CycleResult{
		Candidates: []forecast.Candidate{{
			Class: forecast.ClassProjected, IsProjection: true,
			EntityCEI: "i|cl|shop|Pod|web-a|uid-a", Entity: "Container",
			Metric: "container_memory_working_set_bytes", SeriesKind: "gauge",
			BarValue: 486, Direction: "above", GraphVersion: "v",
			GeneratedAt: wAt, BasisAt: wAt, CrossAt: wAt.Add(13 * time.Minute),
			EarliestAt: wAt.Add(9 * time.Minute), LatestAt: wAt.Add(22 * time.Minute),
			Confidence: "moderate", ContextPoints: 240, HorizonSteps: 240,
			Cadence: 15 * time.Second,
			RegimeShift: &forecast.RegimeShift{
				AtIndex: 160, PreLevel: 100, PostLevel: 300, JumpFraction: 1.0, PostPoints: 80,
			},
		}},
	}
	v := BuildWarnings("v", "r", wAt, true, res, 0, ClockHealthRow{Ready: true}, nil, nil)
	cn := v.Warnings[0].Contamination
	if cn == nil {
		t.Fatal("the regime-shift flag must surface as a contamination caveat")
	}
	if cn.Kind != "undeclared-baseline-shift" || cn.NewRegimePts != 80 || cn.ShiftAgoSecs != 79*15 || cn.Note == "" {
		t.Errorf("contamination fields wrong: %+v", cn)
	}
	if b, _ := json.Marshal(v.Warnings[0]); !strings.Contains(string(b), `"contamination"`) {
		t.Errorf("contamination must serialize into the card JSON: %s", b)
	}
	// single-regime ⇒ no caveat, and omitted from the JSON
	res.Candidates[0].RegimeShift = nil
	v2 := BuildWarnings("v", "r", wAt, true, res, 0, ClockHealthRow{Ready: true}, nil, nil)
	if v2.Warnings[0].Contamination != nil {
		t.Error("a single-regime candidate must carry no contamination caveat")
	}
	if b, _ := json.Marshal(v2.Warnings[0]); strings.Contains(string(b), "contamination") {
		t.Error("contamination must be omitted from the JSON when absent")
	}
}

func TestBuildWarningsDisabledStatesTheGate(t *testing.T) {
	v := BuildWarnings("v", "v0.3.0", wAt, false, forecast.CycleResult{}, 0, ClockHealthRow{}, nil, nil)
	if v.Enabled || v.GateNote == "" {
		t.Fatal("a dark lane must state WHY (the gate rule), never imply quiet")
	}
	if len(v.Warnings) != 0 {
		t.Fatal("nothing may surface while the gate is unpassed")
	}
}

// The register audit (doc 09 M4 exit / doc 01 §5 / 11 M6): every string this
// surface can emit must keep the PROJECTED register — modal, banded, never a
// promise and never a causal claim. Scans the full JSON encoding of a
// populated view, so any future field with a bad register fails here.
func TestWarningsRegisterAudit(t *testing.T) {
	banned := []string{
		"will cross", "will happen", "is going to", "guaranteed", "definitely",
		"because", "caused", "causes", "due to", "leads to", "results in",
		"root cause", "explains", "explained by", "responsible for",
	}
	atRisk := func(phen, anchor string) []detect.AtRisk {
		return []detect.AtRisk{{CEIKey: "k", Phenomenon: phen, Related: "runs-on",
			Temporal: "T0+terminal", Why: "Eventual outcome"}}
	}
	views := []*WarningsView{
		BuildWarnings("v", "r", wAt, true, cycleWithCandidate(), 1, ClockHealthRow{Ready: true}, nil, atRisk),
		BuildWarnings("v", "r", wAt, false, forecast.CycleResult{}, 0, ClockHealthRow{}, nil, nil),
	}
	for _, v := range views {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(string(raw))
		for _, w := range banned {
			if strings.Contains(low, w) {
				t.Errorf("REGISTER VIOLATION %q in warnings surface payload", w)
			}
		}
	}
	// The mandatory marks must be present, not merely the absence of bad ones.
	if views[0].Warnings[0].Confidence == "" {
		t.Error("confidence class is mandatory")
	}
}

// The projected lane on the timeline (10 M5): each warning renders as a
// FORWARD-POINTING band span [earliest, latest]; an open far edge extends to
// the horizon and the note says band-shaped, never a point.
func TestTimelineProjectedLane(t *testing.T) {
	res := cycleWithCandidate()
	v := BuildWarnings("v", "r", wAt, true, res, 0, ClockHealthRow{Ready: true}, nil, nil)
	tl := BuildTimeline(wAt, nil, nil, v.Warnings, true)
	if len(tl.Projected) != 1 {
		t.Fatalf("one warning must yield one projected span: %+v", tl.Projected)
	}
	sp := tl.Projected[0]
	if sp.Class != "PROJECTED" || sp.Surface != "early-warning" {
		t.Errorf("projected lane must stay PROJECTED-class: %+v", sp)
	}
	if !sp.From.Equal(v.Warnings[0].EarliestAt) || !sp.To.Equal(v.Warnings[0].LatestAt) {
		t.Errorf("the span IS the [earliest, latest] band: %+v", sp)
	}
	if sp.From.Equal(sp.To) {
		t.Error("a projected span must be a band, never a point (doc 01 §3)")
	}
}
