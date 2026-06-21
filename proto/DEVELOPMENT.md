# proto — the single cross-language contract

Buf-managed Protobuf (techstack §5). One proto definition yields Go handlers/clients
and the typed TS client; API drift becomes a compile error. **Generated code is
committed** under `gen/` (not gitignored) so it stays greppable.

- Go ↔ browser: Connect RPC (added with the api layer, doc 10).
- Go ↔ clockd: plain gRPC (Python server from the same protos).

**Charter in the schema (doc 01 §4):** `vigil/clock/v1/clock.proto` carries **no
string fields at all** — float arrays, horizon ints, quantile arrays only. The "model
never sees labels" rule is a compile-time property, asserted by
`obsd/internal/clock/conformance_test.go`. Do not add strings (or bytes) to it.

Workflow: edit a `.proto` → `just gen` (runs `buf lint && buf generate`) → commit
`gen/`. CI's `proto` job fails if `gen/` is stale (`just gen-check`) or lint/format
is dirty. `buf breaking` guards against incompatible changes.
