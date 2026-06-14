# Incident-memory backtest GATE — v3 T-B / doc 03 + 14 §1.4 / doc 11 §3.5 — 2026-06-14

The gate that certifies the durable **cross-run incident memory**: a phenomenon that
fires, resolves, and re-fires is ONE incident with a recurrence count — not two
findings and not one continuous span. This is the rigorous, deterministic
certification of the recurrence semantics that the **live kind cluster could not
produce** (its OOM signals are continuous or unreported — see the live-verification
note below).

**Verdict: PASSED** (`just incident-gate`, exit 0) — both halves green.

## What the gate is

FULLY DETERMINISTIC — there is no model in the loop, so the gate asserts **exact
equalities**, not statistical bands. Two halves:

1. **Go unit half** (`go test -race`): the incident core (key-purity,
   graph-version-not-in-key, recurrence-only-across-gap, separation, bucket-boundary,
   restart-invariance, role-unresolved-honest, determinism), the durable store
   (cross-check vs the pure `Accumulator`, **real DB restart-survival**,
   continuous-span no-inflation), and the replay engine fold (`incidentTick` resolves
   findings to role CEIs exactly as obsd's store wiring does).
2. **Python corpus half** (`harness/src/harness/incident_memory_gate.py`) over the
   frozen corpus in `corpus/incident-memory/` — offline, no cluster.

The replay engine's `IncidentEval` pass (`replay -incidents`) folds per-tick findings
through the SAME `incident.Accumulator` obsd's store uses, with the accumulator
**persisted across `FrameRunStart`** (unlike the cascade tracker) — that ordering IS
the restart-invariance. The pass is dispatched after `Digest()` and is never an input
to it (non-gating, off the digest — the v2/phase-E construction).

## Scoring discipline

| floor | rule |
|---|---|
| KEY-PURITY | every incident key is reproducible as `sha256(phenomenon │ role │ bucket)` — the scorer **recomputes the sha256 independently in Python** and rejects any key it cannot reproduce. A learned/opaque key fails. This is the charter's deterministic-grouping guarantee, certified. |
| GROUPING | a recurring condition reduces to ONE incident with recurrence == N (not N incidents, not one continuous span). |
| CONTINUOUS CONTROL | a continuous (sub-resolve-gap) condition stays recurrence **1** — no per-tick inflation (the property the live 12-min node-OOM span also exhibited). |
| RESTART-INVARIANCE | the `restart` bundle (a `Restore()` injected in the gap) has final incidents **byte-identical** to the `recurring` bundle — an incident survives a process restart. |
| SEPARATION | distinct phenomena/roles never collapse into one incident (3 distinct in the multirole bundle). |
| CHARTER | no incident field restates a cause or a projection (incidents carry only phenomenon ids + counts + timestamps). |
| sufficiency (INSUFFICIENT ≠ pass) | all four scenarios (recurring, restart, continuous, separation) must be present. |

## The frozen corpus (`corpus/incident-memory/`)

Source is stated honestly: **synthetic-fixture** — the real `incident.Accumulator` /
`incidentTick` fold (the same code obsd's store runs, cross-checked) over a SYNTHETIC
finding stream with explicit fire/gap/refire timing + a `Restore()` (restart) in the
gap. The recurrence LOGIC is the real code; only the finding stream is synthetic.
Regenerate with `REGEN_INCIDENT_CORPUS=1 go test ./obsd/internal/replay/ -run
TestRegenIncidentCorpus`.

- `events-recurring.jsonl` — fire / same-episode / resolve→refire ⇒ 1 incident,
  **recurrence 2**.
- `events-restart.jsonl` — the SAME observations with a `Restore()` in the gap ⇒
  identical final incidents (restart-invariance).
- `events-continuous.jsonl` — 8 sub-gap ticks ⇒ recurrence **1** (no inflation).
- `events-multirole.jsonl` — distinct phenomena/roles + a role-less node ⇒ **3**
  distinct incidents.

### Result

```
incident-memory backtest — class: measured_cross_run_incident
  scenarios:        ['continuous', 'recurring', 'restart', 'separation']
  key-purity:       OK (key from phenomenon|role|bucket)
  grouping:         OK (recurring N->1 incident, recurrence N)
  continuous ctrl:  OK (no per-tick inflation)
  restart-invar.:   OK (incident survives restart)
  separation:       OK (distinct phenomena/roles never collapse)
  charter:          0 violations (max 0)
  GATE: PASSED — the incident memory's recurrence semantics are certified (deterministic)
```

## Why the gate (not the cluster) certifies recurrence

A live recurrence climb (1→2) was **not reproducible** on the kind cluster:
`container_oom_events_total` reads 0 (no cgroup-OOM detection), and the node OOM
signal is continuous (no >45s quiet gap to cross, even after deliberately deleting the
source and waiting 80s). Those are cluster-signal limitations, not Vigil defects. The
live run DID prove the wiring, role-resolution, honest-partial, separation, and — over
a ~12-minute continuous span — the no-per-tick-inflation guarantee (the gap branch
correctly declining to increment). This gate supplies the deterministic resolve→refire
the cluster could not, certifying the increment branch end-to-end.
