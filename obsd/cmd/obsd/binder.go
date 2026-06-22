package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// binder runs the discovery-time binding compilation (doc 04). Binding runs on
// RE-BINDING TRIGGERS, never continuously (doc 04 §3.6): when the identified
// inventory changes (topology-driven entity creation/loss), or on a slow periodic
// re-poll that picks up config drift (limits edited in place). It never gates the
// identity path (doc 01 non-gating: detection-side layers run identically whether
// binding is present or absent).
//
// Each compile runs the full doc 04 pass: platform facts -> signal availability
// gating (M1) -> instantiation + per-instance bars (M2/M4) -> semantic QA against
// live stream evidence (M3) -> per-phenomenon observability (M5 / 05 M4).
type binder struct {
	graph    *graph.Graph // nil = binding disabled (stated at startup)
	client   kubernetes.Interface
	logger   *slog.Logger
	ingestor *observe.Ingestor // stream evidence for semantic QA (doc 04 M3) + fingerprints (05)
	fpParams observe.FPParams  // primitive/fingerprint constants from the params file

	// kubeletMetricsScraped mirrors --kubelet-metrics-enabled (docs/31 Step 2b): when
	// true, the obtainability gate treats the kubelet's own /metrics signals (volume_stats)
	// as scrapable, not out-of-scope. Off ⇒ coverage is byte-identical to before.
	kubeletMetricsScraped bool

	last        *binding.Result
	lastAvail   *binding.AvailabilityReport
	lastObs     *binding.ObservabilityReport
	lastFinger  string
	ticksUnseen int

	// dumpPath, when set (--dump-bindings), writes the compiled binding.Result as
	// JSON once after the first successful compile — the governance migration
	// exercise (doc 12 M4) reads two such dumps (one per release) and diffs them
	// with the real governance.BindingDiff. Off the hot path; written once.
	dumpPath string
	dumped   bool

	matcher *detect.Matcher        // the phenomenon matcher (doc 07 M1–M3), built once
	tracker *detect.CascadeTracker // windowed cascade memory (doc 07 M5); reset = process restart
	// eventTracker is a SEPARATE windowed cascade memory for the OFF-DIGEST
	// event-augmented surface (graph-robustness #2 G1): cascades over the union of
	// fingerprint findings + event-driven findings. Kept apart from `tracker` so the
	// digest-bearing cascade recognition (fp findings only) is never perturbed.
	eventTracker *detect.CascadeTracker
	unexp        *unexplained.Tracker // the unexplained channel (doc 08); reset = process restart
}

// routeUnexplained runs the unexplained channel (doc 08): loud-but-unmatched
// routing over this tick's fingerprints and findings, after detection so the
// coverage check sees this tick's matches. The tracker ages/dedups across ticks
// in-process (a process restart empties it, the replay engine resets at the
// run boundary), so a captured tick's unexplained cards reproduce byte-identical.
func (b *binder) routeUnexplained(now time.Time, fps []observe.Fingerprint, findings []detect.Finding) []unexplained.Finding {
	if b == nil || b.graph == nil {
		return nil
	}
	if b.unexp == nil {
		b.unexp = unexplained.NewTracker(b.graph.Version)
	}
	return b.unexp.Route(now, fps, findings)
}

// cascades recognizes the authored relations currently manifest (doc 07 §3.4)
// against the tracker's window, then observes this tick's findings — exactly
// once per evaluation tick, in the same order the replay engine uses, so a
// captured tick's cascades reproduce byte-identically.
func (b *binder) cascades(now time.Time, findings []detect.Finding, topo detect.Topology, w identity.TimeWindow) []detect.Cascade {
	if b == nil || b.matcher == nil {
		return nil
	}
	if b.tracker == nil {
		b.tracker = detect.NewCascadeTracker(b.fpParams.EffectiveCascadeWindow())
	}
	cs := b.matcher.Cascades(now, findings, b.tracker, topo, w)
	b.tracker.Observe(now, findings)
	return cs
}

