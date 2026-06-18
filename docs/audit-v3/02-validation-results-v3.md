# 02 — Validation Results (v3, run 2026-06-18)

> What was **actually executed and observed** this session, against v3 @ `50af2c2`.
> The prior AI ("Antigravity") claimed Phase 1 + Phase 2 passed and marked Phase 3
> "not yet executed." **This session independently re-ran Phase 1 + Phase 2 (claims reproduced)
> and additionally ran a real live Phase 3.** Re-run all of this against v4 before trusting it.

## Toolchain on this machine
go 1.26.4 · uv 0.10.0 · just 1.53.0 · kubectl v1.35.5+k3s1 · kind v0.22.0 · python 3.13.12.
**No Docker daemon. `sqlite3` CLI NOT installed** (use the Python `sqlite3` module). Machine runs
**k3s** (single node `fedora`, containerd) — see [05-environment-runbook.md](05-environment-runbook.md).

## Phase 1 — Contract tests: ALL PASS ✅
| Command | Result |
|---|---|
| `just test` (go test -race) | **22 packages ok, 0 fail, exit 0** (Go-cached). Prior AI said "23" — off by one, immaterial. |
| `just clockd-test` | **11 passed, 2 skipped** (~0.21s), ruff clean |
| `just harness-test` | **131 passed** (~1.12s), ruff clean |
| `CGO_ENABLED=0 go build ./...` | **exit 0** (pure-Go confirmed) |
| `just ci` | **"go CI gate passed", exit 0** |

## Phase 2 — All 11 corpus gates: ALL PASS ✅
Key metrics actually printed (all gates exit 0):
- **event-detection** — detection-fidelity exact, no-false-upgrade 0, digest-invariance OK, charter 0.
- **app-slo** — no-fabrication 0, no-cross-talk 0, charter 0.
- **departure** — fp-on-decoys **0 (CARDINAL)**, recall OK, charter 0. *(Also runs real `go test -race -count=1 ./obsd/internal/departure/`.)*
- **transitive-chain** — no-false-chain 0, chain-count OK, root + path fidelity OK.
- **projected-transitive** — band-monotonicity 0, band-width 0 (never collapses), charter 0.
- **xsvc** — root accuracy **1.000**, caller recall **1.000**, caller precision **1.000**, false cascades 0.
- **xsvc-projected** — worst confirmed lead **570s** (floor 120s), 0 stray, 0 false anticipations.
- **validate-claim** — FALSE-BLOCK **0 (ABSOLUTE)**, recall **1.000 (14/14)**, mutation-struct 1.000 (9/9). *(Also runs real `go test ./obsd/internal/api/`.)*
- **mcp** — byte-identical ledger, refusal-recall 1.000 over 9 honeypots, content-leaks 0.
- **incident** — key-purity OK, restart-invariant, charter 0.
- **events** — JOIN-FIDELITY 1.000, 4 standalone surfaced, digest-invariant, charter 0.

## Phase 3 — LIVE run against k3s: mostly PASS, one real miss ⚠️
`obsd` booted against the live cluster (ontology release **v0.4.0**, **63 entities, 62 Tier-A**,
ingested **6260 cadvisor streams**). API served on **`:9095`**. Evidence saved under `evidence/`.

- **S1 Provenance Firewall ✅** — `/api/findings` empty (cluster healthy, 0 crossings — correct);
  `/api/unexplained` has `blindSpot` stated; `/api/warnings` `enabled:false` + honest `gateNote`
  ("never a fabricated future"); `/api/insights` separate structured surface. **Storage:** separate
  `findings`, `unexplained`, `incidents` tables; **NO `warnings` table** (forecasts ephemeral).
- **S3 Unknown Anomaly (restraint) ✅ — the strongest live demo.** Deployed `chaos/stress-rogue`
  (polinux/stress-ng, CPU+mem limits). Entities 63→**64**. `/api/unexplained` → 1 openCard:
  - `mark` = exactly `"anomalous — investigate · not-yet-explained"`
  - `matchCheck` = `"loud on container_cpu_usage_seconds_total; no full or degraded phenomenon match covered these states this window"`
  - loudState: CPU bar-crossing `above`, `barSource: config` (borrowed from the pod's own limit), `status: aging`
  - **Zero causal vocabulary** in the response. (evidence: `evidence/s3-unknown.json`)
- **S5 Replay Determinism ✅** — captured 22 ticks live, then `bin/replay -bundle data/captures`
  twice: **"all 22 ticks replayed byte-identically (doc 05 §3.5 holds)"**, `diff` = **zero**
  (evidence: `evidence/s5-diff.txt`, 0 bytes). The last 5 ticks even captured the stress pod
  (`findings=1`) and were still byte-identical.
- **S7 Overhead ⚠️ FAIL on memory** — CPU **1.6–1.8%** (pass, <0.1 core). **RSS 162.8 → 176.3 MiB
  over time — OVER the documented `<150 MiB` bar.** This was claimed but never measured live; it
  does not hold in the host-run + capture-enabled config. (evidence: `evidence/s7-overhead.txt`)
- **S8 Interdependency ✅ (partial live)** — `/api/topology` **59 nodes / 57 edges / 30 valid /
  31 selected**; `/api/root-cause-chain` returns correct `class: "MEASURED ⋈ AUTHORED (joined,
  never fused)"` and **honestly reports the flow lane OFF** (needs `--flow-enabled` + a conntrack
  DaemonSet that isn't deployed, so live cascade *chains* don't render — the Phase-2 gates prove
  that math). `/api/cross-service` shows honest lane states.
- **S9 Claim Referee ✅** — fabricated "...caused by..." → `flagged:true` (class `causal`,
  `labelledBestEffort:true`); factual claim → `flagged:false`. Works live. (Doc says class
  `generated-causation`; actual is `causal` — see file 03.)

## Verdict on the prior AI's claims
**TRUE and reproduced.** Antigravity did not fabricate results — Phase 1/2 are genuinely green, and
Phase 3 (which it honestly deferred) works live. Its only misses were (a) never running Phase 3, so
(b) the stale Phase-3 commands and the unverified S7 memory bar went unnoticed. See file 03.

## Cleanup done this session
Stress pod + `chaos` namespace deleted; `obsd` stopped. `evidence/` and `data/captures/` left in
place as artifacts. `online-boutique` namespace was already deployed (pre-existing, with some stale
crashlooping replicasets — not created by this session).
