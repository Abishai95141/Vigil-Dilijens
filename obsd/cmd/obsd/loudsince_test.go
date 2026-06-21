package main

import (
	"testing"
	"time"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/onset"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// annotateLoudSince joins a loud-but-unexplained (scope, metric) with its MEASURED onset time
// (doc 22 C2 follow-up). These cover the POPULATED path the live test found unexercised.
func TestAnnotateLoudSince_PopulatesMatchingOnset(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	scope := "i|cl|shop|Pod|web|uid"
	uv := &vapi.UnexplainedView{
		OpenCards: []unexplained.Finding{{
			Scope:      scope,
			LoudStates: []unexplained.LoudState{{Metric: "container_memory_working_set_bytes"}, {Metric: "other_metric"}},
		}},
	}
	ov := &vapi.OnsetView{Onsets: []onset.Onset{
		{EntityCEI: scope, Metric: "container_memory_working_set_bytes", At: at.Add(-2 * time.Minute), Direction: "up", StepZ: 5},
		{EntityCEI: scope, Metric: "container_memory_working_set_bytes", At: at, Direction: "up", StepZ: 7}, // later wins
		{EntityCEI: "i|cl|shop|Pod|other|uid2", Metric: "container_memory_working_set_bytes", At: at, Direction: "up", StepZ: 9},
	}}
	got := annotateLoudSince(uv, ov)
	if len(got.LoudSince) != 1 {
		t.Fatalf("expected 1 loud-since annotation (only the matching scope+metric), got %d", len(got.LoudSince))
	}
	a := got.LoudSince[0]
	if a.Scope != scope || a.Metric != "container_memory_working_set_bytes" || !a.OnsetAt.Equal(at) || a.StepZ != 7 {
		t.Errorf("annotation wrong (must pick the LATEST onset for the matched scope+metric): %+v", a)
	}
	// the source view must NOT be mutated (off-digest, shallow copy).
	if uv.LoudSince != nil {
		t.Error("annotateLoudSince mutated the source view — it must return a copy")
	}
}

func TestAnnotateLoudSince_NoOnsetViewUnchanged(t *testing.T) {
	uv := &vapi.UnexplainedView{OpenCards: []unexplained.Finding{{Scope: "s", LoudStates: []unexplained.LoudState{{Metric: "m"}}}}}
	if got := annotateLoudSince(uv, nil); got != uv || got.LoudSince != nil {
		t.Error("no onset view ⇒ the view is returned unchanged (honest: no onset lane, no annotation)")
	}
}

func TestAnnotateLoudSince_NoMatchNoAnnotation(t *testing.T) {
	uv := &vapi.UnexplainedView{OpenCards: []unexplained.Finding{{Scope: "scopeA", LoudStates: []unexplained.LoudState{{Metric: "m1"}}}}}
	ov := &vapi.OnsetView{Onsets: []onset.Onset{{EntityCEI: "scopeB", Metric: "m1"}, {EntityCEI: "scopeA", Metric: "m2"}}}
	if got := annotateLoudSince(uv, ov); len(got.LoudSince) != 0 {
		t.Errorf("no scope+metric match ⇒ no annotation, got %d", len(got.LoudSince))
	}
}
