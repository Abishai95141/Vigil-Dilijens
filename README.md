# Vigil

**Kubernetes-native AI Observability** — a curated, versioned ontology graph plus a
narrow forecasting clock. The system detects current failure *phenomena* across the
cluster's topology and forecasts imminent threshold crossings, under a strict
epistemic discipline: **measured facts, authored relationships, and projections never
merge in the data.** It never auto-remediates and never invents causation.

Design lives in [`docs/`](docs/) (blueprint suite `00–14` + `techstack.md`). The
agent/dev guide is [`CLAUDE.md`](CLAUDE.md). Current status is
[`artifacts/task.md`](artifacts/task.md).

## What's here today (Phase 0a scaffold)

A building, tested foundation across all three languages:

- **`obsd`** (Go modular monolith) — runtime core. The **parameters file** (doc 14 §5)
  is implemented and fully tested; the internal package tree (identity, binding, qss,
  observe, selection, detect, unexplained, clock, graph, api) is laid out with each
  package's contract in its `doc.go`.
- **`proto`** — the clock contract, with the charter's *no-string-fields* rule
  enforced by a Go conformance test. Generated Go is committed.
- **`clockd`** (Python/uv) — an honest **stub clock** (flat on random input, band
  never collapses) conforming to the swap contract. TimesFM lands in Phase 2.
- **`harness`** (Python/uv) — forecast backtest primitives (band coverage, doc 11 §3.5).
- **`tools/graphlint`** — validates ontology YAML against the schema; runs green on
  the example OOM release.
- **`web`** (Vite/React/TS) — scaffold with the three provenance classes as the one
  shared design token (deps installed on demand).
- **`deploy`** — 3-node kind config + read-only RBAC.

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

See `just --list` for the full command surface, and [`CLAUDE.md`](CLAUDE.md) for the
cross-platform contract (macOS writes, Linux builds/tests; all Go is CGO-free; every
`just` recipe behaves identically on both).

## License

TBD.
