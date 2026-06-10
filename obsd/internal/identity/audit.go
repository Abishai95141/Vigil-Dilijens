package identity

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Join-audit tooling and health metrics (doc 03 §5/§6, M5) — and the Phase-0a exit
// gate (doc 11 gate row 0a): "join accuracy >= target on reference clusters, with
// all misses explained as quarantines, not mis-joins."
//
// A mis-join — a stream (or the model) bound to the WRONG CEI — is the silent killer
// (doc 03 §6): every downstream behaviour is only as correct as the join, and the
// failure is invisible without an audit. The audit checks the identity model and
// resolved streams against an INDEPENDENT source of control-plane truth (the raw
// informer listers), distinct from the lifecycle Store/normalizer that produced the
// identities — so a discrepancy reveals a real model bug, not a self-consistent echo.

// TruthSource is the independent control-plane ground truth used to audit identities.
// It is backed by the raw informer listers (current cluster state), NOT by the
// lifecycle Store, so the audit cross-checks our model against reality.
type TruthSource interface {
	// PodUID returns the current UID of the pod named (namespace, name), if present.
	PodUID(namespace, name string) (uid string, ok bool)
	// NodeUID returns the current UID of the node named `name`, if present.
	NodeUID(name string) (uid string, ok bool)
	// PodExistsByUID reports whether a pod with this UID currently exists.
	PodExistsByUID(uid string) bool
	// ListPods / ListNodes enumerate current entities for the consistency audit.
	ListPods() []EntityRef
	ListNodes() []EntityRef
}

// EntityRef is a control-plane entity's identifying coordinates.
type EntityRef struct {
	Namespace string
	Name      string
	UID       string
}

// JoinResult classifies one identity check against truth.
type JoinResult uint8

const (
	JoinCorrect      JoinResult = iota // the CEI matches control-plane truth
	JoinMisjoin                        // the CEI contradicts truth — a wrong join (the killer)
	JoinVanished                       // the entity is no longer in truth (benign churn since resolution)
	JoinUnverifiable                   // not checkable here (role layer, PVC/service — not in TruthSource)
)

func (r JoinResult) String() string {
	switch r {
	case JoinCorrect:
		return "correct"
	case JoinMisjoin:
		return "misjoin"
	case JoinVanished:
		return "vanished"
	default:
		return "unverifiable"
	}
}

// containerPodUID extracts the owning pod UID from a container CEI's UID
// (minted as "<podUID>/<containerName>", see normalize.go).
func containerPodUID(containerUID string) string {
	if i := strings.LastIndex(containerUID, "/"); i >= 0 {
		return containerUID[:i]
	}
	return containerUID
}

// VerifyJoin checks one resolved instance CEI against control-plane truth.
// Pods and nodes are verified by (name -> UID); containers by their owning pod's
// existence (the container UID embeds the unique pod UID, so it cannot independently
// mis-join). Roles and other kinds are unverifiable against this TruthSource.
func VerifyJoin(cei CEI, truth TruthSource) JoinResult {
	if cei.Layer != LayerInstance {
		return JoinUnverifiable
	}
	switch cei.Kind {
	case "Pod":
		uid, ok := truth.PodUID(cei.Namespace, cei.Name)
		switch {
		case !ok:
			return JoinVanished
		case uid == cei.UID:
			return JoinCorrect
		default:
			return JoinMisjoin
		}
	case "Node":
		uid, ok := truth.NodeUID(cei.Name)
		switch {
		case !ok:
			return JoinVanished
		case uid == cei.UID:
			return JoinCorrect
		default:
			return JoinMisjoin
		}
	case "Container":
		if truth.PodExistsByUID(containerPodUID(cei.UID)) {
			return JoinCorrect
		}
		return JoinVanished
	default:
		return JoinUnverifiable
	}
}

// Misjoin records a detected wrong identity, for surfacing and triage.
type Misjoin struct {
	Kind      string
	Namespace string
	Name      string
	TruthUID  string // what the control plane says
	ModelUID  string // what our model resolved (empty if missing)
}

func (m Misjoin) String() string {
	return fmt.Sprintf("%s %s/%s: model=%q truth=%q", m.Kind, m.Namespace, m.Name, m.ModelUID, m.TruthUID)
}

