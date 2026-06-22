package assoc

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"math/rand"
	"sort"
	"time"
)

// LeadLagParams are the DECLARED constants of the lead-lag witness (docs/31 §5.2). Like
// assoc.Params they are declared (borrowed-normativity), NEVER fit to the data they are
// applied to — a data-fit lag/threshold would be a learned cutoff, the charter ban.
type LeadLagParams struct {
	Lags       []int         // DECLARED symmetric lag grid in bins (e.g. {-4,-2,-1,0,1,2,4}); positive lag = A leads B
	Bin        time.Duration // bin width (matches assoc.Params.Bin so the grid maps to seconds)
	MinOverlap int           // structural floor: a lag with < this many overlapping DIFFERENCED bins is rejected (kills the silent-vanishing-lag bug)
	K          int           // seeded permutation shuffles (declared, e.g. 200)
	Alpha      float64       // significance threshold the empirical p must clear (declared, e.g. 0.05)
	RFloor     float64       // declared |r| floor the detrended peak must clear
}

// DefaultLeadLagParams: a CONTIGUOUS ±60s symmetric grid at 15s bins (±4 bins), an 8-bin
// overlap floor (= assoc MinOverlap), 200 seeded permutations, p<0.05, |r|>=0.5. Declared,
// never fit. The grid is contiguous (every integer bin in [-4,+4]) rather than the sparse
// {-4,-2,-1,0,1,2,4} of the doc's example so there is NO blind gap (e.g. a real +3-bin/+45s
// lead is detected, not lost between grid points); the extra comparisons are fully corrected
// by the same-grid-per-shuffle permutation null, so density costs rigor nothing.
func DefaultLeadLagParams() LeadLagParams {
	return LeadLagParams{
		Lags:       []int{-4, -3, -2, -1, 0, 1, 2, 3, 4},
		Bin:        15 * time.Second,
		MinOverlap: 8,
		K:          200,
		Alpha:      0.05,
		RFloor:     0.5,
	}
}

// LeadLagWitness is the MEASURED lead-lag evidence for a pair (docs/31 §5.2). It carries
// NO direction and NO cause — the peak lag is sign-carrying EVIDENCE (positive = A appeared
// to lead B), surfaced exactly like the onset-order witness, for a HUMAN to weigh. The
// system never turns it into an arrow (the competitor's argmax→arrow is refused).
type LeadLagWitness struct {
	LagPeakBins       int     // the peak lag in bins on the surviving grid (signed; + = A leads B). Never 0 — a lag-0 peak is contemporaneous, NOT a lead, and is silenced.
	LagPeakSeconds    int64   // LagPeakBins * Bin in seconds (signed)
	LagPeakRDetrended float64 // Pearson r of the FIRST-DIFFERENCED series at the peak lag (signed). Distinct from the lag-0 LEVEL coefficient — never conflated.
	LagP              float64 // seeded-permutation empirical p of the peak |r| (lower = stronger)
	EffectiveN        int     // overlapping differenced bins at the peak lag (>= MinOverlap by construction)
}

// LeadLagSeed derives a CONTENT-derived seed for the permutation null: SHA256 of the
// SORTED pair key + window bounds → first 8 bytes as int64. NEVER time/global RNG — this
// is the fix for the competitor's RNG-order fragility (docs/31 §5.3): the same pair over
// the same window always produces the same shuffles, hence a byte-identical p.
func LeadLagSeed(a, b string, lo, hi time.Time) int64 {
	if a > b {
		a, b = b, a
	}
	h := sha256.Sum256([]byte(a + "\x00" + b + "\x00" +
		lo.UTC().Format(time.RFC3339Nano) + "\x00" + hi.UTC().Format(time.RFC3339Nano)))
	return int64(binary.BigEndian.Uint64(h[:8]))
}

