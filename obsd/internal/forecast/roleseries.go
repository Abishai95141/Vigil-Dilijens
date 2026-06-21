package forecast

import (
	"math"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// Churn-stable identity (doc 20 P5). A forecast keyed on a single pod UID dies the
// instant the pod is replaced (HPA scale, rollout, OOM-restart): the new pod has a new
// UID, so its stream is a fresh ring and the history is lost — the gap analysis named
// this the top forecasting risk. The fix is OwnerReference SUCCESSION: forecast the
// WORKLOAD ROLE, whose identity is durable (the identity store already derives RoleCEI
// from the OwnerReference chain), and define the role's series as the deterministic
// per-bin AGGREGATION of whichever member pods exist in each bin.
//
// This is NOT shape-stitching (the charter-rejected move of gluing pod A's value curve
// onto pod B's). We never concatenate two pods' curves. Each bin independently reduces
// the members present THEN (worst toward the bar — see combineToBar), so a replaced pod's
// new UID simply contributes to later bins — the role series is continuous across churn
// because it is defined by live membership, not by any single mortal stream. The
// aggregation is MEASURED arithmetic; the identity succession is AUTHORED (the
// OwnerReference the store reads). Both permitted classes, joined, never fused.
//
// WHY NOT A SUM (doc 22 C1): Vigil's bars are PER-ENTITY (e.g. a container memory limit
// is per-pod). A replica SUM scales with the pod count and crosses a per-pod bar with two
// healthy pods, so RunCycle would silence the role "already crossed" and the early-warning
// would be dead for exactly the multi-pod workloads role-series exists to serve. The
// churn-stable question against a per-pod bar is "is the WORST member about to cross its
// OWN bar" — the max member toward an `above` bar, the min toward a `below` bar.

// AggregateRoleSeries bins each member stream into [start, end] buckets of width `bin`
// and reduces the members present in each bucket with `reduce`, yielding one role-level
// series (oldest first). `reduce` is the per-bin combiner — the worst member toward the
// bar (combineToBar). It is called reduce(acc, v) and never with a bucket's first value
// (which seeds acc), so min/max need no sentinel. A nil reduce yields nil (a misuse).
//
// Pure + deterministic: members are processed in sorted-key order and each member's
// bucket value is its latest sample in that bucket (ascending-time wins), so the output
// is byte-identical regardless of member or sample arrival order. A bucket with no member
// samples is OMITTED (not zero-filled): a gap in scraping is honest absence, not a measured
// zero. The caller aggregates only gauge-class streams (a counter role series is deferred
// upstream exactly as a counter pod series is).
func AggregateRoleSeries(members map[string][]qss.Sample, start, end time.Time, bin time.Duration, reduce func(acc, v float64) float64) []qss.Sample {
	if bin <= 0 || !end.After(start) || reduce == nil {
		return nil
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// bucketIdx -> reduced value across members present in that bucket (first value seeds).
	agg := map[int]float64{}
	seen := map[int]bool{}
	for _, k := range keys {
		binned := binMemberToBuckets(members[k], start, end, bin)
		for idx, v := range binned {
			if !seen[idx] {
				agg[idx], seen[idx] = v, true
			} else {
				agg[idx] = reduce(agg[idx], v)
			}
		}
	}
	if len(agg) == 0 {
		return nil
	}
	idxs := make([]int, 0, len(agg))
	for idx := range agg {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	out := make([]qss.Sample, 0, len(idxs))
	for _, idx := range idxs {
		out = append(out, qss.Sample{
			At:    start.Add(time.Duration(idx) * bin),
			Value: agg[idx],
		})
	}
	return out
}

// combineToBar is the per-bin reducer for a bar direction: the member CLOSEST to crossing
// — max toward an `above` bar, min toward a `below` bar. This is what keeps a role series
// comparable to the per-pod bar it is judged against (doc 22 C1). Unknown/empty ⇒ above.
func combineToBar(direction string) func(acc, v float64) float64 {
	if direction == "below" {
		return math.Min
	}
	return math.Max
}

// binMemberToBuckets maps one member's samples to bucketIdx -> latest value in that
// bucket within [start, end]. Sorting by time first makes "latest in bucket" order-
// independent; non-finite samples are dropped.
func binMemberToBuckets(samples []qss.Sample, start, end time.Time, bin time.Duration) map[int]float64 {
	cp := append([]qss.Sample(nil), samples...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].At.Before(cp[j].At) })
	out := map[int]float64{}
	for _, s := range cp {
		if s.At.Before(start) || s.At.After(end) {
			continue
		}
		if isNonFinite(s.Value) {
			continue
		}
		out[int(s.At.Sub(start)/bin)] = s.Value // ascending time ⇒ latest in bucket wins
	}
	return out
}

func isNonFinite(v float64) bool {
	return v != v || v > 1.7e308 || v < -1.7e308
}

// RoleStreamPrefix marks a synthetic role-level stream id so the adapter can tell a
// role series from a real per-instance stream. Contains a byte no real stream id uses.
const RoleStreamPrefix = "role\x1f"

// MemberResolver returns the active member instance UIDs for a role key, in any order
// (the aggregator sorts). main backs it with the identity store (ActiveInstances grouped
// by RoleCEI), so the forecast package imports neither identity nor client-go.
type MemberResolver func(roleKey string) []string

// RoleSeriesReader is a drop-in StreamReader that makes the forecast run churn-stable:
// for a role-keyed target it resolves the role's current members and returns ONE
// synthetic stream whose LastN is the per-bin aggregated role series; for any other uid
// it delegates to the base reader unchanged. OFF the role path it is byte-identical to
// the base, so wrapping is safe.
type RoleSeriesReader struct {
	Base    StreamReader
	Members MemberResolver
	Bin     time.Duration // bucket width (the scrape cadence)
	Window  time.Duration // how far back to aggregate (≈ the hot-ring span)
	Now     func() time.Time
	// Direction returns the bar direction ("above"/"below") for a role-stream's
	// (uid, metric) so LastN reduces members toward that bar (combineToBar). Nil or an
	// empty result ⇒ "above" (max), the dominant case. main backs it from the rolled-up
	// targets so the package imports neither identity nor the graph.
	Direction func(uid, metric string) string
}

// StreamsFor returns a single synthetic role stream when uid is a role key with live
// members; otherwise it delegates. A role with no members or no member streams yields
// no streams (the runner then silences no-stream honestly).
func (r *RoleSeriesReader) StreamsFor(uid, metric string) []string {
	if r.Members == nil {
		return r.Base.StreamsFor(uid, metric)
	}
	members := r.Members(uid)
	if len(members) == 0 {
		return r.Base.StreamsFor(uid, metric)
	}
	any := false
	for _, m := range members {
		if len(r.Base.StreamsFor(m, metric)) > 0 {
			any = true
			break
		}
	}
	if !any {
		return r.Base.StreamsFor(uid, metric)
	}
	return []string{RoleStreamPrefix + uid + "\x1f" + metric}
}

// LastN aggregates the role's member streams when streamID is a synthetic role stream;
// otherwise it delegates. The aggregation is deterministic (AggregateRoleSeries).
func (r *RoleSeriesReader) LastN(streamID string, n int) []qss.Sample {
	uid, metric, ok := parseRoleStream(streamID)
	if !ok {
		return r.Base.LastN(streamID, n)
	}
	members := map[string][]qss.Sample{}
	for _, m := range r.Members(uid) {
		for _, sid := range r.Base.StreamsFor(m, metric) {
			members[sid] = r.Base.LastN(sid, n)
		}
	}
	direction := ""
	if r.Direction != nil {
		direction = r.Direction(uid, metric)
	}
	end := r.Now().UTC()
	start := end.Add(-r.Window)
	agg := AggregateRoleSeries(members, start, end, r.Bin, combineToBar(direction))
	if n > 0 && len(agg) > n {
		agg = agg[len(agg)-n:]
	}
	return agg
}

// StreamType reports the type of a role stream as the type of its members' streams (all
// gauge by construction — the caller only rolls up gauge-class targets). Delegates for a
// real stream id.
func (r *RoleSeriesReader) StreamType(streamID string) (string, bool) {
	uid, metric, ok := parseRoleStream(streamID)
	if !ok {
		return r.Base.StreamType(streamID)
	}
	for _, m := range r.Members(uid) {
		for _, sid := range r.Base.StreamsFor(m, metric) {
			if t, ok := r.Base.StreamType(sid); ok {
				return t, true
			}
		}
	}
	return "gauge", true
}

// parseRoleStream inverts the id StreamsFor builds: RoleStreamPrefix + uid + "\x1f" +
// metric. The uid is itself the target's StreamUID, which for a CONTAINER target is
// "roleKey\x1fcontainer" — so the payload has THREE \x1f-separated parts
// (roleKey, container, metric), not two. The metric never contains \x1f, so split on
// the LAST separator: everything before is the full uid handed back to Members (which
// re-splits roleKey\x1fcontainer), everything after is the metric. Splitting on the
// FIRST separator (the old bug) glued the container onto the metric and dropped it from
// the uid, so Members resolved container-less pod UIDs, no member stream matched, the
// role series came back empty, and every container target was wrongly silenced
// short-context (doc 20 P5). A pod target (uid == roleKey, no inner \x1f) is unaffected.
func parseRoleStream(streamID string) (uid, metric string, ok bool) {
	if len(streamID) < len(RoleStreamPrefix) || streamID[:len(RoleStreamPrefix)] != RoleStreamPrefix {
		return "", "", false
	}
	rest := streamID[len(RoleStreamPrefix):]
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == '\x1f' {
			return rest[:i], rest[i+1:], true
		}
	}
	return "", "", false
}