// ConsistencyReport is the result of auditing the lifecycle Store against truth.
type ConsistencyReport struct {
	// Ready is false when the audit could not actually run against real truth — most
	// importantly, when the informer caches have not synced yet (the listers then
	// return empty, which must NOT be mistaken for a clean empty cluster). A
	// not-ready report can never pass the gate.
	Ready        bool
	CheckedPods  int
	CheckedNodes int
	Correct      int
	Misjoins     int // store resolves a DIFFERENT UID than truth — a mis-join
	Missing      int // store does not yet resolve a current entity — coverage lag (explained)
	Details      []Misjoin
	At           time.Time
}

// maxMisjoinDetails bounds the detail list so a pathological run cannot balloon memory.
const maxMisjoinDetails = 256

// AuditConsistency checks the lifecycle Store's identity model against control-plane
// truth for every current pod and node: does the Store resolve the same UID the
// control plane reports? A divergence with a different UID is a mis-join (the killer);
// a Store that does not yet know a current entity is coverage lag (Missing), which is
// expected during churn and corresponds to quarantined streams, not a wrong join.
//
// Note on churn: during an active same-name recreate the lister may already show the
// successor's UID while the Store is still processing the predecessor's events, a
// TRANSIENT mismatch counted as a mis-join. The Phase-0a gate is evaluated on settled
// reference clusters (doc 11) where this does not occur; for live monitoring a
// PERSISTENT (not single-tick) non-zero mis-join count is the real signal.
func AuditConsistency(store *Store, truth TruthSource, at time.Time) ConsistencyReport {
	rep := ConsistencyReport{Ready: true, At: at}
	for _, p := range truth.ListPods() {
		rep.CheckedPods++
		uid, ok := store.PodUID(p.Namespace, p.Name, at)
		switch {
		case !ok:
			rep.Missing++
		case uid == p.UID:
			rep.Correct++
		default:
			rep.Misjoins++
			if len(rep.Details) < maxMisjoinDetails {
				rep.Details = append(rep.Details, Misjoin{Kind: "Pod", Namespace: p.Namespace, Name: p.Name, TruthUID: p.UID, ModelUID: uid})
			}
		}
	}
	for _, n := range truth.ListNodes() {
		rep.CheckedNodes++
		uid, ok := store.NodeUID(n.Name, at)
		switch {
		case !ok:
			rep.Missing++
		case uid == n.UID:
			rep.Correct++
		default:
			rep.Misjoins++
			if len(rep.Details) < maxMisjoinDetails {
				rep.Details = append(rep.Details, Misjoin{Kind: "Node", Name: n.Name, TruthUID: n.UID, ModelUID: uid})
			}
		}
	}
	return rep
}

// JoinAccuracy is the fraction of entities the model resolved that it resolved
// CORRECTLY (1.0 iff there are no mis-joins). Coverage (separate) measures how many
// current entities the model resolves at all.
func (r ConsistencyReport) JoinAccuracy() float64 {
	resolved := r.Correct + r.Misjoins
	if resolved == 0 {
		return 1.0
	}
	return float64(r.Correct) / float64(resolved)
}

// Coverage is the fraction of current entities the model resolves (lag indicator).
func (r ConsistencyReport) Coverage() float64 {
	checked := r.CheckedPods + r.CheckedNodes
	if checked == 0 {
		return 1.0
	}
	return float64(r.Correct) / float64(checked)
}

// GateResult is the Phase-0a exit-gate evaluation (doc 03 M5 / doc 11 row 0a).
type GateResult struct {
	Passed       bool
	JoinAccuracy float64
	Coverage     float64
	Misjoins     int
	Missing      int
	Reasons      []string
}

