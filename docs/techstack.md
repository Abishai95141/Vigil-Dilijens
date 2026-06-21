# Technology Stack — K8s AI Observability Platform

**Companions:** blueprint suite `00–13`, decisions in `14-implementation-clarifications.md`.
**Premise:** this is an **agent-first build** (Claude Code drives most implementation), so every stack choice is scored on three axes at once: fitness for the architecture, ecosystem gravity for Kubernetes work, and **agent-loop economics** — how fast and how deterministically an agent can edit → build → test → verify.

---

## 1. Stack verdict

**Go for the runtime core, Python for the forecasting and harness analytics, TypeScript for the surfaces. Protobuf as the single contract language between all three.** This confirms your instinct — and here is the explicit case against Rust, since it deserves one:

| Axis | Why Go wins here over Rust |
|---|---|
| Ecosystem gravity | The entire Kubernetes world is Go-native: client-go informers, kube-state-metrics, Prometheus, Helm, kind. Every reference implementation, every doc, every snippet an agent retrieves while working on informer/scrape/identity code is Go. Fighting that gravity in Rust means hand-rolling watch/informer machinery the Go ecosystem gives for free. |
| Agent-loop economics | `go build` on this codebase will sit in low seconds; `go test ./...` is fast, hermetic, race-detectable, and needs one toolchain. Rust's compile times throttle the edit-test loop that agentic development lives on. gofmt's single canonical style also removes a whole class of agent diff noise. |
| Performance adequacy | The hot path is arithmetic over in-memory rings (05) and 1–2-hop walks over a graph of thousands of nodes (07). Go clears this with an order of magnitude to spare; Go 1.26's default Green Tea GC and faster small-object allocation only widen the margin. The one genuinely compute-heavy component — the model — is PyTorch regardless of host language. |
| Escape hatch | The clock interface (09 §3.1) is language-agnostic by design. If a future component ever justifies Rust, it slots in behind a proto contract without touching anything. |

Python is non-negotiable for 09 and half of 11 (TimesFM is Python/PyTorch; backtest analytics want the pandas/pyarrow world). TypeScript is the only serious choice for 10.

## 2. Agent-first engineering principles (apply to everything below)

1. **One monorepo.** All three languages, the ontology content, the blueprint docs, and the corpus fixtures in a single repository — agents work best with the whole system greppable.
2. **`just` is the single command surface.** Every action an agent or human ever takes is a `just` recipe: `just test`, `just lint`, `just up` (kind cluster + add-ons + workload), `just demo`, `just record-bundle`, `just replay`, `just gen` (codegen). No bare command knowledge required.
3. **DEVELOPMENT.md hierarchy.** Root DEVELOPMENT.md: architecture map (pointing into `docs/` = the 00–14 suite), the charter's prohibited derivations as code-review rules, command index, invariants ("the hot path never touches disk", "no semantic fields in clock protos"). Per-package DEVELOPMENT.md: the package's contract, its owning blueprint doc, do/don't list, test command. The TimesFM repo ships its own SKILL.md for agents — vendor it beside the clockd service.
4. **Determinism-first testing.** The blueprint's replay guarantee (05/07) doubles as the testing strategy: golden-file fixtures in `testdata/` (recorded bundles in miniature), byte-identical assertions, `go test -race` always, injected clocks (no `time.Now` in logic), table-driven tests. Agents verify their own work by re-running; flaky tests poison agent loops, so hermetic unit tests are network-free and integration tests live behind a build tag that requires a kind cluster.
5. **No CGO anywhere in the Go tree.** Pure-Go dependencies only (this dictates the SQLite driver choice) so `go test ./...` works in any sandbox, cross-compiles trivially, and never needs a C toolchain.
6. **Committed codegen.** `buf generate` output is committed, not gitignored — generated types must be greppable by the agent.
7. **Milestone-sized work packets.** Issues/PRs map 1:1 to the blueprint milestone ladders (e.g. "03 M2: cAdvisor normalization map"), keeping agent sessions scoped and reviewable.

## 3. Languages and toolchains

