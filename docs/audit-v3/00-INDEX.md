# Vigil — Testing & Audit Context Pack (v3 snapshot)

> **Purpose.** This folder preserves everything established in the v3 testing/validation/audit
> session so a fresh session (on **v4** or anywhere) starts with full context — no need to
> re-derive it from chat history.
>
> **Snapshot point.** Branch `v3`, commit **`50af2c2`** ("console(graph): fix periodic
> collapse"), dated 2026-06-17. Audit/validation performed **2026-06-18**.
>
> **Project.** Vigil — Kubernetes-native AI observability for the ABB Accelerator hackathon,
> **Theme 2: AI agents for real-time pod resource discovery + dependency mapping.**

---

## ⚠️ READ FIRST — the v3 → v4 caveat

This entire pack describes **v3**. A teammate created **v4** with changes. Therefore:

- **File paths and line numbers cited here are v3.** They may have shifted in v4 — re-locate by
  symbol/name, not by line.
- **Findings may be stale.** A flaw listed here might already be fixed in v4, or v4 may have new
  code these tests don't cover. **Re-run the validation against v4 before trusting any verdict.**
- The fastest re-validation path is in [05-environment-runbook.md](05-environment-runbook.md):
  `just ci`, `just clockd-test`, `just harness-test`, the 11 gates, then the live Phase-3 run.
- **First thing to do in v4:** `git log v3..v4 --oneline` and `git diff v3..v4 --stat` to see what
  actually changed, then re-run the gates and diff the audit findings against the new code.

---

## TL;DR verdict (one paragraph)

The **deterministic detection core is genuinely and non-circularly tested** against the real
engine — provenance separation, join-never-fuse, replay determinism, identity mis-joins, and
must-NOT-fire restraint all have real teeth. **Phase 1 (contract) and Phase 2 (11 corpus gates)
pass for real**, exactly matching the prior AI's claims. **Phase 3 (live) was never run by the
prior AI; this session ran it** against a live k3s cluster and it works (detection, the
unknown-anomaly restraint test, replay determinism, the claim referee). **BUT** the suite is **not
yet business-grade**: the headline forecasting clock (TimesFM) is **never validated by any
automated test**, there is **no automated live end-to-end test**, **zero API auth**, **no scale/soak
coverage**, and **no DB-migration story**. Pitch the proven core confidently; present forecasting
+ enterprise scale/security as a roadmap, not as done.

---

## Files in this pack

| File | What's in it |
|---|---|
| [01-testing-framework.md](01-testing-framework.md) | What the testing framework IS — 3 phases, 9 scenarios, 5 guarantees, 11 corpus gates, every `just` recipe |
| [02-validation-results-v3.md](02-validation-results-v3.md) | The **actual results** this session produced — Phase 1, Phase 2, and a real **live Phase 3** run against k3s, with numbers + evidence files |
| [03-flaws-and-doc-bugs.md](03-flaws-and-doc-bugs.md) | Concrete, fixable bugs — TESTING.md wrong port/flags/commands, the S7 overhead miss, S9 class-name mismatch |
| [04-deep-audit-verdict.md](04-deep-audit-verdict.md) | The **10-agent deep audit** — what's genuinely strong, what's circular/weak (ranked), business-grade gaps, and a prioritized 7-item roadmap |
| [05-environment-runbook.md](05-environment-runbook.md) | Exact, copy-pasteable commands that **actually work on this machine** (k3s, not kind) to reproduce all three phases |

## Related artifacts already on disk (v3 working tree)

- `TESTING.md` (repo root) — the framework doc the prior AI authored (has the doc bugs noted in file 03).
- `evidence/` — JSON/text captured from the live Phase-3 run (s1–s9). See file 02.
- `data/captures/` — the replay bundle captured live (manifest + bars-1..5 + segment). Used for S5.
- Prior AI ("Antigravity") work: `~/.gemini/antigravity/brain/d9e2f3f7-c766-4d96-8360-0cfd474fb1e3/`
  (`walkthrough.md` = its claimed results; `implementation_plan.md` = its plan). Second session
  `f21c073e-…` holds an "enterprise PoV" plan.
- Claude Code memory (auto-loaded next session): `live-test-environment`, `testing-validation-outcome`.
