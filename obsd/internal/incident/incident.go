// Package incident is the cross-run phenomenon memory (v3 T-B / doc 03 + 14 §1.4):
// it joins a PHENOMENON across time the way the identity layer joins an ENTITY across
// restarts. A finding that fires, resolves, and re-fires days later is ONE incident
// with a recurrence count — not two findings and not one continuous span (the two
// failure modes the durable findings table cannot distinguish).
//
// CHARTER (the T-B guardrail): the incident identity is a DETERMINISTIC GROUPING, not a
// learned clustering. The key is a pure sha256 of three deterministic strings; the
// recurrence count is MEASURED arithmetic. There is NO ML similarity, NO learned
// threshold, NO "these are probably the same". Recurrence is a deterministic
// consequence of MEASURED findings, so it is MEASURED (doc 01). This package contains
// no time.Now (every instant is injected) and is byte-deterministic: the same finding
// sequence yields the same incidents regardless of process-restart boundaries.
package incident

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// keySep is the field separator inside an incident key. It is a control byte that
// cannot occur in a phenomenon id or a CEI key, so the three fields can never collide
// (e.g. a role key ending where a phenomenon id begins).
const keySep = "\x1f"

// Key is the deterministic incident identity: sha256(phenomenon | role-CEI | bucket).
//
//   - phenomenonID — the stable authored id (e.g. PHEN_MEMORY_LEAK).
//   - roleCEIKey   — the DURABLE role identity (doc 03; survives restart/reschedule).
//     When a finding's role is unresolvable, the caller passes the instance key with a
//     stated unresolved flag — never a guessed role.
//   - bucketStart  — the window-bucket boundary (see Bucket). Keeping the bucket (not a
//     raw timestamp) in the key is what lets two episodes in the same week share one
//     incident.
//
// GRAPH VERSION IS DELIBERATELY ABSENT from the key: a phenomenon-definition bump
// (governance, doc 12) must NOT split a recurring incident into two. The graph version
// is carried as an Incident attribute, never a key field.
func Key(phenomenonID, roleCEIKey string, bucketStart time.Time) string {
	h := sha256.New()
	h.Write([]byte(phenomenonID))
	h.Write([]byte(keySep))
	h.Write([]byte(roleCEIKey))
	h.Write([]byte(keySep))
	// RFC3339 in UTC is a stable, collision-free rendering of the bucket boundary.
	h.Write([]byte(bucketStart.UTC().Format(time.RFC3339)))
	return hex.EncodeToString(h.Sum(nil))
}

// Bucket floors an instant to the start of its window bucket (UTC), the week-scale
// baseline boundary. size must be > 0. Deterministic: a pure function of (t, size),
// computed against the Unix epoch so bucket boundaries are stable across processes and
// independent of wall-clock "now".
func Bucket(t time.Time, size time.Duration) time.Time {
	if size <= 0 {
		return t.UTC()
	}
	u := t.UTC()
	// Floor the absolute nanoseconds-since-epoch to a multiple of size.
	n := u.UnixNano()
	floored := n - mod(n, int64(size))
	return time.Unix(0, floored).UTC()
}

// mod is a floored modulo (handles negative instants — pre-epoch — without skewing the
// bucket boundary), keeping bucketing deterministic for any instant.
func mod(a, m int64) int64 {
	r := a % m
	if r < 0 {
		r += m
	}
	return r
}

// Incident is one durable cross-run phenomenon record. All fields are MEASURED-derived
// (counts, timestamps) or an AUTHORED phenomenon label surfaced verbatim — never a
// cause, never a projection.
type Incident struct {
	Key             string        // the deterministic identity
	Phenomenon      string        // authored phenomenon id (verbatim)
	RoleCEI         string        // the role this incident is keyed on
	RoleUnresolved  bool          // true when keyed on an instance because the role was unresolvable (stated, not guessed)
	WindowBucket    time.Time     // the bucket boundary in the key
	FirstSeen       time.Time     // earliest observation in this incident
	LastSeen        time.Time     // latest observation
	RecurrenceCount int           // distinct fire-episodes (increments ONLY across a resolve gap)
	Lifespan        time.Duration // LastSeen - FirstSeen
	GraphVersion    string        // attribute, NOT part of the key (a graph bump must not split an incident)
}

