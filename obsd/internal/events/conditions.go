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
	Detections []Detection     `yaml:"event_detections"`
}

// Detection is one AUTHORED detection condition (graph-robustness #2, G1): a curated
// mapping from a discrete Event reason (on an involved kind) to a phenomenon whose
// REQUIRED member that event satisfies. Unlike a Corroboration (an event corroborates
// a separate GAUGE phenomenon on a shared role), a Detection says "this event reason
// IS a required member of THIS phenomenon" — exactly as the KG already authors it (e.g.
// SIG_oomkilled_oomkilling is a required member of PHEN_OOM_KILL_CGROUP). So a real
// event produces a DEGRADED, MEASURED phenomenon finding: the event is a fact, the
// match is its deterministic consequence; the phenomenon's gauge/probe members are
// named unobservable on the discrete-event signal set (degrade-never-fabricate).
//
// This is NOT making an event a "trigger" or proving cause — it recognizes that one
// authored required member is observed. MemberSignal MUST be a role=required member of
// Phenomenon (validated by the caller against the loaded graph, like CorroborationTargets).
type Detection struct {
	Reason        string `yaml:"reason" json:"reason"`
	InvolvedKind  string `yaml:"involved_kind" json:"involvedKind"`
	Phenomenon    string `yaml:"phenomenon" json:"phenomenon"`      // the phenomenon this event partially constitutes
	MemberSignal  string `yaml:"member_signal" json:"memberSignal"` // the required member signal id the event satisfies
	TemporalOrder string `yaml:"temporal_order" json:"temporalOrder"`
	Why           string `yaml:"why" json:"why"` // AUTHORED, surfaced verbatim
	Author        string `yaml:"author" json:"author"`
	Version       string `yaml:"version" json:"version"`
	Status        string `yaml:"status" json:"status"`
}

// DetectionRef is one (phenomenon, member_signal) pair a caller validates against the
// loaded graph: the member must exist on the phenomenon AND be role=required.
type DetectionRef struct {
	Phenomenon   string
	MemberSignal string
	Reason       string
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

// LoadEventDetections reads the authored `event_detections` block from the overlay
// (the same file the corroboration conditions live in). Structural validation only;
// referential validity (MemberSignal is a role=required member of Phenomenon) is the
// caller's check against the loaded graph, exactly like CorroborationTargets. An
// absent block is not an error — it yields no detections (the lane simply produces no
// event-driven findings). AUTHORED content: every detection carries a reason, an
// involved_kind, a phenomenon, a member_signal, a why, and an author.
func LoadEventDetections(path string) ([]Detection, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f conditionsFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("events: parse detections %q: %w", path, err)
	}
	out := make([]Detection, 0, len(f.Detections))
	seen := map[string]bool{}
	for i := range f.Detections {
		d := f.Detections[i]
		if d.Author == "" {
			d.Author = f.Author
		}
		if d.Version == "" {
			d.Version = f.Version
		}
		if d.Status == "" {
			d.Status = f.Status
		}
		if strings.TrimSpace(d.Reason) == "" {
			return nil, fmt.Errorf("events: detection %d missing reason", i)
		}
		if strings.TrimSpace(d.InvolvedKind) == "" {
			return nil, fmt.Errorf("events: detection %q missing involved_kind", d.Reason)
		}
		if strings.TrimSpace(d.Phenomenon) == "" {
			return nil, fmt.Errorf("events: detection %q missing phenomenon", d.Reason)
		}
		if strings.TrimSpace(d.MemberSignal) == "" {
			return nil, fmt.Errorf("events: detection %q missing member_signal", d.Reason)
		}
		if strings.TrimSpace(d.Why) == "" {
			return nil, fmt.Errorf("events: detection %q missing why (surfaced verbatim, doc 02 §3.6)", d.Reason)
		}
		if strings.TrimSpace(d.Author) == "" {
			return nil, fmt.Errorf("events: detection %q missing author provenance (doc 02 §3.6)", d.Reason)
		}
		k := condKey(d.Reason, d.InvolvedKind)
		if seen[k] {
			return nil, fmt.Errorf("events: duplicate detection for reason %q kind %q", d.Reason, d.InvolvedKind)
		}
		seen[k] = true
		out = append(out, d)
	}
	return out, nil
}

// DetectionRefs returns the (phenomenon, member_signal, reason) triples a caller
// validates against the loaded graph (member exists on phenomenon AND is required).
func DetectionRefs(dets []Detection) []DetectionRef {
	out := make([]DetectionRef, 0, len(dets))
	for _, d := range dets {
		out = append(out, DetectionRef{Phenomenon: d.Phenomenon, MemberSignal: d.MemberSignal, Reason: d.Reason})
	}
	return out
}
