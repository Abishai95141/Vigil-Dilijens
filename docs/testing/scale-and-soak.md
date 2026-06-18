# Scale, soak & churn — the operational envelope (Track 2)

> Closes audit-v3 roadmap **#6** ("scale uncharacterized"). Answers the ABB Theme-2
> headline directly — *"even single-node deployments often host hundreds of pods across
> multiple namespaces"* — with **measured numbers**, not assertions. All figures below
> were produced on the dev box (Intel i7-14650HX, k3s single node); re-run anywhere with
> the recipes at the end. Re-measure on the target hardware before quoting externally.

## TL;DR

- **Detection scales linearly and cheaply.** The per-tick matcher costs **~9 ms at
  1,000 entities** and **~41 ms at 5,000** — i.e. ≤0.3 % of a 15 s evaluation tick even
  at 5,000 synthetic entities, an order of magnitude past "hundreds of pods."
- **The SQLite findings store is not a bottleneck.** Single-writer upsert of **1,000
  findings/tick = ~7.8 ms**.
- **Live discovery is correct and bounded at scale.** +50 real pods → obsd discovered
  all of them, **RSS +37 MiB (~775 KiB/entity)**, **zero mis-joins**, and churn-to-zero
  GC'd identities back to baseline with mis-joins still zero.
- **Steady-state RSS is ~185 MiB** at ~64 entities (host-run, replay capture on),
  plateauing as the qss rings fill — which **corrects the ungrounded "<150 MiB" bar**
  the prior docs claimed (audit finding B).

## 1. Detection hot path — `BenchmarkMatch` (hermetic, real graph)

`obsd/internal/detect/scale_bench_test.go`. Entity-local matching over N synthetic
fingerprints (≈5 % faulty) against the **real released ontology graph**.

| entities | ns/op | per-tick | B/op (alloc) | findings |
|---:|---:|---:|---:|---:|
| 100   | 1,009,678  | ~1.0 ms  | 680 KB  | 5   |
| 500   | 4,544,825  | ~4.5 ms  | 3.4 MB  | 25  |
| 1,000 | 8,988,105  | ~9.0 ms  | 6.8 MB  | 50  |
| 5,000 | 41,158,496 | ~41 ms   | 33.9 MB | 250 |

Cost is ≈O(entities) (each entity is screened against every entity-local phenomenon).
At the 15 s eval tick, **1,000 entities consume ~0.06 % of the tick budget**. (Spanned/
first-order phenomena add a topology walk on top; measured here is the entity-local core.)

## 2. Findings store — `BenchmarkUpsertFindings` (single-writer SQLite)

`obsd/internal/store/scale_bench_test.go`. The store is deliberately single-writer
(`SetMaxOpenConns(1)`, CGO-free `modernc.org/sqlite`) and **off the deterministic path**.

| findings/tick | ns/op | per-tick |
|---:|---:|---:|
| 10    | 102,272   | ~0.10 ms |
| 100   | 773,691   | ~0.77 ms |
| 1,000 | 7,750,612 | ~7.8 ms  |

Linear; even 1,000 findings/tick is well under the tick. The single-writer design is
not a throughput concern at realistic finding volumes.

## 3. Live scale + churn — `TestLiveScale` (integration)

`obsd/internal/e2e/scale_test.go`. Packs N synthetic pods (`VIGIL_SCALE_N`, default 50),
asserts discovery + bounded RSS + **zero mis-joins** + clean churn GC.

Measured (N=50 on top of the existing workload):

```
baseline : 69 entities · RSS 124 MiB
at scale : 119 entities (+50) · RSS 162 MiB (+37 MiB, ~775 KiB/entity)
integrity: 0 mis-joins at 119 entities
churn→0  : entities returned to ~baseline · 0 mis-joins
```

~775 KiB/entity reflects the per-stream qss hot rings (each entity carries several
cAdvisor streams × 240-sample rings). **k3s defaults to `--max-pods=110`**, so the local
default is 50; on a cloud node raise the pod cap and set `VIGIL_SCALE_N` into the
hundreds (see the GCP runbook).

## 4. Soak / leak — `TestLiveSoak` (integration, opt-in)

`obsd/internal/e2e/soak_test.go`. Runs for `VIGIL_SOAK_DURATION`, sampling RSS and
asserting it stays bounded with zero mis-joins. A 2-minute smoke run:

```
soak start : RSS 125 MiB
+30s : 156 MiB   +60s : 171 MiB   +90s : 185 MiB   +120s : 184 MiB   (peak 185)
misjoins 0 throughout
```

RSS **rises as the rings fill, then plateaus** — the expected shape. A real leak test is
a multi-hour run (`VIGIL_SOAK_DURATION=2h`); do it on a *steady* workload (see the known
limitation below).

## Honest findings (this is the point of measuring)

- **The "<150 MiB" footprint bar was ungrounded** (audit finding B). Real steady-state at
  ~64 entities is **~185 MiB** (host-run + `--store-dir` capture). The tests now measure
  the footprint against a generous runaway ceiling (1 GiB) rather than a made-up bar; pick
  a doc-traceable budget from these numbers for the deployment profile.
- **Known limitation (task #51):** the qss hot rings do not yet evict dead-entity streams,
  so under heavy *churn* RSS grows ~4 KiB per departed stream until tombstone-driven ring
  eviction lands. Soak on a steady entity set; the churn test exercises identity GC (which
  *does* reclaim), not ring eviction.
- **node-exporter sharpens precision at scale:** without it node-PSI is unobservable, so
  `THROTTLING_CASCADE` can only ever be degraded and fires (degraded) on any throttling
  pod. Deploy node-exporter in the test/cloud environment for precise cascade detection.

## Running it

```bash
just bench                                   # hermetic detection + store benchmarks (no cluster)
VIGIL_TEST_KUBECONFIG=/etc/rancher/k3s/k3s.yaml just scale          # live, N=50
VIGIL_TEST_KUBECONFIG=/etc/rancher/k3s/k3s.yaml VIGIL_SCALE_N=300 just scale   # cloud, raised pod cap
VIGIL_TEST_KUBECONFIG=/etc/rancher/k3s/k3s.yaml VIGIL_SOAK_DURATION=2h just soak
```
