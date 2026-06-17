package observe

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// Ingest (doc 05 M1, doc 14 A1): WE own the scraper — exposition text parsed
// directly from the source endpoints, every series passed through the identity
// normalizer (03), and resolved samples stored CEI-stamped in the hot store.
// Receive time stamps the audit trail; a sample's own timestamp (cAdvisor sets
// one) is the JOIN time (doc 14 A12). Unjoinable series quarantine with typed
// reasons and are COUNTED — the ingest summary is part of honest coverage.
//
// v1 scope, stated: the cAdvisor endpoint per node (the container-runtime
// dialect). KSM and node-exporter ingest activate when those tools are deployed
// (binding's availability report gates them). Histogram/summary families are
// counted and skipped — the threshold/rate primitives consume scalars; quantile
// sub-variables are an open question owned by doc 02 §8.

// Fetcher fetches one node-proxied metrics payload. Implementations: the
// API-server proxy (kube.ProxyFetcher) in production; fixtures in tests.
type Fetcher interface {
	NodeMetrics(ctx context.Context, nodeName, path string) (body []byte, receivedAt time.Time, err error)
}

// reasonNonFiniteValue counts samples whose value is NaN/±Inf — refused at the
// ingest gate (no arithmetic, no canonical encoding is possible over them).
const reasonNonFiniteValue = "non-finite-value"

// StreamMeta is the descriptor of one live stream (doc 05 §3.6): identity, the
// concrete metric, exposition type, and scrape provenance. CadenceClass is
// "scrape" for everything this scraper produces.
type StreamMeta struct {
	CEIKey  string
	UID     string // CEI UID (container streams: podUID/container — the QA join key)
	Kind    string // Container | Pod | Node
	Metric  string
	Type    string // gauge | counter | untyped (exposition TYPE — QA's character check input)
	Node    string // scrape source node
	Cadence string // "scrape"
}

// IngestSummary is one scrape cycle's honest accounting.
type IngestSummary struct {
	Nodes            int
	NodeErrors       []string // per-node fetch failures, stated (partial scrape ≠ silent gap)
	SeriesResolved   int
	SamplesStored    int
	SeriesDropped    map[string]int // deliberate drops by reason (pause sandbox, non-pod cgroups)
	SeriesQuarantine map[string]int // quarantines by reason (never guessed)
	SkippedFamilies  int            // histogram/summary families (stated v1 limit)
}

// Ingestor scrapes, normalizes, and stores. It holds the stream-meta registry
// (the store itself stays dumb, doc 05 §3.1). The registry is lock-guarded: the
// scrape loop writes while QA/report readers run on the evaluation goroutine.
type Ingestor struct {
	norm *identity.Normalizer
	hot  *qss.HotStore

	// tap, when set, receives every stored sample (with its stream definition and
	// receive stamp) — the warm-tier/replay capture hook (doc 14 §2.3). Set it
	// BEFORE scraping starts; it is not lock-guarded.
	tap func(def qss.StreamDef, recv time.Time, s qss.Sample)

	mu   sync.RWMutex
	meta map[string]StreamMeta // streamID -> descriptor
}

// NewIngestor wires the scrape pipeline: normalizer (identity join) + hot store.
func NewIngestor(norm *identity.Normalizer, hot *qss.HotStore) *Ingestor {
	return &Ingestor{norm: norm, hot: hot, meta: map[string]StreamMeta{}}
}

// SetTap installs the capture hook. Must be called before the first scrape.
func (in *Ingestor) SetTap(tap func(def qss.StreamDef, recv time.Time, s qss.Sample)) {
	in.tap = tap
}

// NodePayload is one node's fetched exposition body (or its failure), produced by
// the network phase and consumed by the ingest phase. Splitting the two lets the
// caller hold a store gate only around the ingest (CPU) phase, so evaluation
// ticks always observe whole scrape cycles — the property byte-identical replay
// depends on (doc 05 §3.5). Family says which normalization dialect the body
// speaks (empty = cAdvisor, the founding lane).
type NodePayload struct {
	Node       string
	Family     identity.Family
	Body       []byte
	ReceivedAt time.Time
	Err        error

	// PodNS/PodName are the scrape-target pod for FamilyApp payloads (doc 15 cap.
	// A): an app's /metrics endpoint is served BY a pod, and that pod is the entity
	// every series in the body is attributed to (identity from the target, never the
	// labels). Empty for node-scoped families.
	PodNS   string
	PodName string
}

