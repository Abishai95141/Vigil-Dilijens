package notify

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite" (CGO-free)
)

// sqlTime is the fixed-width timestamp format (matches internal/store + internal/
// candidate): every value is the same width so SQL's lexicographic ORDER BY is exactly
// chronological.
const sqlTime = "2006-01-02T15:04:05.000000000Z07:00"

const schema = `
CREATE TABLE IF NOT EXISTS alerts (
  dedup_key   TEXT PRIMARY KEY,
  priority    INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  headline    TEXT NOT NULL,
  entity      TEXT,
  last_sent   TEXT NOT NULL,
  send_count  INTEGER NOT NULL,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS alerts_last_sent ON alerts(last_sent DESC);
`

// Store is the SQLite-backed alert ledger: the per-key last-sent state that drives
// cooldown (idempotent across restart — a restart must not re-alert everything) plus a
// durable history of what was emailed. OFF the deterministic path (the firewall test
// proves no replay-deterministic package reaches it).
type Store struct {
	db *sql.DB
}

// Open opens (creating) alerts.db at path. An empty path uses a private in-memory
// database (tests). The pure-Go driver is pinned to one connection, matching the other
// stores.
func Open(path string) (*Store, error) {
	dsn := path
	if dsn == "" {
		dsn = ":memory:"
	}
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("notify: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("notify: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// LastSent returns when an alert with this dedup key was last emailed. found is false
// (zero time, nil error) when the key has never been sent.
func (s *Store) LastSent(dedupKey string) (time.Time, bool, error) {
	var ts string
	err := s.db.QueryRow(`SELECT last_sent FROM alerts WHERE dedup_key = ?`, dedupKey).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("notify: last sent: %w", err)
	}
	t, perr := time.Parse(sqlTime, ts)
	if perr != nil {
		return time.Time{}, false, fmt.Errorf("notify: parse last_sent: %w", perr)
	}
	return t, true, nil
}

// RecordSent stamps an emailed alert: a new key inserts (send_count=1, created_at=now);
// a re-sent key advances last_sent and increments send_count. `now` is injected.
func (s *Store) RecordSent(now time.Time, a Alert) error {
	at := now.UTC().Format(sqlTime)
	_, err := s.db.Exec(`
INSERT INTO alerts (dedup_key, priority, kind, headline, entity, last_sent, send_count, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(dedup_key) DO UPDATE SET
  priority   = excluded.priority,
  kind       = excluded.kind,
  headline   = excluded.headline,
  entity     = excluded.entity,
  last_sent  = excluded.last_sent,
  send_count = alerts.send_count + 1,
  updated_at = excluded.updated_at`,
		a.DedupKey, int(a.Priority), a.Kind, a.Headline, a.Entity, at, at, at)
	if err != nil {
		return fmt.Errorf("notify: record sent: %w", err)
	}
	return nil
}

// Record is one row of the durable alert history.
type Record struct {
	DedupKey  string    `json:"dedupKey"`
	Priority  int       `json:"priority"`
	Kind      string    `json:"kind"`
	Headline  string    `json:"headline"`
	Entity    string    `json:"entity"`
	LastSent  time.Time `json:"lastSent"`
	SendCount int       `json:"sendCount"`
	CreatedAt time.Time `json:"createdAt"`
}

// History returns the most-recently-sent alerts, newest first (for an operator surface).
func (s *Store) History(limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT dedup_key, priority, kind, headline, entity, last_sent, send_count, created_at
FROM alerts ORDER BY last_sent DESC, dedup_key LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("notify: history: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var last, created string
		if err := rows.Scan(&r.DedupKey, &r.Priority, &r.Kind, &r.Headline, &r.Entity, &last, &r.SendCount, &created); err != nil {
			return nil, fmt.Errorf("notify: scan: %w", err)
		}
		r.LastSent, _ = time.Parse(sqlTime, last)
		r.CreatedAt, _ = time.Parse(sqlTime, created)
		out = append(out, r)
	}
	return out, rows.Err()
}
