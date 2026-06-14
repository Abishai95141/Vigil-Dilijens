# events-gate — evidence (v3 T-C, discrete k8s events ingestion)

**Verdict: PASSED** (`just events-gate`, exit 0). Certifies the discrete-event JOIN
deterministically, offline, no cluster.

## What is certified

k8s Events (OOMKilled, CrashLoopBackOff, …) are ingested as a **distinct typed
MEASURED finding source** and JOINED — corroborate, never fuse — to the gauge
phenomena on the same workload **role CEI**. The load-bearing design constraint
(doc 07): events do NOT flow through the fingerprint-keyed matcher; they are a
finding-level join by exact CEI. The "which event reason corroborates which
phenomenon" mapping is **AUTHORED** (`ontology/graph/overlays/experimental/event-conditions-v1.yaml`),
surfaced verbatim with provenance — never invented by code.

## Why a standalone JOIN gate (not a bundle-replay pass)

k8s events are not in the replay bundle and never enter the deterministic digest
(`replay.Digest` hashes fingerprints/findings/cascades/unexplained only). A
bundle-replay `-events` pass would have nothing real to read. Instead the gate folds
synthetic-but-real-shaped scenarios through the **REAL** code:
`events.ResolveEventRole` (real `identity.Store` minting + lookup) →
`events.Corroborate` (the exact-CEI join) → `replay.Digest` (the digest-invariance
check). The anti-shallow core is a **LABEL ORACLE**: every event's ground-truth role
identity is fixed by how the regen test builds the identity store and recorded
INDEPENDENTLY of the emitter's own resolution. The scorer grades the resolved role
against the oracle, and a deliberate **identity-mismatch** scenario MUST fail to
corroborate.

This mirrors the mcp-gate precedent (synthetic inventory folded through the real
`binding.Compile`); the corpus is synthetic-fixture, stated honestly.

## Floors (all PASSED)

| Floor | Result | Meaning |
|---|---|---|
| JOIN-FIDELITY == 1.0 | **1.000** (7 events) | every event resolves to its oracle role; corroborates iff the oracle says so. The identity-mismatch scenario (event on role A, gauge on role B) did **not** corroborate. A single false join fails the gate. |
| NO-PHANTOM == 0 | **OK** | no corroboration where the oracle forbids it (an unrelated event cannot manufacture corroboration). |
| STANDALONE-VISIBILITY ≥ 1 | **4** | not-corroborated events (CrashLoopBackOff, a role-less node OOM, an OOMKilled with no gauge on its role) are STILL surfaced as visible MEASURED findings — never dropped, never upgraded to a match. This is the blind-spot-closing property. |
| VISIBILITY | **OK** | no event silently dropped (every oracle event appears as a row). |
| COUNTS | **OK** | corroborated/standalone/unresolved tallies match the label per scenario. |
| DIGEST-INVARIANCE | **OK** | `digestBefore == digestAfter` per scenario — the join is read-only w.r.t. the deterministic findings (join, never fuse; events ride OFF the digest). |
| CHARTER == 0 | **0** | no event row restates a cause — an event is a co-occurrence. |
| Sufficiency | **OK** | all 5 required scenarios present. |

## Corpus (`corpus/events/`, frozen)

Regenerate: `REGEN_EVENTS_CORPUS=1 go test ./obsd/internal/events -run RegenEventsCorpus`

- `corroborated-oom` — OOMKilled on currency + a cgroup-OOM gauge finding on the same role → **corroborated** (the blind-spot-closing join; cAdvisor `container_oom_events_total==0` on kind, yet the event fires).
- `multi-role` — two OOMKilled events on two roles, two same-role gauge findings → two corroborations, **no cross-role join** (separation).
- `identity-mismatch` — OOMKilled on currency but the only gauge finding is on cart → **MUST NOT corroborate** (the identity mis-join trap; exact-CEI match is the guarantee).
- `standalone-crashloop` — CrashLoopBackOff + an OOMKilled with no gauge + a role-less node OOM → all visible, none corroborated, node unresolved.
- `healthy-negative` — gauge findings exist but NO events occurred → zero event rows, nothing manufactured.

