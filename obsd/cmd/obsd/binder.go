package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
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

	last        *binding.Result
	lastAvail   *binding.AvailabilityReport
	lastObs     *binding.ObservabilityReport
	lastFinger  string
	ticksUnseen int

	matcher *detect.Matcher // entity-local phenomenon matcher (doc 07 M1), built once
}

// detectFindings runs the entity-local matcher (doc 07 M1) over the live
// fingerprints of SELECTED entities (the Tier-A set, doc 06 §3.3 — selection
// provides the watch list to detection). selected is the deterministic
// selection.TierASet; nil means selection is unavailable, in which case
// detection runs unfiltered (selection never gates detection into silence —
// its absence widens attention, never narrows it). Stateless; the matcher is
// built once from the (static) graph.
func (b *binder) detectFindings(fps []observe.Fingerprint, selected map[string][]string) []detect.Finding {
	if b == nil || b.graph == nil {
		return nil
	}
	if b.matcher == nil {
		b.matcher = detect.NewMatcher(b.graph)
	}
	var out []detect.Finding
	for _, fp := range fps {
		if selected != nil && len(selected[fp.CEIKey]) == 0 {
			continue
		}
		out = append(out, b.matcher.MatchFingerprint(fp)...)
	}
	return out
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

// bound is the composite the renderer consumes.
type bound struct {
	Result *binding.Result
	Avail  *binding.AvailabilityReport
	Obs    *binding.ObservabilityReport
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
	snapshot := func() *bound {
		if b.last == nil {
			return nil
		}
		return &bound{Result: b.last, Avail: b.lastAvail, Obs: b.lastObs}
	}
	finger := inventoryFingerprint(inventory)
	if b.last != nil && finger == b.lastFinger && b.ticksUnseen < configDriftRebindTicks {
		b.ticksUnseen++
		return snapshot()
	}

	snap, err := kube.SnapshotConfig(ctx, b.client)
	if err != nil {
		b.logger.Warn("binding: config snapshot failed; keeping previous bound graph", "err", err)
		return snapshot()
	}
	facts, err := kube.GatherFacts(ctx, b.client)
	var avail *binding.AvailabilityReport
	if err != nil {
		// Facts unavailable: availability gating skipped FOR THIS COMPILE, stated.
		b.logger.Warn("binding: platform facts unavailable; availability gating skipped this compile", "err", err)
	} else {
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
	return snapshot()
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
