package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// Durable history for the unexplained channel (doc 08), so the anomaly timeline
// (doc 10 M4) survives restarts and shows aging spans of cards that have since
// closed. Off the deterministic path, like the findings table — a write failure
// is surfaced, never allowed to perturb detection. Keyed by (scope, signature,
// graph) where signature is the sorted uncovered-metric set: one row per
// distinct loud pattern, first_seen→last_seen carrying its span.

const unexplainedSchema = `
CREATE TABLE IF NOT EXISTS unexplained (
  scope          TEXT NOT NULL,
  signature      TEXT NOT NULL,
  graph_version  TEXT NOT NULL,
  namespace      TEXT,
  name           TEXT,
  kind           TEXT,
  metrics        TEXT,
  status         TEXT NOT NULL,
  superseded_by  TEXT,
  occurrences    INTEGER,
  first_seen     TEXT NOT NULL,
  last_seen      TEXT NOT NULL,
  PRIMARY KEY (scope, signature, graph_version)
);
CREATE INDEX IF NOT EXISTS unexplained_last_seen ON unexplained(last_seen DESC);
`

// UnexplainedRow is one persisted unexplained card as the timeline reads it back.
type UnexplainedRow struct {
	Scope        string    `json:"scope"`
	Namespace    string    `json:"namespace"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	Metrics      []string  `json:"metrics"`
	Status       string    `json:"status"`
	SupersededBy string    `json:"supersededBy"`
	Occurrences  int       `json:"occurrences"`
	FirstSeen    time.Time `json:"firstSeen"`
	LastSeen     time.Time `json:"lastSeen"`
}

// UpsertUnexplained records this window's routed cards: a new (scope, signature)
// sets first_seen; a recurring one advances last_seen and refreshes status /
// occurrences. A resolved/superseded card lands its terminal status (the row is
// kept — the timeline wants the closed span). Cards carry the signature in their
// LoudStates metrics; we derive it the same way the tracker does.
func (s *Store) UpsertUnexplained(at time.Time, cards []unexplained.Finding) error {
	if len(cards) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`
INSERT INTO unexplained (scope, signature, graph_version, namespace, name, kind,
  metrics, status, superseded_by, occurrences, first_seen, last_seen)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(scope, signature, graph_version) DO UPDATE SET
  status=excluded.status, superseded_by=excluded.superseded_by,
  occurrences=excluded.occurrences, last_seen=excluded.last_seen`)
	if err != nil {
		return fmt.Errorf("store: prepare: %w", err)
	}
	defer stmt.Close()
	for _, c := range cards {
		metrics := metricsOf(c.LoudStates)
		sig := strings.Join(metrics, ",")
		first := c.FirstSeen.UTC().Format(sqlTime)
		last := c.LastSeen.UTC().Format(sqlTime)
		if c.LastSeen.IsZero() {
			last = at.UTC().Format(sqlTime)
		}
		if _, err := stmt.Exec(c.Scope, sig, c.GraphVersion, c.Namespace, c.Name, c.Kind,
			sig, string(c.Status), c.SupersededBy, c.Occurrences, first, last); err != nil {
			return fmt.Errorf("store: upsert unexplained: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ActiveUnexplained returns the most-recently-seen unexplained cards, newest
// first — the timeline's aging-span source.
func (s *Store) ActiveUnexplained(limit int) ([]UnexplainedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT scope, namespace, name, kind, metrics, status, superseded_by, occurrences, first_seen, last_seen
FROM unexplained ORDER BY last_seen DESC, scope, signature LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: query unexplained: %w", err)
	}
	defer rows.Close()
	var out []UnexplainedRow
	for rows.Next() {
		var r UnexplainedRow
		var metrics, first, last string
		if err := rows.Scan(&r.Scope, &r.Namespace, &r.Name, &r.Kind, &metrics,
			&r.Status, &r.SupersededBy, &r.Occurrences, &first, &last); err != nil {
			return nil, fmt.Errorf("store: scan unexplained: %w", err)
		}
		if metrics != "" {
			r.Metrics = strings.Split(metrics, ",")
		} else {
			r.Metrics = []string{}
		}
		r.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		r.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, r)
	}
	return out, rows.Err()
}

// metricsOf derives the sorted distinct metric set of a card's loud states —
// the signature, matching the tracker's dedup key.
func metricsOf(states []unexplained.LoudState) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range states {
		if !seen[s.Metric] {
			seen[s.Metric] = true
			out = append(out, s.Metric)
		}
	}
	// stable sort without importing sort twice — small slice
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
