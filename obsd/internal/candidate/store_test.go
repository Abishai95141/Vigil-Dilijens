package candidate

import (
	"testing"
	"time"
)

// fixed injected clock — the package must never read the wall clock.
var t0 = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutGetRoundTrip(t *testing.T) {
	s := newStore(t)
	c := Candidate{
		Kind:    KindNode,
		Subject: "cei:pod/unmapped-xyz",
		Payload: map[string]any{"metric": "weird_exporter_widgets_total"},
		Evidence: []EvidenceRef{
			{Kind: "measured-series", Ref: "stream:abc", Detail: "10 samples"},
		},
		Lineage: Lineage{Source: "cei-fallback", Method: "discrete-label-intersection", GraphVersion: "v0.8.0"},
	}
	id, err := s.Put(t0, c)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, found, err := s.Get(id)
	if err != nil || !found {
		t.Fatalf("Get(%s): found=%v err=%v", id, found, err)
	}
	if got.Status != StatusCandidate {
		t.Errorf("new candidate status = %q, want %q", got.Status, StatusCandidate)
	}
	if !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t0) {
		t.Errorf("timestamps not the injected clock: created=%v updated=%v want %v", got.CreatedAt, got.UpdatedAt, t0)
	}
	if got.Subject != c.Subject || got.Lineage.Source != "cei-fallback" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Ref != "stream:abc" {
		t.Errorf("evidence not round-tripped: %+v", got.Evidence)
	}
}

func TestPruneNonOperational(t *testing.T) {
	s := newStore(t)
	put := func(subject string) string {
		id, err := s.Put(t0, Candidate{Kind: KindNode, Subject: subject, Lineage: Lineage{Source: "cei-fallback"}})
		if err != nil {
			t.Fatalf("Put(%q): %v", subject, err)
		}
		return id
	}
	// non-operational strays → pruned
	put("stray:go_gc_duration_seconds/aaaa")
	put("stray:apiserver_request_total/bbbb")
	put("stray:apiserver_request_total/bbbb ~> i|c|n|Pod|x|u") // its edge prunes too
	put("stray:workqueue_depth/cccc")
	// operational stray → kept
	opID := put("stray:kube_persistentvolume_capacity_bytes/dddd ~> i|c|n|PVC|x|u")
	// a non-stray candidate (trace topology) → kept (StrayMetricFromSubject = false)
	traceID := put("trace-call:a->b")
	// a DECIDED non-operational stray → NEVER pruned (audit trail)
	decID := put("stray:etcd_requests_total/eeee")
	if err := s.Decide(t0, decID, StatusRejected, "alice", "not a real signal"); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	byClass, err := s.PruneNonOperational()
	if err != nil {
		t.Fatalf("PruneNonOperational: %v", err)
	}
	// runtime(1) + control-plane(1 node + 1 edge) + client(1) = 4 deleted, decided one untouched.
	total := 0
	for _, n := range byClass {
		total += n
	}
	if total != 4 {
		t.Errorf("pruned %d (byClass=%v), want 4 (decided + operational + non-stray kept)", total, byClass)
	}
	remaining, _ := s.List(Filter{})
	keep := map[string]bool{opID: true, traceID: true, decID: true}
	if len(remaining) != len(keep) {
		t.Fatalf("remaining = %d, want %d", len(remaining), len(keep))
	}
	for _, c := range remaining {
		if !keep[c.ID] {
			t.Errorf("unexpected survivor: %s (%s)", c.ID, c.Subject)
		}
	}
}

func TestPutDeterministicIDAndIdempotentUpsert(t *testing.T) {
	s := newStore(t)
	c := Candidate{Kind: KindNode, Subject: "cei:x", Payload: map[string]any{"a": 1, "b": 2}}
	id1, err := s.Put(t0, c)
	if err != nil {
		t.Fatal(err)
	}
	// Same content, later clock, reordered map: same id, one row, created_at stable, updated_at advances.
	c2 := Candidate{Kind: KindNode, Subject: "cei:x", Payload: map[string]any{"b": 2, "a": 1}}
	t1 := t0.Add(time.Hour)
	id2, err := s.Put(t1, c2)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("content id not deterministic: %s vs %s", id1, id2)
	}
	all, err := s.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("idempotent put created %d rows, want 1", len(all))
	}
	got := all[0]
	if !got.CreatedAt.Equal(t0) {
		t.Errorf("created_at moved on re-put: %v want %v", got.CreatedAt, t0)
	}
	if !got.UpdatedAt.Equal(t1) {
		t.Errorf("updated_at did not advance: %v want %v", got.UpdatedAt, t1)
	}
}

