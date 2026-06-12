package unexplained

import (
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// Routing, aging, and dedup (doc 08 §3.3/§3.4). An entity that is loud, and that
// participates in NO full or degraded phenomenon match covering the loud states,
// routes to the unexplained surface as "anomalous — investigate" — MEASURED, no
// causal vocabulary, the not-yet-explained mark mandatory. Persistent loudness
// collapses into one aging card; a covering match supersedes it (the unexplained
// became explained, by authored knowledge, not inference); resolution retires it.

// Mark is the constant not-yet-explained mark every card carries (doc 08 §3.7).
const Mark = "anomalous — investigate · not-yet-explained"

// BlindSpotNotice states the channel's own residual coverage limit (doc 08 §3.2),
// surfaced reflexively — never implied away. A signal carrying neither a resolved
// bar nor a rate guard CANNOT be loud, so a novel failure expressing itself only
// through un-thresholded, un-guarded signals is invisible even here.
const BlindSpotNotice = "blind spot (stated): loudness requires a resolved bar or an authored rate guard, so signals carrying neither can never be loud — this channel covers known signals exhibiting unknown patterns, not unknown signals (doc 08 §3.2)"

// Status is one unexplained card's lifecycle state (doc 08 §3.7).
type Status string

const (
	StatusNew        Status = "new"
	StatusAging      Status = "aging"
	StatusSuperseded Status = "superseded-by-match" // a covering phenomenon match now explains it
	StatusResolved   Status = "resolved"            // the loud states are no longer loud
)

// Finding is the unexplained-anomaly finding (doc 08 §3.7): MEASURED, conspicuously
// NOT authored — the ABSENCE of a reason is the defining property, kept visible.
type Finding struct {
	Scope        string      `json:"scope"` // the loud entity CEI
	Namespace    string      `json:"namespace"`
	Name         string      `json:"name"`
	Kind         string      `json:"kind"`
	LoudStates   []LoudState `json:"loudStates"`   // the UNCOVERED loud states (the unexplained scope)
	MatchCheck   string      `json:"matchCheck"`   // why no match covered these — descriptive, no causal vocabulary
	Status       Status      `json:"status"`       //
	Mark         string      `json:"mark"`         // the constant not-yet-explained mark
	FirstSeen    time.Time   `json:"firstSeen"`    //
	LastSeen     time.Time   `json:"lastSeen"`     //
	Occurrences  int         `json:"occurrences"`  // evaluation windows the card has persisted
	SupersededBy string      `json:"supersededBy"` // phenomenon id of the covering match (Superseded only)
	GraphVersion string      `json:"graphVersion"`
}

// card is the tracker's mutable record per (entity, uncovered-metric-set).
type card struct {
	scope     string
	namespace string
	name      string
	kind      string
	metrics   []string // sorted uncovered loud metrics (the dedup key component)
	states    []LoudState
	firstSeen time.Time
	lastSeen  time.Time
	occurs    int
}

func cardKey(scope string, metrics []string) string {
	return scope + "\x1f" + strings.Join(metrics, ",")
}

func (c *card) finding(status Status, gv, supersededBy, matchCheck string) Finding {
	return Finding{
		Scope: c.scope, Namespace: c.namespace, Name: c.name, Kind: c.kind,
		LoudStates: append([]LoudState(nil), c.states...),
		MatchCheck: matchCheck, Status: status, Mark: Mark,
		FirstSeen: c.firstSeen, LastSeen: c.lastSeen, Occurrences: c.occurs,
		SupersededBy: supersededBy, GraphVersion: gv,
	}
}

// Tracker holds the open unexplained cards across evaluation windows and the
// long-run recurrence aggregation (doc 08 §3.4/§3.6). Live it persists in-process;
// in replay the engine resets it at every run-start frame (the process restart
// that emptied it), so routing replays byte-identically. The recurrence map (M4)
// is a curation surface OFF the deterministic digest path, so it accumulates
// freely — it is never compared for replay byte-identity. Not safe for concurrent
// use (one evaluation goroutine).
type Tracker struct {
	graphVersion string
	open         map[string]*card
	recur        map[string]*recurStat // M4 — see candidate.go
}

// NewTracker builds a tracker pinned to a graph version (every card cites it).
func NewTracker(graphVersion string) *Tracker {
	return &Tracker{graphVersion: graphVersion, open: map[string]*card{}, recur: map[string]*recurStat{}}
}

// Reset empties the open cards (a process-run boundary). The recurrence
// aggregation is intentionally NOT reset — it is the long-run growth signal for
// curation, off the digest path.
func (t *Tracker) Reset() { t.open = map[string]*card{} }

// Route is one evaluation window's pass (doc 08 §3.3): compute the loud set over
// fingerprints, subtract the states any full/degraded phenomenon match covered,
// and reconcile what remains against the open cards. Returns the cards touched
// this window (new + aging continuing, and the superseded/resolved that closed),
// canonically sorted — deterministic given (now, fps, findings, open state).
func (t *Tracker) Route(now time.Time, fps []observe.Fingerprint, findings []detect.Finding) []Finding {
	covered := coveredMetrics(findings)

	// Per loud entity: the full loud set (pre-coverage) and the UNCOVERED subset.
	loudAll := map[string]map[string]bool{} // entity -> metric -> loud this window
	currentByKey := map[string]*card{}      // key -> the would-be card (uncovered loud)
	for _, fp := range fps {
		states := Loud(fp)
		if len(states) == 0 {
			continue
		}
		set := map[string]bool{}
		var uncovered []LoudState
		for _, s := range states {
			set[s.Metric] = true
			if !covered[fp.CEIKey][s.Metric] {
				uncovered = append(uncovered, s)
			}
		}
		loudAll[fp.CEIKey] = set
		if len(uncovered) == 0 {
			continue // every loud state is explained by an authored match — nothing unexplained
		}
		metrics := metricsOf(uncovered)
		currentByKey[cardKey(fp.CEIKey, metrics)] = &card{
			scope: fp.CEIKey, namespace: fp.Namespace, name: fp.Name, kind: fp.Kind,
			metrics: metrics, states: uncovered,
		}
	}

	var out []Finding
	handled := map[string]bool{}

	// Reconcile existing open cards first (so aging keeps the original FirstSeen).
	openKeys := make([]string, 0, len(t.open))
	for k := range t.open {
		openKeys = append(openKeys, k)
	}
	sort.Strings(openKeys)
	for _, k := range openKeys {
		c := t.open[k]
		if cur, still := currentByKey[k]; still {
			// Still loud AND still uncovered: one aging card, not a stream of repeats.
			c.states = cur.states
			c.lastSeen = now
			c.occurs++
			out = append(out, c.finding(StatusAging, t.graphVersion, "", t.matchCheck(c, false, "")))
			t.recordRecurrence(c)
			handled[k] = true
			continue
		}
		// The card's key left the uncovered-loud set. Distinguish supersede vs resolve:
		// is the entity STILL loud on all the card's metrics, but now covered?
		stillLoud := true
		for _, m := range c.metrics {
			if !loudAll[c.scope][m] {
				stillLoud = false
				break
			}
		}
		c.lastSeen = now
		if stillLoud {
			by := coveringPhenomenon(findings, c.scope, c.metrics)
			out = append(out, c.finding(StatusSuperseded, t.graphVersion, by, t.matchCheck(c, true, by)))
		} else {
			out = append(out, c.finding(StatusResolved, t.graphVersion, "", "loud states resolved (no longer at/over their bar or guard)"))
		}
		delete(t.open, k)
	}

	// New cards: uncovered-loud keys with no open card.
	newKeys := make([]string, 0, len(currentByKey))
	for k := range currentByKey {
		if !handled[k] {
			newKeys = append(newKeys, k)
		}
	}
	sort.Strings(newKeys)
	for _, k := range newKeys {
		c := currentByKey[k]
		c.firstSeen, c.lastSeen, c.occurs = now, now, 1
		t.open[k] = c
		out = append(out, c.finding(StatusNew, t.graphVersion, "", t.matchCheck(c, false, "")))
		t.recordRecurrence(c)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return statusRank(out[i].Status) < statusRank(out[j].Status)
	})
	return out
}

