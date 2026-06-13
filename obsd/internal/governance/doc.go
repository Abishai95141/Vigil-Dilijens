// Package governance is the change machinery for the ontology graph (doc 12):
// how authored knowledge is proposed, classified by blast radius, regression-gated,
// migrated onto bound customers, staged, rolled back, and grown — with humans
// authoring every assertion, always.
//
// Governance is "a human-and-process layer with tooling, not a runtime component"
// (doc 12 §1). Nothing here runs on the deterministic hot path (cmd/obsd); the
// tooling is exercised offline through cmd/govern and the harness. It reuses the
// real loaders — internal/graph (the released artifact) and internal/binding (the
// bound customer graph) — so a classification or a migration diff is computed over
// exactly the structures the runtime consumes, never a parallel mock.
//
// Milestone map (doc 12 §7):
//
//   - M1 versioning + pinning — internal/graph (Release, VerifyRelease). DONE.
//   - M2 change classes + review workflow — changeclass.go, proposal.go.
//   - M3 harness-wired regression gates — regression.go (+ harness/governance.py).
//   - M4 migration + binding diffs — diff.go.
//   - M5 staged rollout + rollback — rollout.go.
//   - M6 curation intake loop — intake.go.
//
// Epistemic discipline (doc 12 §4): governance is where AUTHORED-class integrity
// is manufactured — sole write path, human authorship with identity + evidence on
// every assertion, generated text barred from entering, defaults flagged at the
// source. The tooling enforces the mechanical gates (class derivation, required
// regression, immutability); it never approves — approval is human (doc 12 §3.3:
// "the harness can block; it cannot approve").
package governance