// FetchCAdvisor fetches every node's cAdvisor payload (network only, no store
// writes). Nodes are fetched in sorted order; failures are carried, not fatal.
func FetchCAdvisor(ctx context.Context, f Fetcher, nodes []string) []NodePayload {
	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	out := make([]NodePayload, 0, len(sorted))
	for _, node := range sorted {
		body, receivedAt, err := f.NodeMetrics(ctx, node, "metrics/cadvisor")
		out = append(out, NodePayload{Node: node, Family: identity.FamilyCAdvisor, Body: body, ReceivedAt: receivedAt, Err: err})
	}
	return out
}

// nodeExporterPort is node-exporter's conventional hostNetwork port. The fetch
// rides the SAME API-server node proxy as cAdvisor, port-qualified
// (nodes/<name>:9100/proxy/metrics, doc 14 A2's dev path).
const nodeExporterPort = "9100"

// FetchNodeExporter fetches node-exporter payloads from the given nodes (the
// caller passes only nodes where the tool is actually running — presence is
// detected, doc 04 §3.1, so an undeployed exporter produces no error noise).
// Payload.Node carries the BARE node name: identity for this family comes from
// scrape-target metadata (doc 14 §3.2 row 3), never from the port-qualified
// proxy coordinate.
func FetchNodeExporter(ctx context.Context, f Fetcher, nodes []string) []NodePayload {
	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	out := make([]NodePayload, 0, len(sorted))
	for _, node := range sorted {
		body, receivedAt, err := f.NodeMetrics(ctx, node+":"+nodeExporterPort, "metrics")
		out = append(out, NodePayload{Node: node, Family: identity.FamilyNodeExporter, Body: body, ReceivedAt: receivedAt, Err: err})
	}
	return out
}

// PodFetcher fetches one pod's /metrics endpoint through the API server's
// pods/proxy subresource (doc 15 cap. A — the sibling of nodes/proxy). Separate
// from Fetcher so node-scoped fixtures stay unaffected.
type PodFetcher interface {
	PodMetrics(ctx context.Context, namespace, name, port, path string) (body []byte, receivedAt time.Time, err error)
}

// PodTarget is one application scrape target: the pod, the port, and the metrics
// path (from the customer's own prometheus.io/scrape annotations). Discovery of
// targets is the caller's job (doc 15 cap. A2); this lane only fetches+ingests.
type PodTarget struct {
	Namespace string
	Name      string
	Port      string // e.g. "8080"
	Path      string // e.g. "metrics" (no leading slash)
}

// FetchKSM fetches kube-state-metrics' /metrics payload(s) through the pods/proxy
// subresource (the SAME reach as app-metrics), in sorted (ns,name) order for
// determinism. Unlike FetchPodMetrics, KSM identity rides on the SERIES labels
// (namespace/pod/uid/node), NOT the scrape-target pod: KSM is a cluster-singleton
// that reports the whole cluster's object state, so PodNS/PodName are left empty
// and ingestExposition routes every series through normalize.ksm() by its own
// labels (doc 14 §3.2 row backbone). Family=FamilyKSM selects that dialect.
func FetchKSM(ctx context.Context, f PodFetcher, targets []PodTarget) []NodePayload {
	sorted := append([]PodTarget(nil), targets...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Namespace != sorted[j].Namespace {
			return sorted[i].Namespace < sorted[j].Namespace
		}
		return sorted[i].Name < sorted[j].Name
	})
	out := make([]NodePayload, 0, len(sorted))
	for _, t := range sorted {
		path := t.Path
		if path == "" {
			path = "metrics"
		}
		body, receivedAt, err := f.PodMetrics(ctx, t.Namespace, t.Name, t.Port, path)
		out = append(out, NodePayload{
			Node: t.Namespace + "/" + t.Name, Family: identity.FamilyKSM,
			Body: body, ReceivedAt: receivedAt, Err: err,
		})
	}
	return out
}

