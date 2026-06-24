package dgx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	// MaxToolIterations bounds the multi-turn tool loop (doc 21 Phase 2) — a hard ceiling so
	// the agent always terminates even if the model keeps calling tools. 0 ⇒ the default.
	MaxToolIterations int
	// MaxToolResultChars caps one tool result rendered into the prompt (keeps the running
	// context under the model's TPM limit across turns). 0 ⇒ the default.
	MaxToolResultChars int
	// MinSupport is the DECLARED support floor (doc 21 §3 Phase 3 slice 5): a proposal whose
	// deterministic agreed-evidence support count is below this is rejected BEFORE staging — a
	// stronger bar than MinEvidence for operators who want only well-corroborated proposals to
	// reach a human. 0 ⇒ OFF (no regression: MinEvidence still applies). NEVER a learned
	// threshold — a count of agreed MEASURED facts compared to a declared integer.
	MinSupport int
	// MinCaptureSample is the equiv_group-specific floor: a stray→group mapping whose proposed
	// pattern absorbs fewer than this many seen strays is rejected (it would author a one-off
	// pattern, not a real dialect bridge). 0 ⇒ OFF.
	MinCaptureSample int
}

// DefaultParams: cite ≥1 grounded fact; at most 20 accepted per run; ~6k-char context
// budget; ≤4 tool turns with ≤1500-char tool results (the Phase-2 retrieval loop stays
// bounded under typical model TPM limits, surfaced live against the boutique). The threshold
// floors (MinSupport, MinCaptureSample) ship OFF (0) — opt-in, so there is no regression.
var DefaultParams = Params{MinEvidence: 1, MaxProposals: 20, MaxContextChars: 6000, MaxToolIterations: 4, MaxToolResultChars: 1500, MinSupport: 0, MinCaptureSample: 0}

// Rejection records why a proposal was discarded (auditable, never silent).
type Rejection struct {
	Subject string
	Reason  string
}

// Report is the outcome of one agent run.
type Report struct {
	Provider       string
	Proposed       int
	Accepted       int
	Rejected       []Rejection
	ToolCalls      int // tool calls dispatched this run (Phase 2)
	Iterations     int // tool-loop turns taken (Phase 2)
	ThresholdGated int // proposals rejected by a DECLARED support/capture floor (slice 5 telemetry)
	// doc 33 P4 telemetry: directional "why" suggestions on causal hypotheses.
	DirectionsSuggested int // directions ADMITTED (dual-witness agreed) as a PROJECTED hint for a human
	DirectionsWithheld  int // directions the model proposed but the dual-witness did NOT support
	Note                string
}

// Agent is the propose→verify harness. It holds a provider + the declared gate params,
// and (Phase 2) an optional read-only tool registry the agent may call to gather evidence.
type Agent struct {
	provider Provider
	params   Params
	tools    *ToolRegistry
}

// New builds an agent. A zero Params uses DefaultParams; a zero MaxContextChars /
// MaxToolIterations / MaxToolResultChars is filled with the default budget.
func New(provider Provider, params Params) *Agent {
	if params.MinEvidence == 0 && params.MaxProposals == 0 {
		params = DefaultParams
	}
	if params.MaxContextChars <= 0 {
		params.MaxContextChars = DefaultParams.MaxContextChars
	}
	if params.MaxToolIterations <= 0 {
		params.MaxToolIterations = DefaultParams.MaxToolIterations
	}
	if params.MaxToolResultChars <= 0 {
		params.MaxToolResultChars = DefaultParams.MaxToolResultChars
	}
	return &Agent{provider: provider, params: params}
}

// SetTools attaches a read-only tool registry (doc 21 Phase 2). With tools attached the
// agent runs the bounded multi-turn retrieval loop; with none it stays the Phase-1
// single-shot proposer. Setting nil (or DGX_TOOLS=off upstream) is the safe rollback.
func (a *Agent) SetTools(r *ToolRegistry) { a.tools = r }

// Propose asks the provider for proposals over the context and runs the VERIFY gates,
// returning the survivors (as candidate.Candidate, status defaulted at staging) and a
// report. With a tool registry attached it runs the bounded multi-turn retrieval loop;
// otherwise the Phase-1 single-shot path. A parse failure or provider error is returned
// as an error (nothing is staged). Back-compat: Propose with no ledger uses an empty one.
func (a *Agent) Propose(ctx context.Context, c Context) ([]candidate.Candidate, Report, error) {
	return a.propose(ctx, c, Ledger{})
}

