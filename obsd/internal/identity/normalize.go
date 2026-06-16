package identity

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Label normalization — doc 03 §3.3, with the exact dialect facts and traps of
// doc 14 §3.2. Each exporter family labels telemetry in its own dialect; the
// normalization map per family translates a source label set into CEI coordinates.
// Normalization is the single point where dialects collapse: downstream components
// never see raw labels. Streams whose labels cannot be normalized to a known CEI
// are QUARANTINED (stored, counted, surfaced in the coverage report) — never
// guessed into an identity.

// Family identifies an exporter family (one normalization map each).
type Family string

const (
	// FamilyCAdvisor is the kubelet /metrics/cadvisor endpoint (container runtime).
	FamilyCAdvisor Family = "cadvisor"
	// FamilyKSM is kube-state-metrics — the identity backbone (UIDs, ownership,
	// resource limits).
	FamilyKSM Family = "kube-state-metrics"
	// FamilyNodeExporter is node-exporter (node hardware signals).
	FamilyNodeExporter Family = "node-exporter"
	// FamilyApp is an application's own /metrics endpoint (doc 15 cap. A): RED,
	// queue, freshness — bound to CUSTOMER-DECLARED SLO bars (borrowed normativity).
	// Identity is the scrape-target pod, never the series labels.
	FamilyApp Family = "app"

	// OTel semconv is deliberately the FOURTH lane, added in Phase 0b–1 after the
	// first maps work, to prove the machinery is not single-dialect-shaped
	// (doc 14 §3.2). It is the easy dialect: it carries k8s.pod.uid natively.
)

// mapVersions: normalization maps are versioned and regression-tested when an
// exporter family's version moves (doc 03 §6, doc 11 §3.2). Bump on any rule change.
var mapVersions = map[Family]string{
	FamilyCAdvisor:     "0.1.0",
	FamilyKSM:          "0.1.0",
	FamilyNodeExporter: "0.1.0",
	FamilyApp:          "0.1.0",
}

// MapVersion returns the current normalization-map version for a family.
func MapVersion(f Family) string { return mapVersions[f] }

// Outcome classifies what normalization did with a series.
type Outcome uint8

const (
	// OutcomeResolved: the series joined a CEI by exact coordinates.
	OutcomeResolved Outcome = iota + 1
	// OutcomeDropped: the row is RECOGNIZED and deliberately not modeled (e.g. the
	// cAdvisor pause-sandbox row). Distinct from quarantine: we know exactly what
	// it is and choose not to bind it. Counted, with a stated reason.
	OutcomeDropped
	// OutcomeQuarantined: the series was expected to join but could not. Stored,
	// counted, surfaced in the coverage report — never guessed (doc 03 §3.3).
	OutcomeQuarantined
)

func (o Outcome) String() string {
	switch o {
	case OutcomeResolved:
		return "resolved"
	case OutcomeDropped:
		return "dropped"
	case OutcomeQuarantined:
		return "quarantined"
	default:
		return "unknown"
	}
}

// Reason is the stated reason for a drop or quarantine. Every non-resolved
// outcome carries one; "no reason" is not an option (honest coverage, doc 01).
type Reason string

const (
	ReasonNone Reason = ""

	// Deliberate drops (recognized, not modeled).
	ReasonPauseSandbox Reason = "pause-sandbox-row" // cAdvisor sandbox: container="POD" (dockershim) or container="" with a pause image (containerd/CRI-O)
	ReasonNonPodCgroup Reason = "non-pod-cgroup"    // cAdvisor system.slice etc.

	// Quarantines (expected to join, could not).
	ReasonMissingLabels  Reason = "missing-identity-labels"
	ReasonUnknownPod     Reason = "unknown-pod"
	ReasonUnknownNode    Reason = "unknown-node"
	ReasonUnmappedMetric Reason = "unmapped-metric-class"
	ReasonUnknownFamily  Reason = "unknown-exporter-family"
	ReasonMissingSource  Reason = "missing-scrape-source"
	ReasonMalformed      Reason = "malformed-coordinates"
)

