package forecast

import (
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
// onto pod B's). We never concatenate two pods' curves. Each bin independently sums the
// members present THEN, so a replaced pod's new UID simply contributes to later bins —
// the role series is continuous across churn because it is defined by live membership,
// not by any single mortal stream. The aggregation is MEASURED arithmetic; the identity
// succession is AUTHORED (the OwnerReference the store reads). Both permitted classes,
// joined, never fused.

// AggregateRoleSeries bins each member stream into [start, end] buckets of width `bin`
// and SUMS the members present in each bucket, yielding one role-level series (oldest
// first). Pure + deterministic: members are processed in sorted-key order and each
// member's bucket value is its latest sample in that bucket (ascending-time wins), so
// the output is byte-identical regardless of member or sample arrival order.
//
// A bucket with no member samples is OMITTED (not zero-filled): a gap in scraping is
// honest absence, not a measured zero. Counter members are summed like gauges here —
// the caller is responsible for only aggregating gauge-class streams (a counter role
// series is deferred upstream exactly as a counter pod series is).
func AggregateRoleSeries(members map[string][]qss.Sample, start, end time.Time, bin time.Duration) []qss.Sample {
	if bin <= 0 || !end.After(start) {
		return nil
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// bucketIdx -> summed value across members present in that bucket.
	sum := map[int]float64{}
	for _, k := range keys {
		binned := binMemberToBuckets(members[k], start, end, bin)
		for idx, v := range binned {
			sum[idx] += v
		}
	}
	if len(sum) == 0 {
		return nil
	}
	idxs := make([]int, 0, len(sum))
	for idx := range sum {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	out := make([]qss.Sample, 0, len(idxs))
	for _, idx := range idxs {
		out = append(out, qss.Sample{
			At:    start.Add(time.Duration(idx) * bin),
			Value: sum[idx],
		})
	}
	return out
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
	roleKey, metric, ok := parseRoleStream(streamID)
	if !ok {
		return r.Base.LastN(streamID, n)
	}
	members := map[string][]qss.Sample{}
	for _, m := range r.Members(roleKey) {
		for _, sid := range r.Base.StreamsFor(m, metric) {
			members[sid] = r.Base.LastN(sid, n)
		}
	}
	end := r.Now().UTC()
	start := end.Add(-r.Window)
	agg := AggregateRoleSeries(members, start, end, r.Bin)
	if n > 0 && len(agg) > n {
		agg = agg[len(agg)-n:]
	}
	return agg
}

// StreamType reports the type of a role stream as the type of its members' streams (all
// gauge by construction — the caller only rolls up gauge-class targets). Delegates for a
// real stream id.
func (r *RoleSeriesReader) StreamType(streamID string) (string, bool) {
	roleKey, metric, ok := parseRoleStream(streamID)
	if !ok {
		return r.Base.StreamType(streamID)
	}
	for _, m := range r.Members(roleKey) {
		for _, sid := range r.Base.StreamsFor(m, metric) {
			if t, ok := r.Base.StreamType(sid); ok {
				return t, true
			}
		}
	}
	return "gauge", true
}

func parseRoleStream(streamID string) (roleKey, metric string, ok bool) {
	if len(streamID) < len(RoleStreamPrefix) || streamID[:len(RoleStreamPrefix)] != RoleStreamPrefix {
		return "", "", false
	}
	rest := streamID[len(RoleStreamPrefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\x1f' {
			return rest[:i], rest[i+1:], true
		}
	}
	return "", "", false
}