func (a *Agent) propose(ctx context.Context, c Context, led Ledger) ([]candidate.Candidate, Report, error) {
	if a.tools.Len() > 0 {
		return a.proposeWithTools(ctx, c, led)
	}
	return a.proposeOneShot(ctx, c, led)
}

// proposeOneShot is the Phase-1 single-shot path (also the graceful fallback when the
// provider cannot do tool-calling). It grounds proposals against the SEED context only.
func (a *Agent) proposeOneShot(ctx context.Context, c Context, led Ledger) ([]candidate.Candidate, Report, error) {
	rep := Report{Provider: a.provider.Name()}
	raw, err := a.provider.Complete(ctx, systemPrompt, userPrompt(c, led, a.params))
	if err != nil {
		return nil, rep, fmt.Errorf("dgx: provider: %w", err)
	}
	doc, err := parseProposals(raw)
	if err != nil {
		return nil, rep, fmt.Errorf("dgx: %w", err)
	}
	out := a.verifyProposals(doc, c, c.refIndex(), &rep)
	rep.Accepted = len(out)
	return out, rep, nil
}

// proposeWithTools runs the bounded multi-turn retrieval loop (doc 21 Phase 2). The agent
// may call read-only tools to gather MEASURED evidence; each tool's returned refs are ADDED
// to the accumulating grounding index `acc`, so a proposal may cite ONLY a fact a tool
// actually returned (the anti-fabrication guarantee — groundEvidence is unchanged, the
// index just grows). The loop is hard-bounded by MaxToolIterations and degrades gracefully:
// a tools-unsupported provider falls back to single-shot, and any tool error becomes a
// TOOL ERROR turn (the model recovers; nothing is fabricated).
func (a *Agent) proposeWithTools(ctx context.Context, c Context, led Ledger) ([]candidate.Candidate, Report, error) {
	rep := Report{Provider: a.provider.Name()}
	acc := c.refIndex() // accumulating grounding index: seed facts ∪ tool-returned refs
	msgs := []ChatTurn{
		{Role: "system", Content: systemPrompt + toolSystemSuffix(a.tools)},
		{Role: "user", Content: userPrompt(c, led, a.params)},
	}
	for iter := 0; iter < a.params.MaxToolIterations; iter++ {
		rep.Iterations++
		turn, err := a.provider.CompleteTools(ctx, msgs, a.tools.Schemas())
		if err != nil {
			// A provider that cannot do tool-calling — at ANY iteration — degrades gracefully to
			// the single-shot Complete() path (a different transport that should work). Seeded
			// from the same context; the partial tool telemetry is preserved for the log.
			if errors.Is(err, ErrToolsUnsupported) {
				cands, frep, ferr := a.proposeOneShot(ctx, c, led)
				frep.Note = "tools-unsupported: single-shot fallback"
				frep.ToolCalls, frep.Iterations = rep.ToolCalls, rep.Iterations
				return cands, frep, ferr
			}
			return nil, rep, fmt.Errorf("dgx: provider: %w", err)
		}
		if len(turn.ToolCalls) == 0 {
			// Final text turn: parse + verify against the accumulated index.
			doc, perr := parseProposals(turn.Content)
			if perr != nil {
				return nil, rep, fmt.Errorf("dgx: %w", perr)
			}
			out := a.verifyProposals(doc, c, acc, &rep)
			rep.Accepted = len(out)
			return out, rep, nil
		}
		// Dispatch deterministically (sorted by name,id) so the accumulation order — and
		// thus the staged candidates given the same provider responses — is reproducible.
		calls := append([]ToolCall(nil), turn.ToolCalls...)
		sort.Slice(calls, func(i, j int) bool {
			if calls[i].Name != calls[j].Name {
				return calls[i].Name < calls[j].Name
			}
			return calls[i].ID < calls[j].ID
		})
		msgs = append(msgs, ChatTurn{Role: "assistant", ToolCalls: calls})
		rep.ToolCalls += len(calls)
		for _, call := range calls {
			obs, terr := a.tools.Call(ctx, call.Name, json.RawMessage(call.Arguments))
			if terr != nil {
				// A tool error is surfaced as a tool-result turn, NEVER a crash, and adds NO
				// refs to acc — so a failed tool can never launder an invented fact.
				msgs = append(msgs, ChatTurn{Role: "tool", ToolCallID: call.ID,
					Content: "TOOL ERROR: " + terr.Error() + " (no refs returned)"})
				continue
			}
			msgs = append(msgs, ChatTurn{Role: "tool", ToolCallID: call.ID,
				Content: accumulate(acc, obs, a.params.MaxToolResultChars)})
		}
	}
	// Budget exhausted: one final forced text turn over what was gathered; if it still will
	// not emit proposals, accept nothing (bounded — never an infinite loop).
	rep.Note = "tool budget exhausted; forced final turn"
	msgs = append(msgs, ChatTurn{Role: "user",
		Content: "Tool budget exhausted. Output your proposals JSON now using ONLY the refs already gathered."})
	final, err := a.provider.CompleteTools(ctx, msgs, nil)
	if err == nil {
		if doc, perr := parseProposals(final.Content); perr == nil {
			out := a.verifyProposals(doc, c, acc, &rep)
			rep.Accepted = len(out)
			return out, rep, nil
		}
	}
	return nil, rep, nil
}

