package governance

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

func TestFromUnexplainedCandidates(t *testing.T) {
	cands := []unexplained.CandidateReport{
		{Metrics: []string{"foo_bytes"}, EntityKind: "Container", Windows: 12, Entities: []string{"a", "b"}},
	}
	items := FromUnexplainedCandidates(cands)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if items[0].Source != SourceUnexplained || items[0].Recurrence != 12 {
		t.Errorf("unexpected item: %+v", items[0])
	}
	if items[0].ProposedAction == "" {
		t.Error("intake item must propose an action for the curator")
	}
}

func TestFromCoverageGapsAggregatesByMetric(t *testing.T) {
	rep := &binding.CoverageReport{
		UnboundedWorkloads: []string{
			"pod-a container_memory_working_set_bytes: unbounded",
			"pod-b container_memory_working_set_bytes: unbounded",
			"pod-c container_cpu_usage: unbounded",
		},
		Validation: binding.QASummary{Findings: []string{"FAILED THR_X cAdvisor: scope mismatch"}},
	}
	items := FromCoverageGaps(rep)
	// 2 metrics aggregated + 1 QA failure = 3 items.
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d: %+v", len(items), items)
	}
	// The working_set metric should aggregate 2 workloads.
	var found bool
	for _, it := range items {
		if it.Signature == "unbounded:container_memory_working_set_bytes" && it.Recurrence == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected aggregated working_set unbounded item (recurrence 2); items: %+v", items)
	}
}

func TestTriageDedupAndOrder(t *testing.T) {
	unexp := FromUnexplainedCandidates([]unexplained.CandidateReport{
		{Metrics: []string{"m"}, EntityKind: "Container", Windows: 5, Entities: []string{"a"}},
	})
	fals := FromFalsification([]FalsificationDiscrepancy{
		{Element: "PHEN_X", Claim: "T0- precursor", Evidence: "corpus/c1", Occurrend: 3},
	})
	cov := FromCoverageGaps(&binding.CoverageReport{UnboundedWorkloads: []string{"p m: unbounded"}})
	// Add a duplicate unexplained item to test dedup.
	dupe := FromUnexplainedCandidates([]unexplained.CandidateReport{
		{Metrics: []string{"m"}, EntityKind: "Container", Windows: 9, Entities: []string{"a"}},
	})

	out := Triage(fals, unexp, cov, dupe)
	// Falsification ranks first (most urgent).
	if out[0].Source != SourceFalsification {
		t.Errorf("falsification should rank first, got %s", out[0].Source)
	}
	// Dedup: the duplicate unexplained signature appears once.
	count := 0
	for _, it := range out {
		if it.Source == SourceUnexplained {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected deduped unexplained item, got %d", count)
	}
	st := Stats(out)
	if st.Total != len(out) {
		t.Errorf("stats total mismatch")
	}
}

func TestIntakeNoCausalVocabulary(t *testing.T) {
	// Charter: intake proposes; it never authors a cause. The summary must not carry
	// causal vocabulary (because/caused/triggers/...).
	items := FromUnexplainedCandidates([]unexplained.CandidateReport{
		{Metrics: []string{"m"}, EntityKind: "Container", Windows: 5, Entities: []string{"a"}},
	})
	banned := []string{"because", "caused", "causes", "triggers", "due to", "results in"}
	for _, it := range items {
		for _, w := range banned {
			if containsFold(it.Summary, w) {
				t.Errorf("intake summary carries causal vocabulary %q: %s", w, it.Summary)
			}
		}
	}
}

func containsFold(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := toLower(s), toLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
