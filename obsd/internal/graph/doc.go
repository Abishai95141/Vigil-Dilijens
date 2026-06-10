// Package graph loads the curated ontology graph (the type-level knowledge artifact)
// and holds the bound customer graph (the instance-level projection) in memory.
//
// Owning docs: 02-ontology-graph-specification.md (the type-level schema this loads),
// 04 (the bound graph this holds), 12 (versioning/pinning this enforces).
//
// Responsibilities:
//   - Load a content-hashed, immutable ontology release (YAML) and validate it against
//     the schema and authoring invariants (doc 02 §3.6). Every loaded release pins its
//     version; every downstream finding stamps the version it ran under (doc 12 §3.1).
//   - Hold the bound customer graph as in-memory adjacency structures with a periodic
//     snapshot file and reconcile-on-start (doc 14 A8). Explicitly NO graph database —
//     thousands of nodes, 1-2-hop walks.
//
// Everything in the ontology is AUTHORED-class by definition; there is no field in
// the schema where a measurement or model output could be stored (doc 02 §4).
//
// Do NOT: write model output or learned values into either graph; mutate a released
// graph in place (change means a new immutable release, doc 12 §3.1).
package graph
