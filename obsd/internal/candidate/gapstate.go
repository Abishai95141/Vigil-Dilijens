package candidate

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Gap exploration scheduler (doc 21 §3, Phase 3 slice 3). A "gap" is one thing the agent
// might explore — an unmapped stray, a silence-ledger row, an open unexplained card, a
// discrete event. Re-feeding every gap into the LLM context on every sweep wastes tokens on
// gaps that keep yielding nothing. gap_state fixes that DETERMINISTICALLY: each visit records
// an attempt and schedules the next revisit at `now + base * 2^min(attempts, cap)` — pure
// arithmetic over a stored count, NO learned multiplier and NO data-fit interval. A gap is
// re-examined only once it is overdue. The attempt count also surfaces as the Recurrence
// field of candidate Support (slice 1) — a count of how often a gap deterministically
// re-surfaced, never a confidence.
//
// This lives in the firewalled candidate store, OFF the deterministic replay path. The clock
// is always injected (`now`) — the package never reads the wall clock.

// GapState is one gap's scheduling record: counts + injected timestamps, nothing learned.
type GapState struct {
	GapID          string    `json:"gapId"`
	Kind           string    `json:"kind"`
	AttemptCount   int       `json:"attemptCount"`
	RejectionCount int       `json:"rejectionCount"`
	LastReason     string    `json:"lastReason,omitempty"`
	FirstSeenAt    time.Time `json:"firstSeenAt"`
	LastAttemptAt  time.Time `json:"lastAttemptAt"`
	NextRevisitAt  time.Time `json:"nextRevisitAt"`
}

// GapBackoffCap bounds the exponent so the interval can never overflow or run away: at the cap
// the revisit interval is base * 2^cap (e.g. base=5m, cap=6 ⇒ ~5.3h max).
const GapBackoffCap = 6

// backoffInterval = base * 2^min(attempts, cap). Pure arithmetic over the stored attempt
// count — the entire scheduling policy, with no learned or fitted term. attempts<=0 ⇒ base.
func backoffInterval(base time.Duration, attempts, capExp int) time.Duration {
	n := attempts
	if n > capExp {
		n = capExp
	}
	if n < 0 {
		n = 0
	}
	return base * time.Duration(int64(1)<<uint(n))
}

// RecordGapAttempt records that the agent examined this gap on this sweep: it increments the
// attempt count and pushes next_revisit out by the deterministic backoff. Returns the updated
// state. `now` is injected. A first sighting sets first_seen_at = now.
func (s *Store) RecordGapAttempt(now time.Time, gapID, kind string, base time.Duration, capExp int) (GapState, error) {
	return s.bumpGap(now, gapID, kind, base, capExp, false, "")
}

// RecordGapRejection records that a candidate derived from this gap was REJECTED (by a gate or
// a named human): it increments BOTH the attempt and the rejection count and applies the same
// backoff, and records the reason for the decision trail. A rejected gap is thus revisited
// less often, not silently retried every sweep. `now` is injected.
func (s *Store) RecordGapRejection(now time.Time, gapID, reason string, base time.Duration, capExp int) error {
	_, err := s.bumpGap(now, gapID, "", base, capExp, true, reason)
	return err
}

// bumpGap is the shared upsert: read the current row (if any), increment counts, recompute the
// backoff from the NEW attempt count, and write it back. Deterministic given (now, prior row).
func (s *Store) bumpGap(now time.Time, gapID, kind string, base time.Duration, capExp int, rejection bool, reason string) (GapState, error) {
	if strings.TrimSpace(gapID) == "" {
		return GapState{}, errors.New("candidate: empty gap_id")
	}
	if base <= 0 {
		return GapState{}, fmt.Errorf("candidate: gap backoff base must be positive, got %s", base)
	}
	cur, found, err := s.GetGapState(gapID)
	if err != nil {
		return GapState{}, err
	}
	g := GapState{GapID: gapID, Kind: kind, FirstSeenAt: now}
	if found {
		g = cur
		if kind != "" {
			g.Kind = kind
		}
	}
	g.AttemptCount++
	if rejection {
		g.RejectionCount++
		g.LastReason = reason
	}
	g.LastAttemptAt = now
	// The k-th look schedules the next revisit at now + base·2^min(k-1, cap): the 1st look waits
	// `base`, the 2nd `2·base`, the 3rd `4·base`, … (deterministic, no learned term).
	g.NextRevisitAt = now.Add(backoffInterval(base, g.AttemptCount-1, capExp))
	if g.Kind == "" {
		g.Kind = gapKindFromID(gapID)
	}
	_, err = s.db.Exec(`
INSERT INTO gap_state (gap_id, kind, attempt_count, rejection_count, last_reason, first_seen_at, last_attempt_at, next_revisit_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(gap_id) DO UPDATE SET
  kind            = excluded.kind,
  attempt_count   = excluded.attempt_count,
  rejection_count = excluded.rejection_count,
  last_reason     = excluded.last_reason,
  last_attempt_at = excluded.last_attempt_at,
  next_revisit_at = excluded.next_revisit_at`,
		g.GapID, g.Kind, g.AttemptCount, g.RejectionCount, g.LastReason,
		g.FirstSeenAt.UTC().Format(sqlTime), g.LastAttemptAt.UTC().Format(sqlTime), g.NextRevisitAt.UTC().Format(sqlTime))
	if err != nil {
		return GapState{}, fmt.Errorf("candidate: record gap: %w", err)
	}
	return g, nil
}

