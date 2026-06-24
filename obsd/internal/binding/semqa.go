package binding

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// Semantic equivalence validation — binding QA (doc 04 §3.2, M3). Name match is
// necessary, not sufficient: a false equivalence silently corrupts every
// downstream check for that customer. Each bound pair is checked against REAL
// observed evidence before it is trusted:
//
//   - character check  — cumulative vs instantaneous: the exposition TYPE and the
//     observed shape must match the variable's expected character (a counter
//     masquerading as a gauge fails here).
//   - scope check      — the stream's entity granularity must match the rule's
//     declared entity scope (container vs pod vs node).
//   - range sanity     — observed values must be physically plausible for the
//     quantity (bytes within node capacity, ratios near [0,1], counters
//     non-negative).
//   - platform-variant check — an ACTIVE distro gate on the signal is a
//     semantics shift; the binding stays suspect until the gated interpretation
//     is encoded.
//
// Verdicts: verified (all checks passed against evidence) / suspect (no or thin
// evidence, active gate, indeterminate) / failed (hard contradiction). Failed
// bindings keep their record — the failure IS the finding.

// StreamEvidence is the read-side view of the observation layer the QA consumes.
// (The adapter over observe.Ingestor lives in cmd; tests use fixtures.)
type StreamEvidence interface {
	// StreamsFor returns stream ids for (CEI UID, metric). Container streams are
	// keyed by podUID/container — exactly what container bindings know.
	StreamsFor(uid, metric string) []string
	// StreamInfo returns the stream's CEI kind and exposition type.
	StreamInfo(streamID string) (kind, expoType string, ok bool)
	// History returns up to n most recent samples, oldest first.
	History(streamID string, n int) []EvidencePoint
}

// EvidencePoint is one observed sample.
type EvidencePoint struct {
	At    time.Time
	Value float64
}

// RangeBounds are the plausibility envelopes for range sanity, derived from the
// cluster's own declared capacity (never learned).
type RangeBounds struct {
	MaxMemoryBytes float64 // ~2x the largest node allocatable; 0 => 1 TiB fallback
	MaxRatio       float64 // 0 => 1.5
}

// QASummary is the validation rollup for the coverage report.
type QASummary struct {
	Verified int
	Suspect  int
	Failed   int
	// Findings are the hard failures and notable suspects, human-readable.
	Findings []string
}

const evidenceWindow = 8 // samples consulted per check

// ValidateBindings runs semantic QA over every BOUND pair in the result,
// upgrading suspect→verified where evidence supports the binding and demoting to
// failed where it contradicts it. Mutates Validation/Reason in place and returns
// the rollup. Deterministic: bindings are already sorted, evidence reads are
// pure, `now` is unused on purpose (recency policy belongs to the primitives,
// not QA).
func ValidateBindings(res *Result, avail *AvailabilityReport, rules map[string]*graph.ThresholdRule, ev StreamEvidence, bounds RangeBounds) QASummary {
	if bounds.MaxMemoryBytes == 0 {
		bounds.MaxMemoryBytes = 1 << 40 // 1 TiB: generous, still catches absurdities
	}
	if bounds.MaxRatio == 0 {
		bounds.MaxRatio = 1.5
	}
	var sum QASummary
	for i := range res.Bindings {
		b := &res.Bindings[i]
		if b.State != StateBound {
			continue
		}
		verdict, reason := validateOne(b, avail, rules[b.RuleID], ev, bounds)
		b.Validation = verdict
		if reason != "" {
			b.Reason = strings.TrimSpace(strings.Join([]string{b.Reason, reason}, " "))
		}
		switch verdict {
		case ValidationVerified:
			sum.Verified++
		case ValidationSuspect:
			sum.Suspect++
		case ValidationFailed:
			sum.Failed++
			sum.Findings = append(sum.Findings, fmt.Sprintf("FAILED %s %s: %s", b.RuleID, evidenceTag(b), reason))
		}
	}
	sort.Strings(sum.Findings)
	res.Coverage.Validation = sum
	return sum
}

