# DB persistence & migration (Track 6)

> Closes audit-v3 roadmap **#7** ("persistence/upgrade has no story: CREATE TABLE IF NOT
> EXISTS only; no PRAGMA user_version, no migration ladder; no test that a new binary opens
> an old DB; ontology releases silently orphan old rows"). The findings/incident store
> (`obsd/internal/store`, pure-Go `modernc.org/sqlite`, off the deterministic path) now has
> a versioned schema with a tested upgrade path.

## Schema versioning + the migration ladder

`obsd/internal/store/migrate.go`. The DB stamps `PRAGMA user_version`; `Open` runs an
ordered, **append-only** ladder, applying each step whose version exceeds the DB's current
version, each in its own transaction (user_version lives in the SQLite header and is
transactional, so a crash mid-migration rolls back and the step re-runs next open).

- **v1** is the baseline — the idempotent `CREATE TABLE IF NOT EXISTS` for findings,
  unexplained, incidents. A pre-versioning DB written by an older binary (tables present,
  `user_version` still 0) migrates cleanly up to v1.
- **Discipline:** never edit a shipped migration — append a new one. (`{2, "ALTER TABLE
  findings ADD COLUMN ...;"}` etc.)

## The upgrade guarantee (tested)

`obsd/internal/store/migrate_test.go`:

| Test | Proves |
|---|---|
| `TestMigrateFreshDBReachesLatest` | a fresh DB ends at the latest schema version |
| `TestNewBinaryOpensLegacyDB` | **the current binary opens a DB written by an older binary** (base tables, `user_version` 0, rows present), migrates it, and **preserves the rows** |
| `TestLadderAppliesInOrderAndIsIdempotent` | steps apply in order (a real `ALTER`), and a re-open re-applies nothing (idempotent — the ALTER would error 'duplicate column' otherwise) |
| `TestIncidentMemorySurvivesOntologyUpgrade` | the durable incident memory persists across an ontology release (see below) |

## Findings vs. incidents across an ontology upgrade — by design

This is the deliberate answer to "an ontology release silently orphans old rows":

- **Findings are pinned to the graph that produced them.** The findings PK is
  `(entity_cei, phenomenon, graph_version)`, so a new ontology release starts *new* rows
  rather than mutating point-in-time findings under a graph they weren't computed against.
  Cross-version replay is *refused*, never silently migrated — a finding is a fact about a
  specific graph version. (Stale rows age out of the surfaces via the freshness horizon;
  they are historical, not orphaned-and-wrong.)
- **Incident MEMORY survives the upgrade.** The incidents PK is
  `incident_key = Key(phenomenon, role, window-bucket)` — `graph_version` is a COALESCE'd
  column, **not** part of the key. So a phenomenon that recurs across an ontology release
  keeps the *same* incident and increments its recurrence; `graph_version` advances to the
  latest occurrence. Operational memory ("this has happened N times to this workload")
  rightly persists across upgrades, while point-in-time findings stay graph-pinned.

`TestIncidentMemorySurvivesOntologyUpgrade` proves it: an occurrence under graph `vA`, a
resolve gap, then the same phenomenon+role under graph `vB` → one incident, recurrence 2,
`graph_version = vB`.

## Status

Schema versioning + an append-only ladder + the new-binary-opens-old-DB test + the
findings/incident upgrade semantics close the "no persistence/upgrade story" finding. The
store remains single-writer and off the deterministic path; migrations never touch the
replay digest.
