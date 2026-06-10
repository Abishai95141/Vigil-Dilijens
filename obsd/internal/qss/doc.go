// Package qss is the Quantitative State Store: a deliberately thin time-series store
// that stores and retrieves raw numbers, one stream per (CEI, canonical variable),
// and NEVER interprets them.
//
// Owning doc: 05-observation-and-fingerprint-pipeline.md §3.1; sizing/persistence
// in 14-implementation-clarifications.md §2.
//
// Architecture (doc 14 §2.3):
//   - Hot ring (in memory): one fixed-capacity ring per (CEI, variable), 60 min
//     capacity. The ONLY thing the hot path ever reads. The hot path never touches disk.
//   - Warm segments (on disk): append-only 2 h segment files; sealed segments immutable;
//     crash recovery replays the unsealed segment; fsync batched every 1 s.
//   - Retention: delete segments older than 7 d.
//   - Bundle export: sealed segments + topology log + graph version + bar set + params
//     -> Parquet + JSON manifest (the replay bundle, doc 11).
//
// Pure-Go only (no CGO): plain 16-byte records (ts 8B + value 8B) first; Gorilla
// compression is a later milestone, not a prerequisite.
//
// Do NOT: compute statistics here, read disk on the hot path, or outsource storage
// to an external TSDB (that would break CEI-stamped-at-ingest, doc 14 A1).
package qss
