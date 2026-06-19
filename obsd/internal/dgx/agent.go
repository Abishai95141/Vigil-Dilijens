package dgx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// Observation is one read-only MEASURED fact the agent may reason over. Ref is the
// stable identifier the model must cite (the grounding key); Detail is the fact.
type Observation struct {
	Ref    string
	Kind   string
	Detail string
}

// Context is the read-only bundle handed to the agent. The agent may cite ONLY refs
// that appear in Observations (grounding) and may reference only ValidEntities for
// entity edges.
type Context struct {
	GraphVersion  string
	Observations  []Observation
	ValidEntities map[string]bool
	// KnownGroups is the equivalence-group catalog (id → canonical OTel) the agent maps
	// strays into (doc 21 §5). A stray whose meaning matches a listed group is proposed as
	// an `equiv_group` into that id; a stray matching none may be proposed as a NEW EQG_ id.
	// Read-only; never a write target.
	KnownGroups map[string]string
}

func (c Context) refIndex() map[string]Observation {
	m := make(map[string]Observation, len(c.Observations)+len(c.ValidEntities))
	for _, o := range c.Observations {
		m[o.Ref] = o
	}
	// Entity keys are citable too, as "entity:<key>". An edge that maps an observation to
	// a real entity is then grounded on BOTH ends — the entity binding is a cited MEASURED
	// fact, not free text — which makes the prompt's "an edge may reference these"
	// enforceable (closing a latent gap a review flagged) and lets the stray-mapping path
	// cite the workload it proposes a stray belongs to.
	for k := range c.ValidEntities {
		ref := "entity:" + k
		m[ref] = Observation{Ref: ref, Kind: "entity", Detail: k}
	}
	return m
}

// Params are the DECLARED gate cutoffs (doc 20 P3), never data-fit. In production they
// live in the parameters file; DefaultParams is the versioned default.
type Params struct {
	MinEvidence     int // evidence-sufficiency floor: a proposal must cite ≥ this many grounded refs
	MaxProposals    int // cap accepted per run (bounds a runaway model)
	MaxContextChars int // bound the rendered prompt so it stays under the model's token/rate limit
}

// DefaultParams: cite ≥1 grounded fact; at most 20 accepted per run; ~6k-char context
// budget (a real cluster can have hundreds of observations — the budget keeps the
// prompt under typical model TPM limits, surfaced live against the boutique).
var DefaultParams = Params{MinEvidence: 1, MaxProposals: 20, MaxContextChars: 6000}

// Rejection records why a proposal was discarded (auditable, never silent).
type Rejection struct {
	Subject string
	Reason  string
}

// Report is the outcome of one agent run.
type Report struct {
	Provider string
	Proposed int
	Accepted int
	Rejected []Rejection
}

// Agent is the propose→verify harness. It holds a provider + the declared gate params.
type Agent struct {
	provider Provider
	params   Params
}

// New builds an agent. A zero Params uses DefaultParams; a zero MaxContextChars is
// filled with the default budget.
func New(provider Provider, params Params) *Agent {
	if params.MinEvidence == 0 && params.MaxProposals == 0 {
		params = DefaultParams
	}
	if params.MaxContextChars <= 0 {
		params.MaxContextChars = DefaultParams.MaxContextChars
	}
	return &Agent{provider: provider, params: params}
}

// Propose asks the provider for proposals over the context and runs the VERIFY gates,
// returning the survivors (as candidate.Candidate, status defaulted at staging) and a
// report. The provider's text is untrusted: a parse failure or provider error is
// returned as an error (nothing is staged).
func (a *Agent) Propose(ctx context.Context, c Context) ([]candidate.Candidate, Report, error) {
	rep := Report{Provider: a.provider.Name()}
	raw, err := a.provider.Complete(ctx, systemPrompt, userPrompt(c, a.params))
	if err != nil {
		return nil, rep, fmt.Errorf("dgx: provider: %w", err)
	}
	doc, err := parseProposals(raw)
	if err != nil {
		return nil, rep, fmt.Errorf("dgx: %w", err)
	}
	index := c.refIndex()
	var out []candidate.Candidate
	for _, rp := range doc.Proposals {
		rep.Proposed++
		subj := rp.Subject
		if len(out) >= a.params.MaxProposals {
			rep.Rejected = append(rep.Rejected, Rejection{subj, "max-proposals cap reached"})
			continue
		}
		cand, reason := a.buildCandidate(rp, c, index)
		if reason != "" {
			rep.Rejected = append(rep.Rejected, Rejection{subj, reason})
			continue
		}
		out = append(out, cand)
	}
	rep.Accepted = len(out)
	return out, rep, nil
}

// RunOnce proposes and stages the survivors. `now` is injected. A candidate that
// fails the store's structural guard at staging is counted as rejected (never aborts
// the run) — the store is the final structural authority.
func (a *Agent) RunOnce(ctx context.Context, store *candidate.Store, now time.Time, c Context) (Report, error) {
	cands, rep, err := a.Propose(ctx, c)
	if err != nil {
		return rep, err
	}
	for i := range cands {
		if _, err := store.Put(now, cands[i]); err != nil {
			rep.Accepted--
			rep.Rejected = append(rep.Rejected, Rejection{cands[i].Subject, "structural: " + err.Error()})
		}
	}
	return rep, nil
}