// augmentedCascades recognizes cascades over the UNION of fingerprint findings and
// event-driven findings (graph-robustness #2 G1) — the OFF-DIGEST surface that lights
// up the flagship cascades live (THROTTLING_CASCADE→PROBE_FAILURE_RESTART,
// MEMORY_LEAK→OOM_KILL_CGROUP). Uses a SEPARATE tracker from cascades() so the
// digest-bearing recognition is untouched; called only when the events lane runs, so
// with --events-enabled off the obsd output is byte-identical (this never executes).
func (b *binder) augmentedCascades(now time.Time, union []detect.Finding, topo detect.Topology, w identity.TimeWindow) []detect.Cascade {
	if b == nil || b.matcher == nil {
		return nil
	}
	if b.eventTracker == nil {
		b.eventTracker = detect.NewCascadeTracker(b.fpParams.EffectiveCascadeWindow())
	}
	cs := b.matcher.Cascades(now, union, b.eventTracker, topo, w)
	b.eventTracker.Observe(now, union)
	return cs
}

// detectFindings runs the matcher (doc 07 M1+M2) over the live fingerprints of
// SELECTED entities (the Tier-A set, doc 06 §3.3 — selection provides the
// watch list to detection): entity-local phenomena everywhere, first-order
// phenomena at their authored anchors walking topo under the validity
// contract. selected is the deterministic selection.TierASet; nil means
// selection is unavailable, in which case detection runs unfiltered (selection
// never gates detection into silence — its absence widens attention, never
// narrows it). topo is the per-tick SNAPSHOT-rebuilt store (the same snapshot
// the capture records — recorded == evaluated by construction); nil skips
// first-order matching, stated.
func (b *binder) detectFindings(fps []observe.Fingerprint, selected map[string][]string, topo detect.Topology, w identity.TimeWindow) []detect.Finding {
	if b == nil || b.graph == nil {
		return nil
	}
	if b.matcher == nil {
		b.matcher = detect.NewMatcher(b.graph)
		// The M6-calibrated degraded-surfacing floor, from the pinned parameter
		// set (doc 07 §3.6) — set once, before the first Match.
		b.matcher.MinCompleteness = b.fpParams.MinCompleteness
	}
	return b.matcher.Match(fps, selected, topo, w)
}

// fingerprints materializes per-entity fingerprints (doc 05 M3) from the latest
// bound graph and the LIVE hot-store samples. Re-run EVERY evaluation tick (the
// bound graph is discovery-time and cached; the fingerprints are per-tick), so a
// crossing appears within a tick of the sample that caused it.
func (b *binder) fingerprints(now time.Time) []observe.Fingerprint {
	if b == nil || b.graph == nil || b.last == nil || b.ingestor == nil {
		return nil
	}
	rules := make(map[string]*graph.ThresholdRule, len(b.graph.Rules))
	for _, r := range b.graph.Rules {
		rules[r.ID] = r
	}
	return observe.Materialize(b.last, rules, b.ingestor, b.fpParams, now)
}

// bound is the composite the renderer consumes. Stale means the result was
// compiled for an EARLIER inventory (this tick's re-bind failed): consumers
// must state it, and selection must not brand binding-unseen entities by kind.
type bound struct {
	Result *binding.Result
	Avail  *binding.AvailabilityReport
	Obs    *binding.ObservabilityReport
	Stale  bool
}

// configDriftRebindTicks forces a re-bind every Nth inventory tick even with an
// unchanged inventory, so an in-place config edit (a re-binding trigger we cannot
// watch yet) is picked up within ~1 minute on the dev profile.
const configDriftRebindTicks = 4

