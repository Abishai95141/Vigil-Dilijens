package flow

import (
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v3"
)

// Relation is the ONE authored cross-service phenomenon_relation, read by this
// package's OWN reader. The production overlay loader discards phenomenon_relation
// blocks, so this never enters the bound graph — it is surfaced verbatim, only by
// the spike, as the AUTHORED "why".
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

// LoadRelation reads the experimental authored relation from a YAML file.
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