// FetchPodMetrics fetches every app target's /metrics payload (network only, no
// store writes), in sorted (ns,name) order for determinism. Each payload carries
// its target pod so ingest attributes every series to that pod's CEI — identity
// from the target, never the series labels.
func FetchPodMetrics(ctx context.Context, f PodFetcher, targets []PodTarget) []NodePayload {
	sorted := append([]PodTarget(nil), targets...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Namespace != sorted[j].Namespace {
			return sorted[i].Namespace < sorted[j].Namespace
		}
		return sorted[i].Name < sorted[j].Name
	})
	out := make([]NodePayload, 0, len(sorted))
	for _, t := range sorted {
		path := t.Path
		if path == "" {
			path = "metrics"
		}
		body, receivedAt, err := f.PodMetrics(ctx, t.Namespace, t.Name, t.Port, path)
		out = append(out, NodePayload{
			Node: t.Namespace + "/" + t.Name, Family: identity.FamilyApp,
			PodNS: t.Namespace, PodName: t.Name,
			Body: body, ReceivedAt: receivedAt, Err: err,
		})
	}
	return out
}

// IngestPayloads ingests previously-fetched payloads into the store (no network).
// A partial scrape is a stated partial, never a silent one.
func (in *Ingestor) IngestPayloads(payloads []NodePayload) IngestSummary {
	sum := IngestSummary{
		Nodes:            len(payloads),
		SeriesDropped:    map[string]int{},
		SeriesQuarantine: map[string]int{},
	}
	for _, p := range payloads {
		if p.Err != nil {
			sum.NodeErrors = append(sum.NodeErrors, fmt.Sprintf("%s: %v", p.Node, p.Err))
			continue
		}
		family := p.Family
		if family == "" {
			family = identity.FamilyCAdvisor
		}
		in.ingestExposition(p.Body, family, p.Node, p.PodNS, p.PodName, p.ReceivedAt, &sum)
	}
	return sum
}

// ScrapeCAdvisor fetches and ingests in one call (no gate). Callers that need
// evaluation ticks to see whole cycles use FetchCAdvisor + IngestPayloads with a
// store gate instead.
func (in *Ingestor) ScrapeCAdvisor(ctx context.Context, f Fetcher, nodes []string) IngestSummary {
	return in.IngestPayloads(FetchCAdvisor(ctx, f, nodes))
}

