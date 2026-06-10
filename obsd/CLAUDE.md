# obsd — Go runtime core

The modular monolith (techstack §4): one binary, strict `internal/` package
boundaries mirroring the blueprint docs. Pure Go, **CGO-free** (verify with
`CGO_ENABLED=0 go build ./...`). Module: `github.com/Abishai95141/Vigil-Dilijens`
(single module rooted at the repo, so `proto/gen` imports cleanly).

Each package's contract — owning doc, do/don't, invariants — is in its `doc.go`.
Component → package map: `docs/techstack.md` §4. Map of packages to docs:

| Package | Doc | Owns |
|---|---|---|
| `internal/identity` | 03 | CEI minting, normalization maps, lifecycle, timestamped edges |
| `internal/binding` | 04 | compile ontology→bound graph, semantic QA, threshold resolution, coverage report |
| `internal/qss` | 05/14 | the thin time-series store (hot rings + warm segments) — never interprets |
| `internal/observe` | 05 | the three primitives + fingerprints; nothing learns here |
| `internal/selection` | 06 | the attention funnel + tiers (note: `select` is a Go keyword) |
| `internal/detect` | 07 | deterministic phenomenon matching, spans, cascades, blast radius |
| `internal/unexplained` | 08 | loud-but-unmatched routing; the stated blind spot |
| `internal/clock` | 09 | the clock client + charter conformance test |
| `internal/graph` | 02/04/12 | ontology loader + bound-graph in-memory structures |
| `internal/api` | 10 | the surfacing back end (Connect RPC + SSE); the join, never the fusion |
| `internal/params` | 14 §5 | the one parameters file (implemented + tested) |
| `internal/version` | — | build identity (ldflags) |

`cmd/obsd` is the runtime; `cmd/replay` is the harness replay runner (doc 11).

Testing: `go test -race ./...`; golden fixtures in `testdata/`; inject clocks, never
call `time.Now` in logic; integration tests behind `//go:build integration`.
