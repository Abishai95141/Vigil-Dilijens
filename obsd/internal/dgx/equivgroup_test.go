package dgx_test

import (
	"context"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

// equivContext: an operational stray + a small equivalence-group catalog the agent may map
// it into. The stray ref is the canonical "stray:<metric>/<key>" shape the live loop mints.
func equivContext() dgx.Context {
	return dgx.Context{
		GraphVersion: "v0.9.0",
		Observations: []dgx.Observation{
			{Ref: "stray:redis_connected_clients/abc", Kind: "stray-metric", Detail: "unmapped metric redis_connected_clients labels{pod=redis-0}"},
			{Ref: "stray:redis_blocked_clients/def", Kind: "stray-metric", Detail: "unmapped metric redis_blocked_clients labels{pod=redis-0}"},
		},
		KnownGroups: map[string]string{
			"EQG_WORKING_SET": "k8s.container.memory.working_set",
		},
	}
}

// The agent maps an operational stray into a NEW equivalence group, grounded on the stray
// observation. The deterministic capture SUPPORT over the visible strays is recorded.
func TestAgentEquivGroupNewGroup(t *testing.T) {
	resp := `{"proposals":[
	 {"kind":"equiv_group","subject":"redis_connected_clients","group":"EQG_REDIS_CLIENTS","canonical":"db.redis.connected_clients","label":"Redis connected clients","pattern":"^redis_.*clients$","evidence":["stray:redis_connected_clients/abc"],"rationale":"redis exporter clients gauge"}
	]}`
	ag := dgx.New(dgx.NewStaticProvider("fake", resp), dgx.DefaultParams)
	cands, rep, err := ag.Propose(context.Background(), equivContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("accepted %d, want 1: rejected=%+v", len(cands), rep.Rejected)
	}
	c := cands[0]
	if c.Kind != candidate.KindEquivGroup {
		t.Fatalf("kind = %s, want equiv_group", c.Kind)
	}
	if c.Subject != candidate.EquivGroupSubject("redis_connected_clients") {
		t.Errorf("subject = %q", c.Subject)
	}
	p, err := candidate.ParseEquivGroupPayload(c.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if p.NewGroup == nil || p.NewGroup.ID != "EQG_REDIS_CLIENTS" || p.NewGroup.CanonicalOTel != "db.redis.connected_clients" {
		t.Errorf("new_group = %+v", p.NewGroup)
	}
	// The pattern "^redis_.*clients$" captures BOTH visible redis strays — the support set.
	if len(p.CaptureSample) != 2 {
		t.Errorf("capture sample = %v, want both redis strays", p.CaptureSample)
	}
	// The candidate is grounded on the stray observation.
	if len(c.Evidence) != 1 || c.Evidence[0].Kind != "context:stray-metric" {
		t.Errorf("evidence = %+v", c.Evidence)
	}
}

// The agent maps a stray into an EXISTING group (cited group id is in the catalog).
func TestAgentEquivGroupExisting(t *testing.T) {
	ctx := dgx.Context{
		GraphVersion: "v0.9.0",
		Observations: []dgx.Observation{
			{Ref: "stray:myapp_working_set_bytes/p1", Kind: "stray-metric", Detail: "unmapped metric myapp_working_set_bytes"},
		},
		KnownGroups: map[string]string{"EQG_WORKING_SET": "k8s.container.memory.working_set"},
	}
	resp := `{"proposals":[
	 {"kind":"equiv_group","subject":"myapp_working_set_bytes","group":"EQG_WORKING_SET","pattern":"^myapp_working_set_bytes$","evidence":["stray:myapp_working_set_bytes/p1"],"rationale":"myapp working set under a custom name"}
	]}`
	ag := dgx.New(dgx.NewStaticProvider("fake", resp), dgx.DefaultParams)
	cands, rep, err := ag.Propose(context.Background(), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("accepted %d, want 1: rejected=%+v", len(cands), rep.Rejected)
	}
	p, _ := candidate.ParseEquivGroupPayload(cands[0].Payload)
	if p.GroupID != "EQG_WORKING_SET" || p.NewGroup != nil {
		t.Errorf("want existing-group mapping, got group=%q new=%+v", p.GroupID, p.NewGroup)
	}
}

// Charter/grounding rejections: an equiv_group that cites no stray, whose pattern misses its
// own metric, or (new group) omits the canonical, is rejected before staging.
func TestAgentEquivGroupRejections(t *testing.T) {
	cases := map[string]string{
		"no stray cited":      `{"proposals":[{"kind":"equiv_group","subject":"x","group":"EQG_WORKING_SET","pattern":"^x$","evidence":[],"rationale":"r"}]}`,
		"pattern misses self": `{"proposals":[{"kind":"equiv_group","subject":"redis_connected_clients","group":"EQG_WORKING_SET","pattern":"^mysql_.*$","evidence":["stray:redis_connected_clients/abc"],"rationale":"r"}]}`,
		"new missing canon":   `{"proposals":[{"kind":"equiv_group","subject":"redis_connected_clients","group":"EQG_NOVEL","pattern":"^redis_connected_clients$","evidence":["stray:redis_connected_clients/abc"],"rationale":"r"}]}`,
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			ag := dgx.New(dgx.NewStaticProvider("fake", resp), dgx.Params{MinEvidence: 1, MaxProposals: 20, MaxContextChars: 6000})
			cands, rep, err := ag.Propose(context.Background(), equivContext())
			if err != nil {
				t.Fatal(err)
			}
			if len(cands) != 0 {
				t.Errorf("accepted %d, want 0 (rejected); report=%+v", len(cands), rep.Rejected)
			}
		})
	}
}
