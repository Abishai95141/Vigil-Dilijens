# web — operator surfaces (doc 10)

Vite + React + TypeScript (techstack §10). Node 22 + pnpm. **This is the join** — the
one place the three provenance classes meet, each still wearing its label — and it
must never fuse them on screen or in language (doc 10 §3.1, doc 01 §5).

- `biome` is the single lint+format tool; `vitest` for unit tests.
- Provenance design tokens are defined ONCE (`src/provenance.ts`) and used everywhere.
- Coming as surfaces land: Connect-Web typed client from `/proto`, **Cytoscape.js**
  (topology, with suspect edges visibly distinct), **Apache ECharts** (timeline —
  PROJECTED content draws as **bands, never lines**), Tailwind, SSE finding streams.

First surface to build is the **Coverage Report** (doc 10 M1): the honest map of what
the system can and cannot watch, shown before any finding.

Deps install on demand: `just web-install`, then `just web-dev` / `just web-test`.
(node_modules is gitignored; the pnpm lockfile is committed.)

Register rules at render (doc 01 §5): MEASURED indicative, PROJECTED explicitly modal
with a band, AUTHORED attributed. A card without a complete derivation path is a
release-blocking defect. Never upgrade a class; never paraphrase an authored note into
a fused causal sentence.
