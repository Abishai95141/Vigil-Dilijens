# MCP read-only harness backtest GATE — v3 T-A / doc 11 §3.5 — 2026-06-14

The gate that certifies the **T-A first prototype**: a read-only MCP server exposing
Vigil's already-classed payloads to an LLM client, led by the **deterministic silence
ledger** (a provable account of what is NOT watched and why — the truth an LLM
hallucinates), plus the **ADVISORY-shell** (the generated 4th class, charter-guarded
and gate-withheld). Built on branch `v3` (off `v2`), behind `--mcp-enabled` (default
off) + `mcpAdvisoryGatePassed=false`. Auth/RBAC is a SEPARATE, later track.

**Verdict: PASSED** (`just mcp-gate`, exit 0) — both halves green.

## What the gate is

Unlike the forecast gates, this lane has **no model in the loop**, so the gate asserts
**exact equalities**, not statistical bands. Two halves:

1. **Go unit half** (`go test -race ./obsd/internal/mcp/ ./obsd/internal/api/`):
   class-integrity round-trip (a PROJECTED payload keeps its `isProjection` mark + a
   non-collapsing band across the MCP wire), the ADVISORY teeth (refusal-recall 1.0
   over honeypots), **no-write-back** (a full MCP session — incl. `emit_advisory` —
   leaves the source snapshots byte-identical), JSON-RPC determinism + transport, and
   the silence-ledger reconciliation against the **real `binding.Compile`** output.
2. **Python corpus half** (`harness/src/harness/mcp_gate.py`) over the frozen corpus
   in `corpus/mcp/` — offline, no cluster.

**Non-gating** is structural: the MCP server is mounted in `serveHealth`, entirely
outside the eval/replay path; the replay digest never sees it (the full `go test -race
./obsd/...` suite is byte-identical with the lane present). **No-write-back** is
structural too: `internal/mcp` imports only `internal/api` view types + the
`CharterViolations` linter — no graph/findings/binding writer is in scope.

## Scoring discipline

| floor | rule |
|---|---|
| SILENCE-LEDGER DETERMINISM | two independent `BuildSilenceLedger` builds are byte-identical (A==B) |
| ABSENCE-COMPLETENESS | `totalPairs == watched + silent == the compiler's binding count`; every silent row carries a non-empty reason + a known class — **0 pairs unaccounted** |
| RECONCILIATION | the silence classes reconcile EXACTLY with the compiler's independent per-rule coverage (`unbounded/out-of-scope/unresolved` sums; `watched == configBound+defaultBound − no-stream-key`; `total == instantiated+outOfScope+unresolved`) — the ledger cannot pass as a convenient subset |
| ADVISORY TEETH | refusal-recall **== 1.0** over the banned honeypots, across all three registers (causal / future-certainty / fusion) |
| NO FALSE-BLOCK | 0 register-clean drafts refused |
| NO CONTENT LEAK | 0 refused/withheld advisories carrying emitted text |
| CHARTER | the ledger payload carries no banned register (0) |
| sufficiency (INSUFFICIENT ≠ pass) | ≥2 silence classes incl. the no-stream-key (dark-bar); ≥9 honeypots spanning all 3 registers; ≥3 clean drafts |

The reconciliation floor is the anti-shallow core: it is the critic's demanded
"binding-result + per-rule coverage" denominator — a ledger that dropped or invented
a pair fails immediately (regression-tested in `test_mcp_gate.py`).

## The frozen corpus (`corpus/mcp/`)

Source is stated honestly: **synthetic-fixture** — a real `binding.Compile` over the
released ontology with a SYNTHETIC boutique inventory, NOT a live-cluster capture. The
gate's claims (determinism, completeness, reconciliation, teeth) are fully
deterministic and do **not** depend on cluster realism (unlike the forecast gates,
which need real TimesFM behaviour). Regenerate with `REGEN_MCP_CORPUS=1 go test ...`.