// Accumulator folds a stream of finding observations into incidents. The SAME logic
// runs live (incremental Observe per finding, off the deterministic tick) and in
// replay (folding a frozen finding stream) — so the live memory and the gate verdict
// can never diverge. Restart-invariant: Restore() rehydrates from the durable store,
// after which Observe continues the exact sequence it would have had without a restart.
type Accumulator struct {
	resolveHorizon time.Duration // a gap longer than this between observations = a new recurrence
	bucketSize     time.Duration // the window-bucket size (week-scale baseline)
	incidents      map[string]*Incident
}

// NewAccumulator constructs a folder. resolveHorizon MUST exceed the evaluation tick
// (so consecutive ticks of one continuous condition do not inflate the count) and is a
// declared parameter, never a magic constant. bucketSize is the baseline window.
func NewAccumulator(resolveHorizon, bucketSize time.Duration) *Accumulator {
	return &Accumulator{
		resolveHorizon: resolveHorizon,
		bucketSize:     bucketSize,
		incidents:      map[string]*Incident{},
	}
}

// Observe folds one finding observation (phenomenon on a role, at an instant) into its
// incident and returns the incident key. roleUnresolved records the honest-partial case
// (the role could not be resolved; the caller passed the instance key). Deterministic.
func (a *Accumulator) Observe(phenomenonID, roleCEIKey string, roleUnresolved bool, graphVersion string, at time.Time) string {
	bucket := Bucket(at, a.bucketSize)
	key := Key(phenomenonID, roleCEIKey, bucket)
	inc, ok := a.incidents[key]
	if !ok {
		a.incidents[key] = &Incident{
			Key: key, Phenomenon: phenomenonID, RoleCEI: roleCEIKey, RoleUnresolved: roleUnresolved,
			WindowBucket: bucket, FirstSeen: at.UTC(), LastSeen: at.UTC(), RecurrenceCount: 1,
			GraphVersion: graphVersion,
		}
		return key
	}
	// A gap longer than the resolve horizon means the condition resolved and re-fired:
	// a distinct episode. A sub-horizon gap is the same continuous episode (no increment).
	if at.UTC().Sub(inc.LastSeen) > a.resolveHorizon {
		inc.RecurrenceCount++
	}
	if at.UTC().After(inc.LastSeen) {
		inc.LastSeen = at.UTC()
	}
	if at.UTC().Before(inc.FirstSeen) {
		inc.FirstSeen = at.UTC()
	}
	inc.Lifespan = inc.LastSeen.Sub(inc.FirstSeen)
	// The latest graph version that observed the incident is recorded (attribute only).
	if graphVersion != "" {
		inc.GraphVersion = graphVersion
	}
	return key
}

// Restore rehydrates the accumulator from durable incidents (a process restart). After
// Restore, Observe continues the sequence identically to a no-restart run — the
// restart-invariance the gate asserts. The incidents are copied, not aliased.
func (a *Accumulator) Restore(incidents []Incident) {
	for i := range incidents {
		c := incidents[i]
		a.incidents[c.Key] = &c
	}
}

// Snapshot returns the current incidents in deterministic key order (a stable,
// serialisable view). Off the deterministic detection path — surfacing only.
func (a *Accumulator) Snapshot() []Incident {
	keys := make([]string, 0, len(a.incidents))
	for k := range a.incidents {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make([]Incident, 0, len(keys))
	for _, k := range keys {
		out = append(out, *a.incidents[k])
	}
	return out
}

// sortStrings is a tiny dependency-free sort (avoids importing sort for one slice).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
