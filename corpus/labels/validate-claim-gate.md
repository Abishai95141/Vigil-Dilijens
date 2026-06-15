# validate-claim-gate — evidence (v3 T-D, the LLM-claim referee)

**Verdict: PASSED** (`just validate-claim-gate`, exit 0). Certifies the deterministic
referee over a FROZEN corpus, offline.

## What it certifies

`api.ValidateClaim` is a REFEREE for free prose written by an EXTERNAL generator (an
LLM via MCP, or any claim about the system). It reports whether the claim crosses the
charter (doc 01) — a generated cause, a projection restated as a measurement, a future
certainty — and **never blocks**. It returns `{Flagged, Reasons, MatchedAuthored,
LabelledBestEffort=true}`; the consumer decides.

Two backstops:
- **Substring** — the charter denylist (`CharterViolations`): banned causal /
  future-certainty / fusion phrasings. Blunt; knows no graph.
- **Structural (graph-aware)** — extracts `(trigger → downstream)` causal pairs from
  the prose and checks them against the AUTHORED phenomenon relations. A causal claim
  *with* an authored basis is a (clumsy) surfacing of a real relation → **rescued**,
  not flagged (advisory: "surface it as authored"). A causal claim with *no* authored
  basis is a generated cause → flagged. It also catches a MEASURED-tense crossing
  about a subject that is only PROJECTED now (class fusion).

The structural backstop is what gives the referee teeth a denylist cannot have: it
catches a honeypot phrased *without* any banned substring ("the storage saturation
sparked the memory leak"), and it RESCUES a true authored relation a denylist would
wrongly flag ("the memory leak led to the oom kill" — the real `PHEN_MEMORY_LEAK →
PHEN_OOM_KILL_CGROUP` "Eventual outcome" relation exists).

## The cardinal rule

**FALSE-BLOCK == 0 (ABSOLUTE).** A referee that flags a TRUE statement is worse than
none. The legitimate set includes the trap cases that *share vocabulary* with the
fabrications — and none of them is flagged:
- a clumsy causal restatement of a REAL authored relation (rescued),
- a legitimate banded projection ("projected to cross between 10:09–10:22" — shares
  "cross" with the future-certainty fabrications),
- a true MEASURED crossing ("has crossed its limit" about a MEASURED subject).

## Floors (all PASSED)

| Floor | Result |
|---|---|
| FALSE-BLOCK == 0 (absolute) | **0** / 9 legit |
| RECALL ≥ 0.90 | **1.000** (12/12 fabrications flagged) |
| per-category ≥ 1 | relation-absent 4, class-fusion 3, future-certainty 2, structural-honeypot 3 |
| MUTATION ≥ 0.80 (substring OFF → structural-only) | **1.000** (7/7 relation-absent + honeypot caught with the denylist disabled) |
| LABELLED-BEST-EFFORT == true | every verdict |
| NEVER-BLOCK | no verdict carries a block directive (structural: the type has no such field) |

## Anti-shallow cores

- The corpus's `authoredLinks` are a **subset of the REAL graph** `phenomenon_relation`
  edges, so legit claims cite real relations and fabrications use **absent or REVERSED**
  pairs (e.g. "the oom kill caused the memory leak" — the reverse of the authored
  leak→OOM; shares both phenomena with a real relation but the direction is fabricated).
- The MUTATION floor disables the substring denylist and proves the structural backstop
  *independently* catches relation-absent causation — it is not merely riding the
  denylist. The structural honeypots are caught with class `generated-causation` ONLY
  (no `causal` substring class), confirming they evade the denylist entirely.

## Two halves

- **Go** (`go test -race ./obsd/internal/api -run 'ValidateClaim|Legit|...'`): the
  referee unit tests (FALSE-BLOCK-safe rescue, class-fusion, future-certainty,
  structural honeypot, mutation mode, never-blocks-serialization, determinism) + the
  frozen-corpus **drift guard** (`verdicts.jsonl` must equal a fresh `ValidateClaim`
  run over `claims.json` — a code change that forgets to regenerate fails here).
- **Python** (`harness/src/harness/validate_claim_gate.py` + 10 adversarial tests).

## Corpus / regen

`corpus/validate-claim/claims.json` (authored claims + labels + the real authored
links) → `verdicts.jsonl` (frozen, produced by the REAL `ValidateClaim` in full +
structural-only modes). Regenerate: `REGEN_VALIDATE_CLAIM_CORPUS=1 go test
./obsd/internal/api -run RegenValidateClaimCorpus`.

## Adversarial hardening (11-agent workflow, 27 exploits)

A workflow fanned out 6 generators (grounded in the real authored relations) + 5
attackers against the first cut of the referee. Rather than trust their hand-simulation,
**all 80 candidate/exploit claims were run through the real `ValidateClaim`**: the first
cut had **13 false-blocks + 23 misses**. These were real, *general* defects (not
corpus-specific), all fixed:

- **Passive/reverse-cue direction** — "the oom kill **was caused by** the memory leak"
  restates the authored `MEMORY_LEAK→OOM_KILL`, but "B caused by A" was read as the
  reverse pair → false-block; symmetrically a passive fabrication was wrongly rescued.
  Fixed: causal cues now carry DIRECTION (forward vs passive), longest-match-first.
- **Class-fusion unbound from its subject** — "currencyservice has crossed; cartservice
  is only projected" flagged because the crossing cue was found *anywhere*. Fixed: the
  crossing must sit within one clause AFTER the projected subject.
- **Negation-blindness** — "has **not** crossed", "**neither** is responsible for" were
  flagged. Fixed: a negated assertion is not a claim.
- **First-occurrence-only indexing** — a phenomenon mentioned twice (or a downstream
  after a later cue) was missed → rescue-bleed. Fixed: ALL occurrences indexed.
- **Rescue-bleed** — one authored clause suppressed an *unrelated* fabricated clause's
  flag. Fixed: the rescue is PER-CUE (suppresses only the exact authored phrase).
- **Substring subject match** — "cart" matched inside "cartservice". Fixed: word-boundary.

After the fixes: **0 real false-blocks** (the 3 residual "false-blocks" the agents
reported were mislabels — the prose genuinely asserts an unauthored link, so flagging is
correct; relabelled). Residual misses are best-effort under-flagging on hedged future
phrasings + convoluted parenthetical prose — the safe direction. 8 of the fixed defects
are now frozen regression cases in the corpus + `TestHardening`.

## Isolation / non-gating

The referee is a pure function; exposed behind `--referee-enabled` (default off) as an
MCP `validate_claim` tool + `/api/validate-claim`. Off ⇒ obsd byte-identical. It is
advisory by construction — it never gates detection, never blocks a claim, never
writes back.
