# Development Instructions — Vigil

Kubernetes-native AI Observability. One curated, versioned **ontology graph**
(type-level knowledge) compiled per cluster into a **bound customer graph**, a
**deterministic detection engine** that reports what is happening *now*, and a
**narrow forecasting clock** (TimesFM) that estimates what crosses a bar *soon*.
The system never auto-remediates and never invents causation.

> Full design is in `docs/` (the blueprint suite). Start at
> [`docs/00-master-system-design.md`](docs/00-master-system-design.md); the
> constitution is [`docs/01-epistemic-separation-charter.md`](docs/01-epistemic-separation-charter.md);
> the stack rationale is [`docs/techstack.md`](docs/techstack.md); pre-execution
> decisions and constants are [`docs/14-implementation-clarifications.md`](docs/14-implementation-clarifications.md).
> **Read the owning doc before working on a component.** Current build status and
> the task checklist live in [`artifacts/task.md`](artifacts/task.md).

---

## The discipline (non-negotiable — these are code-review rules, not style)

Every statement-bearing datum carries exactly one **provenance class**, assigned at
birth, immutable (doc 01):

- **MEASURED** — a fact read from the store, or a deterministic arithmetic
  consequence of facts (a threshold state, a rate, a co-occurrence, a match).
- **PROJECTED** — a forecast: an extrapolation against a bar, with a mandatory
  uncertainty band that **never collapses to a line**.
- **AUTHORED** — a curated graph relationship, surfaced verbatim with author +
  version provenance.

**Join, never fuse.** Classes may be presented adjacently (each labelled) at exactly
one place — the surfacing layer. No component may derive a statement of a stronger
class than its weakest input. Reject in review:

- Co-occurrence → causal claim. A topological match is a co-occurrence, never a proof of cause.
- Cascade → inferred causation. A cascade is an authored trigger→downstream edge lighting up.
- PROJECTED restated as MEASURED — anywhere (feeds, chat, summaries).
- Model output written into the graph. There is **no learned edge, weight, or threshold** anywhere.
- Generated text surfaced as a "reason." Reasons are only human-authored graph notes.

**Permitted graph→model flows are exhaustive (two):** (a) selecting which series the
clock runs on; (b) subtracting a graph-explained footprint before inference. The
model's input is otherwise bare floats — no labels, names, units, or structure. This
is enforced at the wire: `proto/vigil/clock/v1/clock.proto` carries **no string
fields**, asserted by `obsd/internal/clock/conformance_test.go`. Do not add strings to it.

**Non-gating (absolute):** detection never waits on forecasting. The deterministic
path produces identical results whether the clock is present, degraded, or absent.

**Borrowed normativity:** thresholds come from the customer's own config (declared
limits), never from learned behaviour. Where nothing is declared, say so (unbounded,
Tier-B-ineligible, listed) — never invent a bar.

**Honest partial coverage:** binding gaps, degraded matches, unresolvable bars, the
unexplained channel's residual blind spot — all stated, never hidden. Every
(entity, variable) pair has a visible state.

## Engineering mandate

**Build robust and complete. Never fall back to a shallow proxy to "show" a goal
met.** If something is developed, test it for real (against the determinism guarantee
or a live cluster), and report outcomes faithfully — if a step is deferred or a test
is skipped, say so. The blueprint is risk-driven: silent failure modes (identity
mis-joins, false equivalences, unfalsified phenomena, mis-calibrated bands) are
attacked before the visible features they would corrupt.

## Determinism-first testing (the test strategy IS the replay guarantee)

Same readings + same graph version + same topology snapshot ⇒ same fingerprints and
matches (doc 05/07). Therefore: golden-file fixtures in `testdata/`, byte-identical
assertions, **`go test -race` always**, injected clocks (no `time.Now` in logic),
table-driven tests. Hermetic unit tests are network-free; integration tests live
behind the `//go:build integration` tag and need a kind cluster.

## Stack & layout

Go (runtime core, `obsd`) · Python/uv (clockd forecasting, harness analytics) ·
TypeScript (web surfaces) · Protobuf/buf (the one cross-language contract). `obsd`
is a **modular monolith**: one binary, strict internal package boundaries mirroring
the docs. `clockd` is the only separate process (different language/lifecycle,
deliberately replaceable per the clock contract).

```
docs/        blueprint 00–14 core + 15–30 tracks proto/       buf-managed contracts (gen/ committed)
ontology/    schema/ · graph/ (YAML) · releases/  obsd/        Go monolith (cmd/ + internal/)
clockd/      Python TimesFM clock (uv)            harness/     Python backtests/falsification (uv)
console/     Vite/React surfaces                  deploy/      kind · RBAC · helm · workloads
corpus/      chaos · flags · bundles · labels     tools/graphlint  ontology lints
artifacts/   task.md (status)
```

Each Go package's contract is in its `doc.go` (owning doc + do/don't). The component
→ package map is in `docs/techstack.md` §4.

**One documented naming deviation:** the selection engine (doc 06, "select") lives in
`obsd/internal/selection` (`select` is a reserved Go keyword).

## Commands (the single surface — `just`)

| Recipe | Does |
|---|---|
| `just test` | `go test -race ./...` (the standing gate) |
| `just lint` | go vet + gofmt check + buf lint |
| `just build` | version-stamped `bin/obsd` + `bin/replay` |
| `just obsd` / `just replay` | run the binaries |
| `just gen` / `just gen-check` | regenerate / verify committed proto code |
| `just clockd-test` / `just harness-test` | Python suites (ruff + pytest) |
| `just up` / `just down` | create / delete the kind cluster (Linux) |
| `just ci` | the full local Go gate (lint + gen-check + test) |

`just --list` for all. Heavier linters install on demand via `just tools`.

## Cross-platform contract (READ THIS)

Code is written on **macOS**; the local **kind** cluster and tests run on a native
**Linux/Ubuntu** machine. Therefore:

- **All Go code is CGO-free.** Pure-Go deps only (e.g. `modernc.org/sqlite`, not
  mattn/go-sqlite3). `go test ./...` must work in any sandbox with no C toolchain.
  Verify with `CGO_ENABLED=0 go build ./...`.
- **Every `just` recipe must behave identically on macOS and Linux.** Bash is the
  shell on both; avoid GNU-only flags (`sed -i`, `readlink -f`) and bash-4 features
  (macOS ships bash 3.2). Prefer Go tooling (gofmt, go test) which is identical.
- Committed codegen (`proto/gen/`) and lockfiles (`go.sum`, `uv.lock`,
  `pnpm-lock.yaml`) keep both machines in lockstep.

## Git / commits

- Remote: `https://github.com/Abishai95141/Vigil-Dilijens` (default branch `main`).
- **Do NOT add a Claude co-author trailer** (or any AI-attribution) to commit
  messages. Plain, conventional messages tied to blueprint milestones
  (e.g. `params: add v0 parameters file (14 §5)`).
- The repo is rooted at `Desktop/vigil/`; the surrounding home directory happens to
  be a separate git repo — never commit there.

## Build order (Phase 0a, from techstack)

repo scaffold + justfile + CI → proto + buf → `obsd` skeleton with params file →
**identity layer (doc 03 — prerequisite zero)** against the kind cluster. First demo
target: `just up && just obsd` printing a live, correctly-joined entity inventory of
the Online Boutique. Build per the phased roadmap in `docs/13-rd-execution-roadmap.md`;
the harness (11) and governance (12) start far earlier than their numbers suggest.
