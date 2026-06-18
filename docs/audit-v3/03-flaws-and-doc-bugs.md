# 03 — Concrete Flaws & Doc Bugs (fixable)

> These are the **specific, actionable** defects found this session. Distinct from the deeper
> rigor critique in [04-deep-audit-verdict.md](04-deep-audit-verdict.md). Verify each still exists
> in v4 (some may already be fixed, paths may have moved).

## A. `TESTING.md` Phase-3 commands are wrong (they were never run, so nobody caught these)

1. **API port is `:9095`, not `:8080`.** Every Phase-3 `curl localhost:8080/api/...` in TESTING.md
   fails to connect. obsd serves `/api` on the **health server**, default `--health-addr ":9095"`.
   (Source: `obsd/cmd/obsd/main.go` `healthAddr` default; no `8080` anywhere.)
2. **The `obsd` start command is missing flags.** TESTING.md shows
   `bin/obsd --store-dir data/captures --db data/vigil.db`. As written it:
   - has **no `--kubeconfig`** → obsd never binds to the cluster → **0 entities** (empty inventory).
   - has **no `--referee-enabled`** → `/api/validate-claim` (S9) is absent / non-flagging, so S9 can
     "pass" against a dead stub.
   Correct: `bin/obsd --kubeconfig /etc/rancher/k3s/k3s.yaml --db data/vigil.db --store-dir data/captures --referee-enabled --health-addr ":9095"`
3. **`replay` is invoked wrong.** TESTING.md shows `bin/replay data/captures` (positional). The real
   flag is `-bundle`: `bin/replay -bundle data/captures`.
4. **`just up` cannot work on this machine.** It runs `kind create cluster`, which needs Docker.
   This machine has **no Docker** — it runs **k3s** (already up). Phase 3 must run against the
   existing k3s cluster, not via `just up`. (k3s IS a valid Theme-2 single-node target.)
5. **`sqlite3` CLI not installed.** S1's storage checks (`sqlite3 data/vigil.db ".tables"`) won't
   run. Use the Python `sqlite3` module instead (works fine).

## B. The S7 overhead bar is unverified and violated
- TESTING.md/walkthrough assert obsd RSS **`< 150 MiB`** as a pass criterion. Measured live it was
  **162.8 → 176.3 MiB** (grows with the capture buffer). The "150 MiB" number is **ungrounded** (no
  doc-traceable source) and the claim **does not hold** in the host-run + `--store-dir` config.
- CPU was fine (~1.8%).
- **Fix:** either constrain/measure the real footprint and pick a doc-traceable bar, or correct the
  claim. Also automate it (replace the manual `ps` snapshot — see file 04, TODO #6).

## C. S9 referee class-name mismatch (doc vs code)
- TESTING.md/walkthrough expect the flagged reason `class == "generated-causation"`.
- The actual API returns `class: "causal"` ("banned causal register (denylist): \"caused by\"").
- Behavior is correct; the **doc name is wrong**. Align the doc to the code (or vice-versa).

## D. TESTING.md oversells gate uniformity
- It presents all 11 corpus gates as equal "mathematical proof." In reality **5 are weaker**
  (`xsvc`, `xsvc-projected`, `events`, `mcp`, `incident`) — fixture-only at run time or guarded only
  by skip-only regen tests. See [04-deep-audit-verdict.md](04-deep-audit-verdict.md) §3. Re-label
  honestly or add the missing drift guards.

## Quick-win fixes (cheap, do early in v4)
- Patch the four TESTING.md command bugs (A1–A4) + the storage note (A5).
- Fix the S9 class name (C) and the S7 bar wording (B).
- These are doc/string edits; low risk, high credibility payoff if a judge follows the doc.
