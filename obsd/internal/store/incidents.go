package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/incident"
)

// The incidents table (v3 T-B): the durable cross-run phenomenon memory. It lives in
// the SAME SQLite DB the findings table does and is equally OFF the deterministic path
// — the replay digest never reads it; a write failure is surfaced, never allowed to
// perturb detection. UpsertIncident applies the SAME deterministic recurrence logic as
// incident.Accumulator (cross-checked in incidents_test.go), so the live durable memory
// and the replay-gate fold can never diverge.
const incidentsSchema = `
CREATE TABLE IF NOT EXISTS incidents (
  incident_key     TEXT PRIMARY KEY,
  phenomenon       TEXT NOT NULL,
  role_cei         TEXT NOT NULL,
  role_unresolved  INTEGER NOT NULL DEFAULT 0,
  window_bucket    TEXT NOT NULL,
  first_seen       TEXT NOT NULL,
  last_seen        TEXT NOT NULL,
  recurrence_count INTEGER NOT NULL,
  lifespan_seconds INTEGER NOT NULL,
  graph_version    TEXT
);
CREATE INDEX IF NOT EXISTS incidents_last_seen ON incidents(last_seen DESC);
`

// IncidentRow is one persisted incident as the surfaces read it back.
type IncidentRow struct {
	Key             string    `json:"key"`
	Phenomenon      string    `json:"phenomenon"`
	RoleCEI         string    `json:"roleCei"`
	RoleUnresolved  bool      `json:"roleUnresolved"`
	WindowBucket    time.Time `json:"windowBucket"`
	FirstSeen       time.Time `json:"firstSeen"`
	LastSeen        time.Time `json:"lastSeen"`
	RecurrenceCount int       `json:"recurrenceCount"`
	LifespanSeconds int64     `json:"lifespanSeconds"`
	GraphVersion    string    `json:"graphVersion"`
}

// UpsertIncident folds one finding observation into its durable incident. A new key
// inserts at recurrence 1; an existing key increments recurrence ONLY across a gap
// longer than resolveHorizon (a resolve→refire), never per tick. Pure given its inputs
// (no time.Now: the observation instant is injected). resolveHorizon + bucketSize are
// declared parameters (params file), never magic constants.
func (s *Store) UpsertIncident(at time.Time, phenomenonID, roleCEIKey string, roleUnresolved bool, graphVersion string, resolveHorizon, bucketSize time.Duration) error {
	at = at.UTC()
	bucket := incident.Bucket(at, bucketSize)
	key := incident.Key(phenomenonID, roleCEIKey, bucket)

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: incident begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var firstStr, lastStr string
	var rc int
	scanErr := tx.QueryRow(`SELECT first_seen, last_seen, recurrence_count FROM incidents WHERE incident_key=?`, key).
		Scan(&firstStr, &lastStr, &rc)
	switch scanErr {
	case sql.ErrNoRows:
		if _, err := tx.Exec(`
INSERT INTO incidents (incident_key, phenomenon, role_cei, role_unresolved, window_bucket,
  first_seen, last_seen, recurrence_count, lifespan_seconds, graph_version)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
			key, phenomenonID, roleCEIKey, b2i(roleUnresolved), bucket.UTC().Format(sqlTime),
			at.Format(sqlTime), at.Format(sqlTime), 1, 0, graphVersion); err != nil {
			return fmt.Errorf("store: incident insert: %w", err)
		}
	case nil:
		first, _ := time.Parse(time.RFC3339Nano, firstStr)
		last, _ := time.Parse(time.RFC3339Nano, lastStr)
		newRC := rc
		if at.Sub(last) > resolveHorizon { // a resolve gap ⇒ a distinct episode
			newRC++
		}
		newLast := last
		if at.After(last) {
			newLast = at
		}
		newFirst := first
		if at.Before(first) {
			newFirst = at
		}
		lifespan := int64(newLast.Sub(newFirst).Seconds())
		if _, err := tx.Exec(`
UPDATE incidents SET last_seen=?, first_seen=?, recurrence_count=?, lifespan_seconds=?,
  graph_version=COALESCE(NULLIF(?, ''), graph_version)
WHERE incident_key=?`,
			newLast.Format(sqlTime), newFirst.Format(sqlTime), newRC, lifespan, graphVersion, key); err != nil {
			return fmt.Errorf("store: incident update: %w", err)
		}
	default:
		return fmt.Errorf("store: incident read: %w", scanErr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: incident commit: %w", err)
	}
	return nil
}

// ActiveIncidents returns incidents most-recently-seen first.
func (s *Store) ActiveIncidents(limit int) ([]IncidentRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT incident_key, phenomenon, role_cei, role_unresolved, window_bucket,
  first_seen, last_seen, recurrence_count, lifespan_seconds, graph_version
FROM incidents ORDER BY last_seen DESC, incident_key LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: incident query: %w", err)
	}
	defer rows.Close()
	var out []IncidentRow
	for rows.Next() {
		var r IncidentRow
		var ru int
		var bucket, first, last string
		var gv sql.NullString
		if err := rows.Scan(&r.Key, &r.Phenomenon, &r.RoleCEI, &ru, &bucket,
			&first, &last, &r.RecurrenceCount, &r.LifespanSeconds, &gv); err != nil {
			return nil, fmt.Errorf("store: incident scan: %w", err)
		}
		r.RoleUnresolved = ru != 0
		r.WindowBucket, _ = time.Parse(time.RFC3339Nano, bucket)
		r.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		r.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		r.GraphVersion = gv.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// IncidentCount returns the number of persisted incidents (health/metrics + tests).
func (s *Store) IncidentCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM incidents`).Scan(&n)
	return n, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
