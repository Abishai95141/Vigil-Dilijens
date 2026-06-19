package qss

import (
	"math/rand"
	"testing"
	"time"
)

// These tests deepen the hot-ring mechanics (doc 14 §2.3): the ring invariant
// (population never exceeds capacity, the live window is exactly the last
// hotCapacity arrivals in arrival order), the dead-stream growth limit (a ring's
// footprint is bounded regardless of how many samples were ever appended), and
// the timestamp-newest Latest semantics under regressing event time.

// modelRing is an independent, obviously-correct reference: an unbounded arrival
// log whose "live view" is just its last hotCapacity entries. The ring under test
// must agree with it after every append, for any append sequence. If the real
// ring's wrap arithmetic regressed (off-by-one start, wrong modulus, dropped
// newest instead of oldest) the two would diverge and the test would fail.
type modelRing struct {
	log []Sample
}

func (m *modelRing) append(s Sample) { m.log = append(m.log, s) }

func (m *modelRing) liveView() []Sample {
	n := len(m.log)
	if n > hotCapacity {
		return append([]Sample(nil), m.log[n-hotCapacity:]...)
	}
	return append([]Sample(nil), m.log...)
}

// TestHotRingPropertyAppendOrdering drives many randomized append sequences of
// varying length (some far exceeding capacity) and asserts, after EACH append,
// that LastN(hotCapacity) equals the model's last-hotCapacity arrivals in
// arrival order. Arrival order — not event-time order — is the contract: the
// store stores and retrieves, it does not reorder.
func TestHotRingPropertyAppendOrdering(t *testing.T) {
	for seed := int64(0); seed < 64; seed++ {
		seed := seed
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			h := NewHotStore()
			var m modelRing

			// Length deliberately spans below, at, and well above capacity.
			steps := rng.Intn(hotCapacity*3) + 1
			for step := 0; step < steps; step++ {
				// Event time deliberately jitters (including regressions) to
				// prove arrival order is preserved independent of At.
				jitter := time.Duration(rng.Intn(2001)-1000) * time.Millisecond
				s := Sample{
					At:    t0.Add(time.Duration(step)*15*time.Second + jitter),
					Value: rng.NormFloat64(),
				}
				h.Append("s", s)
				m.append(s)

				want := m.liveView()
				got := h.LastN("s", hotCapacity)
				if len(got) != len(want) {
					t.Fatalf("seed %d step %d: population = %d, want %d", seed, step, len(got), len(want))
				}
				// Ring invariant: population never exceeds capacity.
				if len(got) > hotCapacity {
					t.Fatalf("seed %d step %d: ring overflowed capacity: %d", seed, step, len(got))
				}
				for i := range want {
					if got[i].At != want[i].At || got[i].Value != want[i].Value {
						t.Fatalf("seed %d step %d idx %d: got %+v, want %+v (order/window broken)",
							seed, step, i, got[i], want[i])
					}
				}
			}
		})
	}
}

// TestHotRingLastNMonotoneWindows asserts the nested-window property: for any
// k1 <= k2, LastN(k1) is exactly the suffix (newest k1) of LastN(k2). A ring
// whose lastN computed the wrong start offset would violate this even when the
// full-window test happened to pass.
func TestHotRingLastNMonotoneWindows(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	h := NewHotStore()
	for i := 0; i < hotCapacity+137; i++ {
		h.Append("s", Sample{At: t0.Add(time.Duration(i) * time.Second), Value: rng.Float64()})
	}
	full := h.LastN("s", hotCapacity)
	for _, k := range []int{0, 1, 7, 60, 120, 239, 240} {
		got := h.LastN("s", k)
		if len(got) != k {
			t.Fatalf("LastN(%d) length = %d", k, len(got))
		}
		wantSuffix := full[len(full)-k:]
		for i := range got {
			if got[i] != wantSuffix[i] {
				t.Fatalf("LastN(%d)[%d] = %+v, want suffix %+v", k, i, got[i], wantSuffix[i])
			}
		}
	}
}

