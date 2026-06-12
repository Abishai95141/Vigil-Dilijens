// Package store is the surfacing-side persistence (doc 14 A7): a pure-Go SQLite
// database (modernc.org/sqlite — CGO-free, matching the cross-platform contract)
// for findings, selection records, unexplained cards, and audit trails that the
// operator surfaces (doc 10) query.
//
// IMPORTANT — this store is OFF the deterministic path. Detection (07) and the
// replay guarantee (05 M5) never read from it; it is a one-way sink the eval
// loop writes findings into so the UI has a queryable history. Persisting here
// can never change a fingerprint, a match, or a digest. (Replay reproduces from
// the bundle, not from this DB.)
//
// Model: one row per (entity, phenomenon, graph version) carrying first_seen /
// last_seen and the latest snapshot — so "active findings" fall out naturally
// and the table stays bounded under a steady condition, while the time span of a
// condition is preserved for the timeline (doc 10 §3.3).
package store