// Series is one scraped series presented for identity normalization.
type Series struct {
	Family Family
	Metric string
	Labels map[string]string

	// SourceNode is scrape-target metadata: the node whose endpoint produced this
	// series. kubelet/cAdvisor and node-exporter are per-node endpoints, so the
	// scraper always knows it. Node-scoped identity comes from HERE, not from
	// series labels — node name ↔ Node object is the only join needed (doc 14 §3.2).
	SourceNode string

	// SourcePodNS/SourcePodName are scrape-target metadata for FamilyApp: the POD
	// whose /metrics endpoint produced this series (doc 15 cap. A). An application's
	// own metrics (queue depth, request rate, freshness) carry no k8s identity
	// labels, so — exactly like node-exporter's node identity — the entity is the
	// SCRAPE TARGET, never guessed from the series' own labels. Resolved to a pod
	// CEI through the same time-aware control-plane lookup cAdvisor uses.
	SourcePodNS   string
	SourcePodName string

	// At is the RECEIVE timestamp (doc 14 A12: wall-clock UTC at ingest receive
	// time). It stamps the audit record; it is NOT the join time when the source
	// carries its own sample timestamp (see EventTime).
	At time.Time

	// EventTime is the sample's own timestamp when the source carries one (doc 14
	// A12: "event time recorded alongside when sources carry it") — cAdvisor stamps
	// samples with collection time, routinely seconds stale. Zero when the source
	// carries none. The identity JOIN keys off EventTime when present, else At:
	// a late sample must join the pod that was alive when the sample was TAKEN,
	// not whatever same-name successor exists when it is received — otherwise the
	// recreate-with-same-name race (the join honeypot, doc 14 §3.2) silently
	// mis-joins, which doc 14 §1.4's tombstones exist to prevent.
	EventTime time.Time
}

// joinTime is the instant identity resolution keys off: the sample's own event
// time when the source carries one, else the receive time.
func (s Series) joinTime() time.Time {
	if !s.EventTime.IsZero() {
		return s.EventTime
	}
	return s.At
}

// Result is the outcome of normalizing one series.
type Result struct {
	Outcome    Outcome
	CEI        CEI // valid only when Outcome == OutcomeResolved
	Reason     Reason
	Family     Family
	MapVersion string
}

// Lookup answers identity questions from the control-plane view (the informer
// cache + lifecycle store in M3; fakes in tests). Implementations must be
// time-aware: "which pod was (ns, name) at time t", using creation/termination
// stamps, so same-name succession disambiguates (doc 14 §3.2). Callers pass the
// series' join time (event time when carried, else receive time). Lifetime
// intervals are half-open [born, died): an instant exactly at death belongs to
// the successor (or to nothing), never to both.
type Lookup interface {
	PodUID(namespace, name string, at time.Time) (uid string, ok bool)
	NodeUID(name string, at time.Time) (uid string, ok bool)
}

// Normalizer applies the per-family normalization maps.
type Normalizer struct {
	cluster string // cluster id = kube-system namespace UID (doc 14 A9)
	lookup  Lookup
}

func NewNormalizer(cluster string, lookup Lookup) *Normalizer {
	return &Normalizer{cluster: cluster, lookup: lookup}
}

// Normalize translates one series' source labels into a CEI, a deliberate drop,
// or a quarantine. It never guesses.
func (n *Normalizer) Normalize(s Series) Result {
	switch s.Family {
	case FamilyCAdvisor:
		return n.cadvisor(s)
	case FamilyKSM:
		return n.ksm(s)
	case FamilyNodeExporter:
		return n.nodeExporter(s)
	case FamilyApp:
		return n.app(s)
	default:
		return n.quarantine(s.Family, ReasonUnknownFamily)
	}
}