// GetGapState returns the scheduling row for a gap. found is false (nil error) when there is
// no row — a never-examined gap, which is always due.
func (s *Store) GetGapState(gapID string) (GapState, bool, error) {
	row := s.db.QueryRow(`
SELECT gap_id, kind, attempt_count, rejection_count, last_reason, first_seen_at, last_attempt_at, next_revisit_at
FROM gap_state WHERE gap_id = ?`, gapID)
	g, err := scanGap(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GapState{}, false, nil
	}
	if err != nil {
		return GapState{}, false, err
	}
	return g, true, nil
}

// DueGaps reports, for each of the given gap_ids, whether it is DUE for the agent to examine
// now: a gap with no row (never examined) is always due; one with a row is due iff its
// next_revisit_at <= now. The not-due set is the backoff in action — those gaps are skipped
// this sweep. Never errors on an unknown id (it is simply due).
func (s *Store) DueGaps(now time.Time, gapIDs []string) (map[string]bool, error) {
	states, err := s.gapStatesByID(gapIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(gapIDs))
	for _, id := range gapIDs {
		g, ok := states[id]
		out[id] = !ok || !g.NextRevisitAt.After(now) // no row ⇒ due; else due iff next <= now
	}
	return out, nil
}

// GapAttempts returns the attempt count for a gap (0 if never examined) — the Recurrence count
// surfaced in candidate Support. Never errors on an unknown id.
func (s *Store) GapAttempts(gapID string) (int, error) {
	g, found, err := s.GetGapState(gapID)
	if err != nil || !found {
		return 0, err
	}
	return g.AttemptCount, nil
}

// gapStatesByID loads the rows for a set of ids into a map (absent ids simply missing).
func (s *Store) gapStatesByID(gapIDs []string) (map[string]GapState, error) {
	out := make(map[string]GapState, len(gapIDs))
	for _, id := range gapIDs {
		g, found, err := s.GetGapState(id)
		if err != nil {
			return nil, err
		}
		if found {
			out[id] = g
		}
	}
	return out, nil
}

// ListGapStates returns every gap row, ordered deterministically (most-attempted first, then
// id) — for the telemetry log and the decision-trail UI.
func (s *Store) ListGapStates() ([]GapState, error) {
	rows, err := s.db.Query(`
SELECT gap_id, kind, attempt_count, rejection_count, last_reason, first_seen_at, last_attempt_at, next_revisit_at
FROM gap_state`)
	if err != nil {
		return nil, fmt.Errorf("candidate: list gap states: %w", err)
	}
	defer rows.Close()
	var out []GapState
	for rows.Next() {
		g, err := scanGap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AttemptCount != out[j].AttemptCount {
			return out[i].AttemptCount > out[j].AttemptCount
		}
		return out[i].GapID < out[j].GapID
	})
	return out, nil
}

func scanGap(sc scanner) (GapState, error) {
	var (
		g                 GapState
		lastReason        sql.NullString
		first, last, next string
	)
	if err := sc.Scan(&g.GapID, &g.Kind, &g.AttemptCount, &g.RejectionCount, &lastReason, &first, &last, &next); err != nil {
		return GapState{}, err
	}
	g.LastReason = lastReason.String
	var err error
	if g.FirstSeenAt, err = time.Parse(sqlTime, first); err != nil {
		return GapState{}, fmt.Errorf("candidate: scan gap first_seen_at: %w", err)
	}
	if g.LastAttemptAt, err = time.Parse(sqlTime, last); err != nil {
		return GapState{}, fmt.Errorf("candidate: scan gap last_attempt_at: %w", err)
	}
	if g.NextRevisitAt, err = time.Parse(sqlTime, next); err != nil {
		return GapState{}, fmt.Errorf("candidate: scan gap next_revisit_at: %w", err)
	}
	return g, nil
}

// gapKindFromID infers a descriptive kind from the gap_id prefix (stray:/silence:/
// unexplained:/event:). Descriptive only — never gates.
func gapKindFromID(gapID string) string {
	if i := strings.IndexByte(gapID, ':'); i > 0 {
		return gapID[:i]
	}
	return "gap"
}