| Toolchain | Pin | Purpose | Implementation considerations |
|---|---|---|---|
| **Go** | 1.26.x (1.26 released Feb 2026; Green Tea GC now default) | Runtime core: identity (03), binding (04), store + observation (05), selection (06), detection (07), unexplained (08), graph loader, API server, replay runner | `gofumpt` formatting; `golangci-lint` + `govulncheck` in CI; std `log/slog` for structured logs; `prometheus/client_golang` for self-metrics; pprof endpoints on by default in dev |
| **Python** | 3.12.x via **uv** (lockfile committed) | clockd (TimesFM adapter, 09); harness analytics (backtests, precision/recall, 11); corpus tooling | `ruff` (lint+format), `pyright` (types), `pytest`; uv makes env setup a single deterministic command — exactly what agent sessions need |
| **TypeScript** | TS 5.x · Node 22 LTS · **pnpm** | Web surfaces (10) | `biome` as the single lint+format tool (one config, fast); `vitest` for unit tests; Playwright deferred until M5 surfaces exist |
| **Protobuf** | **buf** CLI (lint, breaking-change checks, codegen) | The single cross-language contract | See §5 |
| **Task runner** | **just** | The command surface | Recipes wrap everything incl. kind/helm/kubectl |

## 4. Component → language → package map

| Blueprint doc | Component | Lives in | Language |
|---|---|---|---|
| 02 | Ontology content + schema + lints | `/ontology` (YAML) + `obsd/internal/graph` (loader) + `tools/graphlint` | YAML · Go |
| 03 | Identity & correlation | `obsd/internal/identity` | Go |
| 04 | Binding & generalization | `obsd/internal/binding` | Go |
| 05 | Store + observation ("qss") | `obsd/internal/qss`, `obsd/internal/observe` | Go |
| 06 | Selection | `obsd/internal/selection` (`select` is a reserved Go keyword) | Go |
| 07 | Detection | `obsd/internal/detect` | Go |
| 08 | Unexplained channel | `obsd/internal/unexplained` | Go |
| 09 | Clock client / clock service | `obsd/internal/clock` (client) · `clockd/` (service) | Go · Python |
| 10 | API + web app | `obsd/internal/api` · `web/` | Go · TypeScript |
| 11 | Replay runner / analytics | `obsd/cmd/replay` · `harness/` | Go · Python |
| 12 | Governance tooling | git + CI + `tools/graphlint` + release scripts | — |

`obsd` is a **modular monolith**: one binary, strict internal package boundaries mirroring the blueprint docs. Splitting into services later happens along those same boundaries; clockd is the only separate process from day one (different language, different lifecycle, deliberately replaceable per the clock contract).

## 5. Contracts and codegen

- **Protobuf everywhere** under `/proto`, managed by buf (lint + breaking-change gate in CI).
- **Go ↔ browser:** Connect RPC (`connect-go` server, `connect-web`/connect-query typed client) — one proto definition yields the Go handlers and the fully-typed TS client; serves plain HTTP/JSON for debuggability and gRPC-web for the app.
- **Go ↔ clockd:** plain gRPC (`grpcio` server in Python from the same protos).
- **Charter enforced in the schema:** the clock service proto carries *no string fields at all* — float arrays, horizon ints, quantile arrays. The "model never sees labels" rule (01 §4) becomes a compile-time property, and a conformance test asserts the descriptor stays that way.
- **Ontology releases:** YAML documents validated by JSON Schema + `graphlint` (02 M3), packaged as a content-hashed tarball per release (12 M1); the hash is the version everything pins.
- **Replay bundles:** Parquet (readings, topology log) + JSON manifest (graph version, bar set, parameters, labels) — directly readable by the Python harness via pyarrow/polars with zero custom parsers.

## 6. Kubernetes integration (Go runtime)