// app implements the application-metrics dialect (doc 15 cap. A). An app's own
// /metrics series carry no k8s identity labels, so — exactly like node-exporter's
// node identity — the entity is the SCRAPE TARGET pod, resolved through the same
// time-aware control-plane lookup cAdvisor uses (so a late sample joins the pod
// that was alive when it was taken, never a same-name successor). The series'
// own labels are NEVER trusted for identity; they only disambiguate sub-streams
// (streamSubID). A missing target, or a target the control plane has not seen,
// quarantines — it never guesses a pod.
func (n *Normalizer) app(s Series) Result {
	if s.SourcePodNS == "" || s.SourcePodName == "" {
		return n.quarantine(FamilyApp, ReasonMissingSource)
	}
	uid, ok := n.lookup.PodUID(s.SourcePodNS, s.SourcePodName, s.joinTime())
	if !ok {
		return n.quarantine(FamilyApp, ReasonUnknownPod)
	}
	return n.pod(FamilyApp, s.SourcePodNS, s.SourcePodName, uid, s.At)
}

// cadvisor implements the container-runtime dialect (doc 14 §3.2 row 1).
// Identity labels: namespace, pod, container (+ id, image, name for sandbox
// discrimination). THE TRAPS, encoded:
//   - No pod UID label → join via (namespace, pod) → UID against the time-aware
//     control-plane mapping at ingest.
//   - container="" rows with NO concrete container behind them (image="" and
//     name="") are pod-level cgroup AGGREGATES → map to the pod CEI.
//   - The pause sandbox is dropped deliberately, in BOTH dialect shapes:
//     dockershim-era kubelets label it container="POD"; containerd/CRI-O emit it
//     with container="" but a concrete container name/image (the pause image).
//     Folding the sandbox into the pod stream would interleave two different
//     cgroups' numbers in one ring — an identity mis-join, the silent killer.
//     None of these may ever bind to a container CEI.
func (n *Normalizer) cadvisor(s Series) Result {
	// machine_* rows describe the node itself (cAdvisor's machine info).
	if strings.HasPrefix(s.Metric, "machine_") {
		if s.SourceNode == "" {
			return n.quarantine(FamilyCAdvisor, ReasonMissingSource)
		}
		return n.node(FamilyCAdvisor, s.SourceNode, s.joinTime(), s.At)
	}

	// Label aliases: kubelets before v1.16 used pod_name/container_name. Cheap to
	// honor here; this is exactly the dialect variance this layer exists for.
	ns := s.Labels["namespace"]
	pod := firstLabel(s.Labels, "pod", "pod_name")
	container := firstLabel(s.Labels, "container", "container_name")

	if ns == "" && pod == "" {
		// Non-pod cgroup rows (id="/system.slice/...", "/kubepods" roots): real
		// numbers about node system daemons, recognized and deliberately not
		// modeled as pod telemetry.
		return n.drop(FamilyCAdvisor, ReasonNonPodCgroup)
	}
	if ns == "" || pod == "" {
		return n.quarantine(FamilyCAdvisor, ReasonMissingLabels)
	}
	if container == "POD" {
		// The pause-sandbox cgroup. Its numbers are sandbox overhead, not a pod
		// aggregate — dropped deliberately, never bound to a container CEI.
		return n.drop(FamilyCAdvisor, ReasonPauseSandbox)
	}

	uid, ok := n.lookup.PodUID(ns, pod, s.joinTime())
	if !ok {
		return n.quarantine(FamilyCAdvisor, ReasonUnknownPod)
	}

	if container == "" {
		// container="" covers TWO different cgroups on containerd/CRI-O: the pod
		// slice aggregate (no concrete container: image="" and name="") and the
		// pause sandbox (a real container cgroup — concrete name/image, the pause
		// image). Only the aggregate maps to the pod CEI; the sandbox is dropped
		// like its dockershim-era container="POD" shape above.
		if s.Labels["image"] != "" || s.Labels["name"] != "" {
			return n.drop(FamilyCAdvisor, ReasonPauseSandbox)
		}
		return n.pod(FamilyCAdvisor, ns, pod, uid, s.At)
	}
	return n.container(FamilyCAdvisor, ns, pod, uid, container, s.At)
}

