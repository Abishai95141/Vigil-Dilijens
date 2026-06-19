package qss

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These tests deepen the replay-bundle export path that lives in qss: the warm
// segment log is the READINGS component of the replay bundle (warm.go header),
// and byte-identical replay rests on (a) the export being a deterministic
// function of the append sequence, (b) ListSegments giving a total, stable order,
// (c) the segment-roll boundary being exact, and (d) growth being bounded. The
// replay engine (internal/replay) and the Parquet exporter (internal/replay/
// export) both consume exactly ListSegments + ReplayFrames + the run-start/def/
// sample/tick frames asserted here.

// segmentBytes reads every sealed segment in dir, in ListSegments order, and
// returns the concatenated on-disk bytes plus the ordered list of sealed paths.
// This is the exact byte stream a bundle consumer (Parquet export / replay) sees.
func segmentBytes(t *testing.T, dir string) ([]byte, []string) {
	t.Helper()
	segs, err := ListSegments(dir)
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	var buf bytes.Buffer
	var names []string
	for _, s := range segs {
		if !s.Sealed {
			t.Fatalf("export over an unsealed segment %s — bundle must be sealed", s.Path)
		}
		raw, err := os.ReadFile(s.Path)
		if err != nil {
			t.Fatalf("read %s: %v", s.Path, err)
		}
		buf.Write(raw)
		names = append(names, filepath.Base(s.Path))
	}
	return buf.Bytes(), names
}

