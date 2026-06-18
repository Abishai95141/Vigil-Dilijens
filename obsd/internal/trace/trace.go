package trace

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// Span is one MEASURED span — the minimal mirror of an OpenTelemetry span the call-graph
// builder needs. Carries no OTLP/client-go dependency; the collector maps the real span
// onto this. Service is the span's service.name resource attribute.
type Span struct {
	TraceID      string    `json:"traceId"`
	SpanID       string    `json:"spanId"`
	ParentSpanID string    `json:"parentSpanId"`
	Service      string    `json:"service"`
	Name         string    `json:"name"`
	StartTime    time.Time `json:"startTime"`
	EndTime      time.Time `json:"endTime"`
	StatusError  bool      `json:"error"`
}

// CallEdge is one MEASURED directed edge of the OBSERVED call graph: caller service
// invoked callee service, witnessed by parent→child span linkage across a service
// boundary. The arrow is OBSERVED STRUCTURE (who called whom), NOT an inferred cause.
// Counts + latency percentiles are aggregated over the callee spans in the batch.
type CallEdge struct {
	Caller    string  `json:"caller"`
	Callee    string  `json:"callee"`
	Calls     int     `json:"calls"`
	Errors    int     `json:"errors"`
	P50Millis float64 `json:"p50Millis"`
	P95Millis float64 `json:"p95Millis"`
	MaxMillis float64 `json:"maxMillis"`
}

// CallGraph is the MEASURED observed call graph over a span batch, WITH the census
// accounting — spans are sampled, so the graph is partial, and that partiality is counted
// and surfaced (never hidden).
type CallGraph struct {
	Edges         []CallEdge `json:"edges"`
	SpansObserved int        `json:"spansObserved"`
	Traces        int        `json:"traces"`
	OrphanSpans   int        `json:"orphanSpans"` // child spans whose parent was not in the batch (caller unknowable)
}

// ServiceResolver maps a service.name to a durable workload role CEI. main backs it with
// the identity store so the core stays decoupled from identity. A miss returns ("", false)
// and the candidate references the raw service name (flagged), never a guessed CEI.
type ServiceResolver func(service string) (roleCEI string, resolved bool)

