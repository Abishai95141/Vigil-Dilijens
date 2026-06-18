package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
)

// Store is the SQLite-backed surfacing sink.
type Store struct {
	db *sql.DB
}

// sqlTime is the fixed-width timestamp format for TEXT columns: unlike
// RFC3339Nano (which trims trailing zeros), every value is the same width, so
// SQL's lexicographic ORDER BY is exactly chronological ("…00Z" would otherwise
// sort after "…00.5Z"). RFC3339Nano still parses it on the way out.
const sqlTime = "2006-01-02T15:04:05.000000000Z07:00"

// FindingRow is one persisted finding as the surfaces read it back.
type FindingRow struct {
	Phenomenon         string    `json:"phenomenon"`
	Label              string    `json:"label"`
	EntityCEI          string    `json:"entityCei"`
	Namespace          string    `json:"namespace"`
	Name               string    `json:"name"`
	Kind               string    `json:"kind"`
	Quality            string    `json:"quality"`
	Completeness       float64   `json:"completeness"`
	RequiredTotal      int       `json:"requiredTotal"`
	RequiredMet        int       `json:"requiredMet"`
	RequiredUnobserved int       `json:"requiredUnobserved"`
	GraphVersion       string    `json:"graphVersion"`
	Members            []byte    `json:"-"`
	Unobservable       []byte    `json:"-"`
	FirstSeen          time.Time `json:"firstSeen"`
	LastSeen           time.Time `json:"lastSeen"`

	// Stale + LastSeenAgoSeconds are DERIVED at serve time (LastSeen vs the
	// response stamp) — never stored, off the deterministic digest. The findings
	// store is durable (it survives restarts, doc 14 A7), so a finding it still
	// holds may have last matched long ago. A surface must not present that as
	// firing NOW: a finding whose last match is older than the freshness horizon
	// is marked stale ("last seen Xs ago"), distinguishing a live match from a
	// resolved one (the audit's cross-surface-disagreement fix).
	Stale              bool    `json:"stale"`
	LastSeenAgoSeconds float64 `json:"lastSeenAgoSeconds"`
}

// MarkFreshness stamps the derived Stale / LastSeenAgoSeconds on a row relative
// to asOf and the freshness horizon (typically a few evaluation ticks). Pure;
// serve-time only — the stored row is never mutated.
func (r *FindingRow) MarkFreshness(asOf time.Time, staleAfter time.Duration) {
	ago := asOf.Sub(r.LastSeen)
	if ago < 0 {
		ago = 0
	}
	r.LastSeenAgoSeconds = ago.Seconds()
	r.Stale = staleAfter > 0 && ago > staleAfter
}

const schema = `
CREATE TABLE IF NOT EXISTS findings (
  entity_cei          TEXT NOT NULL,
  phenomenon          TEXT NOT NULL,
  graph_version       TEXT NOT NULL,
  label               TEXT NOT NULL,
  namespace           TEXT,
  name                TEXT,
  kind                TEXT,
  quality             TEXT,
  completeness        REAL,
  required_total      INTEGER,
  required_met        INTEGER,
  required_unobserved INTEGER,
  members_json        TEXT,
  unobservable_json   TEXT,
  first_seen          TEXT NOT NULL,
  last_seen           TEXT NOT NULL,
  PRIMARY KEY (entity_cei, phenomenon, graph_version)
);
CREATE INDEX IF NOT EXISTS findings_last_seen ON findings(last_seen DESC);
`

// Open opens (creating + migrating) the SQLite database at path. An empty path
// uses a private in-memory database (tests). The pure-Go driver is happiest with
// a single connection, so the pool is pinned to one.
func Open(path string) (*Store, error) {
	dsn := path
	if dsn == "" {
		dsn = ":memory:"
	}
	// _pragma busy_timeout so a concurrent reader never errors on a write lock.
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	// Versioned migration ladder (audit roadmap #7): a newer binary opens an older DB,
	// applies only the missing steps, and preserves existing rows. See migrate.go.
	if _, _, err := migrate(db, schemaLadder); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// UpsertFindings records this tick's findings: a new (entity, phenomenon, graph)
// sets first_seen; a recurring one advances last_seen and refreshes the snapshot.
// The condition's span is preserved (first→last) for the timeline. Off the
// deterministic path — a write failure is surfaced, never allowed to perturb
// detection.
func (s *Store) UpsertFindings(evalAt time.Time, fs []detect.Finding) error {
	if len(fs) == 0 {
		return nil
	}
	at := evalAt.UTC().Format(sqlTime)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`
INSERT INTO findings (entity_cei, phenomenon, graph_version, label, namespace, name, kind,
  quality, completeness, required_total, required_met, required_unobserved,
  members_json, unobservable_json, first_seen, last_seen)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(entity_cei, phenomenon, graph_version) DO UPDATE SET
  label=excluded.label, quality=excluded.quality, completeness=excluded.completeness,
  required_total=excluded.required_total, required_met=excluded.required_met,
  required_unobserved=excluded.required_unobserved,
  members_json=excluded.members_json, unobservable_json=excluded.unobservable_json,
  last_seen=excluded.last_seen`)
	if err != nil {
		return fmt.Errorf("store: prepare: %w", err)
	}
	defer stmt.Close()
	for i := range fs {
		f := &fs[i]
		members, _ := json.Marshal(f.Members)
		unobs, _ := json.Marshal(f.Unobservable)
		if _, err := stmt.Exec(f.EntityCEI, f.Phenomenon, f.GraphVersion, f.Label,
			f.Namespace, f.Name, f.Kind, string(f.Quality), f.Completeness,
			f.RequiredTotal, f.RequiredMet, f.RequiredUnobserved,
			string(members), string(unobs), at, at); err != nil {
			return fmt.Errorf("store: upsert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ActiveFindings returns the most-recently-seen findings, newest first.
func (s *Store) ActiveFindings(limit int) ([]FindingRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT phenomenon, label, entity_cei, namespace, name, kind, quality, completeness,
  required_total, required_met, required_unobserved, graph_version,
  members_json, unobservable_json, first_seen, last_seen
FROM findings ORDER BY last_seen DESC, entity_cei, phenomenon LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	defer rows.Close()
	var out []FindingRow
	for rows.Next() {
		var r FindingRow
		var members, unobs, first, last string
		if err := rows.Scan(&r.Phenomenon, &r.Label, &r.EntityCEI, &r.Namespace, &r.Name, &r.Kind,
			&r.Quality, &r.Completeness, &r.RequiredTotal, &r.RequiredMet, &r.RequiredUnobserved,
			&r.GraphVersion, &members, &unobs, &first, &last); err != nil {
			return nil, fmt.Errorf("store: scan: %w", err)
		}
		r.Members = []byte(members)
		r.Unobservable = []byte(unobs)
		r.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		r.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Count returns the number of persisted finding rows (for health/metrics).
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM findings`).Scan(&n)
	return n, err
}