| Concern | Choice | Considerations |
|---|---|---|
| Watch/discovery | `k8s.io/client-go` SharedInformerFactory (pods, nodes, namespaces, services, **EndpointSlices**, PVC/PV, ReplicaSets/Deployments/StatefulSets/DaemonSets/Jobs/CronJobs, **Leases**, Events) | No controller-runtime and no CRDs — we watch, we never reconcile cluster state. Informer event handlers feed identity (03); resync set per `14` §1.1 |
| Scraping | Own scraper: net/http + `prometheus/common` exposition-format parser against KSM, kubelet `/metrics/cadvisor`, node-exporter | Owning ingest is what makes CEI stamping possible (14 A1). Dev reaches kubelet via the API-server proxy; in-cluster mode scrapes directly |
| RBAC | One read-only ClusterRole: get/list/watch on the resources above + `nodes/proxy` | Shipped as a manifest with the Phase-1 in-cluster Deployment (14 A2) |
| Installs | Helm for kube-state-metrics, node-exporter, Chaos Mesh; kustomize/raw manifests for workloads | All driven by `just up`; versions pinned in the repo |

## 7. Quantitative State Store (`qss`)

Own thin store per `14` §2.3: in-memory rings (hot, 60 min, the only hot-path read surface) + append-only 2 h segment files (warm, 7 d) + Parquet bundle export. Plain 16-byte records first; Gorilla compression as a later milestone. **Rejected:** `tstorage` (dormant since ~2023 — design reference only), external Prometheus/VictoriaMetrics as the store (breaks CEI-at-ingest), embedding the Prometheus TSDB library (the fallback if the bespoke path stalls, behind the same interface). Parquet library: `parquet-go/parquet-go`.

## 8. Graph and persistence

- **Bound customer graph:** in-memory adjacency structures + periodic snapshot file + reconcile-on-start. Explicitly **no graph database** — the ontology is ~842 nodes, a bound cluster graph is thousands, and walks are 1–2 hops; Neo4j-class machinery would be pure operational drag.
- **Findings, selection records, unexplained cards, audit trails:** **SQLite** via `modernc.org/sqlite` (pure Go, no CGO) — zero-ops, single-file, queryable history for the timeline (10), trivially snapshotted into test fixtures.

## 9. Forecasting service (`clockd`)