// ingestExposition parses one exposition payload and routes every scalar sample
// through the normalizer into the hot store. All stored times are canonicalized
// to UTC so fingerprints (and their replay digests) are timezone-independent —
// a bundle captured on one machine must replay byte-identically on another.
func (in *Ingestor) ingestExposition(body []byte, family identity.Family, node, podNS, podName string, receivedAt time.Time, sum *IngestSummary) {
	receivedAt = receivedAt.UTC()
	// UTF8Validation accepts every name LegacyValidation does plus UTF-8 names;
	// kubelet/cAdvisor emit classic charset, so this is permissive at the parse
	// edge — the identity normalizer is the actual gatekeeper.
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		sum.NodeErrors = append(sum.NodeErrors, fmt.Sprintf("%s: parse: %v", node, err))
		return
	}
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic ingest order
	for _, name := range names {
		mf := families[name]
		typ, ok := scalarType(mf)
		if !ok {
			sum.SkippedFamilies++
			continue
		}
		for _, m := range mf.GetMetric() {
			value, ok := scalarValue(mf, m)
			if !ok {
				continue
			}
			// The exposition format permits NaN/±Inf, but they carry no usable
			// telemetry for threshold/rate arithmetic — and a non-finite value
			// stored in a ring would poison every window computation over it and
			// cannot be canonically JSON-encoded (the replay digest). Refused at
			// the gate, counted, never stored (doc 05 §3.1: streams hold numbers
			// the primitives can do arithmetic on).
			if math.IsNaN(value) || math.IsInf(value, 0) {
				sum.SeriesDropped[reasonNonFiniteValue]++
				continue
			}
			labels := make(map[string]string, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			s := identity.Series{
				Family:        family,
				Metric:        name,
				Labels:        labels,
				SourceNode:    node,
				SourcePodNS:   podNS,
				SourcePodName: podName,
				At:            receivedAt,
			}
			// cAdvisor stamps samples with collection time (ms) — the JOIN time.
			if ts := m.GetTimestampMs(); ts != 0 {
				s.EventTime = time.UnixMilli(ts).UTC()
			}
			res := in.norm.Normalize(s)
			switch res.Outcome {
			case identity.OutcomeResolved:
				streamID := res.CEI.Key() + "|" + name + streamSubID(family, labels)
				sampleAt := s.EventTime
				if sampleAt.IsZero() {
					sampleAt = receivedAt
				}
				sample := qss.Sample{At: sampleAt, Value: value}
				in.hot.Append(streamID, sample)
				in.mu.Lock()
				if _, seen := in.meta[streamID]; !seen {
					in.meta[streamID] = StreamMeta{
						CEIKey: res.CEI.Key(), UID: res.CEI.UID, Kind: res.CEI.Kind,
						Metric: name, Type: typ, Node: node, Cadence: "scrape",
					}
				}
				in.mu.Unlock()
				if in.tap != nil {
					in.tap(qss.StreamDef{
						ID: streamID, CEIKey: res.CEI.Key(), UID: res.CEI.UID, Kind: res.CEI.Kind,
						Metric: name, Type: typ, Node: node, Cadence: "scrape",
					}, receivedAt, sample)
				}
				sum.SeriesResolved++
				sum.SamplesStored++
				// KSM derivation: a multi-dimensional KSM gauge (kube_pod_status_reason
				// {reason}, kube_node_status_condition{condition,status}) is ambiguous to
				// the matcher (it needs ONE series per (entity,metric)). Re-emit the
				// labelled row that carries the signal under a clean single-series metric
				// when the row is ACTIVE (value>0) — a deterministic projection, part of
				// the KSM dialect like normalize.ksm itself; the BAR stays authored.
				if family == identity.FamilyKSM && value > 0 {
					in.deriveKSM(s, typ, sampleAt, receivedAt)
				}
			case identity.OutcomeDropped:
				sum.SeriesDropped[string(res.Reason)]++
			case identity.OutcomeQuarantined:
				sum.SeriesQuarantine[string(res.Reason)]++
			}
		}
	}
}

// streamSubID disambiguates label-dimensioned series within one (CEI, metric).
// node-exporter resolves EVERY series to the node CEI (identity from scrape
// target, doc 14 §3.2 row 3), so a multi-series family like
// node_filesystem_avail_bytes{device,mountpoint} would otherwise interleave
// unrelated dimensions in ONE ring — a stream identity mis-join. The full sorted
// label set becomes part of the stream identity instead. Single-series metrics
// (MemAvailable, conntrack, PSI, vmstat) carry no labels and keep clean IDs;
// multi-series ones land as distinct streams, and a (UID, metric) join that
// finds several is an AMBIGUITY the materializer states rather than guesses
// (sub-variable normalization is the doc 02 data_type queue, not this layer).
// cAdvisor/KSM identity rides on the labels themselves and stays unchanged.
func streamSubID(family identity.Family, labels map[string]string) string {
	// cAdvisor is one-series-per-(CEI,metric): its labels fold ENTIRELY into the
	// container CEI, so no sub-id is needed. node-exporter, app, AND KSM each
	// resolve MULTIPLE series to ONE entity CEI — node-exporter/app from the
	// scrape-target's many label dimensions, KSM because one object's gauge family
	// carries dimension labels (kube_node_status_condition{condition,status},
	// kube_pod_status_reason{reason}). Without the full sorted label set as part of
	// the stream identity, those unrelated dimensions would collapse into one ring
	// — a mis-join. (A single-series KSM gauge like restarts_total just gets a
	// redundant, harmless sub-id from its identity labels.)
	if family == identity.FamilyCAdvisor || len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	b.WriteByte('}')
	return b.String()
}