// writeRun replays a fixed scripted append/tick sequence into a fresh store in
// dir and seals it. Two identical scripts on two dirs MUST yield byte-identical
// sealed segments — the export is a pure function of the append sequence.
func writeRun(t *testing.T, dir string) {
	t.Helper()
	w := mustOpen(t, dir, testCfg())
	r := warmBase.Add(time.Minute)
	// Deterministic, hand-rolled script touching every frame kind, def reuse,
	// multiple streams, and non-finite values (which must store bit-exact).
	steps := []struct {
		id   string
		recv time.Duration
		at   time.Duration
		val  float64
		tick string
	}{
		{id: "s1", recv: 0, at: -1 * time.Second, val: 1.5},
		{id: "s2", recv: 0, at: 0, val: -2.25},
		{tick: `{"eval":1,"digest":"abc"}`, recv: 1 * time.Second},
		{id: "s1", recv: 15 * time.Second, at: 14 * time.Second, val: 3},
		{id: "s3", recv: 16 * time.Second, at: 16 * time.Second, val: 1e308},
		{id: "s2", recv: 30 * time.Second, at: 29 * time.Second, val: 0},
		{tick: `{"eval":2}`, recv: 31 * time.Second},
	}
	for _, s := range steps {
		if s.tick != "" {
			if err := w.Tick(r.Add(s.recv), []byte(s.tick)); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			continue
		}
		if err := w.Append(def(s.id), r.Add(s.recv), Sample{At: r.Add(s.at), Value: s.val}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWarmExportByteIdentical is the core export-determinism guarantee: the same
// append sequence produces byte-identical sealed segments AND identical sealed
// filenames. A non-deterministic export (map iteration leaking into frame order,
// a wall-clock stamp, an uninitialized pad byte) would break the hash.
func TestWarmExportByteIdentical(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	writeRun(t, dirA)
	writeRun(t, dirB)

	bytesA, namesA := segmentBytes(t, dirA)
	bytesB, namesB := segmentBytes(t, dirB)

	if fmt.Sprint(namesA) != fmt.Sprint(namesB) {
		t.Fatalf("sealed names diverge:\n A=%v\n B=%v", namesA, namesB)
	}
	hA := sha256.Sum256(bytesA)
	hB := sha256.Sum256(bytesB)
	if hA != hB {
		t.Fatalf("export not byte-identical:\n A=%s\n B=%s", hex.EncodeToString(hA[:]), hex.EncodeToString(hB[:]))
	}
	if len(bytesA) == 0 {
		t.Fatal("export produced no bytes — fixture is vacuous")
	}
}

// TestWarmReplayMatchesRawBytes proves the DECODED frame stream is itself a
// deterministic, lossless image of the on-disk bytes: re-running ReplayFrames
// twice over the same sealed segment yields identical frame sequences, and the
// frame count/kinds match the script. This is what the replay engine relies on
// to reconstruct the hot rings exactly as live evaluation saw them.
func TestWarmReplayMatchesRawBytes(t *testing.T) {
	dir := t.TempDir()
	writeRun(t, dir)
	segs, _ := ListSegments(dir)
	if len(segs) != 1 {
		t.Fatalf("want 1 sealed segment, got %d", len(segs))
	}

	collect := func() string {
		var sb []byte
		err := ReplayFrames(segs[0].Path, func(f Frame) error {
			// A stable textual image of each frame's semantic content.
			sb = append(sb, fmt.Sprintf("k=%d idx=%d recv=%d at=%d val=%v def=%q pay=%q|",
				f.Kind, f.Idx, f.Recv.UnixNano(), f.At.UnixNano(), f.Value, f.Def.ID, string(f.Payload))...)
			return nil
		})
		if err != nil {
			t.Fatalf("ReplayFrames: %v", err)
		}
		return string(sb)
	}
	first := collect()
	second := collect()
	if first != second {
		t.Fatalf("ReplayFrames not deterministic:\n1=%s\n2=%s", first, second)
	}
	// run-start, def(s1), s1, def(s2), s2, tick, s1, def(s3), s3, s2, tick
	wantKinds := []byte{
		FrameRunStart, FrameDef, FrameSample, FrameDef, FrameSample,
		FrameTick, FrameSample, FrameDef, FrameSample, FrameSample, FrameTick,
	}
	var gotKinds []byte
	_ = ReplayFrames(segs[0].Path, func(f Frame) error { gotKinds = append(gotKinds, f.Kind); return nil })
	if !bytes.Equal(gotKinds, wantKinds) {
		t.Fatalf("frame-kind sequence = %v, want %v", gotKinds, wantKinds)
	}
}

// TestWarmPropertyReplayPreservesArrivalOrder is the export-side companion of the
// hot-ring property test: for arbitrary randomized append/tick sequences, the
// sample frames recovered by ReplayFrames (in file order, across rollovers) must
// equal the exact arrival sequence of (streamID, At, Value) appended — same
// elements, same order. This is THE invariant that makes byte-identical replay
// possible (re-appending the log in order rebuilds the rings as live saw them).
func TestWarmPropertyReplayPreservesArrivalOrder(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		seed := seed
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed + 1))
			dir := t.TempDir()
			w := mustOpen(t, dir, testCfg())

			type rec struct {
				id  string
				at  int64
				val float64
			}
			var want []rec
			// recv advances monotonically and sometimes leaps a full segment so
			// rollovers happen mid-sequence; arrival order must survive the seal.
			recv := warmBase.Add(time.Minute)
			ids := []string{"s1", "s2", "s3"}
			steps := rng.Intn(60) + 5
			for i := 0; i < steps; i++ {
				if rng.Intn(5) == 0 {
					if err := w.Tick(recv, []byte(fmt.Sprintf("t%d", i))); err != nil {
						t.Fatal(err)
					}
				} else {
					id := ids[rng.Intn(len(ids))]
					at := recv.Add(-time.Duration(rng.Intn(2000)) * time.Millisecond)
					val := rng.NormFloat64()
					if err := w.Append(def(id), recv, Sample{At: at, Value: val}); err != nil {
						t.Fatal(err)
					}
					want = append(want, rec{id: id, at: at.UTC().UnixNano(), val: val})
				}
				// Advance recv; occasionally jump past a segment boundary.
				if rng.Intn(7) == 0 {
					recv = recv.Add(2*time.Hour + time.Minute)
				} else {
					recv = recv.Add(time.Duration(rng.Intn(120)+1) * time.Second)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}

			// Walk every sealed segment in order, rebuilding the per-segment
			// index->StreamDef dictionary exactly as the replay engine does.
			segs, _ := ListSegments(dir)
			var got []rec
			for _, seg := range segs {
				if !seg.Sealed {
					t.Fatalf("unsealed segment after Close: %+v", seg)
				}
				idxDef := map[uint32]StreamDef{}
				err := ReplayFrames(seg.Path, func(f Frame) error {
					switch f.Kind {
					case FrameDef:
						idxDef[f.Idx] = f.Def
					case FrameSample:
						d, ok := idxDef[f.Idx]
						if !ok {
							t.Fatalf("sample idx %d with no preceding def in segment %s", f.Idx, seg.Path)
						}
						got = append(got, rec{id: d.ID, at: f.At.UnixNano(), val: f.Value})
					}
					return nil
				})
				if err != nil {
					t.Fatalf("ReplayFrames %s: %v", seg.Path, err)
				}
			}
			if len(got) != len(want) {
				t.Fatalf("seed %d: recovered %d samples, want %d", seed, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("seed %d sample %d: got %+v, want %+v (arrival order broken across rollover)",
						seed, i, got[i], want[i])
				}
			}
		})
	}
}

