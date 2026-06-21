# 29 — Anomaly Detection (CUSUM vs ADWIN) & Causal Inference for Associations

> Status: RESEARCH + LIVE EXPERIMENT (branch `v6`, 2026-06-21). Two architecture
> questions, investigated with a verified-real research dossier (workflow `wgziylnzi`,
> 40 agents, 2 load-bearing claims refuted) and a LIVE detector bake-off on the running
> abb-genix kind cluster. Companion: `docs/22` (C2 onset / C3 cohypothesis), `docs/27`
> (forecast trend-rescue), `docs/20` (assoc lane). Experiment harness:
> `harness/anomaly-bakeoff/`. **No production code changed.**

---

## Part A — The CUSUM → ADWIN proposal

### A.1 What we actually have (5 lanes; only 2 are CUSUM)

The critique ("we use CUSUM+MA for anomaly detection; ADWIN would be better; CUSUM
should only be the forecast-activation gate") conflates five distinct things:

| Lane | Algorithm | On digest? | Role |
|---|---|---|---|
| Core detection ([detect/match.go](../obsd/internal/detect/match.go)) | authored **threshold** matching — closed 3-primitive set | **ON** (replay) | the MEASURED path; **no CUSUM, no MA** |
| Onset / C2 ([onset/onset.go](../obsd/internal/onset/onset.go)) | **EWMA-residual CUSUM** + sustained-shift (MinZ) confirm | off | changepoint **TIME** + direction + StepZ |
| Forecast-activation ([forecast/trend.go](../obsd/internal/forecast/trend.go), doc 27) | **same EWMA-CUSUM** (2nd copy) | off | forecast eligibility gate |
| Departure ([departure/departure.go](../obsd/internal/departure/departure.go)) | sample vs own forecast band | off | PROJECTED band-exit |
| Unexplained/loud ([unexplained/loud.go](../obsd/internal/unexplained/loud.go)) | bar-cross OR rate-excursion | ON | loud-but-unmatched routing |

Two facts that reframe the question:
1. The "CUSUM+MA" is the **onset lane** — and its job is **changepoint TIMING, not anomaly
   scoring**. There is no anomaly/novelty score anywhere (charter).
2. "CUSUM should only be the forecast-activation gate" is **already true** — that is
   exactly `trend.go` (doc 27). CUSUM also legitimately serves onset timing.

ADWIN (Bifet & Gavaldà 2007) is a **concept-drift / change-in-mean** detector — the **same
family** as CUSUM, **not a spike detector**, and by design *suppresses* transient spikes.
So "ADWIN for spike/anomaly detection" is a **category error** (verification verdict:
*refuted*). The honest question becomes: for the **onset-timing / creep-detection** job, is
ADWIN better than our EWMA-CUSUM? → settled empirically below.

### A.2 The live experiment

Faithful Python port of `onset.go` (α=0.10, K=0.5, H=4.0, Warmup=12, MinZ=3.0) vs **river**
`ADWIN` and `PageHinkley`, with **ruptures** Binseg as an offline onset oracle. Real
abb-genix series captured via the API-server cAdvisor proxy + app `/metrics` while
injecting controlled faults (ground-truth inject times recorded). ADWIN/PH fed
baseline-normalized input (CUSUM is scale-invariant); determinism + compute measured.

**Results (detection latency after inject; lower = better):**

| Regime (real series) | Vigil CUSUM | ADWIN | Page-Hinkley |
|---|---|---|---|
| **Gradual creep** — mem_leak, pdm-analyzer mem | **+6s** | +66s | +56s |
| **Abrupt step (noisy)** — cpu_burn, stream cpu-rate | **+15s** (−2smp loc.) | **MISS** | MISS |
| **Controlled ramp** (+6σ, clean real-noise baseline) | **+5smp** | +46smp | +32smp |
| **Controlled clean step** (+8σ) | +20smp | **+14smp** | **+7smp** |
| **1-sample spike** (+8σ) | **no fire** | **no fire** | no fire |
| **Stationary controls** (untouched pods) | 0–10 fires* | **0–1** | 3 |

*The CUSUM fires on `smart-sensors`/`asset-registry` are largely **real small memory steps**,
not clearly false; ADWIN suppresses them.

**ADWIN delta sweep (giving it its best shot)** — identical at every delta 0.002→0.3:
creep +66s, cpu-step MISS, ramp +46smp. ADWIN's lag is **structural (windowing)**, not a
tuning artifact. **Determinism:** both byte-identical on re-run (replay-safe). **Compute
(4950 smp):** CUSUM 3.0ms, ADWIN 0.6ms, PH 1.1ms.

### A.3 Verdict — the swap is REFUTED as stated; CUSUM stays; ADWIN is at most a complement

- **Do NOT replace CUSUM with ADWIN for onset/anomaly.** It is a category error (neither
  flags spikes — confirmed) and, decisively, ADWIN **lags the gentle creep ~10×** and
  **misses the noisy real abrupt step at every delta**. Those are the cases Vigil's leak
  early-warning and forecast-activation exist for — replacing CUSUM there would directly
  **regress lead time** (the dossier's predicted "latency-regression risk", now measured).
- **CUSUM is correctly placed** — onset timing ([onset.go](../obsd/internal/onset/onset.go))
  + forecast-activation ([trend.go](../obsd/internal/forecast/trend.go), already the
  prescribed "CUSUM as activation gate"). The experiment confirms CUSUM is sharpest exactly
  on the creep the gate rescues.
- **ADWIN's genuine, narrow value:** near-parameter-free (one delta), quiet on noisy
  stationary controls (≈0 fires), cheapest compute, competitive on *clean large abrupt
  steps*. That makes it a candidate **complementary off-digest regime-shift / baseline-
  staleness sensor** — never a replacement — and only if its noise-suppression earns its
  keep on real onset triage.
- **If ever adopted (complement only), charter constraints:** pinned declared `delta`
  (never data-fit), stays **off-digest**, preserves a deterministic `(samples, params)`
  contract with pinned input order, never surfaced as a customer-declared MEASURED bar
  (its Hoeffding cut is self-relative). Pure-Go starting point:
  [monochromegane/adwin](https://github.com/monochromegane/adwin) (MIT, ADWIN2) — but it is
  dormant/5★/no go.mod → **vendor + `-race` + determinism audit**, do not depend on live.

**Honest caveats:** the live mem arm had a dead-flat pre-inject baseline (floored σ →
hypersensitive) — but the **controlled ramp on real-noise baseline still shows CUSUM 9×
faster**, so the conclusion holds independently. The live transient-queue arm was
noisy/multi-event and inconclusive → the clean 1-sample-spike control is the transient
test (neither fires). ADWIN required normalized input to be competitive at all (operational
note). "Stationary" fires may be real steps, not false positives.

**Recommended cleanup (charter-neutral, do regardless):** `onset.go` and `trend.go` are two
copy-ported CUSUMs kept in lockstep by hand — extract a shared internal CUSUM core so any
future change is one edit, not two.

---

## Part B — Causal inference over the associations

### B.1 The premise correction: there is no lagged correlation today

The assoc lane ([assoc/assoc.go](../obsd/internal/assoc/assoc.go)) computes **instantaneous
(lag-0) Pearson** over 15s-binned, same-index overlapping bins (|r|≥0.6, first 256 hot
streams ⇒ C(256,2)≈32,640 candidate pairs/cycle; live `streamsTotal`≈8,487). The "lead-lag /
partial-correlation" in [docs/20:78](20-dynamic-graph-extension.md) **was never
implemented** — it is aspirational. So the "thousands of *lagged* correlations" are
thousands of **contemporaneous** associations. Pairwise correlation cannot remove
confounder-driven or transitive edges, which is why the list over-connects.

### B.2 The charter-clean plan: offline conditional-independence pruning → ranked shortlist

A two-stage **offline harness** pass (Python/uv, NOT in obsd) that turns thousands of
undirected pairs into *tens* of confounder-aware, lag-annotated, human-adjudicable
candidates — **without auto-asserting direction**:

1. **Stage 1 — fixed-lag enrichment (NOT argmax).** For each surviving assoc pair, compute
   Pearson at a **pinned, declared** lag set τ ∈ {0, ±1, ±2, ±4 bins} → attach a lead-lag
   *hint* + |Δt| as MEASURED evidence (the order C3 already carries as `observedFirst`,
   never as direction). An argmax-τ fitted from data would be a **learned parameter** and
   read as a causal arrow — forbidden.
2. **Stage 2 — PCMCI / PCMCI+ conditional-independence discovery** ([tigramite](https://github.com/jakobrunge/tigramite))
   over only the survivors, seeded by the observed-flow topology. The MCI conditioning step
   is precisely what **removes common-driver and transitive edges** — the dominant
   dependency-graph noise. Output = a sparse, lag-resolved, confounder-flagged shortlist.
3. **Surface as CANDIDATES only.** The directed edges PCMCI produces are PROJECTED
   hypotheses → the **same firewalled candidate store** and the **same operator-authors-
   direction** path C3 uses (`/api/causal-hypotheses/author`, DecidedBy mandatory). The
   system continues to **REFUSE auto-direction**.
4. **Optional falsifier:** once an operator authors an edge, run [DoWhy](https://github.com/py-why/dowhy)
   refutation (placebo / random-common-cause / subset) to try to break it before it ships —
   matching Vigil's "falsify before surface" discipline. DoWhy consumes an authored DAG; it
   does not discover direction (the most charter-aligned causal tool).

**Verified-real frameworks** (offline harness, never vendored into obsd — all Python):
[tigramite](https://github.com/jakobrunge/tigramite) (GPL-3 → separate process),
[causal-learn](https://github.com/py-why/causal-learn) (MIT, PC/FCI/Granger/LiNGAM),
[lingam](https://github.com/cdt15/lingam) (MIT, orients contemporaneous same-bin edges),
[DoWhy](https://github.com/py-why/dowhy) (MIT, falsifier).

**Charter fit:** offline, off-digest, output is a ranked CANDIDATE list (never graph
writes); no learned edge/threshold enters the ontology; join-never-fuse (association + lag +
confounder-status presented adjacently); operator authors direction. The field's own
evidence (CIRCA/MicroRCA keep the graph human-provided; the doc-22 E3b/E4b results) and the
charter agree: **auto-discovered causal direction is unreliable** — use discovery to PRUNE
and RANK, not to orient.

**Honest limits:** PCMCI assumes causal sufficiency + **stationarity** (broken by HPA churn /
rollouts / the known regime-shift contamination) and an independent Sandia benchmark found
**high false-negative rates at scale** → the shortlist will MISS some true edges. Treat it
as high-recall decision *support*, gated to stationary windows, not an oracle.

### B.3 — PoC result (LIVE, on the abb pipeline)

Built and run end-to-end (`harness/causal-discovery/`): captured the pipeline panel on the
live cluster while **stepping the gateway load** (a common driver, 47→296 req/s) to create
propagating dynamics + confounding, then ran lag-0 → fixed-lag → PCMCI+.

- **Method validated** (toy with known ground truth): recovered the true chain x0→x1→x2 and
  **refused the spurious transitive x0→x2** (the conditioning that separates causation from
  correlation).
- **Pruning works (the core value):** on the live panel, **26 cross-entity lag-0 edges → 6
  directed candidates (77% fewer)**. The confounder audit identified **7 spurious common-driver
  edges** (e.g. `gateway.cpu ~ stream.cpu` r=0.53 → 0.27 once conditioned on the load driver) —
  and PCMCI **dropped all 7/7**. This is exactly "correlation ≠ causation": edges the assoc
  lane asserts as dependencies are revealed as shared-driver artifacts and removed.
- **Deterministic** (run1==run2 — the replay surrogate), **445 ms** for 12 streams × 62 bins.
- **Direction is unreliable at 15s bins** (the pipeline propagates faster than one bin → most
  links are contemporaneous; e.g. `stream.queue→gateway.req` came out backwards). This
  **validates the charter**: direction stays a PROJECTED hint the operator authors, never
  auto-asserted. Resolving lead-lag cleanly needs **finer sampling** and/or **slower
  integrator signals** (queue depth, staleness, memory) and/or a **longer window** (more bins
  → better orientation power).

**Verdict:** the harness works as intended for its primary purpose — turning the
over-connected lag-0 list into a small, confounder-pruned, ranked CANDIDATE shortlist,
charter-clean (offline; direction operator-authored). It is worth adopting. The lead-lag /
direction precision is resolution-limited today and is the obvious next iteration (finer
capture + a fault-cascade with clearly-lagged propagation), but the pruning value is real now.

### B.4 — Wired into obsd (the operator-authoring round-trip)

The harness shortlist is now ingested by obsd as DIRECTION-FREE candidates, surfaced at the
existing C3 surface, authored through the existing operator path — no new write path, no
auto-direction.

- **Producer** `obsd/internal/cohypothesis/discovery.go` (in the already-firewalled cohypothesis
  package, mirrors the C3 co-onset producer): parses the harness JSON shortlist and stages each
  surviving link as a `KindCausalHypothesis` candidate — Subject = the SORTED pair "a ~ b",
  Relation = "co-occurrence", source = `offline-causal-discovery`, method = `pcmci-parcorr`.
  PCMCI's direction + lag are PROJECTED hints in the payload, **excluded from the content id**
  (added to `nonIdentityPayloadKeys`), so re-running the harness updates the pair in place.
- **Lane** `--causal-discovery-path <shortlist.json>` (`causalDiscoveryLoop` in main.go): reads
  the file each interval (and once at startup), stages into the firewalled candidate store. Needs
  `--dgx-enabled`. Off-digest; never feeds detection (the candidate import-firewall enforces it).
- **Surfacing + authoring reuse C3 verbatim**: the candidates appear at `/api/causal-hypotheses`
  and a named operator authors the arrow at `/api/causal-hypotheses/author` (or rejects as
  not-causal). The system still REFUSES to author direction itself.
- **Live end-to-end validated** (test obsd on :9096, user's :9095 untouched): the lane staged the
  6-link load shortlist at startup → all 6 surfaced as `offline-causal-discovery` (direction-free)
  → empty-operator author **REFUSED** ("the system never authors") → a named operator authored
  `a-to-b` → **promoted + committable overlay** emitted → the candidate left the pending queue.
- **Gates green**: `go test -race ./...` (incl. the candidate/cohypothesis import-firewalls + 3
  new discovery tests: direction-free, hint-mapping, pair-stable dedup), `just lint`, CGO-free
  build, **graph hash unchanged** (`6c9e75be` — pure off-digest surfacing).

### B.5 — Closing the actual flood (the value that lands on the page)

The offline harness and the §B.4 wiring proved the *method*, but they did NOT touch the real
operational pain: the live governance page had **1613 `causal_hypothesis` candidates** — and
**1408 of them were over an hour stale** (a pair that co-stepped once and never again). The C3
co-onset lane staged every cycle with **no expiry and no cap**, so dead co-occurrences
accumulated forever. The pruning had to be applied **in-line on the lane that floods**, plus the
store had to be bounded — flagged in [docs/28 §L0] but not built until now.

Three changes, all off-digest, all MEASURED arithmetic (no learned threshold, only *removes*
candidates):
- **`candidate.Store.ExpireStale(now, ttl, kinds…)`** — deletes status=candidate rows whose
  `updated_at` is older than the TTL. A co-occurrence is "live" only while re-observed (Put's
  ON CONFLICT refreshes `updated_at`), so a pair that stops co-stepping ages out. DECIDED rows
  (the audit trail) are never touched.
- **`candidate.Store.CapKind(kind, max)`** — backstop: keep the N most-recently-updated.
- **`assoc.PruneConfounders(...)`** — the Go in-line form of the PCMCI prune: an ORDER-1
  conditional-independence test (the PC algorithm's first step). It removes an association a~b
  when conditioning on a single neighbour drops the partial correlation below a DECLARED floor
  (0.5) — a common driver / transitive path explains it away. So the C3 lane stages the
  surviving handful, not thousands of co-movers.
- **Wired into the co-onset loop** (`coHypTTL=15m`, `coHypMax=200`, `coHypIndepFloor=0.5`,
  `COHYP_TTL` env-overridable): expiry+cap run **every tick** (even with no new onsets); the
  prune runs before staging.

**Proven on the real `_run/abb.candidates.db`: 1613 → 40 (97% fewer)** — the TTL alone does it
(the cap doesn't even bind). Charter intact: off-digest, deterministic, decided rows preserved,
no learned cutoff. This is the change that actually empties the page.

---

## Appendix — Reproducibility

Harness: `harness/anomaly-bakeoff/` (`capture.py` cAdvisor+app capture, `vigil_onset.py`
faithful CUSUM port, `run_timeline.sh` live injection timeline, `analyze.py` bake-off).
Run: `uv venv && uv pip install river numpy scipy pandas ruptures statsmodels`, bring up the
abb cluster (doc 24) + simulator, `bash run_timeline.sh`, `python analyze.py`. Full research
dossier: workflow `wgziylnzi` (verified-real repos, 2 refuted claims).
