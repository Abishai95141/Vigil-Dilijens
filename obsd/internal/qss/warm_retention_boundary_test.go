package qss

import (
	"os"
	"testing"
	"time"
)

// These tests pin the warm-tier retention / eviction BOUNDARY (applyRetention:
// a sealed segment is deleted when s.End.Before(cutoff), cutoff = now-Retention,
// anchored on the just-sealed segment's end — no wall clock in logic). Retention
// bounds total on-disk growth (the 7 d window of doc 14 §2.3); getting the
// boundary inclusive/exclusive wrong would either keep stale segments forever or
// evict still-in-window readings the replay bundle needs.

// TestWarmRetentionBoundaryInclusive crafts segments whose ends straddle the
// cutoff exactly. With Retention = R and the anchor segment ending at T, the
// cutoff is T-R. A segment ending strictly BEFORE T-R is evicted; one ending
// EXACTLY at T-R survives (Before is strict). A boundary flipped to <= would
// wrongly evict the survivor; flipped to a wall-clock anchor would be
// non-deterministic.
func TestWarmRetentionBoundaryInclusive(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg()
	seg := cfg.SegmentDuration    // 2h
	cfg.Retention = 4 * time.Hour // R
	w := mustOpen(t, dir, cfg)

	// We want three sealed segments with controlled ends:
	//   A: ends just BEFORE (anchorEnd - R)  -> must be evicted
	//   B: ends EXACTLY at   (anchorEnd - R)  -> must survive (Before is strict)
	//   C: the anchor, ends at anchorEnd      -> survives, triggers retention
	//
	// Segment end = the last recv in that segment. We pick recvs precisely.
	// Windows must be distinct so the segments don't merge.
	start := warmBase.UTC().Truncate(seg) // 10:00

	// Anchor C end time: choose a recv well after the others.
	anchorEnd := start.Add(8 * seg).Add(7 * time.Minute) // 26:07-ish, window 26:00
	cutoff := anchorEnd.Add(-cfg.Retention)

	// B must end EXACTLY at cutoff. Put B in cutoff's own segment window.
	bRecv := cutoff
	// A must end strictly before cutoff: one minute earlier, in an earlier window.
	aRecv := cutoff.Add(-(seg + time.Minute))

	// Sanity: all three in distinct segment windows.
	wins := map[int64]bool{}
	for _, r := range []time.Time{aRecv, bRecv, anchorEnd} {
		wins[r.Truncate(seg).UnixMilli()] = true
	}
	if len(wins) != 3 {
		t.Fatalf("test setup: recvs must fall in 3 distinct windows, got %d", len(wins))
	}

	// Drive appends in time order so each rolls the previous and seals it.
	for i, r := range []time.Time{aRecv, bRecv, anchorEnd} {
		if err := w.Append(def("s1"), r, Sample{At: r, Value: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	segs, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A evicted; B and C survive.
	var ends []time.Time
	for _, s := range segs {
		if !s.Sealed {
			t.Fatalf("unexpected unsealed segment: %+v", s)
		}
		ends = append(ends, s.End)
	}
	if len(segs) != 2 {
		t.Fatalf("retention should leave 2 segments (the exact-cutoff one survives), got %d: ends=%v", len(segs), ends)
	}
	// The earliest survivor must be B, ending exactly at the cutoff.
	if !segs[0].End.Equal(cutoff) {
		t.Fatalf("the segment ending EXACTLY at the cutoff must survive (Before is strict); earliest survivor end = %v, cutoff = %v", segs[0].End, cutoff)
	}
}

// TestWarmRetentionDisabledKeepsAll confirms Retention <= 0 disables eviction
// entirely — every sealed segment is kept regardless of age. A retention bug
// that treated 0 as "evict everything" or used a wall clock would fail here.
func TestWarmRetentionDisabledKeepsAll(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Retention = 0 // disabled
	w := mustOpen(t, dir, cfg)
	seg := cfg.SegmentDuration
	base := warmBase.Add(time.Minute)
	for k := 0; k < 5; k++ {
		r := base.Add(time.Duration(k*100) * seg) // far apart in time
		if err := w.Append(def("s1"), r, Sample{At: r, Value: float64(k)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	segs, _ := ListSegments(dir)
	if len(segs) != 5 {
		t.Fatalf("retention disabled must keep all 5 segments, got %d", len(segs))
	}
}

// TestWarmRetentionDeterministicByAnchor proves retention is a deterministic
// function of the SEALED-SEGMENT ENDS, not of wall-clock time: two stores driven
// with the identical recv script evict the identical set, and re-running the same
// script years apart (different real clock) would too. We assert the surviving
// segment set is exactly reproducible.
func TestWarmRetentionDeterministicByAnchor(t *testing.T) {
	run := func() []int64 {
		dir := t.TempDir()
		cfg := testCfg()
		cfg.Retention = 5 * time.Hour
		w := mustOpen(t, dir, cfg)
		seg := cfg.SegmentDuration
		start := warmBase.UTC().Truncate(seg)
		recvs := []time.Time{
			start.Add(time.Minute),
			start.Add(seg + time.Minute),
			start.Add(2*seg + time.Minute),
			start.Add(8*seg + time.Minute), // anchor far ahead -> evicts the early ones
		}
		for i, r := range recvs {
			if err := w.Append(def("s1"), r, Sample{At: r, Value: float64(i)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		segs, _ := ListSegments(dir)
		var starts []int64
		for _, s := range segs {
			starts = append(starts, s.Start.UnixMilli())
		}
		return starts
	}
	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatalf("retention non-deterministic: survivor counts %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("retention non-deterministic: survivor %d = %d vs %d", i, a[i], b[i])
		}
	}
	// And it must have actually evicted something (non-vacuous): the far-ahead
	// anchor sits 8 segments past the start with a 5h (<8*2h) window, so the
	// earliest segments are gone.
	if len(a) == 4 {
		t.Fatalf("retention evicted nothing — fixture is vacuous: survivors=%v", a)
	}
}

// TestWarmSealImmutableBytesStable is the export-stability companion to seal
// immutability: once sealed, a segment's bytes never change. We seal, snapshot
// the bytes, drive more appends into LATER segments, and re-read the first
// sealed segment — it must be byte-for-byte unchanged. A writer that reopened or
// appended to a sealed segment would corrupt replay; this catches it.
func TestWarmSealImmutableBytesStable(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Retention = 0
	w := mustOpen(t, dir, cfg)
	seg := cfg.SegmentDuration
	r1 := warmBase.Add(time.Minute)
	if err := w.Append(def("s1"), r1, Sample{At: r1, Value: 1}); err != nil {
		t.Fatal(err)
	}
	// Roll to seal the first segment.
	r2 := r1.Add(seg)
	if err := w.Append(def("s1"), r2, Sample{At: r2, Value: 2}); err != nil {
		t.Fatal(err)
	}

	segs, _ := ListSegments(dir)
	var firstSealed string
	for _, s := range segs {
		if s.Sealed {
			firstSealed = s.Path
		}
	}
	if firstSealed == "" {
		t.Fatal("first segment should be sealed after the roll")
	}
	before, err := os.ReadFile(firstSealed)
	if err != nil {
		t.Fatal(err)
	}

	// More appends into the active (later) segment must not touch the sealed one.
	for k := 0; k < 10; k++ {
		r := r2.Add(time.Duration(k) * time.Second)
		if err := w.Append(def("s2"), r, Sample{At: r, Value: float64(100 + k)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(firstSealed)
	if err != nil {
		t.Fatalf("sealed segment vanished or unreadable: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("sealed segment bytes changed after later appends — immutability violated (%d vs %d bytes)", len(before), len(after))
	}
	if len(before) == 0 {
		t.Fatal("sealed segment empty — fixture is vacuous")
	}
}
