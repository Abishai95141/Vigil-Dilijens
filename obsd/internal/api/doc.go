// Package api is the surfacing back end: the Connect RPC server that feeds the web
// app (doc 10) and the SSE finding streams. It RENDERS and composes adjacently — it
// never produces findings (07/08/09) or knowledge (02).
//
// Owning doc: 10-surfacing-and-operator-experience.md (the back-end half).
//
// The join (doc 01 §4, doc 10 §3.1): this is the ONE place the three provenance
// classes meet — each still wearing its label. MEASURED matches, PROJECTED early
// warnings, AUTHORED notes are composed ADJACENTLY, never fused into a derived
// statement of a stronger class. Register rules (doc 01 §5) are applied at render:
// MEASURED indicative, PROJECTED explicitly modal with a band, AUTHORED attributed.
//
// Persistence (doc 14 A7): findings, selection records, unexplained cards, and audit
// trails live in SQLite via modernc.org/sqlite (pure Go, no CGO) — zero-ops,
// replayable, queryable for the timeline.
//
// Transport: Connect RPC (connect-go server) from the shared protos in /proto, so
// one definition yields the Go handlers and the typed TS client; SSE for live
// finding streams.
//
// Do NOT: paraphrase an authored note into a fused causal sentence; emit a card
// without a complete derivation path (an evidence dead-end is a release-blocking
// defect); upgrade a class.
package api
