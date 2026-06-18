# `overlays/candidate/` — the firewalled candidate-overlay staging area

This subdirectory is the **propose** sink of the Dynamic Graph eXtension pipeline
(see [`docs/20-dynamic-graph-extension.md`](../../../../docs/20-dynamic-graph-extension.md)).

**Nothing in here is ever loaded by the running system.** The overlay loader's
`overlayPaths()` (`obsd/internal/graph/overlay.go`) globs only the `*.yaml`/`*.yml`
files in the `overlays/` **root** and **skips subdirectories** — so files placed here:

- never enter a release content hash (`graph.Version`),
- are never read by the deterministic detection or forecast path,
- never affect replay.

This is enforced by tests, not convention:

- `obsd/internal/graph/overlay_firewall_test.go` — proves `overlayPaths` skips this
  subdir and the pinned release hash is byte-identical with and without it.
- `obsd/internal/candidate/firewall_test.go` — proves no deterministic package
  imports `internal/candidate`.

## Lifecycle

Candidate proposals live in the `candidate` SQLite store (`candidates.db`,
`obsd/internal/candidate`) with `status ∈ {candidate, promoted, rejected, shadow}`.
A proposal reaches the authoritative graph **only** by the governance promotion gate:
a **named human** authors the final overlay into the `overlays/` root and cuts a new
release. The model's proposed text — including any name — is discarded at promotion;
the human authors the note, name, and version.

Do **not** commit loose `*.yaml` candidate overlays here by hand. This directory
exists to make the firewall boundary explicit and is kept by this README.