// The agent rewords its free-text rationale on every exploration cycle (LLM output is
// non-deterministic). Re-proposing the SAME edge with a reworded rationale must dedup to
// ONE row — the rationale is "shown for context but discarded at promotion" and is NOT
// part of proposal identity. Regression for the queue-flooding bug (47/92 pending were
// rationale-rewording duplicates of the same stray->entity edges).
func TestContentIDIgnoresModelRationale(t *testing.T) {
	s := newStore(t)
	mk := func(rationale string) Candidate {
		return Candidate{
			Kind:     KindEdge,
			Subject:  "stray:kube_persistentvolume_status_phase/abc ~> entity:i|pvc|erpnext|uid",
			Relation: "associated-with",
			Payload:  map[string]any{"proposed": true, "strayMetric": "kube_persistentvolume_status_phase", "rationale": rationale},
			Evidence: []EvidenceRef{{Kind: "context:stray-metric", Ref: "stray:kube_persistentvolume_status_phase/abc", Detail: "labels{persistentvolume=pvc-uid}"}},
			Lineage:  Lineage{Source: "dgx-agent", Method: "llm:custom", GraphVersion: "v0.10.0"},
		}
	}
	id1, err := s.Put(t0, mk("label persistentvolume=pvc-uid matches the PVC entity with the same UID"))
	if err != nil {
		t.Fatal(err)
	}
	// Same edge, different LLM wording on the next cycle.
	id2, err := s.Put(t0.Add(30*time.Minute), mk("stray metric labels reference persistentvolume=pvc-uid which matches the PVC entity UID suffix"))
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("reworded rationale produced a NEW id (dedup defeated): %s vs %s", id1, id2)
	}
	all, err := s.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("rationale rewording created %d rows, want 1 (the duplication bug)", len(all))
	}
	// But a change to a STRUCTURAL payload key (real identity) must still produce a new id.
	c3 := mk("any rationale")
	c3.Payload["strayMetric"] = "kube_persistentvolume_capacity_bytes"
	id3, err := s.Put(t0.Add(time.Hour), c3)
	if err != nil {
		t.Fatal(err)
	}
	if id3 == id1 {
		t.Fatal("a structural payload change collapsed into the same id — over-stripped identity")
	}
}

func TestSetStatusPreservedAcrossReput(t *testing.T) {
	s := newStore(t)
	id, err := s.Put(t0, Candidate{Kind: KindNode, Subject: "cei:y"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(t0.Add(time.Minute), id, StatusShadow, "superseded"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	// Re-proposing the same content must NOT resurrect it to candidate.
	if _, err := s.Put(t0.Add(2*time.Minute), Candidate{Kind: KindNode, Subject: "cei:y"}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusShadow {
		t.Errorf("re-put clobbered lifecycle status: got %q, want %q", got.Status, StatusShadow)
	}
	if got.Reason != "superseded" {
		t.Errorf("system reason lost: %q", got.Reason)
	}
}

func TestSetStatusMissingIsError(t *testing.T) {
	s := newStore(t)
	if err := s.SetStatus(t0, "cand:nope", StatusRejected, "x"); err == nil {
		t.Fatal("SetStatus on missing id should error")
	}
}

func TestListFilters(t *testing.T) {
	s := newStore(t)
	mustPut(t, s, Candidate{Kind: KindNode, Subject: "n1"})
	eid := mustPut(t, s, Candidate{Kind: KindEdge, Subject: "a->b", Relation: "associated-with"})
	mustPut(t, s, Candidate{Kind: KindMember, Subject: "PHEN_X/sig"})
	if err := s.SetStatus(t0, eid, StatusRejected, "evidence<k"); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.List(Filter{Kind: KindNode}); len(got) != 1 {
		t.Errorf("filter by kind=node: got %d, want 1", len(got))
	}
	if got, _ := s.List(Filter{Status: StatusCandidate}); len(got) != 2 {
		t.Errorf("filter by status=candidate: got %d, want 2", len(got))
	}
	if got, _ := s.List(Filter{Status: StatusRejected, Kind: KindEdge}); len(got) != 1 {
		t.Errorf("combined filter: got %d, want 1", len(got))
	}
}

// THE STRUCTURAL CHARTER GUARD: a causal edge can never be staged as an
// authoritative edge; it must use KindCausalHypothesis (direction-free).
func TestEdgeRejectsCausalRelation(t *testing.T) {
	s := newStore(t)
	for _, rel := range []string{"causes", "leads-to", "because-of", ""} {
		if _, err := s.Put(t0, Candidate{Kind: KindEdge, Subject: "a->b", Relation: rel}); err == nil {
			t.Errorf("edge relation %q should be rejected (not structural)", rel)
		}
	}
	for _, rel := range []string{"topology", "associated-with", "topo-adjacent"} {
		if _, err := s.Put(t0, Candidate{Kind: KindEdge, Subject: "a->b", Relation: rel}); err != nil {
			t.Errorf("structural edge relation %q should be accepted: %v", rel, err)
		}
	}
}

func TestCausalHypothesisIsDirectionFree(t *testing.T) {
	s := newStore(t)
	if _, err := s.Put(t0, Candidate{Kind: KindCausalHypothesis, Subject: "change->incident", Relation: "co-occurrence"}); err != nil {
		t.Errorf("co-occurrence hypothesis should be accepted: %v", err)
	}
	if _, err := s.Put(t0, Candidate{Kind: KindCausalHypothesis, Subject: "change->incident", Relation: "causes"}); err == nil {
		t.Error("a causal_hypothesis asserting 'causes' must be rejected — hypotheses are co-occurrence only")
	}
}

func TestUnknownKindAndEmptySubjectRejected(t *testing.T) {
	s := newStore(t)
	if _, err := s.Put(t0, Candidate{Kind: "made-up", Subject: "x"}); err == nil {
		t.Error("unknown kind should be rejected")
	}
	if _, err := s.Put(t0, Candidate{Kind: KindNode, Subject: "   "}); err == nil {
		t.Error("empty subject should be rejected")
	}
}

func mustPut(t *testing.T, s *Store, c Candidate) string {
	t.Helper()
	id, err := s.Put(t0, c)
	if err != nil {
		t.Fatalf("Put(%+v): %v", c, err)
	}
	return id
}
