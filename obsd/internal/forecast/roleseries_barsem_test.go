package forecast

import (
	"context"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// --- C1 bar-semantics (doc 22): a role series must be compared against a bar that
// MEANS the same thing as the series. The per-entity bars Vigil authors (e.g. a
// container memory limit) are PER-POD. The churn-stable role question is therefore
// "is ANY member about to cross ITS OWN bar" — the WORST member toward the bar
// (max for an `above` bar), NOT the SUM (which scales with replica count and crosses
// a per-pod bar with two healthy pods). These tests pin that contract.

// memberSeries makes n gauge samples ending at `end`, climbing from `start` by `step`
// at `bin` cadence (a mild real-dynamics ramp, never flat).
func memberSeries(end time.Time, n int, start, step float64, bin time.Duration) []qss.Sample {
	out := make([]qss.Sample, n)
	for i := range out {
		out[i] = qss.Sample{At: end.Add(-time.Duration(n-1-i) * bin), Value: start + step*float64(i)}
	}
	return out
}

// roleReaderOver builds a RoleSeriesReader over the given member pod series for one
// role+metric, using the real package aggregation. `now` is the frozen clock.
func roleReaderOver(now time.Time, roleKey, metric string, members map[string][]qss.Sample, bin time.Duration) (*RoleSeriesReader, Target) {
	streams := map[string][]string{}
	samples := map[string][]qss.Sample{}
	memberUIDs := make([]string, 0, len(members))
	for uid, s := range members {
		sid := "s-" + uid
		streams[uid+"\x1f"+metric] = []string{sid}
		samples[sid] = s
		memberUIDs = append(memberUIDs, uid)
	}
	base := &fakeReader{streams: streams, samples: samples}
	r := &RoleSeriesReader{
		Base: base,
		Members: func(uid string) []string {
			if uid == roleKey {
				return memberUIDs
			}
			return nil
		},
		Bin:    bin,
		Window: time.Duration(4096) * bin,
		Now:    func() time.Time { return now },
	}
	tg := Target{
		CEIKey: roleKey, Entity: "Pod", Metric: metric, StreamUID: roleKey,
		SeriesKind: "gauge", BarValue: 486, BarUnit: "bytes", BarSource: "config",
		Direction: "above", PrecursorPhenomena: []string{"PHEN_OOM"},
	}
	return r, tg
}

// THE BUG, PROVEN: three replicas each at ~210 bytes — far below the per-pod bar of
// 486 — must NOT read as "already crossed". With SUM aggregation the role series is
// ~630 (> 486) and RunCycle silences SilenceAlreadyCrossed, silently killing the
// memory-leak early-warning for exactly the multi-pod workloads role-series exists to
// serve. The correct (worst-member) aggregation keeps the role at ~210 (< 486) and a
// crossing forecast is emitted.
func TestRoleSeries_UnderLimitReplicasNotAlreadyCrossed(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	bin := 15 * time.Second
	metric := "container_memory_working_set_bytes"
	roleKey := "r|cl|shop|Deployment|web"
	// three healthy replicas, each climbing 180->210 (real dynamics, all << 486).
	members := map[string][]qss.Sample{
		"uid-p1": memberSeries(now, 24, 180, 1.2, bin),
		"uid-p2": memberSeries(now, 24, 178, 1.3, bin),
		"uid-p3": memberSeries(now, 24, 182, 1.1, bin),
	}
	r, tg := roleReaderOver(now, roleKey, metric, members, bin)

	// what the runner actually sees as the role's latest level:
	agg := r.LastN(RoleStreamPrefix+roleKey+"\x1f"+metric, maxContextFetch)
	last := agg[len(agg)-1].Value
	t.Logf("role latest level = %.1f (bar = %.0f, per-pod max member ~210)", last, tg.BarValue)

	in := CycleInput{Now: now, Targets: []Target{tg}, Reader: r, Cadence: bin, GraphVersion: "v", P: fp()}
	// a clock that projects a clear future crossing (so the ONLY thing that can stop a
	// candidate is the already-crossed bar-semantics bug).
	cc := &scriptedClock{fc: scriptedForecast(fp().HorizonSteps, 220, 12, 8)}
	res := RunCycle(context.Background(), cc, in)

	for _, s := range res.Silences {
		if s.Reason == SilenceAlreadyCrossed {
			t.Fatalf("BAR-SEMANTICS BUG: 3 replicas at ~210 (bar 486) read as ALREADY-CROSSED "+
				"(role level %.1f) — the role forecast is dead for multi-pod workloads", last)
		}
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 forecast candidate for the climbing role, got %d (silences=%+v)", len(res.Candidates), res.Silences)
	}
}

// A genuine single-pod leak among healthy replicas must still fire: the worst member
// (the leaker) climbs toward its own limit while the others sit low. SUM would bury
// the leaker's signal under the replica count AND mis-bar it; the worst-member series
// tracks the leaker exactly.
func TestRoleSeries_SingleLeakerAmongHealthyTracksWorst(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	bin := 15 * time.Second
	metric := "container_memory_working_set_bytes"
	roleKey := "r|cl|shop|Deployment|web"
	members := map[string][]qss.Sample{
		"uid-leak": memberSeries(now, 24, 300, 6.0, bin), // climbs 300->438, the leaker (still < 486)
		"uid-ok-1": memberSeries(now, 24, 150, 0.2, bin), // healthy, flat-ish low
		"uid-ok-2": memberSeries(now, 24, 155, 0.2, bin),
	}
	r, tg := roleReaderOver(now, roleKey, metric, members, bin)
	agg := r.LastN(RoleStreamPrefix+roleKey+"\x1f"+metric, maxContextFetch)
	last := agg[len(agg)-1].Value
	// worst-member aggregation must equal the LEAKER's latest (~438), not the sum (~748).
	if last > 486 {
		t.Fatalf("worst-member role level %.1f exceeds bar 486 though the leaker is still below it "+
			"(SUM aggregation bug — should track the single worst member)", last)
	}
	in := CycleInput{Now: now, Targets: []Target{tg}, Reader: r, Cadence: bin, GraphVersion: "v", P: fp()}
	cc := &scriptedClock{fc: scriptedForecast(fp().HorizonSteps, last, 8, 6)}
	res := RunCycle(context.Background(), cc, in)
	if len(res.Candidates) != 1 {
		t.Fatalf("a single-pod leak toward the per-pod limit must forecast a crossing, got %d candidates (silences=%+v)", len(res.Candidates), res.Silences)
	}
	t.Logf("worst-member role level = %.1f (leaker tracked, bar 486)", last)
}

// THE RESCUE (the whole point of role-series, doc 20 P5 / 22 C1): under heavy churn each
// pod is short-lived — shorter than MinContext — so the PER-POD forecast is structurally
// impossible (every pod silences short-context and no early-warning is ever produced). The
// ROLE worst-member series is continuous across the OwnerReference handoffs, so it carries
// enough context to forecast. This proves role-series adds capability the per-pod path
// cannot, AND (with the bar-semantics fix) does so against the valid per-pod bar.
func TestRoleSeries_ChurnRescuesShortContextForecast(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	bin := 15 * time.Second
	metric := "container_memory_working_set_bytes"
	roleKey := "r|cl|shop|Deployment|web"
	// four short-lived pods (6 bins each, < MinContext=8), values continuously climbing
	// across the rolling handoffs: a workload churning while its memory creeps toward limit.
	mk := func(firstBin int, base float64) []qss.Sample {
		s := make([]qss.Sample, 6)
		for i := range s {
			s[i] = qss.Sample{At: now.Add(-time.Duration(23-(firstBin+i)) * bin), Value: base + float64(i)*5}
		}
		return s
	}
	members := map[string][]qss.Sample{
		"uid-gen1": mk(0, 300),  // bins 0..5   300..325
		"uid-gen2": mk(6, 330),  // bins 6..11  330..355
		"uid-gen3": mk(12, 360), // bins 12..17 360..385
		"uid-gen4": mk(18, 390), // bins 18..23 390..415  (all < 486)
	}
	r, tg := roleReaderOver(now, roleKey, metric, members, bin)

	// PER-POD baseline: forecast the latest pod alone — only 6 points < MinContext ⇒ dead.
	base := r.Base
	perPod := tg
	perPod.CEIKey, perPod.StreamUID = "i|cl|shop|Pod|web-gen4|uid-gen4", "uid-gen4"
	cc := &scriptedClock{fc: scriptedForecast(fp().HorizonSteps, 415, 10, 8)}
	perRes := RunCycle(context.Background(), cc, CycleInput{
		Now: now, Targets: []Target{perPod}, Reader: base, Cadence: bin, GraphVersion: "v", P: fp(),
	})
	if len(perRes.Candidates) != 0 || len(perRes.Silences) != 1 || perRes.Silences[0].Reason != SilenceShortContext {
		t.Fatalf("per-pod under churn should be short-context-dead, got candidates=%d silences=%+v", len(perRes.Candidates), perRes.Silences)
	}

	// ROLE path: the worst-member series is continuous across all four generations ⇒ enough
	// context to forecast, and it sits below the per-pod bar (so it is not falsely crossed).
	agg := r.LastN(RoleStreamPrefix+roleKey+"\x1f"+metric, maxContextFetch)
	if len(agg) < fp().MinContext {
		t.Fatalf("role series should be continuous across churn (≥MinContext points), got %d", len(agg))
	}
	roleRes := RunCycle(context.Background(), cc, CycleInput{
		Now: now, Targets: []Target{tg}, Reader: r, Cadence: bin, GraphVersion: "v", P: fp(),
	})
	if len(roleRes.Candidates) != 1 {
		t.Fatalf("role-series should RESCUE the forecast across churn, got %d candidates (silences=%+v)", len(roleRes.Candidates), roleRes.Silences)
	}
	t.Logf("rescue: per-pod=%s (dead); role=%d continuous points → 1 candidate", perRes.Silences[0].Reason, len(agg))
}