// compile returns the current bound customer graph, re-compiling only on a
// trigger. A snapshot/list failure keeps the previous result and says so — a
// stale-but-stated report, never a silent gap.
func (b *binder) compile(ctx context.Context, inventory []identity.InstanceRecord, now time.Time) *bound {
	if b == nil || b.graph == nil {
		return nil
	}
	finger := inventoryFingerprint(inventory)
	snapshot := func() *bound {
		if b.last == nil {
			return nil
		}
		// Stale iff the kept result was compiled for a DIFFERENT inventory than
		// the one this tick sees (a periodic recompile of an unchanged inventory
		// is not stale).
		return &bound{Result: b.last, Avail: b.lastAvail, Obs: b.lastObs, Stale: finger != b.lastFinger}
	}
	if b.last != nil && finger == b.lastFinger && b.ticksUnseen < configDriftRebindTicks {
		b.ticksUnseen++
		return snapshot()
	}

	snap, err := kube.SnapshotConfig(ctx, b.client)
	if err != nil {
		b.logger.Warn("binding: config snapshot failed; keeping previous bound graph (STALE for this inventory)", "err", err)
		return snapshot()
	}
	facts, err := kube.GatherFacts(ctx, b.client)
	var avail *binding.AvailabilityReport
	if err != nil {
		// Facts unavailable: availability gating skipped FOR THIS COMPILE, stated.
		b.logger.Warn("binding: platform facts unavailable; availability gating skipped this compile", "err", err)
	} else {
		// docs/31 Step 2b: surface the kubelet-/metrics lane state so volume_stats
		// gate obtainable when the lane is on (off ⇒ no change).
		facts.KubeletMetricsScraped = b.kubeletMetricsScraped
		avail = binding.GateSignals(b.graph, facts)
	}

	res := binding.Compile(b.graph, inventory, snap, avail, now)

	// Semantic QA (doc 04 M3) against live stream evidence.
	rulesByID := make(map[string]*graph.ThresholdRule, len(b.graph.Rules))
	for _, r := range b.graph.Rules {
		rulesByID[r.ID] = r
	}
	bounds := binding.RangeBounds{MaxMemoryBytes: 2 * float64(snap.MaxNodeAllocatableMemory())}
	qa := binding.ValidateBindings(res, avail, rulesByID, evidenceAdapter{b.ingestor}, bounds)

	var obs *binding.ObservabilityReport
	if avail != nil {
		obs = binding.PhenomenonObservability(b.graph, avail)
	}

	b.last, b.lastAvail, b.lastObs = res, avail, obs
	b.lastFinger = finger
	b.ticksUnseen = 0
	b.logger.Info("bound customer graph compiled",
		"bindings", len(res.Bindings),
		"roles", len(res.Roles),
		"resolvability", res.Coverage.Resolvability,
		"unbounded", len(res.Coverage.UnboundedWorkloads),
		"default_bars", res.Coverage.DefaultBars,
		"qa_verified", qa.Verified, "qa_suspect", qa.Suspect, "qa_failed", qa.Failed,
	)
	b.maybeDumpBindings(res)
	return snapshot()
}

// maybeDumpBindings writes the compiled binding.Result as JSON once (--dump-bindings),
// for the governance migration exercise (doc 12 M4). It is best-effort and off the
// hot path: a write failure is logged, never fatal.
func (b *binder) maybeDumpBindings(res *binding.Result) {
	if b.dumpPath == "" || b.dumped {
		return
	}
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		b.logger.Warn("dump-bindings: marshal failed", "err", err)
		return
	}
	if err := os.WriteFile(b.dumpPath, raw, 0o644); err != nil {
		b.logger.Warn("dump-bindings: write failed", "path", b.dumpPath, "err", err)
		return
	}
	b.dumped = true
	b.logger.Info("dump-bindings: wrote bound customer graph", "path", b.dumpPath, "bindings", len(res.Bindings))
}

// evidenceAdapter exposes the observation layer to binding QA (the interface is
// owned by binding so the internal package graph stays acyclic).
type evidenceAdapter struct{ in *observe.Ingestor }

var _ binding.StreamEvidence = evidenceAdapter{}

func (e evidenceAdapter) StreamsFor(uid, metric string) []string {
	if e.in == nil {
		return nil
	}
	return e.in.StreamsByUIDMetric(uid, metric)
}

func (e evidenceAdapter) StreamInfo(streamID string) (string, string, bool) {
	if e.in == nil {
		return "", "", false
	}
	m, ok := e.in.Meta(streamID)
	return m.Kind, m.Type, ok
}

func (e evidenceAdapter) History(streamID string, n int) []binding.EvidencePoint {
	if e.in == nil {
		return nil
	}
	samples := e.in.Hot().LastN(streamID, n)
	out := make([]binding.EvidencePoint, len(samples))
	for i, s := range samples {
		out[i] = binding.EvidencePoint{At: s.At, Value: s.Value}
	}
	return out
}

// inventoryFingerprint hashes the sorted active CEI keys: the re-binding trigger
// for topology-driven entity creation/loss.
func inventoryFingerprint(inventory []identity.InstanceRecord) string {
	keys := make([]string, 0, len(inventory))
	for _, r := range inventory {
		keys = append(keys, r.CEI.Key())
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
