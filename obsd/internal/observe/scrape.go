package observe

import (
	"bytes"
	"context"
	"fmt"
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
// depends on (doc 05 §3.5).
type NodePayload struct {
	Node       string
	Body       []byte
	ReceivedAt time.Time
	Err        error
}

// FetchCAdvisor fetches every node's cAdvisor payload (network only, no store
// writes). Nodes are fetched in sorted order; failures are carried, not fatal.
func FetchCAdvisor(ctx context.Context, f Fetcher, nodes []string) []NodePayload {
	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	out := make([]NodePayload, 0, len(sorted))
	for _, node := range sorted {
		body, receivedAt, err := f.NodeMetrics(ctx, node, "metrics/cadvisor")
		out = append(out, NodePayload{Node: node, Body: body, ReceivedAt: receivedAt, Err: err})
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
		in.ingestExposition(p.Body, identity.FamilyCAdvisor, p.Node, p.ReceivedAt, &sum)
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
func (in *Ingestor) ingestExposition(body []byte, family identity.Family, node string, receivedAt time.Time, sum *IngestSummary) {
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
			labels := make(map[string]string, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			s := identity.Series{
				Family:     family,
				Metric:     name,
				Labels:     labels,
				SourceNode: node,
				At:         receivedAt,
			}
			// cAdvisor stamps samples with collection time (ms) — the JOIN time.
			if ts := m.GetTimestampMs(); ts != 0 {
				s.EventTime = time.UnixMilli(ts).UTC()
			}
			res := in.norm.Normalize(s)
			switch res.Outcome {
			case identity.OutcomeResolved:
				streamID := res.CEI.Key() + "|" + name
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
			case identity.OutcomeDropped:
				sum.SeriesDropped[string(res.Reason)]++
			case identity.OutcomeQuarantined:
				sum.SeriesQuarantine[string(res.Reason)]++
			}
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
