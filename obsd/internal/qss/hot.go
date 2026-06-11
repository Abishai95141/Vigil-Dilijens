package qss

import (
	"sort"
	"sync"
	"time"
)

// The hot tier (doc 14 §2.3): one fixed-capacity ring per stream, in memory, the
// only thing the hot path ever reads. Records are plain (ts, value) pairs — the
// 16-byte record of the doc. The store stores and retrieves; it NEVER interprets
// (doc 05 §3.1): no statistics, no smoothing, no interpolation live here.
//
// Warm segments (2 h append-only files), 7 d retention, and the Parquet replay
// bundle are the next qss milestone; their absence bounds replay to the hot
// window and is stated wherever the store is surfaced.

// Sample is one reading: a timestamp and a value.
type Sample struct {
	At    time.Time
	Value float64
}

// hotCapacity is the per-stream ring capacity: 60 min of 15 s scrape cadence
// (doc 14 §2.3 hot window / §5 scrape interval).
const hotCapacity = 240

// ring is a fixed-capacity circular buffer of samples, oldest overwritten first.
type ring struct {
	buf   [hotCapacity]Sample
	start int // index of the oldest sample
	n     int // population
}

func (r *ring) append(s Sample) {
	if r.n < hotCapacity {
		r.buf[(r.start+r.n)%hotCapacity] = s
		r.n++
		return
	}
	r.buf[r.start] = s
	r.start = (r.start + 1) % hotCapacity
}

func (r *ring) lastN(n int) []Sample {
	if n > r.n {
		n = r.n
	}
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		out[i] = r.buf[(r.start+r.n-n+i)%hotCapacity]
	}
	return out
}

// HotStore holds the hot rings, keyed by an opaque stream id (the caller keys by
// CEI + canonical variable — identity and naming are 03/04's business, not ours).
type HotStore struct {
	mu    sync.RWMutex
	rings map[string]*ring
}

// NewHotStore builds an empty hot store.
func NewHotStore() *HotStore {
	return &HotStore{rings: map[string]*ring{}}
}

// Append records one sample on a stream, creating the ring on first touch.
// Samples are stored in arrival order; the store does not reorder, deduplicate,
// or interpret (a regressing timestamp is the ingest layer's problem to flag).
func (h *HotStore) Append(streamID string, s Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rings[streamID]
	if !ok {
		r = &ring{}
		h.rings[streamID] = r
	}
	r.append(s)
}

// Latest returns the TIMESTAMP-newest sample on a stream — not merely the last
// appended one. The ring is arrival-ordered and cAdvisor timestamps can regress
// across scrapes (doc 14 A12), so the arrival tail is not always the freshest by
// event time. Scanning ≤hotCapacity records keeps the read O(n) and deterministic,
// and ensures a fingerprint's cited sample and staleness match the sample its rate
// was computed from. The store still stores-and-retrieves; it does not reorder.
func (h *HotStore) Latest(streamID string) (Sample, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.rings[streamID]
	if !ok || r.n == 0 {
		return Sample{}, false
	}
	newest := r.buf[r.start]
	for i := 1; i < r.n; i++ {
		s := r.buf[(r.start+i)%hotCapacity]
		if s.At.After(newest.At) {
			newest = s
		}
	}
	return newest, true
}

// LastN returns up to n most recent samples, oldest first.
func (h *HotStore) LastN(streamID string, n int) []Sample {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.rings[streamID]
	if !ok {
		return nil
	}
	return r.lastN(n)
}

// Streams returns the number of live rings.
func (h *HotStore) Streams() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rings)
}

// Keys returns all stream ids, sorted (deterministic iteration for reports).
func (h *HotStore) Keys() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.rings))
	for k := range h.rings {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
