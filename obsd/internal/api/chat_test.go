package api

import (
	"strings"
	"testing"
	"time"
)

func cleanSnapshot() *ChatSnapshot {
	return &ChatSnapshot{
		GraphRelease: "v0.3.0",
		Matches: []ChatMatch{
			{Phenomenon: "PHEN_MEMORY_LEAK", Label: "Memory leak", Entity: "web-a", Quality: "degraded",
				AuthoredNote: "Slope > 0", Precursors: []string{"PHEN_OOM_KILL_CGROUP"}},
		},
		Warnings: []ChatWarning{
			{Entity: "leaker", Metric: "container_memory_working_set_bytes",
				EarliestAt: time.Date(2026, 6, 13, 21, 22, 0, 0, time.UTC),
				LatestAt:   time.Date(2026, 6, 13, 21, 27, 0, 0, time.UTC), Confidence: "moderate"},
		},
		UnexplainedN: 2, CoverageTierA: 22, CoverageNote: "11 full / 12 partial / 15 none phenomena observable.",
	}
}

// TestChatNeverImprovisesCause is the charter's sharpest chat fixture: a "why"
// question must NOT yield a generated cause. It either cites the AUTHORED relation
// (labelled) or declines — and always passes the register guard.
func TestChatNeverImprovisesCause(t *testing.T) {
	snap := cleanSnapshot()
	for _, q := range []string{
		"why did the memory leak happen?",
		"what caused web-a to fail?",
		"who is responsible for the memory leak?",
	} {
		r := AnswerChat(q, snap)
		if r.Refused {
			continue // a refusal is a safe outcome
		}
		low := strings.ToLower(r.Answer)
		// Must not generate a cause.
		for _, bad := range []string{"because", "caused by", "due to", "root cause"} {
			if strings.Contains(low, bad) {
				t.Errorf("q=%q produced causal vocabulary %q: %s", q, bad, r.Answer)
			}
		}
		// Must explicitly decline to assert a cause.
		if !strings.Contains(low, "do not assert") && !strings.Contains(low, "authored") {
			t.Errorf("q=%q should decline to assert a cause / cite authored relation: %s", q, r.Answer)
		}
	}
}

// TestChatForecastIsModalBanded: a "when will X cross" answer is always "projected
// to cross [band]", never "will cross".
func TestChatForecastRegister(t *testing.T) {
	r := AnswerChat("when will memory cross the limit?", cleanSnapshot())
	if r.Refused {
		t.Fatalf("should answer, not refuse: %s", r.RefusedReason)
	}
	low := strings.ToLower(r.Answer)
	if strings.Contains(low, "will cross") || strings.Contains(low, "will reach") {
		t.Errorf("forecast answer used certainty register: %s", r.Answer)
	}
	if !strings.Contains(low, "projected to cross") {
		t.Errorf("forecast answer must use the projected register: %s", r.Answer)
	}
}

// TestChatStatusIsMeasured: status answers report MEASURED matches, no forecast/cause.
func TestChatStatusRegister(t *testing.T) {
	r := AnswerChat("what is matching right now?", cleanSnapshot())
	if r.Refused || !strings.Contains(r.Answer, "MEASURED") {
		t.Errorf("status answer should be MEASURED: refused=%v %s", r.Refused, r.Answer)
	}
}

// TestChatGuardRefusesPollutedNote proves the guard is a real safety net: if the
// upstream authored note itself carried a banned register (a curation defect), the
// answer that would embed it is REFUSED, not surfaced.
func TestChatGuardRefusesPollutedNote(t *testing.T) {
	snap := cleanSnapshot()
	// Simulate a polluted authored note (should never happen, but the guard must
	// catch it if it does).
	snap.Matches[0].Precursors = nil
	snap.Matches[0].AuthoredNote = "the pod fails because the disk is full"
	r := AnswerChat("why did the memory leak happen?", snap)
	if !r.Refused {
		t.Errorf("guard should REFUSE an answer embedding a banned register, got: %s", r.Answer)
	}
	if !strings.Contains(r.RefusedReason, "charter guard") {
		t.Errorf("refusal should cite the charter guard: %s", r.RefusedReason)
	}
}

// TestChatUnknownIntentDeclines: an out-of-scope question gets an honest decline,
// never an improvised reply.
func TestChatUnknownIntentDeclines(t *testing.T) {
	r := AnswerChat("write me a poem about kubernetes", cleanSnapshot())
	if r.Refused {
		t.Fatal("unknown intent should decline gracefully, not refuse")
	}
	if !strings.Contains(strings.ToLower(r.Answer), "structured findings") {
		t.Errorf("unknown intent should explain what it can answer: %s", r.Answer)
	}
}

// TestChatEveryAnswerPassesGuard: across a broad question battery, no produced
// answer carries a banned register (the guard never has to fire on clean data).
func TestChatEveryAnswerPassesGuard(t *testing.T) {
	snap := cleanSnapshot()
	for _, q := range []string{
		"why", "what caused this", "when will it cross", "is it going to break",
		"what is wrong", "current status", "coverage", "how many entities are watched",
		"forecast", "predict the future", "what's happening",
	} {
		r := AnswerChat(q, snap)
		if r.Refused {
			continue
		}
		if vs := CharterViolations("chat", []byte(r.Answer)); len(vs) > 0 {
			t.Errorf("q=%q produced a register violation %v: %s", q, vs, r.Answer)
		}
	}
}