// ksm implements the cluster-state dialect (doc 14 §3.2 row 2) — the identity
// backbone: UID for CEI minting, ownership for role derivation, and
// kube_pod_container_resource_limits as the threshold-resolution source (04).
// TRAP, encoded: state metrics persist briefly for terminated pods. A uid-carrying
// row still resolves — to the terminated pod's OWN CEI — and lifecycle state (M3)
// gates binding; normalization never re-points a dead pod's series at a successor.
func (n *Normalizer) ksm(s Series) Result {
	switch {
	case strings.HasPrefix(s.Metric, "kube_pod_"):
		ns, pod := s.Labels["namespace"], s.Labels["pod"]
		if ns == "" || pod == "" {
			return n.quarantine(FamilyKSM, ReasonMissingLabels)
		}
		uid := s.Labels["uid"]
		if uid == "" {
			// Rows without the uid label fall back to the time-aware mapping.
			var ok bool
			uid, ok = n.lookup.PodUID(ns, pod, s.joinTime())
			if !ok {
				return n.quarantine(FamilyKSM, ReasonUnknownPod)
			}
		}
		// Identity scope is decided by METRIC CLASS, not by label presence.
		// kube_pod_container_* / kube_pod_init_container_* are inherently
		// container-scoped (this is where kube_pod_container_resource_limits —
		// the threshold-resolution source, doc 14 §3.2 — lives); a row of that
		// class missing its container label is malformed and quarantines rather
		// than silently re-scoping to the pod (a guessed identity, doc 03 §3.3).
		if strings.HasPrefix(s.Metric, "kube_pod_container_") ||
			strings.HasPrefix(s.Metric, "kube_pod_init_container_") {
			c := s.Labels["container"]
			if c == "" {
				return n.quarantine(FamilyKSM, ReasonMissingLabels)
			}
			return n.container(FamilyKSM, ns, pod, uid, c, s.At)
		}
		return n.pod(FamilyKSM, ns, pod, uid, s.At)

	case strings.HasPrefix(s.Metric, "kube_node_"):
		name := s.Labels["node"]
		if name == "" {
			return n.quarantine(FamilyKSM, ReasonMissingLabels)
		}
		return n.node(FamilyKSM, name, s.joinTime(), s.At)

	// Workload metrics map to ROLE-layer CEIs whose keys match DeriveRole's
	// anchors exactly, so workload series and pod role aggregation join by
	// construction. Only kinds that are their own role anchor map directly.
	case strings.HasPrefix(s.Metric, "kube_deployment_"):
		return n.role(FamilyKSM, s, "Deployment", s.Labels["deployment"])
	case strings.HasPrefix(s.Metric, "kube_statefulset_"):
		return n.role(FamilyKSM, s, "StatefulSet", s.Labels["statefulset"])
	case strings.HasPrefix(s.Metric, "kube_daemonset_"):
		return n.role(FamilyKSM, s, "DaemonSet", s.Labels["daemonset"])

	default:
		// kube_replicaset_* (an intermediate controller — anchoring it on its
		// Deployment needs the owner chain, which is M3's resolver), services,
		// PVCs, namespaces, etc.: not yet mapped. Quarantined with a stated
		// reason, never guessed.
		return n.quarantine(FamilyKSM, ReasonUnmappedMetric)
	}
}

// nodeExporter implements the node-exporter dialect (doc 14 §3.2 row 3). Identity
// comes entirely from scrape-target metadata: node name ↔ Node object is the only
// join needed.
func (n *Normalizer) nodeExporter(s Series) Result {
	if s.SourceNode == "" {
		return n.quarantine(FamilyNodeExporter, ReasonMissingSource)
	}
	return n.node(FamilyNodeExporter, s.SourceNode, s.joinTime(), s.At)
}

// --- coordinate minting helpers ----------------------------------------------

func (n *Normalizer) pod(f Family, ns, name, uid string, at time.Time) Result {
	cei, err := MintInstance(InstanceCoords{
		Cluster: n.cluster, Namespace: ns, Kind: "Pod", Name: name, UID: uid,
	}, at)
	if err != nil {
		return n.quarantine(f, ReasonMalformed)
	}
	return n.resolved(f, cei)
}

