# ontology — the type-level knowledge artifact (doc 02)

The single curated, versioned source of all meaning in the product. **Everything here
is AUTHORED-class by construction** — there is no field where a measurement or model
output could be stored (doc 02 §4). Changed only through Governance (doc 12); the
product never learns, the knowledge base grows through humans.

```
schema/   kg.schema.json — JSON Schema for the KG envelope (nodes/edges)
graph/    k8s_signal_kg.json — the authoritative 842-node KG (doc 14 A14)
releases/ immutable, content-hashed release tarballs (doc 12 §3.1) — Phase 0b
```

## The graph

`graph/k8s_signal_kg.json` is the authoritative reference graph: 842 nodes / 3,759
edges / 589 signals (matching doc 02 §2). A `{_meta, stats, nodes, edges}` artifact.

- **10 node types**: Signal (589), CorrelationGroup=phenomenon (38), EquivalenceGroup
  (35), Entity (47), Tool (38), CapabilityPrereq (42), DistroVersionGate (19), Gotcha
  (21), Agent (6), Modality (7).
- **12 edge types**: the key ones are `participates_in` (signal→phenomenon, the
  structured membership), `phenomenon_relation` (cascades/blast radius), `attaches_to`
  (signal→entity), `in_equivalence_group`, `requires_capability`, `behaves_differently_in`
  (distro gates), `has_gotcha`, `derived_from`, `owned_by_agent`.

The runtime loader is `obsd/internal/graph` (typed, indexed, content-hashed). The
offline lint is `tools/graphlint` (schema + referential integrity + the gap report).
`go run ./tools/graphlint` (or `just graphlint`).

## The authoring gap (doc 14 A14) — surfaced by graphlint

The KG carries rich temporal tagging on phenomena but is NOT yet detection-ready.
`graphlint` reports the standing gaps a curator must close via governance (doc 12):

1. **All 38 phenomena lack a declared `span`** (entity-local / first / second-order)
   and `traversal_edge_types`. Doc 02 §3.6: an undeclared span is INVALID, not
   entity-local-by-default. This is the human-review queue before topological
   detection (doc 07) can ship. `graphlint --strict` fails until they are authored.
2. **No structured threshold rules** (config-relative, with config paths/factors,
   doc 04 §3.4) — borrowed normativity has nothing to resolve a bar from yet. 60
   `owned_by_agent` edges carry free-text threshold *hints* to author from.
3. **112 free-text `data_type` variants** need normalization for the forecast funnel
   (doc 09).
4. **Curation item**: 23 `owned_by_agent` edges reference two mistyped Agent names
   (`"Storage / PVC Analysis Agent"` → `"Storage Analysis Agent"`; `"Log / IO Analysis
   Agent"` → `"Log/IO Analysis Agent"`). Organizational only (not detection); a
   warning, not a hard failure.

Authoring invariants (doc 02 §3.6): every reason note is authored prose with author +
version provenance; every phenomenon declares its span and traversal edges; default
thresholds are flagged; temporal tags/spans are falsifiable claims carrying evidence
links for the harness.
