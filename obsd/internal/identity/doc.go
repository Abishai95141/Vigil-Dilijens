// Package identity is prerequisite zero: the referential foundation that joins
// graph nodes to time-series streams and keeps topology truthful in time.
//
// Owning doc: 03-identity-and-correlation-layer.md.
//
// Contract:
//   - Mint the Canonical Entity Identity (CEI) for every instance and every role
//     from stable coordinates; the CEI is the ONLY join key in the system.
//   - The graph<->store join is EXACT match on CEI, never fuzzy, never at read time.
//   - Normalize heterogeneous exporter dialects into CEI coordinates; streams that
//     cannot be normalized are QUARANTINED (counted, surfaced), never guessed.
//   - Record topology edges as timestamped assertions with validity intervals;
//     walks elsewhere intersect edge validity with the evaluation window.
//
// Do NOT: infer identity, fuzzy-match streams, or trust an edge past its staleness
// budget. A mis-join is the silent killer — quarantine instead of guessing.
package identity
