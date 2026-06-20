package candidate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite" (CGO-free)
)

// Kind is the typed shape of a candidate proposal.
type Kind string

const (
	KindNode                Kind = "node"                 // a proposed node (e.g. a stray-metric provisional entity)
	KindEdge                Kind = "edge"                 // a proposed STRUCTURAL edge (never causal)
	KindMember              Kind = "member"               // a proposed phenomenon member binding
	KindBarSource           Kind = "bar_source"           // a proposed declared-config bar pointer
	KindCausalHypothesis    Kind = "causal_hypothesis"    // a direction-free co-occurrence hypothesis (never a cause)
	KindEquivGroup          Kind = "equiv_group"          // a proposed stray→equivalence-group mapping (doc 21 §5); promotion authors a regex the resolver absorbs
	KindPhenomenonCandidate Kind = "phenomenon_candidate" // a recurring unexplained anomaly proposed as a phenomenon for human curation (doc 21 Phase 4); promotion authors a phenomenon skeleton
)

// Status is the lifecycle position of a candidate. It is ORTHOGONAL to the three
// immutable provenance classes — a candidate's underlying readings stay MEASURED;
// only its proposed join/edge carries one of these.
type Status string

const (
	StatusCandidate Status = "candidate" // staged, awaiting verification/promotion
	StatusPromoted  Status = "promoted"  // a human authored it into the graph (terminal)
	StatusRejected  Status = "rejected"  // a deterministic gate or human rejected it
	StatusShadow    Status = "shadow"    // a non-winning proposal kept for lineage/audit (MDM survivorship)
)

// structuralEdgeRelations is the closed set of relations a KindEdge may carry. A
// causal relation is absent BY CONSTRUCTION: a causal proposal must use
// KindCausalHypothesis (direction-free co-occurrence), never an authoritative edge.
var structuralEdgeRelations = map[string]bool{
	"topology":        true,
	"associated-with": true,
	"topo-adjacent":   true,
}

// EvidenceRef is one discrete, MEASURED fact a candidate cites. There is no score
// and no weight — evidence is a set of references, not a fitted confidence (doc 20:
// a learned weight/threshold is forbidden). Ranking, where needed, is a lexicographic
// function of the agreed-evidence set, never a number.
type EvidenceRef struct {
	Kind   string `json:"kind"`             // e.g. "measured-series", "topology-edge", "silence-row", "co-occurrence"
	Ref    string `json:"ref"`              // a stable identifier of the MEASURED fact
	Detail string `json:"detail,omitempty"` // optional human-readable context (never a causal claim)
}

// Lineage records what produced a candidate, so every proposal is auditable back
// to its inputs and method.
type Lineage struct {
	Source       string   `json:"source"`           // e.g. "cei-fallback", "assoc", "dgx-agent"
	Method       string   `json:"method"`           // e.g. "discrete-label-intersection"
	GraphVersion string   `json:"graphVersion"`     // the release the proposal was made against
	Inputs       []string `json:"inputs,omitempty"` // identifiers of the inputs consulted
}

// Suggestion is a PROJECTED model annotation on a candidate (doc 21 Phase 4 §C — agent
// enrichment). It is NEVER part of the content identity (contentID ignores it) and is
// DISCARDED at promotion — the named human authors the authoritative label/note/detection.
// Currently it carries the agent's human-readable label + non-causal description for a
// recurring-anomaly phenomenon candidate, to help the reviewer understand it. It never
// becomes AUTHORED and never drives detection.
type Suggestion struct {
	Label       string `json:"label,omitempty"`       // a concise human-readable name (model's, not authoritative)
	Description string `json:"description,omitempty"` // one line, non-causal — what the recurring pattern may represent
	Model       string `json:"model,omitempty"`       // which provider produced it (provenance)
}