func validateOne(b *Binding, avail *AvailabilityReport, rule *graph.ThresholdRule, ev StreamEvidence, bounds RangeBounds) (Validation, string) {
	// Platform-variant check: an active gate on the rule's signal is a semantics
	// shift this engine has not encoded per-gate yet — suspect, stated.
	var gateNote string
	if avail != nil && rule != nil {
		if av, ok := avail.PerSignal[rule.Signal]; ok && len(av.Gates) > 0 {
			gateNote = "active distro gate(s) " + strings.Join(av.Gates, ",") + " shift semantics (interpretation not yet encoded)"
		}
	}

	uid := evidenceUID(b)
	if uid == "" || ev == nil {
		return ValidationSuspect, "no evidence channel for this entity kind yet"
	}
	streams := ev.StreamsFor(uid, b.Metric)
	if len(streams) == 0 {
		return ValidationSuspect, "no stream evidence yet (not scraped or not emitted)"
	}
	if len(streams) > 1 {
		// One (entity, variable) must be one stream; duplicates mean an identity
		// or naming ambiguity — exactly what QA exists to catch.
		return ValidationFailed, fmt.Sprintf("ambiguous evidence: %d streams for one (entity, variable)", len(streams))
	}
	sid := streams[0]
	kind, expoType, ok := ev.StreamInfo(sid)
	if !ok {
		return ValidationSuspect, "stream meta unavailable"
	}

	// Scope check (doc 04 §3.2): stream granularity vs the rule's entity scope.
	if expected := expectedKind(b.Entity); expected != "" && kind != expected {
		return ValidationFailed, fmt.Sprintf("scope mismatch: rule scope %s but stream is %s-scoped", b.Entity, kind)
	}

	// Character check: expected character from the metric's naming convention
	// (Prometheus: *_total = cumulative counter) vs the exposition TYPE...
	expectCounter := strings.HasSuffix(b.Metric, "_total")
	if expectCounter && expoType == "gauge" {
		return ValidationFailed, "character mismatch: cumulative variable exposed as gauge"
	}
	if !expectCounter && expoType == "counter" {
		return ValidationFailed, "character mismatch: instantaneous variable exposed as counter"
	}

	// ...and against the OBSERVED shape: a declared counter that keeps decreasing
	// is not cumulative (one decrease is a legitimate reset; repeated decreases
	// inside a short window are not).
	hist := ev.History(sid, evidenceWindow)
	if expoType == "counter" || expectCounter {
		decreases := 0
		for i := 1; i < len(hist); i++ {
			if hist[i].Value < hist[i-1].Value {
				decreases++
			}
		}
		if decreases > 1 {
			return ValidationFailed, fmt.Sprintf("character mismatch: declared counter decreased %d times in the last %d samples", decreases, len(hist))
		}
	}

	// Range sanity over the observed window. The checked quantity must be the
	// EVALUATED one: a rule with a divisor metric evaluates a DERIVED ratio, so
	// the raw numerator stream is range-checked only for sign, and the ratio
	// bound applies to numerator/divisor — never to the raw counter (that exact
	// confusion is a false-equivalence shape of its own). The same discipline
	// holds for a divisor-LESS counter under a ratio bar (e.g. a PSI stall-
	// seconds counter whose evaluated quantity is its RATE, a fraction of wall
	// time): the cumulative level is meaningless against the ratio bound, so the
	// bound applies to the observed per-second rate instead (live evidence:
	// node_pressure_cpu_waiting_seconds_total at ~1008s was range-FAILED against
	// 1.5 before this distinction).
	derived := rule != nil && rule.DivisorMetric != ""
	rateEvaluated := !derived && expoType == "counter"
	for _, p := range hist {
		if p.Value < 0 {
			return ValidationFailed, fmt.Sprintf("range: negative value %v", p.Value)
		}
		if derived {
			continue
		}
		if b.Bar != nil && b.Bar.Unit == "bytes" && p.Value > bounds.MaxMemoryBytes {
			return ValidationFailed, fmt.Sprintf("range: %v bytes exceeds plausible capacity %v", p.Value, bounds.MaxMemoryBytes)
		}
		if b.Bar != nil && b.Bar.Unit == "ratio" && !rateEvaluated && p.Value > bounds.MaxRatio {
			return ValidationFailed, fmt.Sprintf("range: ratio %v exceeds %v", p.Value, bounds.MaxRatio)
		}
	}
	if rateEvaluated && b.Bar != nil && b.Bar.Unit == "ratio" && len(hist) >= 2 {
		// The evaluated quantity is the rate: range-check Δvalue/Δt across the
		// window (reset-tolerant: negative deltas are the counter-shape check's
		// concern, skipped here).
		first, last := hist[0], hist[len(hist)-1]
		if dt := last.At.Sub(first.At).Seconds(); dt > 0 && last.Value >= first.Value {
			if rate := (last.Value - first.Value) / dt; rate > bounds.MaxRatio {
				return ValidationFailed, fmt.Sprintf("range: rate %.3f/s exceeds plausible ratio %v (a stall fraction cannot beat wall time)", rate, bounds.MaxRatio)
			}
		}
	}
	if derived {
		// Validate the derived ratio against its bound using both streams' latest
		// cumulative values (a coarse lifetime ratio — windowed rates belong to
		// the rate primitive, not QA).
		divStreams := ev.StreamsFor(uid, rule.DivisorMetric)
		if len(divStreams) != 1 {
			return ValidationSuspect, fmt.Sprintf("derived quantity: divisor stream %s not uniquely present (%d found)", rule.DivisorMetric, len(divStreams))
		}
		divHist := ev.History(divStreams[0], evidenceWindow)
		if len(hist) > 0 && len(divHist) > 0 {
			num, den := hist[len(hist)-1].Value, divHist[len(divHist)-1].Value
			if den < 0 || num < 0 {
				return ValidationFailed, "derived quantity: negative component"
			}
			if den > 0 {
				if ratio := num / den; ratio > bounds.MaxRatio {
					return ValidationFailed, fmt.Sprintf("derived ratio %.3f exceeds plausible %v (false equivalence or unit error)", ratio, bounds.MaxRatio)
				}
			}
		}
	}
	if len(hist) < 2 {
		return ValidationSuspect, "thin evidence (single sample); checks passed so far"
	}

	if gateNote != "" {
		return ValidationSuspect, gateNote
	}
	return ValidationVerified, ""
}

// evidenceUID derives the stream-side CEI UID a binding's evidence carries.
func evidenceUID(b *Binding) string { return b.StreamUID() }

func expectedKind(entityScope string) string {
	switch entityScope {
	case "Container":
		return "Container"
	case "Pod":
		return "Pod"
	case "Node":
		return "Node"
	case "PVC":
		return "PersistentVolumeClaim"
	case "PDB":
		return "PodDisruptionBudget"
	case "Workload":
		// v1 binds the StatefulSet archetype (docs/33 closure 1, v0.18.0); the Deployment
		// twin is a noted follow-up. A Workload binding must land on a StatefulSet instance
		// stream, never a role pseudo-key.
		return "StatefulSet"
	default:
		return ""
	}
}

func evidenceTag(b *Binding) string {
	t := ceiKeyHuman(b.CEIKey)
	if b.Container != "" {
		t += " container=" + b.Container
	}
	return t
}
