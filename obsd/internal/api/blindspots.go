package api

import (
	"sort"
	"strings"
	"time"
)

// The blindspot registry — MEASURED about own coverage (doc 19). The honest manifest
// of what Vigil CANNOT see, handed to an MCP-connected AI so it reasons WITH the blind
// spots instead of papering over them. The failure it prevents is over-claim-by-
// omission: an AI sees a degraded workload and a healthy-looking graph and attributes a
// cause to whatever it CAN see (a visible-but-idle Job, an infra symptom) when the real
// cause is a modality Vigil never had a sensor for. This surface STATES absence; it
// never fabricates a phenomenon and never claims one is occurring.
//
// Two provenances:
//   - static[]  : AUTHORED, architecture-/ontology-level — missing modalities, named-
//                 but-uningested phenomena, and the charter's epistemic floors. Constant
//                 per release, not per cluster (versioned in git, reviewed). Always
//                 returned, even before binding compiles.
//   - dynamic[] : a deterministic re-projection of THIS cluster's silence ledger — every
//                 signal that is out-of-scope/unobtainable or has no ingested stream
//                 here, deduplicated by its verbatim reason. Same silence ledger ⇒
//                 byte-identical dynamic[]; it reconciles with get_silence_ledger by
//                 construction (it is built from it).

// Blindspot categories (doc 19 §2). The first two are recoverable with engineering, the
// third is blind by design (the charter), the fourth is a property of this cluster.
const (
	BlindModalityAbsent   = "modality-absent"
	BlindUnwiredReachable = "unwired-reachable"
	BlindEpistemicFloor   = "epistemic-floor"
	BlindEnvInduced       = "env-induced"
)

// BlindspotRegistryView is the get_blindspots payload.
type BlindspotRegistryView struct {
	Class        string           `json:"class"` // MEASURED (about own coverage)
	GeneratedAt  time.Time        `json:"generatedAt"`
	GraphVersion string           `json:"graphVersion"`
	GraphRelease string           `json:"graphRelease"`
	Available    bool             `json:"available"` // false ⇒ binding not compiled; static[] is still returned
	Static       []BlindspotEntry `json:"static"`
	Dynamic      []BlindspotEntry `json:"dynamic"`
	Note         string           `json:"note"`
}

// BlindspotEntry is one thing Vigil cannot observe. provenance ∈ {static, dynamic};
// category is one of the Blind* constants above.
type BlindspotEntry struct {
	ID            string `json:"id"`
	Label         string `json:"label,omitempty"`
	Category      string `json:"category"`
	Provenance    string `json:"provenance"`
	Reason        string `json:"reason"`
	UnblockedBy   string `json:"unblockedBy,omitempty"`
	AffectedPairs int    `json:"affectedPairs,omitempty"` // dynamic only: (entity,variable) pairs this gap darkens
	SampleMetric  string `json:"sampleMetric,omitempty"`  // dynamic only: an example darkened metric
}

const blindspotNote = "What Vigil cannot observe, stated honestly (doc 19). STATIC entries are " +
	"architecture/ontology-level (missing modalities, un-wired phenomena, and the charter's epistemic " +
	"floors); DYNAMIC entries are THIS cluster's signals that are unobtainable or un-ingested, re-projected " +
	"from the silence ledger with verbatim reasons. This surface only states absence — it never claims a " +
	"phenomenon is occurring. Consult it before attributing a cause to something Vigil cannot see."

