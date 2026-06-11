# Vigil — Task Checklist

Progress tracker, organized by the phased roadmap (`docs/13-rd-execution-roadmap.md`).
Build order is **risk-driven, not feature-driven**: silent failure modes (identity
mis-joins, false equivalences, unfalsified phenomena, mis-calibrated bands) are
attacked before the features they would corrupt.

**Legend:** `[x]` done · `[~]` in progress · `[ ]` not started · 🔒 gate (must pass to exit phase)

**Mandate:** build robust and complete; test functionality for real as it's built;
never substitute a shallow proxy to appear done. Report outcomes faithfully.

---

## ✅ Phase 0 scaffold (setup) — DONE

- [x] Toolchain on dev Mac: Go 1.26.4, just 1.52, buf 1.70 (uv/node/pnpm/docker/kind present)
- [x] Git repo initialized in `Desktop/vigil/`; remote `Abishai95141/Vigil-Dilijens` (empty, ready)
- [x] Directory tree per techstack §13 layout
- [x] Go module `github.com/Abishai95141/Vigil-Dilijens` (single root module; CGO-free)
- [x] `justfile` — cross-platform command surface (macOS + Linux parity)
- [x] CI: `.github/workflows/ci.yml` (go · proto · clockd · harness) + opt-in `integration.yml`
- [x] `proto/` + buf: clock contract with **no-string-fields** charter rule; committed Go gen
- [x] `obsd/internal/params` — parameters file v0 (doc 14 §5) **implemented + fully tested**
- [x] `obsd` skeleton: `cmd/obsd` (loads params, slog), `cmd/replay` (stub), version pkg
- [x] Internal package tree with `doc.go` contracts (identity, binding, qss, observe, selection, detect, unexplained, clock, graph, api)
- [x] `obsd/internal/clock/conformance_test.go` — asserts clock proto stays string-free
- [x] `clockd/` (uv) — StubClock (honest flat/widening-band behaviour) + tests + vendored SKILL.md
- [x] `harness/` (uv) — backtest primitives (band coverage, time-to-cross) + tests
- [x] `tools/graphlint` — ontology schema validator (green on example release) + tests
- [x] `ontology/schema/graph.schema.json` (v1-skeleton) + `graph/example-oom.yaml` (scaffold example)
- [x] `web/` — Vite/React/TS scaffold + provenance design tokens + vitest (deps install on demand)
- [x] `deploy/` — 3-node kind config + read-only RBAC
- [x] Root + per-subtree `CLAUDE.md`; README; corpus README
- [x] Verified: `go build ./...`, `go vet ./...`, `go test -race ./...`, `just lint`, clockd/harness suites all green

---

## Phase 0a — Foundations  (identity is *prerequisite zero*)

