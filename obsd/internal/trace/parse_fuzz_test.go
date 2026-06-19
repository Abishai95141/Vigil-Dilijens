package trace

import (
	"reflect"
	"strings"
	"testing"
)

// FuzzParseSpans fuzzes the spans-JSONL parser (the only untrusted-input boundary
// in the lane: an operator wires --traces-path to an OTel file exporter whose lines
// we do not control). The parser's contract — and the invariants asserted here —
// is: a malformed line is SKIPPED, never fatal; no panic on any byte sequence; and
// every returned span satisfies the parser's own guard (non-empty SpanID + Service).
// We also assert that the parsed slice flows into BuildCallGraph without panic and
// that the downstream graph stays order-invariant on real (possibly multi-line)
// fuzz input — so the determinism guarantee is exercised on adversarial corpora,
// not just hand-picked fixtures.
//
// Run:  go test -run=^$ -fuzz=FuzzParseSpans ./obsd/internal/trace/
// (The unit run below just replays the seed corpus, keeping the suite hermetic and
// fast; the maintainer's go test does not invoke -fuzz.)
func FuzzParseSpans(f *testing.F) {
	// Seed corpus: valid lines, malformed JSON, blanks, guard-failing rows, CRLF,
	// huge/odd content, and a multi-line batch (the real input shape).
	seeds := []string{
		`{"traceId":"t","spanId":"a","parentSpanId":"","service":"frontend","name":"x","startTime":"2026-06-18T12:00:00Z","endTime":"2026-06-18T12:00:00.1Z","error":false}`,
		`{"spanId":"b","parentSpanId":"a","service":"checkout"}`,
		`{ not valid json — skip me }`,
		``,
		`   `,
		`{"spanId":"","service":"x"}`, // empty spanID → guarded out
		`{"spanId":"c","service":""}`, // empty service → guarded out
		`{"spanId":"d","service":"s","error":1}` + "\r", // trailing CR + wrong type for bool
		`{"spanId":"e","parentSpanId":"d","service":"s2","startTime":"bad","endTime":"also-bad"}`,
		"{}",
		"[]",
		"null",
		"\x00\x01\x02",
		strings.Repeat(`{"spanId":"x","service":"y"}`+"\n", 4),
		`{"traceId":"t","spanId":"p","parentSpanId":"","service":"a"}` + "\n" +
			`{"traceId":"t","spanId":"q","parentSpanId":"p","service":"b"}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, blob string) {
		// Split exactly as the live collector would: line per span.
		lines := strings.Split(blob, "\n")

		// Invariant 1: parsing never panics, and every survivor passes the guard.
		spans := ParseSpans(lines)
		for i, s := range spans {
			if s.SpanID == "" {
				t.Fatalf("ParseSpans returned a span with empty SpanID at %d: %+v", i, s)
			}
			if s.Service == "" {
				t.Fatalf("ParseSpans returned a span with empty Service at %d: %+v", i, s)
			}
		}
		// Never returns more spans than there were lines (one span per line at most).
		if len(spans) > len(lines) {
			t.Fatalf("ParseSpans returned %d spans from %d lines", len(spans), len(lines))
		}

		// Invariant 2: idempotent / pure — re-parsing identical input is identical.
		again := ParseSpans(lines)
		if !reflect.DeepEqual(spans, again) {
			t.Fatalf("ParseSpans not pure: re-parse differs")
		}

		// Invariant 3: the parsed slice flows into the aggregator without panic AND
		// the resulting graph is invariant to span order — the core determinism
		// guarantee, exercised on adversarial input.
		g := BuildCallGraph(spans)
		rev := make([]Span, len(spans))
		for i := range spans {
			rev[i] = spans[len(spans)-1-i]
		}
		gRev := BuildCallGraph(rev)
		if !reflect.DeepEqual(g, gRev) {
			t.Fatalf("BuildCallGraph not order-invariant on fuzz input:\n%+v\n%+v", g, gRev)
		}

		// Census accounting must stay self-consistent: counts are non-negative and
		// SpansObserved equals the parsed-span count (every observed span is counted).
		if g.SpansObserved != len(spans) {
			t.Fatalf("SpansObserved=%d but parsed %d spans", g.SpansObserved, len(spans))
		}
		if g.OrphanSpans < 0 || g.Traces < 0 {
			t.Fatalf("negative census counters: %+v", g)
		}
		if g.OrphanSpans > len(spans) {
			t.Fatalf("more orphans (%d) than spans (%d)", g.OrphanSpans, len(spans))
		}
		// Every edge is cross-service (the builder must never emit a self-call) and
		// has at least one call backing it.
		for _, e := range g.Edges {
			if e.Caller == e.Callee {
				t.Fatalf("BuildCallGraph emitted a same-service edge: %+v", e)
			}
			if e.Calls < 1 {
				t.Fatalf("edge with non-positive call count: %+v", e)
			}
			if e.Errors < 0 || e.Errors > e.Calls {
				t.Fatalf("edge error count out of range: %+v", e)
			}
		}
	})
}
