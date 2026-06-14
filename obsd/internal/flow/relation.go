package flow

import (
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v3"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// The cross-service relation's two phenomenon roles, as curated in the ontology
// (doc 15 Phase C). Surfaced verbatim; the actual MEASURED trigger is the callee's
// own finding, the actual MEASURED edge is the observed flow.
const (
	PhenUpstreamDegradation = "PHEN_UPSTREAM_DEGRADATION"
	PhenDownstreamImpact    = "PHEN_DOWNSTREAM_IMPACT"
)

// Relation is the ONE authored cross-service phenomenon_relation surfaced verbatim
// as the AUTHORED "why". As of doc 15 Phase C it is CURATED into the released
// ontology graph (RelationFromGraph) rather than read from the experimental overlay
// file; LoadRelation remains for experimental/spike runs and as an override.
type Relation struct {
	Trigger    string `yaml:"trigger"`    // PHEN_UPSTREAM_DEGRADATION
	Downstream string `yaml:"downstream"` // PHEN_DOWNSTREAM_IMPACT
	Role       string `yaml:"role"`
	Temporal   string `yaml:"temporal_order"`
	Why        string `yaml:"why"`
	Author     string `yaml:"author"`
	Version    string `yaml:"version"`
	Status     string `yaml:"status"`
}

type relationFile struct {
	Relation Relation `yaml:"phenomenon_relation"`
}

// LoadRelation reads the authored relation from a YAML file (experimental overlay
// or an explicit override). Phase C prefers RelationFromGraph.
func LoadRelation(path string) (Relation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Relation{}, err
	}
	var rf relationFile
	if err := yaml.Unmarshal(b, &rf); err != nil {
		return Relation{}, err
	}
	r := rf.Relation
	if r.Trigger == "" || r.Downstream == "" || r.Why == "" {
		return Relation{}, fmt.Errorf("flow: relation %s missing trigger/downstream/why", path)
	}
	return r, nil
}

// RelationFromGraph builds the cross-service Relation from the CURATED ontology
// (doc 15 Phase C): the phenomenon_relation edge PHEN_UPSTREAM_DEGRADATION ->
// PHEN_DOWNSTREAM_IMPACT, surfaced verbatim with the graph's release/version as its
// provenance. This is the governed source — obsd reads the relation from the released
// graph, not the experimental file. Returns ok=false if the curated relation is
// absent (honest degradation: the cascade lane then stays off, stated).
func RelationFromGraph(g *graph.Graph) (Relation, bool) {
	if g == nil {
		return Relation{}, false
	}
	up := g.Phenomena[PhenUpstreamDegradation]
	if up == nil {
		return Relation{}, false
	}
	for _, r := range up.Relations {
		if r.TargetID != PhenDownstreamImpact || r.Why == "" {
			continue
		}
		return Relation{
			Trigger:    PhenUpstreamDegradation,
			Downstream: r.TargetID,
			Role:       r.Role,
			Temporal:   r.TemporalOrder,
			Why:        r.Why,
			Author:     "vigil-engineering",
			Version:    graphVersionLabel(g),
			Status:     "curated",
		}, true
	}
	return Relation{}, false
}

// graphVersionLabel renders the relation's version provenance: the release name
// when loaded via a manifest, else the content-hash pin (truncated for display).
func graphVersionLabel(g *graph.Graph) string {
	if g.Release != "" {
		return g.Release
	}
	v := g.Version
	if len(v) > 19 { // "sha256:" + 12 hex chars
		v = v[:19]
	}
	return v
}
