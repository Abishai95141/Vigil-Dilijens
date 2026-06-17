package api

import "time"

// AuthoredRelationsView is the curated causal map surfaced to a synthesizing agent
// (v3.1): every directed phenomenon->phenomenon relation the ontology AUTHORS, plus
// the phenomenon vocabulary. It is class AUTHORED — the ONLY legitimate causal basis.
// It carries NO measurement and NO live state; it is the static, signed knowledge an
// agent uses to tell an authored cause from a mere co-occurrence. Built from the same
// graph data the validate_claim referee checks against, so the two never disagree.
type AuthoredRelationsView struct {
	Class       string              `json:"class"` // "AUTHORED"
	GeneratedAt time.Time           `json:"generatedAt"`
	Count       int                 `json:"count"`
	Relations   []AuthoredLink      `json:"relations"` // directed Src->Dst + verbatim Why
	Phenomena   map[string][]string `json:"phenomena"` // phenomenon id -> human alias tails
	Note        string              `json:"note"`
}

// BuildAuthoredRelations assembles the curated causal map from the referee's static
// ground (the same phen aliases + authored phenomenon_relation links). Pure; a nil
// graph yields an honest empty map rather than an implied "no causal knowledge".
func BuildAuthoredRelations(phen map[string][]string, links []AuthoredLink, now time.Time) *AuthoredRelationsView {
	if links == nil {
		links = []AuthoredLink{}
	}
	if phen == nil {
		phen = map[string][]string{}
	}
	return &AuthoredRelationsView{
		Class:       "AUTHORED",
		GeneratedAt: now,
		Count:       len(links),
		Relations:   links,
		Phenomena:   phen,
		Note: "AUTHORED — the curated causal map (the ONLY legitimate causal basis). A co-occurrence is " +
			"an authored CAUSE only if a directed relation for it appears here; otherwise it is coincidence. " +
			"Vigil never invents a relation that is not authored. Use Phenomena to map ids in a chain/finding to meaning.",
	}
}