// OpenCards returns the current open (new/aging) cards as a snapshot for the
// surface (doc 10), canonically ordered. It does not mutate the tracker.
func (t *Tracker) OpenCards() []Finding {
	out := make([]Finding, 0, len(t.open))
	for _, c := range t.open {
		status := StatusAging
		if c.occurs <= 1 {
			status = StatusNew
		}
		out = append(out, c.finding(status, t.graphVersion, "", t.matchCheck(c, false, "")))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out
}

// matchCheck renders the descriptive "why no match covered these states" line
// (doc 08 §3.7). DELIBERATELY free of causal vocabulary — the channel describes,
// it never explains (the charter audit test enforces this).
func (t *Tracker) matchCheck(c *card, superseded bool, by string) string {
	ms := strings.Join(c.metrics, ", ")
	if superseded {
		return "loud on " + ms + "; now covered by phenomenon match " + by + " (authored knowledge, not inference)"
	}
	return "loud on " + ms + "; no full or degraded phenomenon match covered these states this window"
}

func statusRank(s Status) int {
	switch s {
	case StatusNew:
		return 0
	case StatusAging:
		return 1
	case StatusSuperseded:
		return 2
	case StatusResolved:
		return 3
	}
	return 4
}

// coveredMetrics returns, per entity CEI, the metrics a full/degraded phenomenon
// match covered this window — anchor members on that entity, PLUS neighbour
// members that reached it across a span (so a node's loud PSI is "covered" by a
// container's first-order cascade that consumed it). Finding.Members holds only
// MET evidence by construction, so membership is coverage.
func coveredMetrics(findings []detect.Finding) map[string]map[string]bool {
	covered := map[string]map[string]bool{}
	add := func(entity, metric string) {
		if covered[entity] == nil {
			covered[entity] = map[string]bool{}
		}
		covered[entity][metric] = true
	}
	for _, f := range findings {
		for _, ev := range f.Members {
			if ev.Neighbour != "" {
				add(ev.Neighbour, ev.Metric)
			} else {
				add(f.EntityCEI, ev.Metric)
			}
		}
	}
	return covered
}

// coveringPhenomenon returns the id of a finding whose met members cover ALL the
// card's metrics on the scope entity (anchor or neighbour) — the authored match
// that superseded the card. Canonical (smallest id) when several qualify.
func coveringPhenomenon(findings []detect.Finding, scope string, metrics []string) string {
	best := ""
	for _, f := range findings {
		got := map[string]bool{}
		for _, ev := range f.Members {
			if (ev.Neighbour == "" && f.EntityCEI == scope) || ev.Neighbour == scope {
				got[ev.Metric] = true
			}
		}
		all := true
		for _, m := range metrics {
			if !got[m] {
				all = false
				break
			}
		}
		if all && (best == "" || f.Phenomenon < best) {
			best = f.Phenomenon
		}
	}
	return best
}
