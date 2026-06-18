# Performance regression guards (hermetic, CI-runnable)

Evidence for the allocation-based perf guards that turn the existing (assertion-free)
`just bench` characterization into a CI gate, WITHOUT flaky ns/op thresholds.

## Why allocations, not time

ns/op is too noisy on shared CI runners to pin. Allocation counts (via
`testing.AllocsPerRun`) are a **deterministic property of the compiled code path** — the
identical count on a fast or slow machine — so they gate cleanly. They catch the common
algorithmic regressions (an accidental per-entity copy, an O(n²) walk, a marshal bloat).

## The adversarial-review lesson (why the first design was a shallow proxy)

The scale benchmark calls `Match(fps, selected, nil, w)` with **`topo == nil`**, which skips
EVERY spanned phenomenon AND the blast-radius walk (`match.go`) — exactly the O(entities²)-
prone code (`cascade.go blastRadius` loops selected-keys per finding). Production always
passes a REAL topology (`binder.go`). A guard over the nil-topo path would certify code
production never runs. So `obsd/internal/detect/perf_guard_test.go` builds a **real
EdgeStore topology** (runs-on edges at scale) + a realistic `selected` participation map and
exercises that production path (blast radius active; `findings > 0` asserted, else vacuous).

## detect — `TestMatchPerfGuard` (real-topology Match)

Baseline (real released graph, `go test -race`, 2026-06-18, Go 1.26), with a FIXED incident
count (~10 simultaneous leaks — a realistic cluster, keeping blast radius O(findings×n)=O(n)):

```
allocs/entity: n=1000 → 357.1,  n=4000 → 349.9   (flat, ratio ≈ 0.98)
```

Two teeth:
- **Alloc ceiling** `allocs/entity ≤ 450` (~26% headroom; a doubling ~710 trips it).
- **Scaling flatness** `allocs/entity(n=4000) / allocs/entity(n=1000) ≤ 1.5` (baseline ≈ 1.0).
  Graph-version-robust (the per-entity phenomenon count cancels in the ratio), so this is the
  primary O(n²) teeth.

**Teeth proven:** switching the fixture to a fixed FRACTION of leaks (the O(n²) broad-
participation shape) drives allocs/entity to 752 → 2245 (ratio **2.98×**); BOTH the ceiling
and the flatness check fire. Restored → PASS.

## store — `TestUpsertPerfGuard` (production-shaped findings)

The scale benchmark uses THIN findings (empty Members), so per-finding JSON-marshal bloat is
invisible — `UpsertFindings` marshals `Members`+`Unobservable` to JSON columns. The guard uses
**production-shaped** findings (3 members each). Baseline: ~32 allocs/finding (plain) / ~38.7
under `-race`; ceiling **60** (~55% headroom; a doubling ~77 trips it).

## Honest limit (the gap is PARTIALLY closed)

A constant-factor CPU/IO regression (≈2× the work, still O(n), with FLAT allocations) is
caught by NEITHER guard — that needs an absolute-ns floor, the flaky quantity we avoid.
`just bench` (`BenchmarkMatch` / `BenchmarkUpsertFindings`) stays the informational manual
catch for it. Live RSS/mis-join floors live in the nightly integration scale/soak job — whose
mis-join integrity assertion was hardened this pass: a MISSING misjoins metric now FAILS
(was a silent `t.Log`-skip), so a renamed/dropped metric can't pass the integrity check
vacuously.

## Cost + escape valve

Both guards run under the standard `go test -race ./...` (CI + `just test`); ~8.5s (detect) +
~3s (store) under race. `-short` skips them for tight local loops; CI runs without `-short`.
Graph-content changes that raise the per-entity baseline require a deliberate re-pin (the
ceilings are documented with their baseline + date in each file header).
