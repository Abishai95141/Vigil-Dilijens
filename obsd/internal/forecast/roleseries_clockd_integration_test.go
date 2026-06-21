//go:build integration

// C1 certification (doc 22): does REAL TimesFM produce a calibrated band and anticipate the
// crossing on the ROLE-SERIES (worst-member, churn-stable) input? Needs clockd on :50051
// (`just clockd CLOCK=timesfm`). Run: go test -tags=integration -run TestRoleSeriesClockdChurnCert
// ./obsd/internal/forecast/ -v
//
// This is the empirical gate the role-series class is "pending": it scores the clock's real
// output against the realized future of a CHURNING leak whose role series is built by the
// actual AggregateRoleSeries (members appear/die; worst-member stays continuous). It is the
// honest certification — not faked, not a scripted clock.
package forecast

import (
	"context"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

func clockdTarget() string {
	if t := os.Getenv("CLOCKD_TARGET"); t != "" {
		return t
	}
	return "127.0.0.1:50051"
}

// churnLeakRoleSeries builds a churn-stable role series via the REAL AggregateRoleSeries: a
// leaking member climbs, dies, and a successor inherits the load (rolling replacement), so the
// worst-member (max) series is one continuous climb across the churn. Climbs from base toward
// (and past) the bar so there is a real crossing.
func churnLeakRoleSeries(n int, base, perStep, noise float64, bin time.Duration) []qss.Sample {
	start := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(11)) // deterministic realistic jitter
	members := map[string][]qss.Sample{}
	gen := 0
	for i := 0; i < n; i++ {
		// churn every ~30 samples: a new member takes over at the current level (continuous).
		if i > 0 && i%30 == 0 {
			gen++
		}
		v := base + perStep*float64(i) + noise*rng.NormFloat64() // realistic Gaussian memory jitter
		key := "pod-gen" + string(rune('A'+gen))
		members[key] = append(members[key], qss.Sample{At: start.Add(time.Duration(i) * bin), Value: v})
	}
	return AggregateRoleSeries(members, start, start.Add(time.Duration(n)*bin), bin, math.Max)
}

// scenario is one distinct churn-leak crossing event (the gate requires ≥3).
type scenario struct {
	name             string
	base, slope, jit float64
}

func certifyScenario(t *testing.T, cl *clock.Client, sc scenario) (bandCov, recall float64) {
	const bar = 0.95
	bin := 15 * time.Second
	series := churnLeakRoleSeries(150, sc.base, sc.slope, sc.jit, bin)
	vals := make([]float64, len(series))
	for i, s := range series {
		vals[i] = s.Value
	}
	realizedCross := -1
	for i, v := range vals {
		if v >= bar {
			realizedCross = i
			break
		}
	}
	if realizedCross < 0 {
		t.Fatalf("%s: series never crosses the bar", sc.name)
	}
	quantiles := []float64{0.1, 0.5, 0.9}
	horizon := 48
	var covHits, covTot, anticipated, crossingsScored int
	for _, asof := range []int{40, 55, 70, 85, 100} {
		if asof+1 >= len(vals) || asof < 24 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		fc, err := cl.Forecast(ctx, vals[:asof], horizon, quantiles)
		cancel()
		if err != nil {
			t.Fatalf("%s clock.Forecast as-of %d: %v", sc.name, asof, err)
		}
		lo, hi := fc.Quantiles[0], fc.Quantiles[2]
		for k := 0; k < horizon && asof+k < len(vals); k++ {
			covTot++
			if vals[asof+k] >= lo[k]-1e-9 && vals[asof+k] <= hi[k]+1e-9 {
				covHits++
			}
		}
		if asof < realizedCross {
			crossingsScored++
			lead := realizedCross - asof
			warned := false
			for k := 0; k < horizon; k++ {
				if hi[k] >= bar {
					warned = true
					break
				}
			}
			// honest lead rule: only count anticipation when the crossing is REACHABLE within
			// the horizon (lead ≤ horizon); a "cannot see it yet" silence at long lead is correct.
			if lead <= horizon {
				if warned && lead >= 8 {
					anticipated++
				}
			} else {
				crossingsScored-- // out of horizon — not scored (silence is honest, not a miss)
			}
		}
	}
	bandCov = float64(covHits) / float64(covTot)
	// Per-EVENT recall (gate EVENT_RECALL_MIN): the crossing is recalled if ANY as-of
	// anticipated it with adequate lead — a later "too-late-now" as-of is not a miss, the
	// warning already came (and an out-of-horizon early silence is honest, not a miss).
	recall = 0.0
	if anticipated > 0 {
		recall = 1.0
	}
	t.Logf("  %-12s realized-cross@%d  band_coverage=%.3f  event_recalled=%v (%d/%d as-ofs anticipated, %d scored)",
		sc.name, realizedCross, bandCov, recall == 1.0, anticipated, crossingsScored, crossingsScored)
	return bandCov, recall
}

func TestRoleSeriesClockdChurnCert(t *testing.T) {
	cl, err := clock.New(clockdTarget())
	if err != nil {
		t.Fatalf("dial clockd (%s): %v — is `just clockd CLOCK=timesfm` running?", clockdTarget(), err)
	}
	defer cl.Close()

	// ≥3 DISTINCT crossing events (gate MIN_CROSSING_EVENTS=3): different slopes/levels/jitter,
	// each a churn-leak whose role series is built by the REAL worst-member AggregateRoleSeries.
	scenarios := []scenario{
		{"steady", 0.20, 0.0075, 0.045},
		{"steep", 0.10, 0.011, 0.05},
		{"gentle-noisy", 0.30, 0.006, 0.06},
	}
	t.Log("REAL TimesFM role-series churn certification (gate: band_coverage∈[0.65,0.98], recall≥0.70, ≥3 events):")
	pass := 0
	for _, sc := range scenarios {
		bc, rc := certifyScenario(t, cl, sc)
		if bc >= 0.65 && bc <= 0.98 && rc >= 0.70 {
			pass++
		} else {
			t.Errorf("%s FAILED gate: band_coverage=%.3f recall=%.3f", sc.name, bc, rc)
		}
	}
	t.Logf("CERTIFICATION: %d/%d crossing events pass the role-series forecast gate against real TimesFM", pass, len(scenarios))
	if pass < 3 {
		t.Errorf("only %d/3 events passed — role-series class NOT certified", pass)
	}
}
