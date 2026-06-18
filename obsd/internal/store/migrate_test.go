package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestMigrateFreshDBReachesLatest: a brand-new DB ends at the latest schema version.
func TestMigrateFreshDBReachesLatest(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != latestSchemaVersion() {
		t.Errorf("fresh DB user_version=%d, want %d", v, latestSchemaVersion())
	}
}

// TestNewBinaryOpensLegacyDB is the audit-roadmap-#7 guarantee: a database written by an
// OLDER binary (the base tables, user_version still 0, rows present) is opened by the
// CURRENT binary — which migrates it to the latest version and preserves the rows.
func TestNewBinaryOpensLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Simulate the old binary: create the base schema directly, never stamp a version.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schema + unexplainedSchema + incidentsSchema); err != nil {
		t.Fatal(err)
	}
	ts := "2026-01-01T00:00:00.000000000Z"
	if _, err := raw.Exec(
		`INSERT INTO findings (entity_cei,phenomenon,graph_version,label,namespace,name,kind,quality,
		  completeness,required_total,required_met,required_unobserved,members_json,unobservable_json,
		  first_seen,last_seen) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"i|cl|ns|Pod|legacy|uid", "PHEN_MEMORY_LEAK", "sha256:old", "Memory leak", "ns", "legacy", "Container",
		"degraded", 0.5, 2, 1, 1, "null", "null", ts, ts); err != nil {
		t.Fatal(err)
	}
	var v0 int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&v0); err != nil {
		t.Fatal(err)
	}
	if v0 != 0 {
		t.Fatalf("legacy DB should be at user_version 0, got %d", v0)
	}
	_ = raw.Close()

	// The current binary opens it.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("current binary failed to open a legacy DB: %v", err)
	}
	defer s.Close()

	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != latestSchemaVersion() {
		t.Errorf("legacy DB not migrated to latest: user_version=%d, want %d", v, latestSchemaVersion())
	}
	rows, err := s.ActiveFindings(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.EntityCEI == "i|cl|ns|Pod|legacy|uid" {
			found = true
		}
	}
	if !found {
		t.Error("the legacy finding row was lost across migration (data must be preserved)")
	}
}

// TestLadderAppliesInOrderAndIsIdempotent exercises the ladder mechanics with a custom
// 2-step ladder (a real ALTER), proving steps apply in order and a re-open re-applies
// nothing (the ALTER would error 'duplicate column' if it did).
func TestLadderAppliesInOrderAndIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ladder.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ladder := []migration{
		{1, "CREATE TABLE t (id INTEGER PRIMARY KEY);"},
		{2, "ALTER TABLE t ADD COLUMN note TEXT;"},
	}
	from, to, err := migrate(db, ladder)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if from != 0 || to != 2 {
		t.Errorf("first migrate from/to = %d/%d, want 0/2", from, to)
	}
	if _, err := db.Exec("INSERT INTO t (id, note) VALUES (1, 'x')"); err != nil {
		t.Fatalf("v2 column not applied: %v", err)
	}

	// Re-run: nothing should re-apply (idempotent).
	from2, to2, err := migrate(db, ladder)
	if err != nil {
		t.Fatalf("idempotent re-migrate failed (a shipped migration re-ran?): %v", err)
	}
	if from2 != 2 || to2 != 2 {
		t.Errorf("re-migrate from/to = %d/%d, want 2/2 (no re-apply)", from2, to2)
	}
}

// TestIncidentMemorySurvivesOntologyUpgrade: the durable incident memory is keyed by
// (phenomenon, role, window-bucket) — NOT by graph_version — so a phenomenon that recurs
// across an ontology release keeps the same incident and increments its recurrence. This
// is the deliberate counterpart to findings (whose PK embeds graph_version, so a release
// starts fresh rows): operational MEMORY persists across upgrades, point-in-time findings
// are pinned to the graph that produced them.
func TestIncidentMemorySurvivesOntologyUpgrade(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "inc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	t0 := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	const resolve = 5 * time.Minute
	const bucket = time.Hour

	// First episode under graph vA.
	if err := s.UpsertIncident(t0, "PHEN_MEMORY_LEAK", "role|cl|ns|Deployment/web", false, "sha256:vA", resolve, bucket); err != nil {
		t.Fatal(err)
	}
	// A resolve gap, then the SAME phenomenon+role recurs under a NEW graph version vB
	// (an ontology release happened between episodes). Same incident_key ⇒ recurrence
	// increments ⇒ the memory survives the upgrade.
	if err := s.UpsertIncident(t0.Add(10*time.Minute), "PHEN_MEMORY_LEAK", "role|cl|ns|Deployment/web", false, "sha256:vB", resolve, bucket); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ActiveIncidents(10)
	if err != nil {
		t.Fatal(err)
	}
	var got *IncidentRow
	for i := range rows {
		if rows[i].Phenomenon == "PHEN_MEMORY_LEAK" {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatal("incident memory lost across the ontology upgrade")
	}
	if got.RecurrenceCount != 2 {
		t.Errorf("recurrence=%d, want 2 (the across-upgrade recurrence must be counted)", got.RecurrenceCount)
	}
	if got.GraphVersion != "sha256:vB" {
		t.Errorf("graph_version=%q, want sha256:vB (the latest occurrence's graph, COALESCE'd forward)", got.GraphVersion)
	}
}
