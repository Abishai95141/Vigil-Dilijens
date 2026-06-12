package unexplained

import (
	"sort"
	"strings"
	"time"
)

// Curation feedback loop (doc 08 §3.6). Recurring unexplained patterns are the
// graph's GROWTH SIGNAL: the channel aggregates recurrences (same signal sets,
// on the same entity kind) into candidate-phenomenon reports for human curators
// via governance (doc 12). Humans author; the system NEVER writes to the graph —
// this closes the knowledge loop without crossing the authored/inferred line.
//
// The aggregation is OFF the deterministic digest path (like the findings store):
// it accumulates across runs and uses no wall clock of its own, so it never
// affects replay byte-identity. It proposes; it never decides.

// recurStat accumulates one signature's recurrences across evaluation windows.
type recurStat struct {
	metrics   []string
	kind      string
	windows   int             // total evaluation windows a card of this signature was active
	entities  map[string]bool // distinct entities that exhibited it
	firstSeen time.Time
	lastSeen  time.Time
}

// signatureKey is the recurrence key: the loud-signal SET on a given entity kind
// (doc 08 §3.6 "same signal sets, similar topological arrangements"). v1 keys on
// (kind, sorted metric set); richer topological shape is future work, stated.
func signatureKey(kind string, metrics []string) string {
	return kind + "\x1f" + strings.Join(metrics, ",")
}

// recordRecurrence is called per active card per window (new or aging). It
// accumulates the signature's window count, distinct entities, and time span.
func (t *Tracker) recordRecurrence(c *card) {
	k := signatureKey(c.kind, c.metrics)
	rs := t.recur[k]
	if rs == nil {
		rs = &recurStat{
			metrics: append([]string(nil), c.metrics...), kind: c.kind,
			entities: map[string]bool{}, firstSeen: c.lastSeen,
		}
		t.recur[k] = rs
	}
	rs.windows++
	rs.entities[c.scope] = true
	rs.lastSeen = c.lastSeen
}

// CandidateReport is a candidate-phenomenon proposal for human curation (doc 08
// §3.6 / §3.7). MEASURED aggregation; conspicuously NOT a phenomenon and NOT
// authored — the Rationale carries no causal vocabulary, only the recurrence fact.
type CandidateReport struct {
	Metrics      []string  `json:"metrics"`    // the recurring loud-signal set
	EntityKind   string    `json:"entityKind"` // the kind the pattern recurs on
	Windows      int       `json:"windows"`    // total evaluation windows aggregated
	Entities     []string  `json:"entities"`   // distinct entities exhibiting it
	FirstSeen    time.Time `json:"firstSeen"`  //
	LastSeen     time.Time `json:"lastSeen"`   //
	Rationale    string    `json:"rationale"`  // recurrence fact only — no causal vocabulary
	GraphVersion string    `json:"graphVersion"`
}

// Candidates returns the signatures whose recurrence meets minWindows OR
// minEntities — recurring across TIME or across ENTITIES is each a growth signal
// (doc 08 §3.6). Sorted most-recurrent first. minWindows/minEntities are policy
// thresholds (the §8 open question — set by the caller, harness, or governance),
// not hard-coded here.
func (t *Tracker) Candidates(minWindows, minEntities int) []CandidateReport {
	out := []CandidateReport{} // non-nil: a clean empty JSON array on the surface
	for _, rs := range t.recur {
		if rs.windows < minWindows && len(rs.entities) < minEntities {
			continue
		}
		ents := make([]string, 0, len(rs.entities))
		for e := range rs.entities {
			ents = append(ents, e)
		}
		sort.Strings(ents)
		out = append(out, CandidateReport{
			Metrics: append([]string(nil), rs.metrics...), EntityKind: rs.kind,
			Windows: rs.windows, Entities: ents, FirstSeen: rs.firstSeen, LastSeen: rs.lastSeen,
			Rationale: "recurring unexplained loudness on " + strings.Join(rs.metrics, ", ") +
				" across " + plural(len(ents), "entity", "entities") +
				"; candidate phenomenon for human curation (the system proposes, it never authors)",
			GraphVersion: t.graphVersion,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Windows != out[j].Windows {
			return out[i].Windows > out[j].Windows
		}
		mi, mj := strings.Join(out[i].Metrics, ","), strings.Join(out[j].Metrics, ",")
		if mi != mj {
			return mi < mj
		}
		return out[i].EntityKind < out[j].EntityKind // total order: same signature on two kinds
	})
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
