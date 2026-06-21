package onset

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestParamSweep is a tuning aid (run with -run TestParamSweep -v): it reports the
// false-onset rate on pure noise and the detection rate at several step sizes for a grid
// of (alpha, h). Not an assertion — it informs DefaultParams. Deterministic seeds.
func TestParamSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("tuning aid")
	}
	const trials = 400
	fp := func(p Params) float64 {
		hits := 0
		for s := 0; s < trials; s++ {
			rng := rand.New(rand.NewSource(int64(10000 + s)))
			noise := series(120, func(i int) float64 { return 100 + rng.NormFloat64()*2 })
			if len(Detect("c", "m", noise, p)) > 0 {
				hits++
			}
		}
		return float64(hits) / trials
	}
	det := func(p Params, stepSigma float64) float64 {
		hits := 0
		for s := 0; s < trials; s++ {
			if len(Detect("c", "m", noiseStep(int64(20000+s), 120, 80, 100, 2, stepSigma*2), p)) > 0 {
				hits++
			}
		}
		return float64(hits) / trials
	}
	fmt.Println("alpha   h   minZ   FP     det4σ  det5σ  det6σ  det8σ")
	for _, alpha := range []float64{0.1, 0.15, 0.2} {
		for _, h := range []float64{3.5, 4.0, 4.5} {
			for _, mz := range []float64{3.0, 4.0} {
				p := Params{Alpha: alpha, K: 0.5, H: h, Warmup: 12, MinZ: mz}
				fmt.Printf("%.2f  %.1f  %.1f  %.3f  %.3f  %.3f  %.3f  %.3f\n",
					alpha, h, mz, fp(p), det(p, 4), det(p, 5), det(p, 6), det(p, 8))
			}
		}
	}
}
