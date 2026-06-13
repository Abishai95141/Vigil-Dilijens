package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
)

// forecastInputs is the frozen per-tick state the warm path consumes: the
// budgeted Tier-B targets, the attention/topology context the blast-radius
// walk needs, and the inventory for display names. Published by the
// deterministic tick via atomic pointer; the forecast loop only ever READS a
// snapshot — the two paths share no mutable state (non-gating by
// construction).
type forecastInputs struct {
	targets    []forecast.Target
	unbudgeted int
	selected   map[string][]string
	topo       detect.Topology
	matcher    *detect.Matcher
	inventory  []identity.InstanceRecord
	evalWindow identity.TimeWindow
}

// forecastLoop is the warm path (doc 09 §3.3; pipeline C of doc 00 §7):
// parallel to detection, never gating it. Each cycle: freeze the series
// (briefly under the store gate's read side), run the budgeted pipeline
// against clockd lock-free, attach the AUTHORED blast radius, publish the
// PROJECTED surface. Clock trouble = a visible degraded state (doc 14 A13),
// never a fabricated candidate and never a stalled tick.
// spliceRelevanceWindow bounds which operator context windows are passed to
// decomposition each cycle: a window older than the context's reach (≈ maxContextFetch
// × scrape interval ≈ 4.3h at dev cadence) can never fall inside the frozen series, so
// 8h covers the context + horizon envelope with margin (09 M5 §3.4).
const spliceRelevanceWindow = 8 * time.Hour

func forecastLoop(ctx context.Context, logger *slog.Logger, gate *sync.RWMutex,
	in *atomic.Pointer[forecastInputs], ingestor *observe.Ingestor,
	graphVersion, graphRelease string, p params.Params,
	warningsView *atomic.Pointer[vapi.WarningsView], cwStore *vapi.ContextWindowStore) {

	cl, err := clock.New(p.Forecast.ClockdTarget)
	if err != nil {
		// Lazy dial means this is a malformed target, not an absent clockd.
		logger.Error("forecast: clockd target invalid; early-warning lane stays dark", "err", err)
		return
	}
	defer cl.Close()
	logger.Info("forecast loop running (doc 09 M4 — gate-certified class only)",
		"clockd", p.Forecast.ClockdTarget, "interval", p.Forecast.Interval.Duration().String(),
		"horizon_steps", p.Forecast.HorizonSteps)

	var degradedSince time.Time
	ticker := time.NewTicker(p.Forecast.Interval.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		fin := in.Load()
		if fin == nil {
			continue // no binding tick yet
		}
		now := time.Now().UTC()

		hctx, hcancel := context.WithTimeout(ctx, p.Forecast.Deadline.Duration())
		ready, code, herr := cl.Health(hctx)
		hcancel()
		healthy := herr == nil && ready

		// Freeze exactly the series this cycle needs — microseconds under the
		// read gate; the clock's RPCs then run lock-free on the copy.
		gate.RLock()
		snap := forecast.Snapshot(ingestor, fin.targets, 1024)
		gate.RUnlock()

		var splices []time.Time
		if cwStore != nil {
			// Only splice points within the context window's reach matter (a window
			// holds ~maxContextFetch points; older operator windows can never fall
			// inside it). Filter to bound the per-cycle list + scan cost (09 M5 §3.4).
			cutoff := now.Add(-spliceRelevanceWindow)
			for _, sp := range cwStore.SplicePoints() {
				if sp.After(cutoff) {
					splices = append(splices, sp)
				}
			}
		}
		res := forecast.RunCycle(ctx, cl, forecast.CycleInput{
			Now: now, Targets: fin.targets, Reader: snap,
			Cadence: p.Scrape.Interval.Duration(), GraphVersion: graphVersion,
			P: p.Forecast, SplicePoints: splices,
		})
		if !healthy || res.Degraded {
			if degradedSince.IsZero() {
				degradedSince = now
			}
		} else {
			degradedSince = time.Time{}
		}
		ch := vapi.ClockHealthRow{Ready: healthy && !res.Degraded, StatusCode: code, DegradedSince: degradedSince}

		var atRisk func(phen, anchor string) []detect.AtRisk
		if fin.matcher != nil {
			atRisk = func(phen, anchor string) []detect.AtRisk {
				return fin.matcher.BlastRadiusFor(phen, anchor, fin.selected, fin.topo, fin.evalWindow)
			}
		}
		warningsView.Store(vapi.BuildWarnings(graphVersion, graphRelease, now, true,
			res, fin.unbudgeted, ch, fin.inventory, atRisk))

		logger.Info("forecast cycle",
			"targets", len(fin.targets), "invocations", res.Invocations,
			"warnings", len(res.Candidates), "silences", len(res.Silences),
			"unbudgeted", fin.unbudgeted, "clock_ready", ch.Ready)
	}
}