- [x] **01 Charter** ratified — classes, composition rules, prohibited derivations (encoded in CLAUDE.md as review rules)
- [x] **01 M2** Class carriage convention named in component contracts
- [x] **02 M1** Schema v1 — `ontology/schema/kg.schema.json` matches the real KG node/edge shape (10 node + 12 edge types); validates the graph.
- [x] **02 M2** Reference-graph INGESTED: 842-node KG in `ontology/graph/k8s_signal_kg.json`; runtime loader `obsd/internal/graph` (typed, indexed, content-hashed; resolves phenomenon membership from participates_in / relations from phenomenon_relation) — tested against the real KG (842/3759/589, 457 metric).
- [x] **02 M3** Authoring lints: `tools/graphlint` (schema + referential integrity + gap report) wired to `just graphlint` + CI. **Gap report** surfaces the authoring queue: all 38 phenomena missing spans (doc 02 §3.6), 0 structured threshold rules (60 agent hints), 112 data_type variants to normalize; + a curation warning (23 owned_by_agent edges → 2 mistyped Agent names). graphlint `--strict` gates on these before detection ships.
- [x] **02 M2-author** (spans + threshold rules): AUTHORED overlays in `ontology/graph/overlays/` — `spans-v1.yaml` (span + traversal edges + rationale for all 38 phenomena: 17 entity-local / 16 first-order / 5 second-order; control-plane singletons joined by direct role reference, principle in header) and `threshold-rules-v1.yaml` (8 structured rules: config-relative limits/allocatable/PVC-request paths + flagged defaults). Loader (`LoadWithOverlays`) merges deterministically, version pin covers base+overlays; graphlint validates overlays as hard errors and **`-strict` passes (and gates CI)**. Curation: 2 mistyped Agent names fixed (23 edges). REMAINING authoring queue: data_type normalization (112 variants, doc 09 funnel), 60 agent threshold hints to structure, v2 rules (apiserver inflight, etcd fsync, ARP).
- [x] **03 M1** Identity model + CEI scheme — both layers, minting, role derivation; churn tests pass (restart/reschedule/scale/**recreate-same-name honeypot**). `obsd/internal/identity/cei.go`
- [x] **03 M2** Normalization maps — cAdvisor + KSM + node-exporter (`obsd/internal/identity/normalize.go`). All doc 14 §3.2 traps encoded + tested: no-pod-UID time-aware join, `container=""`→pod aggregate, `container="POD"`→deliberate drop, KSM terminated-pod persistence, class-aware container routing, node-name join, **same-name recreate race incl. genuinely-late samples (EventTime vs receive time)**. Quarantine-never-guess; typed reasons; join audit records; versioned maps. Adversarially verified (4 confirmed findings fixed). *OTel semconv = 4th lane, Phase 0b–1 per doc 14 §3.2.*
- [x] **03 M3** Lifecycle state machine + succession + client-go informer wiring (`lifecycle.go`, `resolver.go`, `informer.go`, `cluster.go`). Two-tier tombstones (15m full / 24h stub / 250k LRU cap, evicted-before-horizon metric); time-aware `Lookup` (heap-backed, clock-injected, horizon-enforced at read); succession + same-name honeypot at store level; role chain resolver (RS→Deployment, Job→CronJob, degraded fallback); SharedInformers (pods/nodes/RS/jobs) with `HasSynced` gate; cluster id = kube-system UID (A9). Fake-clientset tested + `-race`; live wiring in `obsd --kubeconfig`; integration test behind `integration` tag. Adversarially verified.
- [x] **03 M4** Timestamped topology edges + validity-intersection contract (`edges.go`, `edges_watch.go`). EdgeStore: assertions with asserted/last-confirmed/retracted stamps; per-type staleness budgets (params); `Traverse(type,from,to,window)` → valid/suspect/absent (pure fn of state+window — replayable; horizon is a GC boundary, not a traversal input); retracted-edge 30m horizon; monotonic reassert. Wiring from informers: runs-on (pod→node, reconciled on reschedule), mounts (pod→PVC), selects (service→pod, **reconciled across ALL slices** — survives EndpointSlice rebalance), node-lease (node liveness via Lease RenewTime). Fake-client + store tests, `-race` (87 identity tests). Adversarially verified: 5 confirmed defects fixed incl. **blocking** slice-rebalance spurious retraction.
- [x] **03 M5** Join-audit tooling + health metrics (`audit.go`, `truth.go`, `metrics.go`). Mis-join detector: `VerifyJoin` + `AuditConsistency` cross-check the lifecycle Store against INDEPENDENT control-plane truth (raw listers, via the Watcher `TruthSource`) — a stale/wrong CEI surfaces as a mis-join (the silent killer), lag as Missing. `EvaluateGate` = Misjoins==0 AND coverage≥target. Prometheus `Collector` (join accuracy, orphan/quarantine rate by reason, edge staleness per type, tombstone tiers, **`phase0a_gate_passed`**). `obsd` serves `/metrics` `/healthz` `/readyz`; inventory loop logs the live gate verdict. Tested incl. mis-join detection + collector gather (99 identity tests, -race).
- [ ] **05 M1** Stream conventions: CEI stamping, canonical naming, cadence classes, retention tiers
- [ ] Own scraper (exposition-format parser for KSM/kubelet/node-exporter) — doc 14 A1
- [ ] client-go SharedInformerFactory wiring (pods, nodes, ns, services, EndpointSlices, PVC/PV, workloads, Leases, Events)
- [x] First demo: `go run ./obsd/cmd/obsd --kubeconfig <path>` prints a live, correctly-joined entity inventory + join-audit gate verdict (run on the Linux kind box)
- [~] 🔒 **Exit gate MACHINERY built + unit-tested** (M5): `AuditConsistency`/`EvaluateGate`/`/metrics phase0a_gate_passed` + integration test asserts zero mis-joins. **Gate must still be RUN on the reference kind cluster** (Linux box: `just up` + the OB workload + `go test -tags=integration -run LiveCluster`) to certify: join accuracy ≥ target, all misses are quarantines not mis-joins; CEI survives churn review.

## Phase 0b — Binding & local observation

- [~] **04 M1/M2/M4 CORE** (`obsd/internal/binding`): threshold-rule instantiation across entity instances (Container/Node/PVC fan-out from the authored `entity_scope`) + role-layer aggregation (axis 3); per-instance threshold resolution from live cluster config (`kube.SnapshotConfig`: limits, allocatable, PVC requests) with strict precedence (config → default, defaults FLAGGED); eligibility gates (no CPU limit ⇒ throttle rule out-of-scope WITH reason); resolvability metric + unbounded list (every pair has a state); re-binding on inventory-fingerprint change or config-drift poll, never continuously; deterministic (sorted, injected clock, order-independence tested). Wired into `obsd` stdout after the inventory; **verified live on the boutique cluster: 11/11 memory bars byte-exact vs kubectl (limit×0.95), paymentservice listed unbounded, throttle out-of-scope for it, 0 PVCs honestly reported**. All bindings suspect-by-construction until M3. REMAINING for full 04: M1 equivalence-group resolution + capability/distro gating, M3 semantic QA suite, M5 coverage report v1 (per-phenomenon observability), M6 re-binding reactivity.
- [ ] **05 M2** Primitive evaluators (threshold · rate · co-occurrence) with windowing/smoothing/reset handling
- [ ] **05 M3** Fingerprint materializer with full derivation references
- [ ] **05 M4** Coverage accounting (per-phenomenon observability)
- [ ] **05 M5** Replay interface — recorded readings replay byte-identically
- [ ] **06 M1** Selection funnel gates 1–2 (coverage + phenomenon participation), reason codes, Tier-A only
- [ ] **07 M1** Entity-local (zeroth-order) matcher over fingerprints with temporal ordering
- [ ] **qss** store: hot rings + warm 2h segments + 7d retention + Parquet bundle export (pure Go, `modernc.org/sqlite` for findings)
- [ ] **10 M1** Coverage-report surface (the first customer-facing screen — honest visibility map)
- [ ] **11 M1–M2** Replay substrate + binding-QA suite; charter battery started
- [ ] **12 M1** First immutable graph release with pinning wired through 04/07
- [ ] 🔒 **Exit:** deterministic replay byte-identical; binding QA fixtures pass; resolvability/coverage metrics published; entity-local phenomena fire on replay fixtures

## Phase 1 — Topological detection

- [ ] **07 M2–M6** First/second-order spans with edge-validity traversal; degraded matches; cascades + blast radius; sensitivity calibration
- [ ] **06 M2,M4,M6** Neighbourhood closure; criticality weighting + operator scope; re-selection reactivity
- [ ] **08 M1–M4** Unexplained channel: loudness evaluator, routing + dedup, surface card, curation feedback reports
- [ ] **10 M2–M4** Insight feed; topology view (current marks, edge-validity visibility); unexplained surface + timeline
- [ ] **11 M3–M4** Falsification corpus v1 (chaos runs) + phenomenon precision/recall suite; sensitivity sweeps
- [ ] **12 M2–M4** Change classes + review workflow; harness-wired regression gates; migration + binding diffs
- [ ] **04 M6** Re-binding reactivity · **02 M4** evidence linkage in the graph
- [ ] 🔒 **Exit:** phenomenon precision/recall meets per-class targets; **stale-edge fixtures degrade, never fabricate**; sensitivity defaults evidence-backed; a behavioural-class graph change landed through full governance

## Phase 2 — Marquee forecasting

- [ ] **09 M1** Clock interface + reference adapter (TimesFM 2.5); swap test with stub passes; conformance fixtures
- [ ] **09 M2** Eligibility funnel over Tier-B + projection against resolved bars; guardrails + silences
- [ ] **09 M3** 🔒 Backtest calibration gate (band coverage + time-to-cross error per target class)
- [ ] **09 M4** First warning class live — container working-set → OOM, blast radius attached
- [ ] **06 M5** Tier-B budgeting (clock invocation ceiling)
- [ ] **10 M5–M7** Early-warning cards + predictive marks; context windows; chat (adversarial register fixtures)
- [ ] **11 M5–M6** Forecast backtest suite operative; charter battery complete
- [ ] **12 M5–M6** Staged rollout + rollback exercised (seeded bad release caught at canary); curation loop closed end-to-end
- [ ] `clockd` TimesFM behind `model` extra — pin package version AND checkpoint revision (doc 14 A15); gRPC serving wired
- [ ] 🔒 **Exit:** backtest gate passes for every shipped warning class **before any operator sees a warning**; register audit clean; seeded bad release rolled back in exercise

## Phase 3 — Decomposition

- [ ] **09 M5** Context-window splice points; per-event-class footprint models (level shift, transient, ramp); abort criterion
- [ ] **10 M6–M8** Context-window + configuration surfaces complete; chat complete; mobile/on-call
- [ ] 🔒 **Exit:** decomposition improves backtest error without degrading band coverage

## Phase 4 — Known-future covariates

- [ ] **09 M6** Covariate admission (scheduled jobs, calendars); per-class lift evaluation
- [ ] 🔒 **Exit:** covariate targets beat univariate baselines in backtests

## Phase 5 — In-context refinement (optional, hard-gated)

- [ ] **09 M7** Inference-time example conditioning — only if a loadable in-context checkpoint is confirmed for the deployed model line

---

## Cross-cutting follow-ons (tracked so nothing is a silent gap)

- [ ] `graphlint`: referential-integrity + authoring-invariant lints (doc 02 §3.6 — member.variable resolves, relation targets resolve, span↔traversal consistency, defaults flagged, untested-status visibility)
- [ ] `just tools`: pin + adopt gofumpt, golangci-lint, govulncheck in CI (currently go vet + gofmt only)
- [ ] Web CI job once the app exists (Phase 0b); install deps + biome + vitest
- [ ] Staging: k3s on 3 small VMs for node-pressure realism (Phase 1 line item, doc 14 §3.1)
- [ ] Add `prometheus/client_golang` self-metrics (join accuracy, orphan rate, edge staleness, Tier-B budget use) + pprof in dev
- [ ] `prod` params profile (5 min reconcile + confirm-on-demand probe)
- [ ] Replay-bundle anonymization standards before any customer capture (doc 14 A16)

## Open-question ledger (doc 13 §6) — resolve with harness evidence, not intuition

1. Per-edge-type staleness budgets; tombstone horizon (initial values in params; refine from churn data)
2. Suspect-binding participation policy (use-but-mark vs hold-out)
3. Degraded-match representation (qualitative mark vs completeness score)
4. Two-hop traversal ceiling; cascade window scaling per edge type
5. Sensitivity-tuning exposure to operators vs held policy
6. Recurrence thresholds + cross-customer aggregation for curation intake (data-isolation review)
7. Forecast surfacing thresholds (max band width, min horizon)
8. Horizon/cadence defaults per target class
9. Which graph events beyond context windows are clean splice points (most gating for Phase 3)
10. Histogram-quantile targets in scope at all
11. In-context checkpoint availability on the deployed model line (Phase 5 hard gate)

---

### ▶ Immediate next step
**Identity layer (doc 03) is COMPLETE — M1–M5 done, 100 tests, pushed.** Three ways forward:

1. **Certify the Phase-0a exit gate on the Linux kind box** (closes Phase 0a): `just up`,
   deploy the Online Boutique workload, then `go test -tags=integration -run LiveCluster
   ./obsd/internal/identity/` — asserts **zero mis-joins** and reports join accuracy/coverage.
   Or watch live: `go run ./obsd/cmd/obsd --kubeconfig ~/.kube/config` → `curl :9095/metrics | grep phase0a_gate_passed`.
2. **Phase 0b — binding (04) + observation/qss (05)**: compile the ontology onto the bound
   cluster, the three primitives + fingerprints, the thin time-series store, the coverage report.
   (Needs the ontology graph loaded — see option 3.)
3. **Ontology ingestion (02 M2)**: load the authoritative 842-node KG (`~/Desktop/vigil copy/k8s_signal_kg.json`)
   — reconcile the schema to its node/edge shape, build the `internal/graph` loader, upgrade graphlint,
   produce the gap report (spans + threshold rules to author). This unblocks binding (04) + detection (07).