- **`silence-ledger.json`** — two independent ledger builds (the determinism floor) +
  the compiler's per-rule aggregates (the reconciliation floor). 27 bindings, 9 silent
  (1 no-stream-key = the PVC **dark-bar**, surfaced honestly; 1 out-of-scope; 2
  unbounded; 5 unresolved); 18 watched.
- **`advisory-drafts.jsonl`** — 9 banned honeypots (causal / future-certainty /
  fusion) + 3 clean drafts, each carrying the **real guard's** verdict.

### Result

```
MCP read-only harness backtest — class: measured_silence_ledger + advisory_shell
  silence ledger: 27 pairs · 18 watched · 9 silent · classes ['no-stream-key', 'out-of-scope', 'unbounded', 'unresolved']
  determinism:    byte-identical (A==B)
  completeness:   OK (every pair accounted)
  reconciliation: OK (vs per-rule coverage)
  dark-bar shown: yes (no-stream-key present)
  advisory teeth: refusal-recall 1.000 over 9 honeypots, registers ['causal', 'fusion', 'future-certainty']
  advisory clean: 3 drafts · false-blocks 0 (max 0) · withheld-breaks 0 (max 0) · content-leaks 0 (max 0)
  ledger charter: 0 violations (max 0)
  GATE: PASSED — the MCP read-only harness + silence ledger may be exposed (auth + ADVISORY content remain separate, later gates)
```

## LIVE VERIFICATION — real `kind-vigil` cluster (2026-06-14)

Beyond the frozen corpus, the prototype was run live against the real cluster
(`kind-vigil`: 3 nodes, the full Online Boutique, node-exporter, conntrack agents).
`obsd --mcp-enabled --api` compiled bindings over the released ontology (v0.4.0, 34
identified entities) and served the lane. All probes passed:

- **`/api/silence-ledger`** returned a LIVE ledger: **173 (entity,variable) pairs · 114
  watched · 59 silent** (41 out-of-scope, 18 unbounded), class `MEASURED`, every silent
  row carrying a verbatim compiler reason.
- **LIVE RECONCILIATION → PASS.** All six floors held against the *live* coverage
  compiler: `byReason.unbounded==18`, `out-of-scope==41`, `unresolved==0`,
  `watched==configBound+defaultBound−no-stream-key==114`,
  `total==instantiated+outOfScope+unresolved==173`, `watched+silent==total`. The
  anti-shallow reconciliation is proven on real data, not just the fixture.
- **`/mcp` JSON-RPC live**: `initialize` (proto 2024-11-05, server `vigil-obsd v0.4.0`);
  `tools/list` (4 provenance-labelled tools); `tools/call get_silence_ledger` returned
  the classed payload with **`MCP.summary == /api.summary`** (byte-identical round-trip,
  class intact).
- **ADVISORY live**: a causal draft was **REFUSED** (`banned causal register "caused by"`,
  no text emitted); a clean draft was **WITHHELD** (gate off, no text emitted).
- **Non-gating live**: the identity/detection loop logged 12 inventory ticks throughout
  the MCP probing — the deterministic path never paused for the harness.

(The live boutique has no config-relative PVC with a declared request, so the
`no-stream-key` dark-bar class is absent live — correctly reflected, not faked; the
frozen fixture exercises that class.)

## What this gate does NOT certify (stated, not hidden)

- **No auth.** The `/mcp` route is unauthenticated, exactly like `/api`. This is the
  deliberately-deferred final track. The gate does not certify safe public exposure —
  only correct, charter-clean behaviour behind the default-off flag in an isolated
  cluster.
- **ADVISORY content stays withheld.** `mcpAdvisoryGatePassed=false`: the class refuses
  banned drafts and withholds clean ones. Releasing clean ADVISORY content is its own
  later gate.
- **Synthetic corpus.** A live-cluster capture (real boutique inventory, incl. the live
  PVC dark-bar) is the natural follow-on regression — additive, not load-bearing for
  these deterministic floors.