// ParseSpans parses spans JSONL (one Span per line) into a span slice. A malformed line is
// skipped, never fatal. Order is preserved; BuildCallGraph is order-invariant regardless.
func ParseSpans(lines []string) []Span {
	out := make([]Span, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var s Span
		if err := json.Unmarshal([]byte(ln), &s); err != nil {
			continue
		}
		if s.SpanID == "" || s.Service == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// BuildCallGraph aggregates spans into the observed service call graph. A cross-service
// parent→child linkage (parent.Service != child.Service) is one observed call: the edge
// is caller=parent.Service → callee=child.Service, and the call's latency is the CHILD
// span's duration (time spent in the callee, the servicegraph convention). A child whose
// parent span is not in the batch is an ORPHAN (caller unknowable) — counted, never
// guessed. Pure + deterministic: same spans ⇒ byte-identical graph, INVARIANT to span
// order (aggregation keys + percentiles are computed over sorted data; edges sorted).
func BuildCallGraph(spans []Span) CallGraph {
	byID := make(map[string]Span, len(spans))
	traceSet := make(map[string]struct{})
	for _, s := range spans {
		// Deterministic dedup: within one valid trace a spanID is unique, but a merged/
		// concatenated export can collide a spanID onto differing content. Keep a FIXED
		// winner by a total order (never last-write-wins) so a child's resolved caller —
		// and the staged candidate — is invariant to span arrival order.
		if existing, ok := byID[s.SpanID]; ok && lessSpan(existing, s) {
			// keep the existing (smaller) span
		} else {
			byID[s.SpanID] = s
		}
		if s.TraceID != "" {
			traceSet[s.TraceID] = struct{}{}
		}
	}

	type agg struct {
		calls     int
		errors    int
		durations []float64
	}
	edges := make(map[string]*agg)
	orphans := 0
	for _, child := range spans {
		if child.ParentSpanID == "" {
			continue // a root span (entry point) has no caller
		}
		parent, ok := byID[child.ParentSpanID]
		if !ok {
			orphans++ // parent not sampled — the caller is unknowable
			continue
		}
		if parent.Service == child.Service {
			continue // intra-service linkage is not a service-to-service call
		}
		key := parent.Service + "\x00" + child.Service
		a := edges[key]
		if a == nil {
			a = &agg{}
			edges[key] = a
		}
		a.calls++
		if child.StatusError {
			a.errors++
		}
		a.durations = append(a.durations, durationMillis(child))
	}

	out := make([]CallEdge, 0, len(edges))
	for key, a := range edges {
		parts := strings.SplitN(key, "\x00", 2)
		sort.Float64s(a.durations)
		out = append(out, CallEdge{
			Caller: parts[0], Callee: parts[1], Calls: a.calls, Errors: a.errors,
			P50Millis: percentile(a.durations, 0.50),
			P95Millis: percentile(a.durations, 0.95),
			MaxMillis: percentile(a.durations, 1.0),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Caller != out[j].Caller {
			return out[i].Caller < out[j].Caller
		}
		return out[i].Callee < out[j].Callee
	})
	return CallGraph{Edges: out, SpansObserved: len(spans), Traces: len(traceSet), OrphanSpans: orphans}
}

// lessSpan is a total order over spans sharing one spanID, used to pick a deterministic
// winner on a (pathological) spanID collision so dedup never depends on arrival order.
func lessSpan(a, b Span) bool {
	if a.Service != b.Service {
		return a.Service < b.Service
	}
	if a.ParentSpanID != b.ParentSpanID {
		return a.ParentSpanID < b.ParentSpanID
	}
	if !a.StartTime.Equal(b.StartTime) {
		return a.StartTime.Before(b.StartTime)
	}
	if !a.EndTime.Equal(b.EndTime) {
		return a.EndTime.Before(b.EndTime)
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.TraceID < b.TraceID
}

func durationMillis(s Span) float64 {
	d := s.EndTime.Sub(s.StartTime)
	if d < 0 {
		return 0
	}
	return float64(d) / float64(time.Millisecond)
}

// percentile returns the p-quantile (p in [0,1]) of a SORTED slice by linear
// interpolation between closest ranks — deterministic. Empty ⇒ 0.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// ProposeTopology stages a STRUCTURAL topology candidate for each observed call edge: a
// candidate.KindEdge with relation "topology" (never causal — the store rejects a causal
// edge by construction), for a NAMED human to promote into the authored graph. The
// service endpoints resolve to workload role CEIs when the identity store knows them
// (richer join); otherwise the candidate references the raw service name, flagged. Each
// candidate's evidence states the graph is from SAMPLED traces (census-incomplete). Pure +
// deterministic: output sorted by subject.
func ProposeTopology(g CallGraph, r ServiceResolver, clusterID, graphVersion string) []candidate.Candidate {
	out := make([]candidate.Candidate, 0, len(g.Edges))
	for _, e := range g.Edges {
		callerCEI, callerOK := resolve(r, e.Caller)
		calleeCEI, calleeOK := resolve(r, e.Callee)
		subject := "trace-call:" + e.Caller + "->" + e.Callee
		out = append(out, candidate.Candidate{
			Kind:     candidate.KindEdge,
			Relation: "topology",
			Subject:  subject,
			Payload: map[string]any{
				"caller":         e.Caller,
				"callee":         e.Callee,
				"callerCei":      callerCEI,
				"calleeCei":      calleeCEI,
				"callerResolved": callerOK,
				"calleeResolved": calleeOK,
				"calls":          e.Calls,
				"errors":         e.Errors,
				"p95Millis":      e.P95Millis,
			},
			Evidence: []candidate.EvidenceRef{
				{Kind: "observed-call", Ref: e.Caller + "->" + e.Callee,
					Detail: "calls=" + strconv.Itoa(e.Calls) + " errors=" + strconv.Itoa(e.Errors) + " p95=" + strconv.FormatFloat(e.P95Millis, 'f', 1, 64) + "ms"},
				{Kind: "census", Ref: "sampled-traces",
					Detail: "observed from sampled spans (census-incomplete); the call graph may be partial"},
			},
			Lineage: candidate.Lineage{
				Source: "trace", Method: "span-parent-child", GraphVersion: graphVersion,
				Inputs: []string{"service=" + e.Caller, "service=" + e.Callee},
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}

func resolve(r ServiceResolver, service string) (string, bool) {
	if r == nil {
		return "", false
	}
	cei, ok := r(service)
	if !ok || cei == "" {
		return "", false
	}
	return cei, true
}

// ProposeAndStage builds the call graph, proposes the topology candidates, and stages
// them into the firewalled P0 store, returning the count staged. `now` is injected. This
// is the testable per-cycle core of the trace loop; the runtime loop is a thin mapper.
func ProposeAndStage(s *candidate.Store, now time.Time, g CallGraph, r ServiceResolver, clusterID, graphVersion string) (int, error) {
	n := 0
	for _, c := range ProposeTopology(g, r, clusterID, graphVersion) {
		if _, err := s.Put(now, c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