## Two halves

- **Go unit** (`go test -race ./obsd/internal/events ./obsd/internal/api -run 'Events|Corroborate|ResolveEventRole|LoadEventConditions|EventConditions'`): the join, the real role-resolution (resolved/miss/node-unresolved), `LoadEventConditions` validation (missing-why/illegal-role/duplicate/missing-author rejected), the graph-resolution guard (every `corroborates` target is a real phenomenon), and `TestEventsJoinDoesNotPerturbDigest` (the always-on digest-invariance tripwire).
- **Python corpus** (`harness/src/harness/events_corroboration_gate.py` + `harness/tests/test_events_corroboration_gate.py`, 10 adversarial tests, one per floor).

## Live verification (kind-vigil, 2026-06-14)

Ran `obsd --events-enabled --api --mcp-enabled` against the real boutique with two
chaos pods (an OOMer + a CrashLooper) in an isolated namespace. **A live finding
shaped the collector**: on kind an OOM emits **no `OOMKilled` Event at all** (the
event reasons are only BackOff/Started/Created/…); the OOMKilled fact lives ONLY in
`pod.status.containerStatuses[].lastState.terminated.reason` (exitCode 137), and the
canonical CrashLoop reason is `state.waiting.reason == "CrashLoopBackOff"` (the
*event* reason is the noisier `BackOff`). The collector therefore reads **two
sources** — the Events stream AND pod containerStatuses — the latter being the
authoritative, reliable one. This is not a proxy; pod status is the canonical
representation of these facts.

`/api/events` (and MCP `get_events`) then surfaced **3 MEASURED findings**, all
role-resolved (`role_unresolved=0`):
- `OOMKilled` → `online-boutique/Deployment/currencyservice` — a **genuine** boutique
  OOM (restartCount 3), surfaced standalone.
- `OOMKilled` → `vigil-tc-chaos/Deployment/oomer` — the chaos pod, count climbing.
- `CrashLoopBackOff` → `vigil-tc-chaos/Deployment/crasher` — count 5.

All three were `corroborated=false` (**standalone-visible**) — exactly because
cAdvisor reports `container_oom_events_total=0` on kind, so the gauge
`PHEN_OOM_KILL_CGROUP` never fired. **This is the blind spot closing for real**: an
OOM that the entire fingerprint/gauge path missed is now a visible MEASURED finding
joined to the correct workload role. Non-gating confirmed (coverage/findings surfaces
unaffected, no errors, released graph version unchanged). The chaos namespace was
deleted afterward; the boutique was untouched.

**Honest limitation (same as T-B):** the corroboration JOIN *firing* could not be
demonstrated live — it requires a gauge `PHEN_OOM_KILL_CGROUP` finding on the same
role, which cannot exist on kind (`container_oom_events_total=0`). The join is
certified deterministically by this gate's `corroborated-oom` + `multi-role`
scenarios instead.

## Isolation / non-gating

The events lane is behind `--events-enabled` (default off). Off ⇒ obsd is
byte-identical (full `go test -race ./obsd/...` unchanged). The collector runs in its
own goroutine off the eval path; the join is recomputed each tick into an atomic
snapshot the surface reads, never into the findings slice. Events are MEASURED (an
existing class), so they surface behind the enable flag exactly like incident-memory
— there is no separate gate-passed withholding const (that is only for new classes:
PROJECTED / ADVISORY).

The authored corroboration overlay lives under `overlays/experimental/` (outside the
production glob), so it does NOT change the released graph's content hash — zero blast
radius. Promotion into the released ontology is a governed follow-up, mirroring the
cross-service relation's Phase A→C promotion.