// Candidate is one staged proposal. ID is content-derived (deterministic, dedup-
// stable): re-proposing the same content updates the row in place without losing its
// lifecycle position.
type Candidate struct {
	ID       string         `json:"id"`
	Kind     Kind           `json:"kind"`
	Status   Status         `json:"status"`
	Subject  string         `json:"subject"`            // the focal identifier (stray CEI, signal node, "from→to", …)
	Relation string         `json:"relation,omitempty"` // structural relation for KindEdge; co-occurrence label for KindCausalHypothesis; else empty
	Payload  map[string]any `json:"payload,omitempty"`  // typed-per-kind detail (opaque to the store)
	Evidence []EvidenceRef  `json:"evidence,omitempty"`
	Lineage  Lineage        `json:"lineage"`
	Reason   string         `json:"reason,omitempty"` // SYSTEM reason for the current status (e.g. "evidence<k"); never a causal claim
	// Human decision (governance promotion, doc 12 §3.3 — approval is a NAMED human, never
	// the harness). Set only when a human promotes/rejects; the model never writes these.
	DecidedBy string    `json:"decidedBy,omitempty"` // the named human who promoted/rejected
	Note      string    `json:"note,omitempty"`      // the human's AUTHORED note (the model's rationale is discarded at promotion)
	DecidedAt time.Time `json:"decidedAt,omitempty"`
	// Suggestion is a PROJECTED model annotation (agent enrichment), set via SetSuggestion. It is
	// OUTSIDE the content identity (contentID ignores it) and discarded at promotion. nil = none.
	Suggestion *Suggestion `json:"suggestion,omitempty"`
	CreatedAt  time.Time   `json:"createdAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
}

// Filter selects candidates by lifecycle status and/or kind (empty fields match any).
type Filter struct {
	Status Status
	Kind   Kind
}

// sqlTime is the fixed-width timestamp format (matches internal/store): every value
// is the same width so SQL's lexicographic ORDER BY is exactly chronological.
const sqlTime = "2006-01-02T15:04:05.000000000Z07:00"

const schema = `
CREATE TABLE IF NOT EXISTS candidates (
  id            TEXT PRIMARY KEY,
  kind          TEXT NOT NULL,
  status        TEXT NOT NULL,
  subject       TEXT NOT NULL,
  relation      TEXT,
  payload_json  TEXT,
  evidence_json TEXT,
  lineage_json  TEXT,
  reason        TEXT,
  decided_by    TEXT,
  note          TEXT,
  decided_at    TEXT,
  suggestion_json TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS candidates_status ON candidates(status);
CREATE INDEX IF NOT EXISTS candidates_kind   ON candidates(kind);

-- gap_state (doc 21 §3, Phase 3 slice 3): the agent's deterministic revisit scheduler for
-- exploration GAPS (an unmapped stray, a silence row, an unexplained card, an event). It holds
-- only COUNTS + injected timestamps — no learned interval. The agent re-examines a gap only
-- when next_revisit_at <= now; each look pushes next_revisit out by base * 2^min(attempts, cap)
-- (pure arithmetic), so unproductive gaps back off exponentially instead of burning tokens
-- every sweep. OFF the deterministic path (this whole store is firewalled).
CREATE TABLE IF NOT EXISTS gap_state (
  gap_id          TEXT PRIMARY KEY,
  kind            TEXT NOT NULL,
  attempt_count   INTEGER NOT NULL DEFAULT 0,
  rejection_count INTEGER NOT NULL DEFAULT 0,
  last_reason     TEXT,
  first_seen_at   TEXT NOT NULL,
  last_attempt_at TEXT NOT NULL,
  next_revisit_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS gap_state_revisit ON gap_state(next_revisit_at);
`

// migrations bring an older candidates.db up to the current schema. ADD COLUMN is
// idempotent-by-intent here: a "duplicate column" error means the column already
// exists, which is success, so it is ignored.
var migrations = []string{
	`ALTER TABLE candidates ADD COLUMN decided_by TEXT`,
	`ALTER TABLE candidates ADD COLUMN note TEXT`,
	`ALTER TABLE candidates ADD COLUMN decided_at TEXT`,
	`ALTER TABLE candidates ADD COLUMN suggestion_json TEXT`,
}

// Store is the SQLite-backed candidate staging store. It is OUTSIDE the graph
// loader and OFF the deterministic path (see the package doc).
type Store struct {
	db *sql.DB
}

// Open opens (creating + migrating) candidates.db at path. An empty path uses a
// private in-memory database (tests). The pure-Go driver is pinned to one
// connection, matching internal/store.
func Open(path string) (*Store, error) {
	dsn := path
	if dsn == "" {
		dsn = ":memory:"
	}
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("candidate: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("candidate: migrate: %w", err)
	}
	for _, m := range migrations {
		// A "duplicate column" error means the column already exists (an up-to-date db) —
		// that is success. Any other error is fatal.
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("candidate: migrate %q: %w", m, err)
		}
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Put stages a candidate, returning its content-derived ID. `now` is injected (the
// package never reads the wall clock). A new candidate is inserted with
// status=candidate and created_at=now; re-proposing identical content refreshes the
// payload/evidence/lineage and advances updated_at WITHOUT changing the lifecycle
// status, created_at, or system reason. The proposal is validated structurally — a
// causal edge is rejected here, not in review.
func (s *Store) Put(now time.Time, c Candidate) (string, error) {
	if err := validate(c); err != nil {
		return "", err
	}
	c.Evidence = sortedEvidence(c.Evidence)
	id := contentID(c)
	payload, err := marshalJSON(c.Payload)
	if err != nil {
		return "", fmt.Errorf("candidate: marshal payload: %w", err)
	}
	evidence, err := marshalJSON(c.Evidence)
	if err != nil {
		return "", fmt.Errorf("candidate: marshal evidence: %w", err)
	}
	lineage, err := marshalJSON(c.Lineage)
	if err != nil {
		return "", fmt.Errorf("candidate: marshal lineage: %w", err)
	}
	at := now.UTC().Format(sqlTime)
	_, err = s.db.Exec(`
INSERT INTO candidates (id, kind, status, subject, relation, payload_json, evidence_json, lineage_json, reason, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  payload_json  = excluded.payload_json,
  evidence_json = excluded.evidence_json,
  lineage_json  = excluded.lineage_json,
  updated_at    = excluded.updated_at`,
		id, string(c.Kind), string(StatusCandidate), c.Subject, c.Relation,
		payload, evidence, lineage, "", at, at)
	if err != nil {
		return "", fmt.Errorf("candidate: put: %w", err)
	}
	return id, nil
}

// Get returns the candidate with the given ID. found is false (with a nil
// candidate and nil error) when no such row exists.
func (s *Store) Get(id string) (*Candidate, bool, error) {
	row := s.db.QueryRow(`
SELECT id, kind, status, subject, relation, payload_json, evidence_json, lineage_json, reason, decided_by, note, decided_at, suggestion_json, created_at, updated_at
FROM candidates WHERE id = ?`, id)
	c, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// List returns candidates matching the filter, ordered deterministically by
// (created_at, id).
func (s *Store) List(f Filter) ([]Candidate, error) {
	q := `SELECT id, kind, status, subject, relation, payload_json, evidence_json, lineage_json, reason, decided_by, note, decided_at, suggestion_json, created_at, updated_at FROM candidates`
	var conds []string
	var args []any
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, string(f.Kind))
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY created_at ASC, id ASC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("candidate: list: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// SetStatus transitions a candidate's lifecycle status with a SYSTEM reason (never
// a causal claim). `now` is injected. It is an error to transition a missing id.
func (s *Store) SetStatus(now time.Time, id string, st Status, reason string) error {
	if !knownStatus(st) {
		return fmt.Errorf("candidate: unknown status %q", st)
	}
	res, err := s.db.Exec(`UPDATE candidates SET status = ?, reason = ?, updated_at = ? WHERE id = ?`,
		string(st), reason, now.UTC().Format(sqlTime), id)
	if err != nil {
		return fmt.Errorf("candidate: set status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("candidate: set status: no candidate %q", id)
	}
	return nil
}

// Decide records a NAMED HUMAN's governance decision on a candidate (doc 12 §3.3:
// approval is a human act — the harness can block but never approve). It transitions the
// status (candidate→promoted | rejected | shadow) and records WHO decided, their AUTHORED
// note, and WHEN. decidedBy is MANDATORY: a decision with no named human is refused. The
// model's rationale stays in the payload (PROPOSED); the human's note is the AUTHORED
// prose that a promotion carries onto its overlay. `now` is injected. Decided candidates
// stay OFF the deterministic path — promotion produces a committable artifact, it does
// not mutate the released graph here (the firewall holds).
func (s *Store) Decide(now time.Time, id string, st Status, decidedBy, note string) error {
	if !knownStatus(st) {
		return fmt.Errorf("candidate: unknown status %q", st)
	}
	if st == StatusCandidate {
		return errors.New("candidate: Decide records a human decision (promoted | rejected | shadow), not a reset to candidate")
	}
	if strings.TrimSpace(decidedBy) == "" {
		return errors.New("candidate: a governance decision requires a named human (decidedBy) — the harness can block but never approve (doc 12 §3.3)")
	}
	at := now.UTC().Format(sqlTime)
	res, err := s.db.Exec(`UPDATE candidates SET status = ?, reason = ?, decided_by = ?, note = ?, decided_at = ?, updated_at = ? WHERE id = ?`,
		string(st), "human decision", decidedBy, note, at, at, id)
	if err != nil {
		return fmt.Errorf("candidate: decide: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("candidate: decide: no candidate %q", id)
	}
	return nil
}

// SetSuggestion attaches a PROJECTED model annotation (agent enrichment, doc 21 Phase 4 §C) to
// an existing candidate WITHOUT changing its content identity, status, evidence, or lifecycle —
// it is a reviewer hint, discarded at promotion. `now` is injected (advances updated_at). A nil
// suggestion clears it. It is an error to annotate a missing id. The suggestion column is NOT in
// contentID, so a later re-Put of the same content preserves it (the deterministic candidate is
// untouched by the model annotation).
func (s *Store) SetSuggestion(now time.Time, id string, sg *Suggestion) error {
	var js any // SQL NULL when sg is nil
	if sg != nil {
		b, err := marshalJSON(*sg)
		if err != nil {
			return fmt.Errorf("candidate: marshal suggestion: %w", err)
		}
		js = b
	}
	res, err := s.db.Exec(`UPDATE candidates SET suggestion_json = ?, updated_at = ? WHERE id = ?`,
		js, now.UTC().Format(sqlTime), id)
	if err != nil {
		return fmt.Errorf("candidate: set suggestion: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("candidate: set suggestion: no candidate %q", id)
	}
	return nil
}

// --- internals ---

type scanner interface {
	Scan(dest ...any) error
}

func scan(r scanner) (*Candidate, error) {
	var (
		c                                              Candidate
		kind, status, relation, payload, evi, lin, rsn string
		decidedBy, note                                sql.NullString
		decidedAt, suggestion                          sql.NullString
		created, updated                               string
	)
	if err := r.Scan(&c.ID, &kind, &status, &c.Subject, &relation, &payload, &evi, &lin, &rsn, &decidedBy, &note, &decidedAt, &suggestion, &created, &updated); err != nil {
		return nil, err
	}
	c.Kind = Kind(kind)
	c.Status = Status(status)
	c.Relation = relation
	c.Reason = rsn
	c.DecidedBy = decidedBy.String
	c.Note = note.String
	if suggestion.Valid && suggestion.String != "" && suggestion.String != "null" {
		var s Suggestion
		if err := unmarshalJSON(suggestion.String, &s); err != nil {
			return nil, fmt.Errorf("candidate: scan suggestion: %w", err)
		}
		c.Suggestion = &s
	}
	if err := unmarshalJSON(payload, &c.Payload); err != nil {
		return nil, fmt.Errorf("candidate: scan payload: %w", err)
	}
	if err := unmarshalJSON(evi, &c.Evidence); err != nil {
		return nil, fmt.Errorf("candidate: scan evidence: %w", err)
	}
	if err := unmarshalJSON(lin, &c.Lineage); err != nil {
		return nil, fmt.Errorf("candidate: scan lineage: %w", err)
	}
	var err error
	if c.CreatedAt, err = time.Parse(sqlTime, created); err != nil {
		return nil, fmt.Errorf("candidate: scan created_at: %w", err)
	}
	if c.UpdatedAt, err = time.Parse(sqlTime, updated); err != nil {
		return nil, fmt.Errorf("candidate: scan updated_at: %w", err)
	}
	if decidedAt.Valid && decidedAt.String != "" {
		if c.DecidedAt, err = time.Parse(sqlTime, decidedAt.String); err != nil {
			return nil, fmt.Errorf("candidate: scan decided_at: %w", err)
		}
	}
	return &c, nil
}

// Validate reports whether a candidate is structurally well-formed — the SAME check
// Put applies: known kind, non-empty subject, a structural edge relation (a causal
// edge is rejected; causation must use KindCausalHypothesis), and a direction-free
// causal hypothesis. Exposed so a producer (e.g. the dgx agent) can reject a malformed
// or causal proposal BEFORE staging.
func Validate(c Candidate) error { return validate(c) }

func validate(c Candidate) error {
	if !knownKind(c.Kind) {
		return fmt.Errorf("candidate: unknown kind %q", c.Kind)
	}
	if c.Status != "" && !knownStatus(c.Status) {
		return fmt.Errorf("candidate: unknown status %q", c.Status)
	}
	if strings.TrimSpace(c.Subject) == "" {
		return errors.New("candidate: empty subject")
	}
	switch c.Kind {
	case KindEdge:
		if !structuralEdgeRelations[c.Relation] {
			return fmt.Errorf("candidate: edge relation %q is not structural (allowed: topology, associated-with, topo-adjacent) — "+
				"a causal proposal must use kind %q (direction-free co-occurrence), never an authoritative edge", c.Relation, KindCausalHypothesis)
		}
	case KindCausalHypothesis:
		switch c.Relation {
		case "", "co-occurrence", "observed-adjacency":
			// direction-free labels only
		default:
			return fmt.Errorf("candidate: causal_hypothesis relation %q must be empty, co-occurrence, or observed-adjacency — "+
				"a hypothesis is co-occurrence, never a cause", c.Relation)
		}
	default:
		if c.Relation != "" {
			return fmt.Errorf("candidate: kind %q must not carry a relation", c.Kind)
		}
	}
	return nil
}

func knownKind(k Kind) bool {
	switch k {
	case KindNode, KindEdge, KindMember, KindBarSource, KindCausalHypothesis, KindEquivGroup, KindPhenomenonCandidate:
		return true
	}
	return false
}

func knownStatus(s Status) bool {
	switch s {
	case StatusCandidate, StatusPromoted, StatusRejected, StatusShadow:
		return true
	}
	return false
}

func sortedEvidence(e []EvidenceRef) []EvidenceRef {
	if len(e) == 0 {
		return e
	}
	out := make([]EvidenceRef, len(e))
	copy(out, e)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Ref != out[j].Ref {
			return out[i].Ref < out[j].Ref
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

// contentID derives a deterministic, dedup-stable id from the content that DEFINES a
// proposal (kind, subject, relation, payload, sorted evidence). Identity, lineage,
// status, reason, and timestamps are deliberately excluded so the same proposal
// always maps to the same row.
func contentID(c Candidate) string {
	key := struct {
		Kind     Kind           `json:"kind"`
		Subject  string         `json:"subject"`
		Relation string         `json:"relation"`
		Payload  map[string]any `json:"payload"`
		Evidence []EvidenceRef  `json:"evidence"`
	}{c.Kind, c.Subject, c.Relation, c.Payload, c.Evidence}
	raw, _ := json.Marshal(key) // map keys are emitted sorted ⇒ canonical
	sum := sha256.Sum256(raw)
	return "cand:" + hex.EncodeToString(sum[:])
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalJSON(s string, v any) error {
	if s == "" || s == "null" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}
