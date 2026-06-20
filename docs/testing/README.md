# Vigil — Test Strategy

> The honest, current map of how Vigil is tested: what is *proven*, how to run it, and
> what is *roadmap*. It supersedes the deleted root `TESTING.md` (the prior framework
> over-claimed gate uniformity and shipped stale Phase-3 commands; this is the corrected,
> verified version). Phase/audit history: [`../audit-v3/`](../audit-v3/).

## Philosophy (why the tests look like this)

Vigil's contribution is **epistemic discipline** (doc 01): every datum is MEASURED,
PROJECTED, or AUTHORED, joined-never-fused; detection is deterministic and non-gating;
nothing is invented. So the test strategy *is* the product guarantee:

- **Determinism-first.** Same readings + same graph version + same topology ⇒ byte-identical
  fingerprints, matches, and digests. Golden fixtures, `go test -race`, injected clocks
  (no `time.Now` in logic), table-driven.
- **Must-NOT-fire is first-class.** Every silent-failure mode (mis-join, false equivalence,
  fabricated cause, mis-calibrated band) has a negative test. Restraint is asserted, not hoped.
- **Honest partial coverage.** A gap is a stated finding, never a hidden one — including in
  the *tests themselves* (see the gate-rigor tier list below).

## The test tiers (and how to run them)

| Tier | What it proves | Cluster? | Command |
|---|---|---|---|
| **Contract / unit** | determinism, provenance firewall, mis-join defense, restraint, the clock no-strings charter | no | `just ci` (= lint + gen-check + `go test -race ./...`) · `just clockd-test` · `just harness-test` |
| **Corpus gates** | the real engine graded against independent oracles, byte-locked by drift guards | no | the 11 `just *-gate` recipes (see [gate-rigor.md](gate-rigor.md)) |
| **Live E2E** | the whole live pipeline (scrape→identity→bind→detect→`/api`) + restraint + live replay determinism | yes | `just e2e` |
| **Scale / soak / churn** | the per-tick cost curve + live discovery footprint + no mis-joins at scale + no leak over time | partial | `just bench` · `just scale` · `just soak` ([scale-and-soak.md](scale-and-soak.md)) |
| **Forecast gate** | the REAL TimesFM forecast pipeline calibration (band coverage / recall / ttc) | no | `just forecast-gate <bundle>` ([forecast-gate.md](forecast-gate.md)) |
| **Security** | bearer auth deny-by-default on `/api` + `/mcp` | hermetic + live | `go test ./obsd/internal/api/ -run Auth` · `just e2e` (`TestLiveAuth`) ([security-and-auth.md](security-and-auth.md)) |
| **Persistence** | schema migration; a new binary opens an old DB; incident memory across upgrades | no | `go test ./obsd/internal/store/ -run 'Migrate\|NewBinary\|IncidentMemory'` ([db-migration.md](db-migration.md)) |

### The 3 phases (a reviewer's path)

1. **Contract (no cluster, ~10 min):** `just ci && just clockd-test && just harness-test` + `CGO_ENABLED=0 go build ./...`.
2. **Corpus gates (no cluster, ~5 min):** the 11 `just *-gate` recipes — see [gate-rigor.md](gate-rigor.md) for the honest tier of each.
3. **Live (a single-node cluster, ~15 min):** `VIGIL_TEST_KUBECONFIG=<path> just e2e` then `just scale`. On a fresh cloud node, see [cloud-runbook-gcp.md](cloud-runbook-gcp.md).

CI: `ci.yml` runs the hermetic tiers on every PR; `integration.yml` runs the live e2e + a forecast-plumbing smoke nightly + on demand.

## ABB Theme-2 question → the test that answers it

The problem statement's operator questions, each mapped to a *real, runnable* test:

| Theme-2 operator question | Capability | Proving test |
|---|---|---|
| "Which pod is causing unexpected CPU/RAM spikes?" | real-time discovery + loudness routing | `TestLiveDetectionAndRestraint` (memory-leak fires; rogue stress routes to `/api/unexplained` with no invented cause) |
| "How are PVC I/O patterns linked to pod restarts?" | KSM object-state lane | `TestLiveDetectionAndRestraint/volume_mount_failure` (a stuck PVC → `PHEN_VOLUME_MOUNT_FAILURE`) + the events lane (`IMAGE_PULL_FAILURE`, `PROBE_FAILURE_RESTART`) |
| "Are different services influencing each other's resource consumption?" | cross-service / transitive cascade | `xsvc-gate`, `transitive-chain-gate` (MEASURED chains; independent faults never merge) |
| "Which workloads need optimization?" | coverage / resolvability | `/api/coverage` (Tier-A, resolvability, per-phenomenon observability) — surfaced live in the e2e |
| "Hundreds of pods across namespaces on a single node" | scale | `BenchmarkMatch` (to 5,000 entities) + `TestLiveScale` (live discovery, 0 mis-joins, churn) |
| "Intelligent forecasting / minutes of warning" | the clock | `just forecast-gate` (real TimesFM, evidence in `corpus/labels/forecast-gate-09M3.md`) |
| "Anomaly detection without false causation" | the unexplained channel + charter | `TestLiveDetectionAndRestraint/restraint_unexplained` + the charter battery |

## Honest framing — proven vs. roadmap

Lead with the **proven core**: provenance separation, join-never-fuse, **live replay
determinism** (capture on a cloud cluster, replay byte-identical on a laptop), identity
mis-join defense, must-NOT-fire restraint, dependency mapping, the KSM object-state lane,
and now **a live e2e, a measured scale envelope, bearer auth, and a DB-migration path**.

Present as **roadmap, with the gates already architected**:
- **Forecasting:** the real-model gate is reproducible (`just forecast-gate`) and passed a
  recorded campaign (09 M3 evidence), but is **on-demand, not routine CI** (the TimesFM
  download is multi-GB). Pitch it as "validated, reproducible," not "continuously gated."
- **Enterprise security:** bearer auth + threat model land here; **mTLS / per-caller authz
  belong at an ingress/mesh** (documented, not built into obsd).
- **Scale ceiling:** characterized to thousands of entities (bench) and ~tens of live pods;
  the hundreds-of-pods live run needs a node with a raised pod cap (cloud runbook).

What changed from the audit-v3 verdict ("partly business-grade"): the four headline gaps —
no live e2e, scale uncharacterized, zero auth, no migration story — are now **closed and
verified**; forecasting moved from "unbacked" to "validated + reproducible (on-demand)".

## Files in this suite

- [gate-rigor.md](gate-rigor.md) — the honest tier list of all 11 corpus gates (which are drift-guarded vs fixture-replay) + what Track-5 hardening caught.
- [scale-and-soak.md](scale-and-soak.md) — the measured scale/soak/churn envelope.
- [forecast-gate.md](forecast-gate.md) — the reproducible real-model forecast gate.
- [security-and-auth.md](security-and-auth.md) — the auth threat model + what's deferred to the platform.
- [db-migration.md](db-migration.md) — schema versioning + the upgrade guarantee.
- [cloud-runbook-gcp.md](cloud-runbook-gcp.md) — stand up a single-node k3s on GCP and run the live tiers against it.
- [cloud-runbook-aws-azure.md](cloud-runbook-aws-azure.md) — the same single-node rig on AWS (m6i.2xlarge) or Azure (D8s v5); both share `deploy/cloud/bootstrap-vigil-edge.sh`.
