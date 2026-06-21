# 23 — MCP Operator Parity ("eyes all around the cluster")

> Status: BUILT + live-verified on `kind-vigil-abb` (branch `v6`).
> Owning packages: `obsd/internal/mcp` (the adapter), `obsd/internal/api` (the views),
> `obsd/cmd/obsd` (the wiring). Charter: `docs/01`. Companion: `docs/19` (blindspots),
> `docs/22` (the v6 component adoption that added onset/cohypothesis).

## The principle

**The MCP-connected agent is a human operator.** We hand an operator the console —
every surface, every honest gap. The agent interprets the same classed facts to answer
*"look at the cluster and tell me what's happening"*: an early warning with its timestamp,
the chain of events, the culprit workload, the phenomenon, the anomalies, a match against
events/logs/traces, the unmapped tail, and a synthesized recommendation.

So the rule is **read-parity**: *nothing an operator can read in the console may be
invisible to the agent.* The agent gets eyes, not hands.

- **Reads → universal.** Every console `/api` observation surface has an MCP tool.
- **Writes → human.** Governance promote/reject and authoring a causal **direction** are
  a *named human's* act. They are **structurally absent** from `mcp.Sources` (no setter,
  no writer in scope) — the no-write-back guarantee (`doc 01`) stays STRUCTURAL, not
  policy. The agent can *read* the governance queue and the direction-free hypotheses; it
  cannot decide or author.

## What was missing (before doc 23)

The relay shipped 16 read tools. Nine operator surfaces existed on `/api` but had **no
MCP tool** — the agent was blind to them while the operator was not:

| Surface | Why it matters to "what's happening" |
|---|---|
| `get_trace_graph` | the **observed** call graph (who actually called whom) — the named gap |
| `get_timeline` | the chronological spine — *what came first* |
| `get_onsets` | the precise MEASURED *since-when / which-way* of a step (incl. sub-threshold) |
| `get_causal_hypotheses` | direction-free co-onset **leads** for the operator to author |
| `get_findings` | the persisted feed incl. recently **stale** rows (just-fired / just-cleared) |
| `get_config` | declared posture — the borrowed-normativity source of every bar |
| `get_candidates` | what Vigil has **proposed** but not adopted (status=CANDIDATE, firewalled) |
| `get_provisional_coverage` | the *path* to closing a coverage gap (projection, not real coverage) |
| `get_governance` | the review queue — what is proposed and **awaiting a human** |

Excluded deliberately: `Chat` (circular — the console's own chat), `ContextWindows`
(an operator *input*, not an observation), and the three **write** entrypoints
(`GovernanceDecide`, `GovernancePreview`, `AuthorCausalDirection`).

## The engineering (uniform, low-risk)

Each new tool is the same five-line pattern as the existing 16:

1. a read-only `func() *api.XView` field on `mcp.Sources` (no writer ⇒ no-write-back holds);
2. a `toolDef` whose description carries the **provenance class** and the discipline
   (MEASURED / PROJECTED / AUTHORED, or the orthogonal **status=CANDIDATE** for staged
   proposals, or **DIRECTION-FREE** for co-onset leads);
3. a `callTool` case via `toolJSON(orNil(src.X), "<lane off>")` — an off lane is a true
   state, surfaced honestly, never an error;
4. the provider wired in `mcp.New(...)` in `main.go` (`Timeline`/`Findings` are wrapped —
   they return `(view, error)` or need a build);
5. the `synthesisInstructions` incident playbook extended so the agent *uses* the new eyes.

One new view type was added (`api.FindingsView` + `BuildFindingsView`) because
`/api/findings` previously serialized an unexported response struct.

### Charter guardrails (enforced, not aspirational)

- **No-write-back** — STRUCTURAL: `mcp.Sources` exposes only reader funcs.
- **Direction-free C3** — `get_causal_hypotheses` carries no cause and no direction; its
  description forbids the agent from assigning one. A direction becomes legitimate only
  after a human authors it, at which point it appears in `get_authored_relations`.
- **Candidate firewall** — `get_candidates` / `get_provisional_coverage` / `get_governance`
  are status=CANDIDATE, never a provenance class; the deterministic path never reads the
  candidate store (the import-firewall tests still pass).
- **Off the digest** — every new lane (trace/onset/cohypothesis/audit/logs/assoc) is
  off-digest; detection and forecasting are byte-identical with these lanes on or off.
- **Honest gaps** — a tool whose lane is off returns `{"available":false, ...}` with the
  reason, exactly as the console route does.

## Verification

- `go test -race ./...` green; `just lint` OK; `gen-check` clean; **graph hash unchanged**
  (`v0.13.0` / `6c9e75be` — this is a pure surfacing change, no ontology touched).
- Charter firewalls re-run green: `clock` conformance, and the deterministic-path
  import-firewalls for candidate/trace/assoc/audit/logtmpl/dgx.
- **Live** on `kind-vigil-abb`, obsd all-lanes on `:9096` (the user's `:9095` untouched):
  `tools/list` advertises **27 tools** (was 18). All nine new tools answer live —
  `get_candidates` (433 staged strays), `get_provisional_coverage` (429 classified),
  `get_governance` (real pending queue + evidence), `get_findings`
  (`PHEN_THROTTLING_CASCADE`, full), `get_timeline` (real spans), `get_config` (v0.13.0
  posture), `get_trace_graph` (the observed `genix-gateway → telemetry-ingest →
  timeseries-db` call graph from a sample OTel span file), and `get_onsets` /
  `get_causal_hypotheses` reporting the honest "enabled, nothing stepped this tick" when
  the cluster is steady.
