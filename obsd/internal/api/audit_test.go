package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditChangesRouteServesView(t *testing.T) {
	view := NewAuditView(at, 2, 12, false, []AuditChangeRow{
		{AuditID: "a1", Verb: "patch", Resource: "deployments", Namespace: "erpnext", Name: "gunicorn", User: "ci", RoleCEI: "role:Deployment/gunicorn"},
		{AuditID: "a2", Verb: "update", Resource: "configmaps", Namespace: "erpnext", Name: "cfg", User: "admin", RoleUnresolved: true},
	})
	mux := http.NewServeMux()
	Register(mux, Providers{AuditChanges: func() *AuditView { return view }})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/audit-changes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got AuditView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Changes) != 2 || got.ChangesObserved != 2 || got.HypothesesStaged != 2 {
		t.Errorf("served view wrong: %+v", got)
	}
}

func TestAuditChangesRouteOffStateHonest(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Providers{}) // no AuditChanges provider
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/audit-changes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got AuditView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Error("nil provider must yield available=false (the honest OFF state)")
	}
}
