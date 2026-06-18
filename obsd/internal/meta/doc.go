// Package meta holds STRUCTURAL guards over the codebase itself — tests that assert
// invariants about the test suite and the package tree rather than product behaviour.
// Owning concern: doc 11 (validation) + the engineering mandate (CLAUDE.md) that nothing
// ships untested.
//
// These guards are the "future-proofing" layer: they make it impossible for a new feature
// to open a silent test blind spot. Today they enforce two invariants:
//
//   - every operator-visibility gate flag (`…GatePassed`) maps to a real gate recipe, so a
//     new gated feature cannot ship without naming the gate that certifies it;
//   - every Go package under obsd/ either carries tests or is on an explicit allowlist with
//     a documented reason, so a new package cannot ship with zero tests unnoticed.
//
// The package intentionally contains only test files plus this doc.go (the contract).
package meta
