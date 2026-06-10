# ontology — the type-level knowledge artifact (doc 02)

The single curated, versioned source of all meaning in the product. **Everything here
is AUTHORED-class by construction** — there is no field where a measurement or model
output could be stored (doc 02 §4). Changed only through Governance (doc 12); the
product never learns, the knowledge base grows through humans.

```
schema/    JSON Schema for a release (graph.schema.json) — the authoring contract
graph/     YAML content (signals · equivalence groups · entity/edge types · phenomena)
releases/  immutable, content-hashed release tarballs (doc 12 §3.1)
```

- `graph/example-oom.yaml` is a **scaffold example**, not a release. The authoritative
  842-node graph is a named workstream (doc 14 A14, doc 02 M2): schema v1 → mechanical
  conversion → lint → human review of every phenomenon's span/edge declarations.
- Validate with `go run ./tools/graphlint` (structural lints today; referential and
  authoring-invariant lints — doc 02 §3.6 — are tracked follow-ons).

Authoring invariants (doc 02 §3.6): every reason note is authored prose with author +
version provenance; every phenomenon declares its span and traversal edges explicitly
(an undeclared span is invalid); default thresholds are flagged; temporal tags/spans
are falsifiable claims carrying evidence links for the harness.