// staticBlindspots is the AUTHORED architecture/ontology-level table (doc 19 §2). These
// are constants of Vigil's design + the ingest catalogue, not per-cluster facts; they are
// authored here (versioned, reviewed) rather than learned. Each reason STATES an absence
// and asserts no phenomenon, no cause, no future certainty.
var staticBlindspots = []BlindspotEntry{
	// --- modality-absent: no signal on any current lane ---
	{ID: "CERT_EXPIRY", Label: "Certificate expiry", Category: BlindModalityAbsent,
		Reason:      "Vigil scrapes no certificate-expiry series; an expiring TLS cert is invisible.",
		UnblockedBy: "control-plane / apiserver metrics endpoint (RBAC + endpoint access)"},
	{ID: "DNS_FAILURE", Label: "DNS resolution failure / latency", Category: BlindModalityAbsent,
		Reason:      "Vigil scrapes no CoreDNS or resolver series; DNS resolution failures and lookup latency are invisible.",
		UnblockedBy: "CoreDNS / CNI resolver metrics endpoint"},
	{ID: "ETCD_SLOW_PATH", Label: "etcd slow path", Category: BlindModalityAbsent,
		Reason:      "Vigil scrapes no etcd latency series; a slow etcd request path is invisible.",
		UnblockedBy: "etcd metrics endpoint (--listen-metrics-urls flag + RBAC access)"},
	{ID: "CNI_FAILURE", Label: "CNI / dataplane failure", Category: BlindModalityAbsent,
		Reason:      "Vigil scrapes no CNI or dataplane series; CNI-level failures are invisible (ingest feasibility unverified).",
		UnblockedBy: "CNI / dataplane metrics, if reachable"},
	{ID: "APPLICATION_LOGS", Label: "Application logs", Category: BlindModalityAbsent,
		Reason:      "Vigil ingests no application logs. App exceptions, stack traces, slow-query logs, authentication failures, and business-logic errors live only in logs and are invisible — only three discrete Kubernetes Event reasons are read.",
		UnblockedBy: "an application-log ingest lane keyed to authored error patterns (DB '[ERROR]'/deadlock, app tracebacks, nginx 5xx, auth failures), joined never fused"},
	// --- unwired-reachable: ingestable, not yet wired ---
	{ID: "INIT_CONTAINER_FAILURE", Label: "Init-container failure", Category: BlindUnwiredReachable,
		Reason:      "A named phenomenon with no authored members yet; init-container failures are reachable via the events lane but not wired.",
		UnblockedBy: "author structured members + events-lane corroboration"},
	{ID: "PDB_VIOLATION", Label: "PodDisruptionBudget violation", Category: BlindUnwiredReachable,
		Reason:      "A named phenomenon with no CEI mapping and no authored members; PDB violations are not wired (feasibility unverified).",
		UnblockedBy: "author members + a CEI binding strategy"},
	{ID: "APP_DB_INTERNAL_METRICS", Label: "Application / datastore-internal metrics", Category: BlindUnwiredReachable,
		Reason:      "Vigil captures container/node/object resource metrics but no application or datastore-internal metrics — query rate, slow queries, connections, DB buffer-pool, Redis evictions/hit-rate, and request rate/error rate/latency are not ingested. The --app-metrics lane exists but is dormant (no exporters deployed, no vigil.io/slo.* annotations on workloads).",
		UnblockedBy: "deploy datastore/app exporters, annotate workloads vigil.io/slo.*, enable --app-metrics-enabled, and lift the histogram skip so p95/p99 latency ingests"},
	{ID: "L7_PROTOCOL_SEMANTICS", Label: "L7 / protocol semantics on flow edges", Category: BlindUnwiredReachable,
		Reason:      "Flow edges are L3/L4 TCP only — Vigil sees that a connection exists, not its content: HTTP status codes, per-call latency, SQL/Redis command identity, retries, TLS errors, and connection-pool saturation are invisible.",
		UnblockedBy: "an L7 tap (eBPF or proxy) layered onto the flow lane"},
	{ID: "MISSING_EVENT_REASONS", Label: "Untracked Kubernetes event reasons", Category: BlindUnwiredReachable,
		Reason:      "The events lane reads only OOMKilled, CrashLoopBackOff, ImagePullBackOff. Readiness/liveness-probe Unhealthy, FailedScheduling, Evicted, FailedMount, and non-OOM Error exits are not surfaced.",
		UnblockedBy: "author the additional reasons into the event-conditions overlay"},
	// --- epistemic-floor: blind by design (the charter) ---
	{ID: "INTRA_CONTAINER_ATTRIBUTION", Label: "Intra-container process attribution", Category: BlindEpistemicFloor,
		Reason:      "Vigil sees that a container is throttled or saturated (the cAdvisor aggregate), not which process inside it consumes the resource. The container is the minimal observable unit; process-level attribution needs in-container instrumentation outside Vigil's ingest scope. Do not attribute a container's resource use to a specific in-container process, sidecar, library, or neighbouring workload without corroborating in-container or application metrics.",
		UnblockedBy: "in-container instrumentation (process profilers, eBPF) — outside Vigil's scope"},
	{ID: "DATA_CORRECTNESS", Label: "Data correctness", Category: BlindEpistemicFloor,
		Reason: "Vigil sees whether a value crossed a bar, not whether the value or the bar is correct. A wrong-but-unbreached value is invisible by design."},
	{ID: "CAUSAL_DIRECTION_UNAUTHORED", Label: "Unauthored causal direction", Category: BlindEpistemicFloor,
		Reason: "A topological co-occurrence is never proof of cause. Only an authored phenomenon relation (get_authored_relations) legitimizes a causal direction; any other directional claim is the synthesizer's hypothesis, never a Vigil fact."},
	{ID: "UNEXPLAINED_RESIDUAL", Label: "Unexplained-channel residual", Category: BlindEpistemicFloor,
		Reason: "A signal carrying neither a resolved bar nor an authored rate guard can never be loud; a novel failure expressing itself only through un-thresholded, un-guarded signals is invisible even to the unexplained channel (doc 08 §3.2)."},
	{ID: "MISSING_APP_BUSINESS_CONTEXT", Label: "Application / business context", Category: BlindEpistemicFloor,
		Reason: "Vigil has no notion of revenue, SLA tier, user impact, or intent beyond declared SLOs. Business and application context, and the remediation, are the synthesizer's to reason about; Vigil supplies only grounded, classed infrastructure facts."},
}