// TestWarmSegmentRollBoundaryExact pins the rollover boundary (ensureSegment:
// recv.Sub(segStart) < SegmentDuration keeps the current segment; >= rolls).
// segStart = recv.Truncate(SegmentDuration). A recv exactly at
// segStart+SegmentDuration must roll; one nanosecond before must NOT. An
// off-by-one in that comparison would split or merge segments wrongly and
// silently corrupt window evaluation at the seam.
func TestWarmSegmentRollBoundaryExact(t *testing.T) {
	seg := 2 * time.Hour
	cfg := WarmConfig{SegmentDuration: seg, Retention: 0}

	// segStart is base truncated to the segment; our first recv sits inside it.
	segStart := warmBase.UTC().Truncate(seg)
	if !segStart.Equal(warmBase) {
		t.Fatalf("test assumes warmBase is segment-aligned, got start %v", segStart)
	}

	t.Run("just_before_boundary_stays", func(t *testing.T) {
		dir := t.TempDir()
		w := mustOpen(t, dir, cfg)
		justBefore := segStart.Add(seg - time.Nanosecond)
		if err := w.Append(def("s1"), segStart.Add(time.Minute), Sample{At: segStart, Value: 1}); err != nil {
			t.Fatal(err)
		}
		if err := w.Append(def("s1"), justBefore, Sample{At: justBefore, Value: 2}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		segs, _ := ListSegments(dir)
		if len(segs) != 1 {
			t.Fatalf("recv 1ns before the boundary must stay in one segment, got %d: %+v", len(segs), segs)
		}
	})

	t.Run("at_boundary_rolls", func(t *testing.T) {
		dir := t.TempDir()
		w := mustOpen(t, dir, cfg)
		atBoundary := segStart.Add(seg) // exactly segStart + duration -> >= -> roll
		if err := w.Append(def("s1"), segStart.Add(time.Minute), Sample{At: segStart, Value: 1}); err != nil {
			t.Fatal(err)
		}
		if err := w.Append(def("s1"), atBoundary, Sample{At: atBoundary, Value: 2}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		segs, _ := ListSegments(dir)
		if len(segs) != 2 {
			t.Fatalf("recv exactly at the boundary must roll into a new segment, got %d: %+v", len(segs), segs)
		}
		if !segs[0].Start.Equal(segStart) || !segs[1].Start.Equal(segStart.Add(seg)) {
			t.Fatalf("rolled segment windows wrong: %+v", segs)
		}
	})
}

// TestWarmListSegmentsTotalOrder pins the total, stable order ListSegments
// guarantees even when two sealed segments share a start (a crash-recovered seal
// plus a post-restart seal in the same 2h window): order is start, then end,
// then path — never the non-stable sort's whim. Replay/export consume segments
// in this order, so a tie that reordered would change the reconstructed stream.
func TestWarmListSegmentsTotalOrder(t *testing.T) {
	dir := t.TempDir()
	// Hand-craft three sealed files: two share a start, one is earlier.
	mk := func(name string) {
		// Minimal valid-enough file (header only); ListSegments parses the NAME.
		if err := os.WriteFile(filepath.Join(dir, name), []byte(warmMagic), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// start=2000 end=9000, start=2000 end=5000, start=1000 end=3000
	mk(sealedFileName(2000, 9000))
	mk(sealedFileName(2000, 5000))
	mk(sealedFileName(1000, 3000))

	segs, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 {
		t.Fatalf("want 3 segments, got %d", len(segs))
	}
	wantOrder := []struct{ start, end int64 }{
		{1000, 3000}, // earliest start
		{2000, 5000}, // tie on start -> earlier end first
		{2000, 9000},
	}
	for i, w := range wantOrder {
		if segs[i].Start.UnixMilli() != w.start || segs[i].End.UnixMilli() != w.end {
			t.Fatalf("order[%d] = (%d,%d), want (%d,%d)",
				i, segs[i].Start.UnixMilli(), segs[i].End.UnixMilli(), w.start, w.end)
		}
	}
}

// sealedFileName mirrors warm.go's sealedName for test fixtures that need an
// on-disk sealed file with a chosen (start,end) without driving a real store.
func sealedFileName(startMs, endMs int64) string {
	return fmt.Sprintf("seg-%d-%d%s", startMs, endMs, sealedExt)
}

// TestWarmDeadStreamWarmGrowthLimit is the warm-tier dead-stream growth bound: a
// stream that stops appending costs ZERO additional bytes per segment. A def is
// emitted at most once per stream per segment (on first appearance), and a
// segment a dead stream never touches carries neither its def nor any sample for
// it. We open three rollovers; a "dead" stream appears only in the first segment
// and must be absent (no def, no sample) from the later two — bounded growth.
func TestWarmDeadStreamWarmGrowthLimit(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, testCfg())
	seg := 2 * time.Hour
	base := warmBase.Add(time.Minute)

	// Segment 0: both streams alive (and "dead" appended TWICE to prove the def
	// is emitted only once per segment even while alive).
	if err := w.Append(def("dead"), base, Sample{At: base, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(def("dead"), base.Add(15*time.Second), Sample{At: base, Value: 2}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(def("alive"), base.Add(30*time.Second), Sample{At: base, Value: 3}); err != nil {
		t.Fatal(err)
	}
	// Segments 1 and 2: only "alive" appends; "dead" is gone.
	for k := 1; k <= 2; k++ {
		r := base.Add(time.Duration(k) * seg)
		if err := w.Append(def("alive"), r, Sample{At: r, Value: float64(10 + k)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	segs, _ := ListSegments(dir)
	if len(segs) != 3 {
		t.Fatalf("want 3 sealed segments, got %d: %+v", len(segs), segs)
	}

	type counts struct{ deadDefs, deadSamples, aliveDefs int }
	for i, s := range segs {
		var c counts
		err := ReplayFrames(s.Path, func(f Frame) error {
			switch f.Kind {
			case FrameDef:
				if f.Def.ID == "dead" {
					c.deadDefs++
				}
				if f.Def.ID == "alive" {
					c.aliveDefs++
				}
			case FrameSample:
				// Resolve which stream this sample belongs to via the running def.
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// First segment: dead's def appears exactly once despite two appends.
			if c.deadDefs != 1 {
				t.Fatalf("segment 0: dead def count = %d, want exactly 1 (def emitted once per stream per segment)", c.deadDefs)
			}
		} else {
			// Later segments: the dead stream contributes NOTHING (bounded growth).
			if c.deadDefs != 0 {
				t.Fatalf("segment %d: dead stream re-emitted a def (%d) though it never appended there — growth not bounded", i, c.deadDefs)
			}
		}
		// Alive re-emits its def in every segment it touches (self-contained segments).
		if c.aliveDefs != 1 {
			t.Fatalf("segment %d: alive def count = %d, want 1 (each segment is self-contained)", i, c.aliveDefs)
		}
	}

	// Count dead samples across ALL segments: must be exactly the 2 we appended,
	// none leaked into later segments.
	deadSamples := 0
	for _, s := range segs {
		idxDef := map[uint32]string{}
		_ = ReplayFrames(s.Path, func(f Frame) error {
			switch f.Kind {
			case FrameDef:
				idxDef[f.Idx] = f.Def.ID
			case FrameSample:
				if idxDef[f.Idx] == "dead" {
					deadSamples++
				}
			}
			return nil
		})
	}
	if deadSamples != 2 {
		t.Fatalf("dead-stream samples across the bundle = %d, want exactly 2 (no growth after death)", deadSamples)
	}
}
