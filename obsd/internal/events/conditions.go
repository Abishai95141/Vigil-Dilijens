package events

import (
	"fmt"
	"os"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// RoleCorroborating is the only legal role for an event condition: an event is
// corroborating evidence on a shared role, never a trigger, never proof of cause.
const RoleCorroborating = "corroborating"

// conditionsFile is the on-disk shape of an authored event-conditions overlay. The
// file-level author/version/status are provenance defaults applied to any condition
// that omits its own (so the common case stays terse, like the cross-service
// experimental relation file).
type conditionsFile struct {
	Overlay    string          `yaml:"overlay"`
	Version    string          `yaml:"version"`
	Author     string          `yaml:"author"`
	Status     string          `yaml:"status"`
	Conditions []Corroboration `yaml:"event_conditions"`
}

// LoadEventConditions reads the authored corroboration conditions from a YAML
// overlay file (the v3 T-C experimental overlay, or an explicit override). It
// validates structural completeness only — referential validity (that a non-empty
// `corroborates` names a real phenomenon in the loaded graph) is checked against the
// graph by the caller, exactly as flow.RelationFromGraph guards the cross-service
// relation. AUTHORED content: every condition must carry a reason, a why, and an
// author (the authored-knowledge discipline, doc 02 §3.6).
func LoadEventConditions(path string) ([]Corroboration, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f conditionsFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("events: parse conditions %q: %w", path, err)
	}
	if len(f.Conditions) == 0 {
		return nil, fmt.Errorf("events: conditions file %q declares no event_conditions", path)
	}
	out := make([]Corroboration, 0, len(f.Conditions))
	seen := map[string]bool{}
	for i := range f.Conditions {
		c := f.Conditions[i]
		if c.Author == "" {
			c.Author = f.Author
		}
		if c.Version == "" {
			c.Version = f.Version
		}
		if c.Status == "" {
			c.Status = f.Status
		}
		if strings.TrimSpace(c.Reason) == "" {
			return nil, fmt.Errorf("events: condition %d missing reason", i)
		}
		if strings.TrimSpace(c.InvolvedKind) == "" {
			return nil, fmt.Errorf("events: condition %q missing involved_kind", c.Reason)
		}
		if strings.TrimSpace(c.Why) == "" {
			return nil, fmt.Errorf("events: condition %q missing why (surfaced verbatim, doc 02 §3.6)", c.Reason)
		}
		if strings.TrimSpace(c.Author) == "" {
			return nil, fmt.Errorf("events: condition %q missing author provenance (doc 02 §3.6)", c.Reason)
		}
		if c.Role != "" && c.Role != RoleCorroborating {
			return nil, fmt.Errorf("events: condition %q role %q is illegal — an event is %q, never a trigger", c.Reason, c.Role, RoleCorroborating)
		}
		k := condKey(c.Reason, c.InvolvedKind)
		if seen[k] {
			return nil, fmt.Errorf("events: duplicate condition for reason %q kind %q", c.Reason, c.InvolvedKind)
		}
		seen[k] = true
		out = append(out, c)
	}
	return out, nil
}

// CorroborationTargets returns the distinct non-empty `corroborates` phenomenon ids
// the conditions reference — the set a caller validates against the loaded graph.
func CorroborationTargets(conds []Corroboration) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range conds {
		if c.Corroborates != "" && !seen[c.Corroborates] {
			seen[c.Corroborates] = true
			out = append(out, c.Corroborates)
		}
	}
	return out
}
