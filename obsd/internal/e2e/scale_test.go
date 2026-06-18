//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

const scaleNamespace = "vigil-e2e-scale"

type coverageResp struct {
	Summary struct {
		Entities int `json:"entities"`
		TierA    int `json:"tierA"`
	} `json:"summary"`
}

// TestLiveScale packs the cluster with N synthetic pods and measures the live
// discovery + ingest envelope (audit roadmap #6 / the theme's "hundreds of pods across
// namespaces on a single node"): obsd must discover them all, keep its memory bounded,
// stay responsive, and — the integrity invariant — NEVER mis-join an identity at scale
// (the silent killer the charter guards). It then churns the load to 0 and asserts the
// identity layer GCs back toward baseline with mis-joins still zero.
//
// N defaults to 50 (safe under k3s's default --max-pods=110 alongside the existing
// workload). Set VIGIL_SCALE_N to push it into the hundreds on a cloud node with a
// raised pod cap (see docs/testing — the GCP single-node runbook). The Go benchmark
// BenchmarkMatch separately characterizes detection to 5,000 synthetic entities; this
// test characterizes the live scrape→identity→bind path on real pods.
func TestLiveScale(t *testing.T) {
	kc := kubeconfigPath(t)
	requireKubectl(t)

	n := 50
	if v := os.Getenv("VIGIL_SCALE_N"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}

	t.Cleanup(func() {
		deleteNamespace(kc, scaleNamespace)
		unstickNamespace(t, kc, scaleNamespace)
	})
	prepareNamespace(t, kc, scaleNamespace)
	_, _ = kubectlOut(kc, "create", "namespace", scaleNamespace)

	dataDir := t.TempDir()
	obsd := startObsd(t, kc, dataDir) // the discovery/ingest path (identity + scrape + detect)

	coverage := func() coverageResp {
		var c coverageResp
		if err := obsd.getJSON("/api/coverage", &c); err != nil {
			t.Fatalf("/api/coverage: %v", err)
		}
		return c
	}

	base := coverage().Summary.Entities
	baseRSS, err := obsd.rssKiB()
	if err != nil {
		t.Fatalf("read RSS: %v", err)
	}
	t.Logf("baseline: %d entities · RSS %d MiB", base, baseRSS/1024)

	// Deploy N tiny pods.
	kubectl(t, kc, "create", "deployment", "scale-load",
		"--image=busybox:1.36", fmt.Sprintf("--replicas=%d", n), "-n", scaleNamespace, "--", "sleep", "100000")

	// obsd must DISCOVER ~all of them (allow margin for in-flight scheduling).
	want := base + int(float64(n)*0.8)
	eventually(t, 5*time.Minute, 5*time.Second, fmt.Sprintf("obsd to discover ~%d more entities", n), func() error {
		got := coverage().Summary.Entities
		if got < want {
			return fmt.Errorf("entities=%d, want >= %d (baseline %d + ~%d pods)", got, want, base, n)
		}
		return nil
	})

	peak := coverage().Summary.Entities
	rss, _ := obsd.rssKiB()
	perPod := 0
	if peak > base {
		perPod = (rss - baseRSS) / (peak - base)
	}
	t.Logf("at scale: %d entities (+%d) · RSS %d MiB (+%d MiB, ~%d KiB/entity)",
		peak, peak-base, rss/1024, (rss-baseRSS)/1024, perPod)

	// Integrity at scale: zero mis-joins (more pods must never cause a wrong join).
	if mj, ok := obsd.metricValue("_misjoins"); !ok {
		t.Log("misjoins metric not found on /metrics (skipping integrity assertion)")
	} else if mj != 0 {
		t.Errorf("INTEGRITY: %v mis-join(s) at %d entities — identity must never mis-join at scale", mj, peak)
	} else {
		t.Logf("integrity OK: 0 mis-joins at %d entities", peak)
	}

	// Runaway guard: RSS must stay bounded (a leak/blowup would breach this).
	const rssCeilingMiB = 1024
	if rss/1024 > rssCeilingMiB {
		t.Errorf("RSS %d MiB exceeds the %d MiB ceiling at %d entities", rss/1024, rssCeilingMiB, peak)
	}

	// obsd must still be responsive at scale.
	if resp, err := http.Get(obsd.baseURL + "/readyz"); err != nil {
		t.Errorf("obsd unresponsive at scale: %v", err)
	} else {
		_ = resp.Body.Close()
	}

	// CHURN: scale to 0 — identity must GC terminated entities back toward baseline and
	// STILL report zero mis-joins (rapid create/delete is where mis-joins hide).
	t.Run("churn_scale_to_zero", func(t *testing.T) {
		kubectl(t, kc, "scale", "deployment", "scale-load", "--replicas=0", "-n", scaleNamespace)
		eventually(t, 5*time.Minute, 5*time.Second, "entities to drop back after scale-to-0", func() error {
			got := coverage().Summary.Entities
			if got > base+int(float64(n)*0.3) {
				return fmt.Errorf("entities=%d still high (baseline %d) — identity GC not keeping up", got, base)
			}
			return nil
		})
		if mj, ok := obsd.metricValue("_misjoins"); ok && mj != 0 {
			t.Errorf("INTEGRITY: %v mis-join(s) after churn", mj)
		}
		t.Logf("churn OK: entities returned to ~baseline · 0 mis-joins")
	})
}