// buildCandidate runs the VERIFY gates on one raw proposal and builds the candidate, or
// returns a non-empty reason for rejection. Order: grounding → evidence floor →
// structural validity (candidate.Validate, which rejects a causal edge). An equiv_group
// proposal takes the dedicated typed path (buildEquivGroupCandidate) after grounding.
func (a *Agent) buildCandidate(rp rawProposal, c Context, index map[string]Observation) (candidate.Candidate, string) {
	if strings.TrimSpace(rp.Subject) == "" {
		return candidate.Candidate{}, "empty subject"
	}
	// GROUNDING: every cited ref must exist in the context.
	ev, reason := groundEvidence(rp.Evidence, index)
	if reason != "" {
		return candidate.Candidate{}, reason
	}
	// EVIDENCE FLOOR (declared).
	if len(ev) < a.params.MinEvidence {
		return candidate.Candidate{}, fmt.Sprintf("evidence below floor (%d < %d)", len(ev), a.params.MinEvidence)
	}
	if candidate.Kind(rp.Kind) == candidate.KindEquivGroup {
		return a.buildEquivGroupCandidate(rp, ev, c)
	}
	cand := candidate.Candidate{
		Kind:     candidate.Kind(rp.Kind),
		Relation: rp.Relation,
		Subject:  rp.Subject,
		Evidence: ev,
		Lineage: candidate.Lineage{
			Source: "dgx-agent", Method: "llm:" + a.provider.Name(), GraphVersion: c.GraphVersion,
		},
		// The model's words are stored as PROPOSED payload — never a surfaced authored
		// reason. A human authors the note at promotion.
		Payload: map[string]any{"rationale": rp.Rationale, "proposed": true},
	}
	// STRUCTURAL GUARD: unknown kind, or a causal edge, is rejected here (the same check
	// the store applies) so a causal proposal can never even be staged as an edge.
	if err := candidate.Validate(cand); err != nil {
		return candidate.Candidate{}, "structural: " + err.Error()
	}
	return cand, ""
}

// groundEvidence resolves each cited ref against the context index (the grounding gate),
// deduping and returning a context-tagged EvidenceRef per cited fact. A ref not in the
// context is an immediate rejection (the model invented it).
func groundEvidence(refs []string, index map[string]Observation) ([]candidate.EvidenceRef, string) {
	ev := make([]candidate.EvidenceRef, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		obs, ok := index[ref]
		if !ok {
			return nil, "ungrounded evidence ref: " + ref
		}
		ev = append(ev, candidate.EvidenceRef{Kind: "context:" + obs.Kind, Ref: ref, Detail: obs.Detail})
	}
	return ev, ""
}

// buildEquivGroupCandidate builds a stray→equivalence-group mapping candidate (doc 21 §5).
// The focal metric is derived from a CITED stray-metric observation (so it is grounded in a
// real fact, not the model's free text). The target is an EXISTING group (if the cited id is
// in c.KnownGroups) or a NEW group (otherwise — requiring canonical + label). The proposal is
// structurally validated (pattern compiles + matches its own metric, exactly one target), and
// the deterministic capture SUPPORT is computed over the strays the agent saw.
func (a *Agent) buildEquivGroupCandidate(rp rawProposal, ev []candidate.EvidenceRef, c Context) (candidate.Candidate, string) {
	metric := ""
	for _, e := range ev {
		if m, ok := candidate.StrayMetricFromSubject(e.Ref); ok {
			metric = m
			break
		}
	}
	if metric == "" {
		return candidate.Candidate{}, "equiv_group must cite a stray-metric observation (the metric to map)"
	}
	if strings.TrimSpace(rp.Group) == "" {
		return candidate.Candidate{}, "equiv_group: missing target group id"
	}
	prop := candidate.EquivGroupProposal{Metric: metric, Pattern: rp.Pattern}
	if _, ok := c.KnownGroups[rp.Group]; ok {
		prop.GroupID = rp.Group
	} else {
		prop.NewGroup = &candidate.NewEquivGroup{ID: rp.Group, Label: rp.Label, CanonicalOTel: rp.Canonical}
	}
	if err := candidate.ValidateEquivGroupProposal(prop); err != nil {
		return candidate.Candidate{}, "structural: " + err.Error()
	}
	// Deterministic SUPPORT: which of the strays the agent saw this pattern would also
	// capture. A count of MEASURED facts surfaced for the reviewer, never a model confidence.
	sample, err := candidate.EquivGroupSupport(prop.Pattern, strayPool(c.Observations))
	if err != nil {
		return candidate.Candidate{}, "structural: " + err.Error()
	}
	prop.CaptureSample = sample
	payload := candidate.EquivGroupPayload(prop)
	payload["rationale"] = rp.Rationale // PROPOSED model words, discarded at promotion
	payload["proposed"] = true
	cand := candidate.Candidate{
		Kind:     candidate.KindEquivGroup,
		Subject:  candidate.EquivGroupSubject(metric),
		Evidence: ev,
		Lineage:  candidate.Lineage{Source: "dgx-agent", Method: "llm:" + a.provider.Name(), GraphVersion: c.GraphVersion},
		Payload:  payload,
	}
	if err := candidate.Validate(cand); err != nil {
		return candidate.Candidate{}, "structural: " + err.Error()
	}
	return cand, ""
}

// strayPool collects the distinct stray metric names visible in the context observations —
// the deterministic universe over which a proposed pattern's capture support is measured.
func strayPool(obs []Observation) []string {
	var out []string
	seen := map[string]bool{}
	for _, o := range obs {
		if m, ok := candidate.StrayMetricFromSubject(o.Ref); ok && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}
