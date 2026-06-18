package observe

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// DefaultHistogramQuantiles is the p-quantile set derived from HISTOGRAM families
// when the lane is enabled (doc 20 P0.5). A declared, versioned constant — NEVER fit
// to the data it is applied to (a data-fit cutoff would be a learned threshold).
var DefaultHistogramQuantiles = []float64{0.5, 0.95, 0.99}

type histQuantile struct {
	phi    float64
	suffix string
}

// SetHistogramQuantiles enables histogram->quantile derivation for the given φ set
// (e.g. DefaultHistogramQuantiles). nil/empty is the default OFF state — the v1 skip,
// byte-identical. φ outside (0,1) is ignored; duplicates collapse. The set is sorted
// for a deterministic derived-stream order. Set BEFORE scraping starts.
func (in *Ingestor) SetHistogramQuantiles(phis []float64) {
	cp := append([]float64(nil), phis...)
	sort.Float64s(cp)
	var qs []histQuantile
	seen := map[string]bool{}
	for _, phi := range cp {
		if phi <= 0 || phi >= 1 {
			continue
		}
		suf := pSuffix(phi)
		if seen[suf] {
			continue
		}
		seen[suf] = true
		qs = append(qs, histQuantile{phi: phi, suffix: suf})
	}
	in.histQ = qs
}

// pSuffix is the deterministic derived-metric suffix for a φ-quantile: 0.95 -> "_p95",
// 0.5 -> "_p50", 0.999 -> "_p99_9" (a clean single-series name, like deriveKSM's). The
// percentile is rounded to 6 decimals first so float noise (0.95*100 ==
// 95.00000000000001) never leaks into a stream name.
func pSuffix(phi float64) string {
	pct := math.Round(phi*100*1e6) / 1e6
	s := strconv.FormatFloat(pct, 'f', -1, 64) // 95, 50, 99.9
	return "_p" + strings.ReplaceAll(s, ".", "_")
}

// emitHistogramQuantiles derives one gauge stream per (series, φ) from a HISTOGRAM
// family and rides them through the SAME normalize/CEI/hot/tap path as a scraped
// gauge (so they are CEI-stamped, replay-captured, and deterministic). A derived
// quantile is MEASURED — a deterministic arithmetic consequence of the bucket counts.
func (in *Ingestor) emitHistogramQuantiles(mf *dto.MetricFamily, family identity.Family, node, podNS, podName string, receivedAt time.Time) {
	receivedAt = receivedAt.UTC()
	name := mf.GetName()
	for _, m := range mf.GetMetric() {
		h := m.GetHistogram()
		if h == nil {
			continue
		}
		labels := make(map[string]string, len(m.GetLabel()))
		for _, lp := range m.GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}
		var eventTime time.Time
		if ts := m.GetTimestampMs(); ts != 0 {
			eventTime = time.UnixMilli(ts).UTC()
		}
		for _, q := range in.histQ {
			v, ok := histogramQuantile(q.phi, h)
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			s := identity.Series{
				Family:        family,
				Metric:        name + q.suffix,
				Labels:        labels,
				SourceNode:    node,
				SourcePodNS:   podNS,
				SourcePodName: podName,
				At:            receivedAt,
				EventTime:     eventTime,
			}
			in.emitDerivedSample(s, "gauge", v, receivedAt)
		}
	}
}

// emitDerivedSample normalizes a DERIVED series and stores it exactly as the resolved
// scrape path does (append + meta + tap). A derived sample is a projection, so — like
// deriveKSM — it is not added to the scrape accounting; an unresolved derived series
// is silently dropped (its base family already accounts for the scrape).
func (in *Ingestor) emitDerivedSample(s identity.Series, typ string, value float64, receivedAt time.Time) {
	res := in.norm.Normalize(s)
	if res.Outcome != identity.OutcomeResolved {
		return
	}
	sampleAt := s.EventTime
	if sampleAt.IsZero() {
		sampleAt = receivedAt
	}
	streamID := res.CEI.Key() + "|" + s.Metric + streamSubID(s.Family, s.Labels)
	sample := qss.Sample{At: sampleAt, Value: value}
	in.hot.Append(streamID, sample)
	in.mu.Lock()
	if _, seen := in.meta[streamID]; !seen {
		in.meta[streamID] = StreamMeta{
			CEIKey: res.CEI.Key(), UID: res.CEI.UID, Kind: res.CEI.Kind,
			Metric: s.Metric, Type: typ, Node: s.SourceNode, Cadence: "scrape",
		}
	}
	in.mu.Unlock()
	if in.tap != nil {
		in.tap(qss.StreamDef{
			ID: streamID, CEIKey: res.CEI.Key(), UID: res.CEI.UID, Kind: res.CEI.Kind,
			Metric: s.Metric, Type: typ, Node: s.SourceNode, Cadence: "scrape",
		}, receivedAt, sample)
	}
}

// histogramQuantile computes the φ-quantile (0<φ<1) from cumulative histogram buckets
// using the same linear-interpolation method as Prometheus histogram_quantile.
// Returns (value, true) when computable; (0, false) for an empty or degenerate
// histogram (no buckets, zero observations, or only a +Inf bucket). Pure +
// deterministic.
func histogramQuantile(phi float64, h *dto.Histogram) (float64, bool) {
	if h == nil {
		return 0, false
	}
	raw := h.GetBucket()
	if len(raw) < 1 {
		return 0, false
	}
	type bkt struct {
		le float64
		c  float64
	}
	bs := make([]bkt, 0, len(raw)+1)
	for _, b := range raw {
		bs = append(bs, bkt{le: b.GetUpperBound(), c: float64(b.GetCumulativeCount())})
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].le < bs[j].le })
	// total = the count in the highest bucket (the +Inf bucket if present). Some
	// exporters omit +Inf but set SampleCount; use it when it is larger.
	total := bs[len(bs)-1].c
	if sc := float64(h.GetSampleCount()); sc > total {
		total = sc
		if !math.IsInf(bs[len(bs)-1].le, +1) {
			bs = append(bs, bkt{le: math.Inf(+1), c: sc})
		}
	}
	if total <= 0 {
		return 0, false
	}
	rank := phi * total
	i := 0
	for i < len(bs) && bs[i].c < rank {
		i++
	}
	if i >= len(bs) {
		i = len(bs) - 1
	}
	upper := bs[i].le
	if math.IsInf(upper, +1) {
		// The +Inf bucket has no finite bound: return the highest finite le (Prometheus).
		if i == 0 {
			return 0, false
		}
		return bs[i-1].le, true
	}
	lower, cLower := 0.0, 0.0
	if i > 0 {
		lower, cLower = bs[i-1].le, bs[i-1].c
	}
	cUpper := bs[i].c
	if cUpper <= cLower {
		return upper, true // no resolution within the bucket
	}
	return lower + (upper-lower)*((rank-cLower)/(cUpper-cLower)), true
}
