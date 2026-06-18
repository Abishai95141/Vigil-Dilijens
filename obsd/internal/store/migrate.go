package store

import (
	"database/sql"
	"fmt"
)

// Schema versioning + migration ladder (audit roadmap #7). The prior store used only
// CREATE TABLE IF NOT EXISTS, with no recorded schema version and no upgrade path — so
// there was no way to evolve the on-disk schema, and no test that a new binary opens an
// old database. The store now stamps PRAGMA user_version and applies an ordered,
// append-only ladder: a newer binary opens an older DB, runs only the missing steps in
// order, preserves existing rows, and is idempotent on a re-open.
//
// DISCIPLINE: NEVER edit a shipped migration — append a new one. v1 is the baseline
// (idempotent CREATE IF NOT EXISTS), so a pre-versioning database created by an older
// binary (user_version 0, tables already present) migrates cleanly up to v1.
type migration struct {
	version int
	sql     string
}

var schemaLadder = []migration{
	{1, schema + unexplainedSchema + incidentsSchema},
	// {2, "ALTER TABLE findings ADD COLUMN ...;"},  // future — append, never edit above.
}

// migrate brings db up to the latest ladder version, applying each step whose version
// exceeds the DB's current PRAGMA user_version, in order, each in its own transaction.
// (user_version lives in the SQLite database header and is transactional, so a crash
// mid-migration rolls back cleanly and the step re-runs on the next open.) Returns the
// version it started at and the version it reached.
func migrate(db *sql.DB, ladder []migration) (from, to int, err error) {
	var cur int
	if err := db.QueryRow("PRAGMA user_version").Scan(&cur); err != nil {
		return 0, 0, fmt.Errorf("read user_version: %w", err)
	}
	from = cur
	for _, m := range ladder {
		if m.version <= cur {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return from, cur, err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return from, cur, fmt.Errorf("migration %d: %w", m.version, err)
		}
		// PRAGMA user_version cannot be parameterized; m.version is an int we control.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			_ = tx.Rollback()
			return from, cur, fmt.Errorf("migration %d set version: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return from, cur, fmt.Errorf("migration %d commit: %w", m.version, err)
		}
		cur = m.version
	}
	return from, cur, nil
}

// latestSchemaVersion is the version a fully-migrated DB reaches.
func latestSchemaVersion() int {
	if len(schemaLadder) == 0 {
		return 0
	}
	return schemaLadder[len(schemaLadder)-1].version
}