// accumulate adds each tool-returned observation's ref to the grounding index and renders a
// deterministic, budget-capped text block for the tool-result turn. Refs ONLY ever enter the
// index here (or from the seed context), which is what makes grounding tool-aware yet honest.
func accumulate(acc map[string]Observation, obs []Observation, budget int) string {
	if len(obs) == 0 {
		return "(no rows)"
	}
	var b strings.Builder
	for _, o := range obs {
		line := "- [" + o.Ref + "] (" + o.Kind + ") " + truncate(o.Detail, 200) + "\n"
		if b.Len() > 0 && b.Len()+len(line) > budget {
			b.WriteString("(+more rows omitted to fit the tool-result budget)\n")
			break
		}
		b.WriteString(line)
		// A ref becomes groundable ONLY if the model actually SAW it: acc tracks exactly the
		// rows shown, so a budget-truncated row is never silently citable (the anti-fabrication
		// invariant is airtight — acc ⊆ what the model was shown this cycle).
		acc[o.Ref] = o
	}
	return b.String()
}

// verifyProposals runs the VERIFY gates over a parsed proposal doc against `index` (the
// accumulated grounding index in the tool path; the seed index in single-shot), staging the
// survivors. The per-proposal logic is identical to Phase 1 — only the index may be larger.
func (a *Agent) verifyProposals(doc proposalDoc, c Context, index map[string]Observation, rep *Report) []candidate.Candidate {
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
			if strings.Contains(reason, "below floor") {
				rep.ThresholdGated++ // a DECLARED threshold (slice 5) blocked it — telemetry, never silent
			}
			rep.Rejected = append(rep.Rejected, Rejection{subj, reason})
			continue
		}
		// doc 33 P4: a SUGGESTED causal direction on a causal_hypothesis is admitted ONLY when the
		// dual-witness agrees (the cited co-onset hypothesis's lead-lag AGREES with its onset order).
		// It rides the PROJECTED Suggestion (discarded at promotion); a named human authors the arrow.
		// A direction the witnesses do not support is WITHHELD — the hypothesis stays direction-free.
		if cand.Kind == candidate.KindCausalHypothesis && strings.TrimSpace(rp.Direction) != "" {
			dir, ok := normalizeDirection(rp.Direction)
			switch {
			case !ok:
				rep.DirectionsWithheld++ // unparseable direction — kept direction-free
			case dir == "not-causal":
				// A NEGATIVE judgement ("these co-occur but I doubt causation") never invents an
				// edge, so it is admitted freely — no dual-witness needed. The human ratifies it
				// (marking the hypothesis not-causal) or overrides.
				cand.Suggestion = &candidate.Suggestion{Direction: dir, DirectionRationale: rp.Rationale, Model: a.provider.Name()}
				rep.DirectionsSuggested++
			case witnessAgrees(rp.Evidence, index):
				// A POSITIVE direction is admitted ONLY when the dual-witness agrees.
				cand.Suggestion = &candidate.Suggestion{Direction: dir, DirectionRationale: rp.Rationale, Model: a.provider.Name()}
				rep.DirectionsSuggested++
			default:
				rep.DirectionsWithheld++ // a positive direction the witnesses don't support — withheld
			}
		}
		out = append(out, cand)
	}
	return out
}