| Aspect | Choice | Considerations |
|---|---|---|
| Model | **TimesFM 2.5**, checkpoint `google/timesfm-2.5-200m-pytorch` pinned by HF revision hash; `timesfm` package pinned by exact version/commit (the PyPI package version numbering differs from the checkpoint's — pin both, per 14 A15) | Loaded via `TimesFM_2p5_200M_torch.from_pretrained`; `ForecastConfig` set for our envelope: `max_context` up to 16,384, `max_horizon` ~1,024, `normalize_inputs=True` (the scale-independence the clock contract assumes), continuous quantile head on, quantile-crossing fix on |
| Runtime | PyTorch, **CPU-sufficient for dev** (200 M params); `torch_compile` on; GPU optional later | Batch Tier-B targets per cycle within the invocation budget (06 §3.3); warm the model at process start |
| Serving | `grpc.aio` server from the shared protos; stateless; health endpoint feeds the Tier-B degradation panel (14 A13) | The service knows nothing but float arrays — swap-readiness is a test: a stub clock must pass the same conformance suite |
| Later phases | XReg covariate pathway (Phase 4) is supported in the current package line; LoRA/PEFT fine-tuning examples exist if ever needed; ICF stays hard-gated (13 Phase 5) | None of these enter the dev environment until their phase gates |
| Harness side | `pyarrow` + `polars` (bundle reads), `numpy`/`scipy` (band-coverage and error statistics), `matplotlib` (calibration reports) | Backtests consume the same Parquet bundles the Go runtime exports |

## 10. Web surfaces (`web/`)

| Concern | Choice | Considerations |
|---|---|---|
| App | Vite + React + TypeScript, TanStack Router + Query | Connect-Web typed client from `/proto` — API drift becomes a compile error |
| Topology view (10 §3.2) | **Cytoscape.js** | Handles thousands of nodes with proper layouts; custom styling implements the charter's separated visual languages (current vs predictive marks, suspect-edge rendering) |
| Timeline + series (10 §3.3) | **Apache ECharts** | First-class confidence-band rendering — PROJECTED content draws as bands, never lines, straight from the charter |
| Styling | Tailwind | Design tokens for the three provenance classes defined once, used everywhere |
| Live updates | SSE from the Go API for finding streams | Simple, proxy-friendly; upgrade path to bidirectional later |

## 11. Cluster, workload, and corpus tooling

| Tool | Role | Notes |
|---|---|---|
| **kind** | Dev cluster, 1 control-plane + 2 workers | Config checked in: `kind: Cluster` with three `nodes:` entries (`control-plane`, `worker`, `worker`); `just up` creates it, installs add-ons, deploys workload |
| **k3s on 3 small VMs** | Phase-1 staging for node-pressure realism | Per 14 §3.1's shared-kernel caveat |
| **Online Boutique** | Phase-0 workload | Limits verified and customized per 14 §3.3 (one near-threshold, one deliberately unbounded) |
| **OpenTelemetry Demo + flagd** | Phase-1 labeled-failure generator | Memory-leak / high-CPU / error flags toggled via flagd API; every toggle logged into corpus labels |
| **Chaos Mesh** | Infrastructure-level induced faults | StressChaos / PodChaos / NetworkChaos / IOChaos as versioned YAML beside the corpus |
| **Locust** (in OB) / **k6** | Load shaping | Steady background load makes precursor trajectories realistic |
| **k9s, kubectl, helm** | Human + agent cluster driving | Agents use these CLIs directly through `just` recipes |

## 12. CI, quality gates, and self-observability

- **GitHub Actions:** per-language lint+test jobs; `buf lint` + `buf breaking`; a kind-in-CI integration job (tagged tests) on the runtime; `graphlint` + harness regression on any `/ontology` change — wiring 12 M3's "a skipped gate cannot release" into CI mechanics from the start.
- **Pre-commit:** format + lint only (fast); everything heavier lives in CI.
- **Self-observability of obsd:** `log/slog` JSON logs, self-metrics via `client_golang` (join accuracy, orphan rate, edge staleness, tombstone evictions, Tier-B budget use — the health metrics docs 03/06 already name), pprof in dev builds.

## 13. Repository layout

```
/                       DEVELOPMENT.md · justfile · README
├── docs/               the blueprint suite 00–14 (core) + 15–30 (extension tracks) + techstack.md (this file)
├── proto/              contracts (buf-managed; generated code committed)
├── ontology/           schema/ · graph/ (YAML content) · releases/
├── obsd/               Go modular monolith
│   ├── cmd/obsd        runtime binary
│   ├── cmd/replay      harness replay runner
│   └── internal/       identity · binding · qss · observe · selection ·
│                       detect · unexplained · clock · graph · api · params · …
├── clockd/             Python TimesFM service (uv project, vendored SKILL.md)
├── harness/            Python analytics: backtests · falsification · reports
├── web/                Vite/React app
├── deploy/             kind config · helm values · RBAC · workload manifests
├── corpus/             chaos CRDs · flag scripts · bundle manifests · labels
└── tools/graphlint     ontology lints (02 M3)
```

## 14. Version pin policy

Exact pins live in lockfiles (`go.mod`, `uv.lock`, `pnpm-lock.yaml`, Helm values) — this document pins **policies**: Go tracks the latest stable minor (currently 1.26.x); Python stays on 3.12 until torch support says otherwise; Node stays on the active LTS; the TimesFM package and checkpoint are pinned by exact commit/revision and only move through the 09 M3 backtest re-gate; cluster add-on charts are pinned in `deploy/` and bumped deliberately. Renovate/Dependabot proposes; the harness regression decides.

---

*Order of first commits, mirroring Phase 0a: repo scaffold + justfile + CI → proto + buf → `obsd` skeleton with params file (14 §5) → identity layer against the kind cluster. The first demo target is `just up && just obsd` printing a live, correctly-joined entity inventory of the Online Boutique — prerequisite zero, observable.*
