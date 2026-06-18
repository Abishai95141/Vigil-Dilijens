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
}

func (c Context) refIndex() map[string]Observation {
	m := make(map[string]Observation, len(c.Observations))
	for _, o := range c.Observations {
		m[o.Ref] = o
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
		cand, reason := a.buildCandidate(rp, index, c.GraphVersion)
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
// structural validity (candidate.Validate, which rejects a causal edge).
func (a *Agent) buildCandidate(rp rawProposal, index map[string]Observation, graphVersion string) (candidate.Candidate, string) {
	if strings.TrimSpace(rp.Subject) == "" {
		return candidate.Candidate{}, "empty subject"
	}
	// GROUNDING: every cited ref must exist in the context.
	ev := make([]candidate.EvidenceRef, 0, len(rp.Evidence))
	seen := map[string]bool{}
	for _, ref := range rp.Evidence {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		obs, ok := index[ref]
		if !ok {
			return candidate.Candidate{}, "ungrounded evidence ref: " + ref
		}
		ev = append(ev, candidate.EvidenceRef{Kind: "context:" + obs.Kind, Ref: ref, Detail: obs.Detail})
	}
	// EVIDENCE FLOOR (declared).
	if len(ev) < a.params.MinEvidence {
		return candidate.Candidate{}, fmt.Sprintf("evidence below floor (%d < %d)", len(ev), a.params.MinEvidence)
	}
	cand := candidate.Candidate{
		Kind:     candidate.Kind(rp.Kind),
		Relation: rp.Relation,
		Subject:  rp.Subject,
		Evidence: ev,
		Lineage: candidate.Lineage{
			Source: "dgx-agent", Method: "llm:" + a.provider.Name(), GraphVersion: graphVersion,
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