// EvaluateGate decides the Phase-0a exit gate: join accuracy must be perfect (NO
// mis-joins — all misses must be quarantines/lag, never wrong joins) AND coverage
// must meet the target. targetCoverage is configurable to tolerate transient lag.
func EvaluateGate(rep ConsistencyReport, targetCoverage float64) GateResult {
	res := GateResult{
		JoinAccuracy: rep.JoinAccuracy(),
		Coverage:     rep.Coverage(),
		Misjoins:     rep.Misjoins,
		Missing:      rep.Missing,
	}
	// A not-ready report (informers not synced) is NOT a pass: an empty read during
	// the sync window must never publish a false ALL-CLEAR on this release-blocking
	// signal — it is indistinguishable from a clean empty cluster otherwise.
	if !rep.Ready {
		res.Reasons = append(res.Reasons, "identity layer not synced — the gate is not yet measurable")
		return res
	}
	res.Passed = rep.Misjoins == 0 && res.Coverage >= targetCoverage
	if rep.Misjoins > 0 {
		res.Reasons = append(res.Reasons, fmt.Sprintf("%d mis-join(s) — must be 0; a wrong join is the silent killer (doc 03 §6)", rep.Misjoins))
	}
	if res.Coverage < targetCoverage {
		res.Reasons = append(res.Reasons, fmt.Sprintf("coverage %.4f < target %.4f (%d current entities unresolved)", res.Coverage, targetCoverage, rep.Missing))
	}
	return res
}

// Auditor accumulates per-stream normalization outcomes (doc 03 §6: quarantine
// volume and reasons, orphan rate) and verifies sampled resolved streams against
// truth. It is the continuous, scrape-time counterpart to the consistency audit; in
// Phase 0b the observation pipeline (05) feeds it AuditRecords.
type Auditor struct {
	mu sync.Mutex

	resolved           int64
	dropped            int64
	quarantined        int64
	quarantineByReason map[Reason]int64

	verified     int64
	correct      int64
	misjoin      int64
	vanished     int64
	unverifiable int64
	misjoinSeen  []Misjoin
}

func NewAuditor() *Auditor {
	return &Auditor{quarantineByReason: make(map[Reason]int64)}
}

// Record accumulates one normalization outcome (doc 03 §3.6 audit record).
func (a *Auditor) Record(rec AuditRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch rec.Outcome {
	case OutcomeResolved:
		a.resolved++
	case OutcomeDropped:
		a.dropped++
	case OutcomeQuarantined:
		a.quarantined++
		a.quarantineByReason[rec.Reason]++
	}
}

// VerifyStream audits one resolved stream's CEI against truth, updating join-accuracy
// counters and capturing mis-joins for surfacing.
func (a *Auditor) VerifyStream(cei CEI, truth TruthSource) JoinResult {
	r := VerifyJoin(cei, truth)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.verified++
	switch r {
	case JoinCorrect:
		a.correct++
	case JoinMisjoin:
		a.misjoin++
		if len(a.misjoinSeen) < maxMisjoinDetails {
			a.misjoinSeen = append(a.misjoinSeen, Misjoin{Kind: cei.Kind, Namespace: cei.Namespace, Name: cei.Name, ModelUID: cei.UID})
		}
	case JoinVanished:
		a.vanished++
	case JoinUnverifiable:
		a.unverifiable++
	}
	return r
}

// AuditSnapshot is the published health view (doc 03 §6).
type AuditSnapshot struct {
	Resolved           int64
	Dropped            int64
	Quarantined        int64
	QuarantineByReason map[string]int64
	OrphanRate         float64 // quarantined / (resolved + quarantined)

	Verified     int64
	Correct      int64
	Misjoins     int64
	Vanished     int64
	JoinAccuracy float64 // correct / (correct + misjoins)
}

// Snapshot returns the current health view.
func (a *Auditor) Snapshot() AuditSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := AuditSnapshot{
		Resolved:           a.resolved,
		Dropped:            a.dropped,
		Quarantined:        a.quarantined,
		QuarantineByReason: make(map[string]int64, len(a.quarantineByReason)),
		Verified:           a.verified,
		Correct:            a.correct,
		Misjoins:           a.misjoin,
		Vanished:           a.vanished,
	}
	for reason, n := range a.quarantineByReason {
		s.QuarantineByReason[string(reason)] = n
	}
	if joinable := a.resolved + a.quarantined; joinable > 0 {
		s.OrphanRate = float64(a.quarantined) / float64(joinable)
	}
	if verifiedJoins := a.correct + a.misjoin; verifiedJoins > 0 {
		s.JoinAccuracy = float64(a.correct) / float64(verifiedJoins)
	} else {
		s.JoinAccuracy = 1.0
	}
	return s
}