// ksmDerivation selects ONE labelled row of a multi-series KSM gauge and re-emits it
// under a clean single-series metric name. The matcher's variable lookup requires
// exactly one series per (entity, metric); a raw multi-dimensional KSM family —
// kube_pod_status_reason{reason}, kube_node_status_condition{condition,status} — has
// several, so a check could not bind it. The selection is a deterministic projection
// of a MEASURED fact (which row carries the signal), part of the KSM dialect the way
// normalize.ksm() is — NOT a learned threshold; the BAR stays authored in the overlay.
// derived keeps the source's kube_pod_/kube_node_ prefix so normalize.ksm() routes the
// derived series to the SAME CEI as its parent.
type ksmDerivation struct {
	source  string            // the multi-series KSM metric, e.g. "kube_pod_status_reason"
	match   map[string]string // ALL must match the row's labels, e.g. {"reason":"Evicted"}
	derived string            // the clean single-series name, e.g. "kube_pod_status_evicted"
}

// ksmDerivations is the authored selection set (the KSM dialect's multi-series rows).
// Each is referenced by an overlay check's `metric:` and must keep its parent's prefix.
var ksmDerivations = []ksmDerivation{
	// Memory-pressure eviction: the Evicted reason row of a pod's status-reason gauge
	// (PHEN_EVICTION_MEMORY pod-side member, one runs-on hop from the node anchor).
	{source: "kube_pod_status_reason", match: map[string]string{"reason": "Evicted"}, derived: "kube_pod_status_evicted"},
	// Disk/inode pressure: the DiskPressure condition-true row of a node's status-condition
	// gauge (PHEN_DISK_PID_INODE_PRESSURE node anchor). DiskPressure IS the kubelet's own
	// eviction verdict — it already evaluated ITS configured nodefs.available / imagefs.available
	// / nodefs.inodesFree thresholds and set the condition. That is borrowed normativity at its
	// cleanest (the kubelet's number, never ours) with NO per-mountpoint ambiguity. The row is
	// =1 only while the node IS under disk/inode pressure (the status="false" healthy row carries
	// status!="true", so it never matches), so "above 0" is the pressure itself. Cluster-agnostic:
	// every kubelet computes this condition. (PID pressure shares this signal member; it is a
	// NAMED co-member on the degraded finding, a trivial follow-up once the derivation strips
	// selector labels so disk|pid can share one derived stream.)
	{source: "kube_node_status_condition", match: map[string]string{"condition": "DiskPressure", "status": "true"}, derived: "kube_node_status_disk_pressure"},
}

