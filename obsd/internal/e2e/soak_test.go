//go:build integration

package e2e

import (
	"os"
	"testing"
	"time"
)

// TestLiveSoak runs obsd against the cluster for an extended window and asserts its
// memory stays bounded and it never mis-joins — the "no slow leak, no drift over time"
// property a reliable (not prototype) service must hold (audit roadmap #6).
//
// Opt-in by duration so it never slows the default loop:
//
//	VIGIL_SOAK_DURATION=2h just e2e   # or any Go duration; unset/0 => skipped
//
// On a STABLE workload (no churn) RSS should rise as the qss rings fill, then plateau.
// (A churny workload grows ~4KiB per dead-entity stream until tombstone-driven ring
// eviction lands — task #51, a known, documented limitation — so soak on a steady set.)
func TestLiveSoak(t *testing.T) {
	kc := kubeconfigPath(t)
	dur := 0 * time.Second
	if v := os.Getenv("VIGIL_SOAK_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("VIGIL_SOAK_DURATION %q: %v", v, err)
		}
		dur = d
	}
	if dur <= 0 {
		t.Skip("soak is opt-in: set VIGIL_SOAK_DURATION (e.g. 2h)")
	}

	dataDir := t.TempDir()
	obsd := startObsd(t, kc, dataDir)

	initial, err := obsd.rssKiB()
	if err != nil {
		t.Fatalf("read RSS: %v", err)
	}
	t.Logf("soak start: RSS %d MiB, running for %s", initial/1024, dur)

	const sample = 30 * time.Second
	const growthCeiling = 2.5 // RSS may climb as rings fill, but must not blow up
	deadline := time.Now().Add(dur)
	peak := initial
	for time.Now().Before(deadline) {
		time.Sleep(sample)
		rss, err := obsd.rssKiB()
		if err != nil {
			t.Fatalf("read RSS during soak: %v", err)
		}
		if rss > peak {
			peak = rss
		}
		mj, _ := obsd.metricValue("_misjoins")
		t.Logf("soak +%s: RSS %d MiB (peak %d MiB) · misjoins %v", sample, rss/1024, peak/1024, mj)
		if mj != 0 {
			t.Fatalf("INTEGRITY: %v mis-join(s) during soak", mj)
		}
		if float64(rss) > float64(initial)*growthCeiling {
			t.Fatalf("RSS %d MiB exceeded %.1fx the start (%d MiB) — possible leak", rss/1024, growthCeiling, initial/1024)
		}
	}
	t.Logf("soak OK: %s elapsed, peak RSS %d MiB (start %d MiB, < %.1fx), 0 mis-joins throughout",
		dur, peak/1024, initial/1024, growthCeiling)
}
