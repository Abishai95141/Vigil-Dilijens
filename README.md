# Vigil

**Kubernetes-native AI Observability** — a curated, versioned ontology graph plus a
narrow forecasting clock. The system detects current failure *phenomena* across the
cluster's topology and forecasts imminent threshold crossings, under a strict
epistemic discipline: **measured facts, authored relationships, and projections never
merge in the data.** It never auto-remediates and never invents causation.

Design lives in [`docs/`](docs/) (blueprint suite `00–14` core + `15–30` extension
tracks + `techstack.md`). The agent/dev guide is [`DEVELOPMENT.md`](DEVELOPMENT.md). Current
status is [`artifacts/task.md`](artifacts/task.md).

## What's here today (Phases 0a–3 complete; v4–v6 lanes added)

A working, tested system across all three languages:

- **`obsd`** (Go modular monolith) — the runtime core: the deterministic detection
  engine plus 30+ implemented internal packages (identity, binding, qss, observe,
  selection, detect, unexplained, clock, graph, api, events, flow, forecast, governance,
  incident, kube, mcp, notify, onset, store, trace, assoc, audit, candidate,
  cohypothesis, departure, dgx, …), each with its contract in `doc.go`. The
  **parameters file** (doc 14 §5) is embedded and fully tested.
- **`proto`** — the clock contract, with the charter's *no-string-fields* rule
  enforced by a Go conformance test. Generated Go is committed.
- **`clockd`** (Python/uv) — the **TimesFM** forecasting clock behind `--clock timesfm`
  (the honest stub clock remains available), conforming to the no-string swap contract.
- **`harness`** (Python/uv) — the backtest + falsification suite: per-class
  forecast/governance/event/cross-service/departure/incident/validate-claim gates,
  sensitivity sweeps, plus an anomaly bake-off and offline causal-discovery.
- **`ontology`** — the curated graph, currently at release **v0.13.0** (20 overlays).
- **`tools/graphlint`** — validates ontology YAML against the schema; runs green.
- **`console`** (Vite/React/TS) — the operator frontend, with the three provenance
  classes as the one shared design token; off/gate-pending lanes render their honest note.
- **`deploy`** — kind config (boutique + ABB) + read-only RBAC + add-on workloads.

## Prerequisites

- **Go 1.26.x**, **just**, **buf** (code lives here; `go test` runs anywhere, CGO-free)
- **uv** (Python 3.12), **Node 22 + pnpm** (web)
- **Docker + kind + kubectl + helm** on the Linux box for the cluster

## Quickstart

```sh
just test          # go test -race ./...  (the standing gate)
just lint          # go vet + gofmt + buf lint
just obsd          # run the runtime skeleton (loads embedded dev params)
just clockd-test   # ruff + pytest for the stub clock
just harness-test  # ruff + pytest for backtest primitives
just gen           # regenerate committed proto code
go run ./tools/graphlint   # validate the ontology

# On the Linux/Ubuntu machine:
just up            # create the 3-node kind cluster
```

See `just --list` for the full command surface, and [`DEVELOPMENT.md`](DEVELOPMENT.md) for the
cross-platform contract (macOS writes, Linux builds/tests; all Go is CGO-free; every
`just` recipe behaves identically on both).

## License

TBD.