// normalizeDirection canonicalises the model's suggested direction to "a-to-b" | "b-to-a"
// (tolerating arrows / spacing); ok=false for anything else (then the direction is withheld).
func normalizeDirection(s string) (string, bool) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "")) {
	case "a-to-b", "a->b", "a→b", "atob", "ab":
		return "a-to-b", true
	case "b-to-a", "b->a", "b→a", "btoa", "ba":
		return "b-to-a", true
	case "not-causal", "notcausal", "not_causal", "none", "noncausal":
		return "not-causal", true
	}
	return "", false
}

// witnessAgrees is the doc-33-P4 dual-witness gate: a suggested direction is admissible ONLY
// if the proposal cites a causal-hypothesis observation (cohyp:<id>) whose lead-lag witness
// AGREES with its co-onset order (the get_causal_hypotheses tool prints exactly that phrase
// when lagConsistentWithOnset is true). Two independent MEASURED witnesses must agree before
// the agent may even SUGGEST a direction — it can never name one the data does not support twice.
func witnessAgrees(refs []string, index map[string]Observation) bool {
	for _, r := range refs {
		if !strings.HasPrefix(r, "cohyp:") {
			continue
		}
		// Match the parenthesized phrase the get_causal_hypotheses tool emits — "(AGREES …)" —
		// NOT a bare "AGREES" (which is a substring of "DISAGREES").
		if o, ok := index[r]; ok && strings.Contains(o.Detail, "(AGREES with onset order)") {
			return true
		}
	}
	return false
}

// RunOnce proposes (with the prior-proposal ledger as memory) and stages the survivors.
// `now` is injected. A candidate that fails the store's structural guard at staging is
// counted as rejected (never aborts the run) — the store is the final structural authority.
func (a *Agent) RunOnce(ctx context.Context, store *candidate.Store, now time.Time, c Context, led Ledger) (Report, error) {
	cands, rep, err := a.propose(ctx, c, led)
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
	// SUPPORT FLOOR (declared threshold, slice 5): an opt-in stronger bar than MinEvidence — a
	// proposal whose agreed-evidence support is below the declared minimum is rejected before a
	// human ever sees it. OFF by default (0). A count compared to a declared integer, never learned.
	if a.params.MinSupport > 0 && len(ev) < a.params.MinSupport {
		return candidate.Candidate{}, fmt.Sprintf("support below floor (%d < %d)", len(ev), a.params.MinSupport)
	}
	if candidate.Kind(rp.Kind) == candidate.KindEquivGroup {
		return a.buildEquivGroupCandidate(rp, ev, c, index)
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
func (a *Agent) buildEquivGroupCandidate(rp rawProposal, ev []candidate.EvidenceRef, c Context, index map[string]Observation) (candidate.Candidate, string) {
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
	// Deterministic SUPPORT: which of the strays the agent has SEEN (seed context + any
	// pulled via get_strays) this pattern would also capture. A count of MEASURED facts
	// surfaced for the reviewer, never a model confidence.
	sample, err := candidate.EquivGroupSupport(prop.Pattern, strayPoolFromIndex(index))
	if err != nil {
		return candidate.Candidate{}, "structural: " + err.Error()
	}
	// CAPTURE-SAMPLE FLOOR (declared threshold, slice 5): require the proposed pattern to absorb
	// at least the declared number of SEEN strays, so a new equivalence group is a real dialect
	// bridge — not a one-off pattern matching only its own metric. OFF by default (0).
	if a.params.MinCaptureSample > 0 && len(sample) < a.params.MinCaptureSample {
		return candidate.Candidate{}, fmt.Sprintf("equiv_group capture-sample below floor (%d < %d)", len(sample), a.params.MinCaptureSample)
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

// strayPoolFromIndex collects the distinct stray metric names visible in the grounding
// index — the deterministic universe over which a proposed pattern's capture support is
// measured. Sorted because map iteration order is unspecified (determinism of the support).
func strayPoolFromIndex(index map[string]Observation) []string {
	var out []string
	seen := map[string]bool{}
	for _, o := range index {
		if m, ok := candidate.StrayMetricFromSubject(o.Ref); ok && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}