// BuildBlindspotRegistry composes the registry from the (already-computed) silence
// ledger view. Static entries are constant; dynamic entries re-project the ledger's
// sensor/ingest gaps, deduplicated by reason. Pure given its input: same silence view ⇒
// byte-identical registry (GeneratedAt is inherited from the ledger).
func BuildBlindspotRegistry(sv *SilenceLedgerView) *BlindspotRegistryView {
	v := &BlindspotRegistryView{
		Class:   "MEASURED",
		Static:  make([]BlindspotEntry, 0, len(staticBlindspots)),
		Dynamic: []BlindspotEntry{},
		Note:    blindspotNote,
	}
	for _, e := range staticBlindspots {
		e.Provenance = "static"
		v.Static = append(v.Static, e)
	}
	if sv == nil || !sv.Available {
		v.Available = false
		v.GeneratedAt = timeNowUTC()
		if sv != nil {
			v.GraphVersion, v.GraphRelease, v.GeneratedAt = sv.GraphVersion, sv.GraphRelease, sv.GeneratedAt
		}
		v.Note = blindspotNote + " Dynamic cluster-specific blind spots are unavailable until binding compiles."
		return v
	}
	v.Available = true
	v.GeneratedAt = sv.GeneratedAt
	v.GraphVersion, v.GraphRelease = sv.GraphVersion, sv.GraphRelease

	// Dynamic: dedup the SENSOR/INGEST gaps in the silence ledger by reason. A row is a
	// blind spot only if Vigil cannot SEE the signal here — NOT when the operator merely
	// declared no bar (unbounded) or the check is structurally inapplicable.
	type agg struct {
		count           int
		sample, cat, id string
	}
	groups := map[string]*agg{}
	for i := range sv.Silent {
		r := &sv.Silent[i]
		cat, ok := dynamicBlindCategory(r)
		if !ok {
			continue
		}
		g := groups[r.Reason]
		if g == nil {
			g = &agg{cat: cat, sample: r.Metric, id: r.Metric}
			groups[r.Reason] = g
		}
		g.count++
	}
	reasons := make([]string, 0, len(groups))
	for rsn := range groups {
		reasons = append(reasons, rsn)
	}
	sort.Strings(reasons)
	for _, rsn := range reasons {
		g := groups[rsn]
		v.Dynamic = append(v.Dynamic, BlindspotEntry{
			ID: g.id, Category: g.cat, Provenance: "dynamic",
			Reason: rsn, AffectedPairs: g.count, SampleMetric: g.sample,
		})
	}
	return v
}

// dynamicBlindCategory decides whether a silence row is a SENSOR/INGEST blind spot
// (Vigil cannot SEE this here) versus a normativity/structural gap (it could, but the
// operator declared no bar, or the check is inapplicable). Only sensor gaps are blind
// spots; the others are already fully covered by the silence ledger and the coverage
// report and must not be mislabelled "Vigil cannot see this."
func dynamicBlindCategory(r *SilenceLedgerRow) (string, bool) {
	switch r.ReasonClass {
	case SilenceNoStreamKey:
		return BlindEnvInduced, true // a bound bar with no ingested stream on this cluster
	case SilenceUnresolved:
		return BlindEnvInduced, true // the config row could not be read here
	case SilenceOutOfScope:
		if strings.Contains(r.Reason, "unobtainable") {
			return BlindEnvInduced, true // the emitting signal is not produced on this cluster
		}
		return "", false // structurally inapplicable (e.g. no CPU limit ⇒ throttling cannot occur)
	default: // SilenceUnbounded
		return "", false // a declared-limit gap, not a sensor blind spot
	}
}