// ComputeLeadLag is the rigorous online lead-lag witness (docs/31 §5.2). It is a PURE,
// DETERMINISTIC function: same (samples, bounds, seed, params) ⇒ byte-identical output,
// the same contract as onset.Detect / assoc.Associate. It:
//  1. DETRENDS — first-differences both binned series, so a shared co-trend (a linear ramp)
//     differences to a constant and Pearson rejects it (kills the competitor's r=0.933
//     co-trend artifact).
//  2. searches a DECLARED symmetric lag grid (never an argmax over a fit space) for the peak
//     |r|, rejecting any lag whose differenced overlap < MinOverlap (kills the silent
//     vanishing-overlap bug).
//  3. SILENCES a lag-0 peak — contemporaneous co-movement is NOT a lead (it is what the
//     level coefficient + co-occurrence already report); this is the principled defense
//     against a common-cause confounder, which co-moves at lag 0 after detrending.
//  4. gates the nonzero peak on a SEEDED circular-permutation significance test (each shuffle
//     re-searches the SAME grid — the multiple-comparison correction).
//
// Returns (witness, true) only for a NONZERO lag clearing both p<Alpha and |r|>=RFloor;
// otherwise (zero, false) — HONEST SILENCE, never a 0s "contemporaneous" default (else
// every quiet pair would falsely corroborate). A is the first argument (the caller passes
// the sorted-pair "a"), so a positive peak lag means a led b.
func ComputeLeadLag(aPts, bPts []Point, lo, hi time.Time, seed int64, p LeadLagParams) (LeadLagWitness, bool) {
	if len(p.Lags) == 0 || p.K <= 0 || p.MinOverlap < 2 {
		return LeadLagWitness{}, false
	}
	// 1. Bin on the SAME grid the assoc lane uses, then detrend (first-difference).
	da := differenceBinned(binSeries(aPts, lo, hi, p.Bin))
	db := differenceBinned(binSeries(bPts, lo, hi, p.Bin))
	if len(da) < p.MinOverlap || len(db) < p.MinOverlap {
		return LeadLagWitness{}, false // too short after differencing — honest silence
	}

	// 2. Peak over the declared grid (min-overlap-gated).
	peakLag, peakR, peakN, ok := peakLagPearson(da, db, p.Lags, p.MinOverlap)
	// 3. A lag-0 peak is contemporaneous, NOT a lead → silence; below the |r| floor → silence.
	if !ok || peakLag == 0 || math.Abs(peakR) < p.RFloor {
		return LeadLagWitness{}, false
	}

	// 4. Seeded circular-permutation null: rotate db's differenced values K times (preserving
	//    its marginal + autocorrelation, destroying cross-alignment), recompute the peak |r|
	//    over the SAME grid each time, count how many reach the observed peak.
	rng := rand.New(rand.NewSource(seed))
	keysB := sortedKeys(db)
	exceed := 0
	for i := 0; i < p.K; i++ {
		perm := circularShift(db, keysB, rng.Intn(len(keysB)-1)+1)
		_, r, _, pok := peakLagPearson(da, perm, p.Lags, p.MinOverlap)
		if pok && math.Abs(r) >= math.Abs(peakR) {
			exceed++
		}
	}
	// (1+exceed)/(1+K): the standard permutation p-value (North et al. 2002) — it is never
	// exactly 0 (you cannot conclude p < 1/K from K shuffles) and is slightly conservative.
	// At K=200 the floor is 1/201 ≈ 0.005, still well below Alpha for a genuine lead.
	pval := float64(1+exceed) / float64(1+p.K)
	if pval >= p.Alpha {
		return LeadLagWitness{}, false // not significant against the permutation null
	}

	return LeadLagWitness{
		LagPeakBins:       peakLag,
		LagPeakSeconds:    int64((time.Duration(peakLag) * p.Bin) / time.Second),
		LagPeakRDetrended: peakR,
		LagP:              pval,
		EffectiveN:        peakN,
	}, true
}

// differenceBinned first-differences a binned series: for each bin index i whose
// predecessor i-1 is also present, Δ[i] = v[i] - v[i-1]. This is the detrending step — a
// co-trend (shared linear ramp) differences to a constant, which pearsonOverlap then
// rejects (denom 0), so co-trend cannot manufacture a lead.
func differenceBinned(b map[int]float64) map[int]float64 {
	out := make(map[int]float64, len(b))
	for i, v := range b {
		if prev, ok := b[i-1]; ok {
			out[i] = v - prev
		}
	}
	return out
}

// peakLagPearson returns the grid lag (in bins) maximizing |Pearson| of the differenced
// series, with its signed r and overlap. Lag k correlates da[i] with db[i+k] (positive k ⇒
// A leads B). A lag whose overlap < minOverlap is skipped (never correlated). Grid order is
// deterministic (|k| asc, then k asc), so ties resolve to the smaller-|k|/smaller-k lag —
// reproducible. ok=false if no lag qualifies.
func peakLagPearson(da, db map[int]float64, lags []int, minOverlap int) (int, float64, int, bool) {
	order := append([]int(nil), lags...)
	sort.Slice(order, func(i, j int) bool {
		ai, aj := absInt(order[i]), absInt(order[j])
		if ai != aj {
			return ai < aj
		}
		return order[i] < order[j]
	})
	bestLag, bestR, bestN, found := 0, 0.0, 0, false
	for _, k := range order {
		r, n, ok := pearsonOverlap(da, shiftKeys(db, k))
		if !ok || n < minOverlap {
			continue
		}
		if !found || math.Abs(r) > math.Abs(bestR) {
			bestLag, bestR, bestN, found = k, r, n, true
		}
	}
	return bestLag, bestR, bestN, found
}

// shiftKeys returns a map where index i holds b[i+k], so pearsonOverlap(da, shiftKeys(db,k))
// correlates da[i] with db[i+k]. Positive k ⇒ A leads B.
func shiftKeys(b map[int]float64, k int) map[int]float64 {
	if k == 0 {
		return b
	}
	out := make(map[int]float64, len(b))
	for i, v := range b {
		out[i-k] = v
	}
	return out
}

// circularShift rotates the VALUES of b (ordered by ascending key) by offset, keeping the
// key set. This is the permutation null: it destroys cross-series alignment while preserving
// each series' marginal distribution and autocorrelation. Deterministic given keys + offset.
func circularShift(b map[int]float64, keys []int, offset int) map[int]float64 {
	n := len(keys)
	out := make(map[int]float64, n)
	for idx, key := range keys {
		out[key] = b[keys[(idx+offset)%n]]
	}
	return out
}

func sortedKeys(b map[int]float64) []int {
	ks := make([]int, 0, len(b))
	for k := range b {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
