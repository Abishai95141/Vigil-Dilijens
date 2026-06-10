// Package binding is the compiler between the type-level ontology and a specific
// live cluster: it produces the bound customer graph with auto-calibrated bars,
// plus an honest coverage report. Runs at discovery time and on change, never on
// the hot path.
//
// Owning doc: 04-binding-and-generalization-engine.md.
//
// Contract:
//   - Resolve each signal's equivalence group, capability gates, distro gates, and
//     emission metadata; run semantic QA (unit/character/scope/platform/range) and
//     stamp each binding verified | suspect | failed.
//   - Instantiate every variable across three axes: names, entity instances, and
//     identity layers. Thresholds resolve PER INSTANCE from the entity's own config.
//   - Threshold precedence: customer config -> operator override -> ontology default
//     (defaults flagged). No resolvable bar => no crossable limit => Tier-B ineligible,
//     listed as unbounded; NEVER silently skipped.
//   - Every (entity, variable) pair lands in exactly one state: bound | unresolved |
//     out-of-scope. The coverage report enumerates them — hidden gaps are impossible.
//
// Do NOT: guess an equivalence, borrow more authority than a default carries, or
// drop an unbounded workload silently.
package binding