// deriveKSM re-emits the active selected rows of a KSM series under their clean
// single-series metric names (see ksmDerivation). Called only for FamilyKSM rows whose
// value is non-zero, so a derived stream exists EXACTLY for the entities in that state.
// It mirrors the resolved-store path; the derived sample rides the SAME tap (replay
// capture) so detection over it stays deterministic. Derived samples are projections,
// not scrapes — they are not counted in the ingest summary's scrape accounting.
func (in *Ingestor) deriveKSM(orig identity.Series, typ string, sampleAt, receivedAt time.Time) {
	for _, d := range ksmDerivations {
		if d.source != orig.Metric {
			continue
		}
		ok := true
		for k, v := range d.match {
			if orig.Labels[k] != v {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		ds := orig
		ds.Metric = d.derived
		res := in.norm.Normalize(ds)
		if res.Outcome != identity.OutcomeResolved {
			continue
		}
		streamID := res.CEI.Key() + "|" + d.derived + streamSubID(identity.FamilyKSM, ds.Labels)
		sample := qss.Sample{At: sampleAt, Value: 1}
		in.hot.Append(streamID, sample)
		in.mu.Lock()
		if _, seen := in.meta[streamID]; !seen {
			in.meta[streamID] = StreamMeta{
				CEIKey: res.CEI.Key(), UID: res.CEI.UID, Kind: res.CEI.Kind,
				Metric: d.derived, Type: typ, Node: ds.SourceNode, Cadence: "scrape",
			}
		}
		in.mu.Unlock()
		if in.tap != nil {
			in.tap(qss.StreamDef{
				ID: streamID, CEIKey: res.CEI.Key(), UID: res.CEI.UID, Kind: res.CEI.Kind,
				Metric: d.derived, Type: typ, Node: ds.SourceNode, Cadence: "scrape",
			}, receivedAt, sample)
		}
	}
}

// scalarType maps an exposition family to a scalar sample type; histogram and
// summary families are out of v1 ingest scope (counted, skipped).
func scalarType(mf *dto.MetricFamily) (string, bool) {
	switch mf.GetType() {
	case dto.MetricType_GAUGE:
		return "gauge", true
	case dto.MetricType_COUNTER:
		return "counter", true
	case dto.MetricType_UNTYPED:
		return "untyped", true
	default:
		return "", false
	}
}

func scalarValue(mf *dto.MetricFamily, m *dto.Metric) (float64, bool) {
	switch mf.GetType() {
	case dto.MetricType_GAUGE:
		return m.GetGauge().GetValue(), true
	case dto.MetricType_COUNTER:
		return m.GetCounter().GetValue(), true
	case dto.MetricType_UNTYPED:
		return m.GetUntyped().GetValue(), true
	default:
		return 0, false
	}
}

// Meta returns a stream's descriptor.
func (in *Ingestor) Meta(streamID string) (StreamMeta, bool) {
	in.mu.RLock()
	defer in.mu.RUnlock()
	m, ok := in.meta[streamID]
	return m, ok
}

// StreamsByUIDMetric finds streams by (CEI UID, metric) — the binding↔stream join
// used by semantic QA (binding knows pod UID + container; container streams carry
// UID = podUID/container).
func (in *Ingestor) StreamsByUIDMetric(uid, metric string) []string {
	in.mu.RLock()
	defer in.mu.RUnlock()
	var out []string
	for id, m := range in.meta {
		if m.Metric == metric && m.UID == uid {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// MetricNames returns the distinct resolved metric names (for the equivalence
// sweep: which of the cluster's actual names resolve through the dialect bridge).
func (in *Ingestor) MetricNames() []string {
	in.mu.RLock()
	defer in.mu.RUnlock()
	set := map[string]bool{}
	for _, m := range in.meta {
		set[m.Metric] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Hot exposes the underlying store (read-side: primitives, QA).
func (in *Ingestor) Hot() *qss.HotStore { return in.hot }

// --- StreamReader (the fingerprint materializer's read-side view) -----------

var _ StreamReader = (*Ingestor)(nil)

// StreamsFor aliases StreamsByUIDMetric to satisfy StreamReader.
func (in *Ingestor) StreamsFor(uid, metric string) []string {
	return in.StreamsByUIDMetric(uid, metric)
}

// Latest returns a stream's most recent sample.
func (in *Ingestor) Latest(streamID string) (qss.Sample, bool) { return in.hot.Latest(streamID) }

// LastN returns up to n most recent samples, oldest first.
func (in *Ingestor) LastN(streamID string, n int) []qss.Sample { return in.hot.LastN(streamID, n) }

// StreamType returns a stream's exposition type (gauge | counter | untyped).
func (in *Ingestor) StreamType(streamID string) (string, bool) {
	m, ok := in.Meta(streamID)
	return m.Type, ok
}

// String renders the summary compactly for logs.
func (s IngestSummary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "nodes=%d resolved=%d stored=%d skipped_families=%d", s.Nodes, s.SeriesResolved, s.SamplesStored, s.SkippedFamilies)
	if len(s.SeriesDropped) > 0 {
		fmt.Fprintf(&b, " dropped=%v", s.SeriesDropped)
	}
	if len(s.SeriesQuarantine) > 0 {
		fmt.Fprintf(&b, " quarantined=%v", s.SeriesQuarantine)
	}
	if len(s.NodeErrors) > 0 {
		fmt.Fprintf(&b, " node_errors=%v", s.NodeErrors)
	}
	return b.String()
}