// TestHotRingDeadStreamGrowthLimit is the stated dead-stream growth bound for the
// hot tier (doc 14 §2.3 sizing): a stream's in-memory footprint is fixed at the
// ring capacity no matter how many samples it ever received. We append far more
// than capacity and assert the retrievable population is pinned at hotCapacity —
// the ring sheds, it never grows. A buggy ring that grew a slice per append would
// fail because the window would exceed capacity.
func TestHotRingDeadStreamGrowthLimit(t *testing.T) {
	h := NewHotStore()
	const fed = hotCapacity * 50
	for i := 0; i < fed; i++ {
		h.Append("dead", Sample{At: t0.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	// Over-ask must still be clamped to capacity (the bound is the capacity).
	got := h.LastN("dead", fed)
	if len(got) != hotCapacity {
		t.Fatalf("dead-stream window = %d, want exactly hotCapacity %d (footprint must be bounded)", len(got), hotCapacity)
	}
	// And it must be the NEWEST capacity samples, contiguous and in order.
	if got[0].Value != float64(fed-hotCapacity) || got[len(got)-1].Value != float64(fed-1) {
		t.Fatalf("dead-stream window = [%v..%v], want newest [%d..%d]",
			got[0].Value, got[len(got)-1].Value, fed-hotCapacity, fed-1)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Value != got[i-1].Value+1 {
			t.Fatalf("dead-stream window not contiguous at %d: %v after %v", i, got[i].Value, got[i-1].Value)
		}
	}
	// HotCapacity() is the digest-bearing pin replay refuses a mismatch on; it
	// must equal the actual structural capacity the dead-stream bound rests on.
	if HotCapacity() != hotCapacity {
		t.Fatalf("HotCapacity() = %d, diverges from structural capacity %d", HotCapacity(), hotCapacity)
	}
}

// TestHotRingLatestNewestByEventTime pins the Latest contract (hot.go: Latest
// returns the TIMESTAMP-newest sample, not the last appended one, because
// cAdvisor timestamps can regress across scrapes). We append a sequence whose
// arrival tail is NOT the freshest by event time; Latest must still return the
// event-time max. A Latest that returned the arrival tail would fail here.
func TestHotRingLatestNewestByEventTime(t *testing.T) {
	h := NewHotStore()
	// Arrivals (in order) with deliberately non-monotone event time; the max At
	// is in the MIDDLE, and the last arrival regresses.
	ats := []time.Duration{
		0,
		30 * time.Second,
		90 * time.Second, // freshest by event time
		60 * time.Second,
		45 * time.Second, // arrival tail, but stale
	}
	for i, d := range ats {
		h.Append("s", Sample{At: t0.Add(d), Value: float64(i)})
	}
	got, ok := h.Latest("s")
	if !ok {
		t.Fatal("Latest missing")
	}
	if !got.At.Equal(t0.Add(90 * time.Second)) {
		t.Fatalf("Latest.At = %v, want the event-time max %v", got.At, t0.Add(90*time.Second))
	}
	if got.Value != 2 {
		t.Fatalf("Latest.Value = %v, want 2 (the freshest by event time, not the arrival tail)", got.Value)
	}
}

// TestHotRingLatestAfterWrapScansLiveWindowOnly confirms Latest considers only
// the live (post-wrap) window, never an evicted-but-still-in-buffer ghost. The
// largest event time belongs to an EVICTED sample; Latest must ignore it.
func TestHotRingLatestAfterWrapScansLiveWindowOnly(t *testing.T) {
	h := NewHotStore()
	// First sample carries a huge event time, then gets evicted by a full lap.
	h.Append("s", Sample{At: t0.Add(1000 * time.Hour), Value: -1})
	for i := 0; i < hotCapacity; i++ { // hotCapacity more arrivals evict the first
		h.Append("s", Sample{At: t0.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	got, ok := h.Latest("s")
	if !ok {
		t.Fatal("Latest missing")
	}
	if got.At.Equal(t0.Add(1000*time.Hour)) || got.Value == -1 {
		t.Fatalf("Latest returned an EVICTED sample (%+v) — must scan the live window only", got)
	}
	if !got.At.Equal(t0.Add(time.Duration(hotCapacity-1) * time.Second)) {
		t.Fatalf("Latest.At = %v, want newest live %v", got.At, t0.Add(time.Duration(hotCapacity-1)*time.Second))
	}
}

// TestHotRingStreamsIndependent proves rings are per-stream isolated: appends to
// one stream never perturb another's window or population. A shared/aliased ring
// map value would cross-contaminate and fail this.
func TestHotRingStreamsIndependent(t *testing.T) {
	h := NewHotStore()
	for i := 0; i < hotCapacity+10; i++ {
		h.Append("a", Sample{At: t0.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	h.Append("b", Sample{At: t0, Value: 7})

	if got := h.LastN("b", 10); len(got) != 1 || got[0].Value != 7 {
		t.Fatalf("stream b polluted by a's appends: %+v", got)
	}
	if got := h.LastN("a", hotCapacity+50); len(got) != hotCapacity {
		t.Fatalf("stream a window = %d, want capped at %d", len(got), hotCapacity)
	}
	if h.Streams() != 2 {
		t.Fatalf("Streams() = %d, want 2", h.Streams())
	}
}
