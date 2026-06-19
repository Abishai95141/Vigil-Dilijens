package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func govItems() []GovernanceItem {
	return []GovernanceItem{
		// operational stray mapping → actionable → enqueued for review
		{ID: "c1", Kind: "edge", Status: "candidate", Subject: "stray:x ~> entity:y", Relation: "associated-with", Source: "dgx-agent",
			Evidence: []GovernanceEvidence{{Kind: "context:stray-metric", Ref: "stray:x"}}, Actionable: true},
		// trace topology, already decided → the audit trail
		{ID: "c2", Kind: "edge", Status: "promoted", Subject: "trace-call:a->b", Relation: "topology", Source: "trace", DecidedBy: "alice"},
		// pure k8s object-metadata stray → NOT actionable → counted but suppressed from review
		{ID: "c3", Kind: "node", Status: "candidate", Subject: "stray:kube_replicaset_status_replicas/abc", Source: "cei-fallback", Actionable: false},
	}
}

func TestGovernanceViewSplitsPendingAndDecided(t *testing.T) {
	view := NewGovernanceView(at, "v0.8.0", govItems())
	mux := http.NewServeMux()
	Register(mux, Providers{Governance: func() *GovernanceView { return view }})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/governance")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got GovernanceView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// c1 (actionable) → pending; c2 (decided) → trail; c3 (object-metadata) → suppressed, counted.
	if !got.Available || len(got.Pending) != 1 || len(got.Decided) != 1 {
		t.Errorf("split wrong: pending=%d decided=%d", len(got.Pending), len(got.Decided))
	}
	if got.SuppressedMetadata != 1 || got.SuppressedNote == "" {
		t.Errorf("metadata suppression wrong: suppressed=%d note=%q", got.SuppressedMetadata, got.SuppressedNote)
	}
	if got.Counts["candidate"] != 2 || got.Counts["promoted"] != 1 {
		t.Errorf("counts wrong (object-metadata must still be COUNTED): %+v", got.Counts)
	}
}

func TestGovernanceDecideRoute(t *testing.T) {
	// a fake decide that mimics the backend: refuses a missing human, returns an overlay on promote.
	decide := func(req GovernanceDecisionRequest) GovernanceDecisionResult {
		if req.DecidedBy == "" {
			return GovernanceDecisionResult{OK: false, CandidateID: req.CandidateID, Message: "a named human is required"}
		}
		if req.Decision == "promote" {
			return GovernanceDecisionResult{OK: true, CandidateID: req.CandidateID, Status: "promoted", OverlayYAML: "promotion:\n  author: " + req.DecidedBy + "\n", Message: "promoted"}
		}
		return GovernanceDecisionResult{OK: true, CandidateID: req.CandidateID, Status: "rejected", Message: "rejected"}
	}
	mux := http.NewServeMux()
	Register(mux, Providers{GovernanceDecide: decide})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	post := func(body string) (*http.Response, GovernanceDecisionResult) {
		resp, err := http.Post(srv.URL+"/api/governance/decide", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		var r GovernanceDecisionResult
		_ = json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		return resp, r
	}

	// promote with a named human → 200 + overlay.
	resp, r := post(`{"candidateId":"c1","decision":"promote","decidedBy":"alice","note":"ok"}`)
	if resp.StatusCode != 200 || !r.OK || r.Status != "promoted" || r.OverlayYAML == "" {
		t.Errorf("promote wrong: code=%d %+v", resp.StatusCode, r)
	}
	// missing human → 400 + ok=false.
	resp, r = post(`{"candidateId":"c1","decision":"promote","decidedBy":"","note":"x"}`)
	if resp.StatusCode != 400 || r.OK {
		t.Errorf("missing-human must be 400/ok=false: code=%d %+v", resp.StatusCode, r)
	}
}

func TestGovernanceOffState(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Providers{})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/governance")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got GovernanceView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Error("nil provider must yield available=false")
	}
}