// container mints a container-instance CEI anchored on the owning pod instance.
// Containers carry no Kubernetes UID of their own; UID = <podUID>/<containerName>.
// A container restart keeps its name and pod, so it keeps its CEI — restarts are
// signals (restart counters), not identity events. Because the UID embeds the pod
// UID, a recreated same-name pod's containers get NEW container CEIs too.
func (n *Normalizer) container(f Family, ns, pod, podUID, container string, at time.Time) Result {
	cei, err := MintInstance(InstanceCoords{
		Cluster: n.cluster, Namespace: ns, Kind: "Container",
		Name: container, UID: podUID + "/" + container,
	}, at)
	if err != nil {
		return n.quarantine(f, ReasonMalformed)
	}
	_ = pod // pod name is not an identity coordinate for containers; UID anchors it
	return n.resolved(f, cei)
}

// node resolves a node identity: joinAt drives the time-aware lookup; mintedAt
// (receive time) stamps the minted CEI's provenance metadata.
func (n *Normalizer) node(f Family, name string, joinAt, mintedAt time.Time) Result {
	uid, ok := n.lookup.NodeUID(name, joinAt)
	if !ok {
		return n.quarantine(f, ReasonUnknownNode)
	}
	cei, err := MintInstance(InstanceCoords{
		Cluster: n.cluster, Kind: "Node", Name: name, UID: uid,
	}, mintedAt)
	if err != nil {
		return n.quarantine(f, ReasonMalformed)
	}
	return n.resolved(f, cei)
}

func (n *Normalizer) role(f Family, s Series, kind, name string) Result {
	ns := s.Labels["namespace"]
	if ns == "" || name == "" {
		return n.quarantine(f, ReasonMissingLabels)
	}
	cei, err := MintRole(RoleCoords{
		Cluster: n.cluster, Namespace: ns, Kind: kind, RoleKey: kind + "/" + name,
	}, s.At)
	if err != nil {
		return n.quarantine(f, ReasonMalformed)
	}
	return n.resolved(f, cei)
}

func (n *Normalizer) resolved(f Family, cei CEI) Result {
	return Result{Outcome: OutcomeResolved, CEI: cei, Family: f, MapVersion: mapVersions[f]}
}

func (n *Normalizer) drop(f Family, r Reason) Result {
	return Result{Outcome: OutcomeDropped, Reason: r, Family: f, MapVersion: mapVersions[f]}
}

func (n *Normalizer) quarantine(f Family, r Reason) Result {
	return Result{Outcome: OutcomeQuarantined, Reason: r, Family: f, MapVersion: mapVersions[f]}
}

// firstLabel returns the first non-empty value among the given label keys.
func firstLabel(labels map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return ""
}

// --- join audit (doc 03 §3.6) --------------------------------------------------

// AuditRecord is the join audit record: stream key; resolved CEI or drop/quarantine
// reason; ingest timestamp. The continuous join audits of doc 03 M5 sample these
// against control-plane truth.
type AuditRecord struct {
	StreamKey  string
	Family     Family
	MapVersion string
	Outcome    Outcome
	CEIKey     string // empty unless resolved
	Reason     Reason
	At         time.Time
}

// Audit produces the audit record for this result over the series it came from.
func (r Result) Audit(s Series) AuditRecord {
	rec := AuditRecord{
		StreamKey:  StreamKey(s),
		Family:     r.Family,
		MapVersion: r.MapVersion,
		Outcome:    r.Outcome,
		Reason:     r.Reason,
		At:         s.At,
	}
	if r.Outcome == OutcomeResolved {
		rec.CEIKey = r.CEI.Key()
	}
	return rec
}

// StreamKey is the canonical fingerprint of a series: family, scrape source,
// metric name, and the sorted label set. Family and source node are part of the
// fingerprint because for the per-node dialects identity comes from scrape-target
// metadata, not labels — without them, machine_cpu_cores from two different nodes
// would collapse to one fingerprint while resolving to different CEIs, defeating
// the join audit (doc 03 §3.6/M5). Deterministic — equal series produce equal keys.
func StreamKey(s Series) string {
	keys := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(string(s.Family))
	b.WriteString("://")
	b.WriteString(s.SourceNode)
	b.WriteByte('/')
	b.WriteString(s.Metric)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", k, s.Labels[k])
	}
	b.WriteByte('}')
	return b.String()
}
