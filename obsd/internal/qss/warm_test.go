package qss

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var warmBase = time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)

func testCfg() WarmConfig {
	return WarmConfig{SegmentDuration: 2 * time.Hour, Retention: 7 * 24 * time.Hour}
}

func mustOpen(t *testing.T, dir string, cfg WarmConfig) *WarmStore {
	t.Helper()
	w, err := OpenWarm(dir, cfg)
	if err != nil {
		t.Fatalf("OpenWarm: %v", err)
	}
	return w
}

func def(id string) StreamDef {
	return StreamDef{ID: id, CEIKey: "i|cl|ns|Pod|p|uid", UID: "uid/c", Kind: "Container",
		Metric: "m", Type: "gauge", Node: "n1", Cadence: "scrape"}
}

// Round-trip: samples + ticks written across a seal come back frame-for-frame
// identical, defs re-emitted per segment, order preserved.
func TestWarmRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, testCfg())

	r1 := warmBase.Add(10 * time.Second)
	if err := w.Append(def("s1"), r1, Sample{At: r1.Add(-time.Second), Value: 1.5}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(def("s2"), r1, Sample{At: r1, Value: -2.25}); err != nil {
		t.Fatal(err)
	}
	if err := w.Tick(r1.Add(time.Second), []byte(`{"tick":1}`)); err != nil {
		t.Fatal(err)
	}
	// Same stream again: no second def frame in this segment.
	if err := w.Append(def("s1"), r1.Add(15*time.Second), Sample{At: r1.Add(14 * time.Second), Value: 3}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	segs, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 || !segs[0].Sealed {
		t.Fatalf("want 1 sealed segment, got %+v", segs)
	}
	var frames []Frame
	if err := ReplayFrames(segs[0].Path, func(f Frame) error { frames = append(frames, f); return nil }); err != nil {
		t.Fatal(err)
	}
	kinds := ""
	for _, f := range frames {
		kinds += string(rune('0' + f.Kind))
	}
	// def(s1) sample def(s2) sample tick sample
	if kinds != "121232" {
		t.Fatalf("frame order = %s, want 121232", kinds)
	}
	if frames[0].Def.ID != "s1" || frames[2].Def.ID != "s2" {
		t.Errorf("defs wrong: %+v / %+v", frames[0].Def, frames[2].Def)
	}
	if frames[1].Value != 1.5 || !frames[1].At.Equal(r1.Add(-time.Second)) || !frames[1].Recv.Equal(r1) {
		t.Errorf("sample 1 mismatch: %+v", frames[1])
	}
	if frames[3].Value != -2.25 {
		t.Errorf("sample 2 mismatch: %+v", frames[3])
	}
	if string(frames[4].Payload) != `{"tick":1}` {
		t.Errorf("tick payload mismatch: %q", frames[4].Payload)
	}
	if frames[5].Idx != frames[1].Idx {
		t.Errorf("s1's second sample should reuse its index")
	}
}

// Rollover: a recv past the segment window seals the current segment and starts
// a new one; defs are re-emitted in the new segment.
func TestWarmRollover(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, testCfg())

	r1 := warmBase.Add(time.Minute)
	r2 := warmBase.Add(2*time.Hour + time.Minute) // next window
	if err := w.Append(def("s1"), r1, Sample{At: r1, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(def("s1"), r2, Sample{At: r2, Value: 2}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	segs, _ := ListSegments(dir)
	if len(segs) != 2 || !segs[0].Sealed || !segs[1].Sealed {
		t.Fatalf("want 2 sealed segments, got %+v", segs)
	}
	if !segs[0].Start.Equal(warmBase) || !segs[1].Start.Equal(warmBase.Add(2*time.Hour)) {
		t.Errorf("segment windows wrong: %+v", segs)
	}
	// The new segment must re-emit s1's def so it is self-contained.
	var first Frame
	got := false
	if err := ReplayFrames(segs[1].Path, func(f Frame) error {
		if !got {
			first, got = f, true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !got || first.Kind != FrameDef || first.Def.ID != "s1" {
		t.Errorf("second segment must start with s1's def, got %+v", first)
	}
}

// Crash recovery: a torn tail on the unsealed segment is truncated at the last
// whole frame and the segment sealed; the intact prefix replays cleanly.
func TestWarmTornTailRecovery(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, testCfg())
	r1 := warmBase.Add(time.Minute)
	for i := 0; i < 5; i++ {
		if err := w.Append(def("s1"), r1.Add(time.Duration(i)*15*time.Second),
			Sample{At: r1.Add(time.Duration(i) * 15 * time.Second), Value: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: no seal. Tear the tail mid-frame.
	segs, _ := ListSegments(dir)
	if len(segs) != 1 || segs[0].Sealed {
		t.Fatalf("want 1 active segment, got %+v", segs)
	}
	info, _ := os.Stat(segs[0].Path)
	if err := os.Truncate(segs[0].Path, info.Size()-7); err != nil { // mid-sample tear
		t.Fatal(err)
	}
	// Abandon w (crash); recover by reopening.
	w2 := mustOpen(t, dir, testCfg())
	defer w2.Close()
	if w2.Recovered.Segments != 1 || w2.Recovered.TruncatedBytes == 0 {
		t.Fatalf("recovery not reported: %+v", w2.Recovered)
	}
	segs, _ = ListSegments(dir)
	var sealed *SegmentFile
	for i := range segs {
		if segs[i].Sealed {
			sealed = &segs[i]
		}
	}
	if sealed == nil {
		t.Fatalf("recovered segment not sealed: %+v", segs)
	}
	count := 0
	if err := ReplayFrames(sealed.Path, func(f Frame) error {
		if f.Kind == FrameSample {
			count++
		}
		return nil
	}); err != nil {
		t.Fatalf("recovered segment must replay cleanly: %v", err)
	}
	if count != 4 { // 5 written, last torn off
		t.Errorf("samples after recovery = %d, want 4", count)
	}
}

// A corrupted SEALED segment is an error, never silently skipped — sealed
// segments are immutable, so a bad CRC there is real corruption.
func TestWarmSealedCorruptionDetected(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, testCfg())
	r1 := warmBase.Add(time.Minute)
	if err := w.Append(def("s1"), r1, Sample{At: r1, Value: 42}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	segs, _ := ListSegments(dir)
	raw, _ := os.ReadFile(segs[0].Path)
	raw[len(raw)-10] ^= 0xFF // flip a byte inside the sample frame
	if err := os.WriteFile(segs[0].Path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	err := ReplayFrames(segs[0].Path, func(Frame) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "crc") {
		t.Errorf("corruption must surface as a crc error, got %v", err)
	}
}

// Retention: sealing a segment deletes sealed segments older than the window,
// anchored on the sealed end (no wall clock).
func TestWarmRetention(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Retention = 4 * time.Hour
	w := mustOpen(t, dir, cfg)
	// Three windows: 10:00, 12:00, and one 9 hours later (19:00).
	times := []time.Time{warmBase.Add(time.Minute), warmBase.Add(2*time.Hour + time.Minute), warmBase.Add(9*time.Hour + time.Minute)}
	for i, r := range times {
		if err := w.Append(def("s1"), r, Sample{At: r, Value: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	segs, _ := ListSegments(dir)
	// Sealing the 19:00 segment (end ≈ 19:01) should have evicted both morning
	// segments (ends ≈ 10:01 / 12:01, older than 19:01−4h = 15:01).
	if len(segs) != 1 {
		t.Fatalf("retention should leave 1 segment, got %+v", segs)
	}
	if !segs[0].Start.Equal(warmBase.Add(9 * time.Hour).Truncate(2 * time.Hour)) {
		t.Errorf("wrong survivor: %+v", segs[0])
	}
}

// An empty active file (header only) is removed at recovery, not sealed.
func TestWarmRecoverEmptyActive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seg-1000.active"), []byte(warmMagic), 0o644); err != nil {
		t.Fatal(err)
	}
	w := mustOpen(t, dir, testCfg())
	defer w.Close()
	segs, _ := ListSegments(dir)
	if len(segs) != 0 {
		t.Errorf("empty active should be removed, got %+v", segs)
	}
}

// NaN and ±Inf survive the binary round-trip (bit-exact storage, no interpretation).
func TestWarmNonFiniteValues(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, testCfg())
	r1 := warmBase.Add(time.Minute)
	nan := func() float64 { var z float64; return z / z }() // quiet NaN without importing math
	inf := func() float64 { var z float64; return 1 / z }()
	for i, v := range []float64{nan, inf, -inf} {
		if err := w.Append(def("s1"), r1.Add(time.Duration(i)*time.Second), Sample{At: r1, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	segs, _ := ListSegments(dir)
	var vals []float64
	_ = ReplayFrames(segs[0].Path, func(f Frame) error {
		if f.Kind == FrameSample {
			vals = append(vals, f.Value)
		}
		return nil
	})
	if len(vals) != 3 || vals[0] == vals[0] /* NaN != NaN */ || vals[1] <= 1e308 || vals[2] >= -1e308 {
		t.Errorf("non-finite round-trip wrong: %v", vals)
	}
}
