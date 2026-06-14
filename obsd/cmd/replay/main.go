// Command replay is the harness replay runner (doc 11 §3.1, doc 05 M5): it
// re-runs a recorded bundle — readings (qss segments), resolved bars per epoch,
// per-tick topology snapshots, the pinned graph release, and the captured
// parameter set — through the REAL observation and detection components, and
// verifies that every recorded evaluation tick reproduces byte-identically
// (digest equality).
//
// With -eval (doc 11 M4 / 07 M6 sensitivity sweeps) it instead RE-EVALUATES
// the recorded inputs under overridden sensitivity parameters and writes the
// resulting findings — explicitly an evaluation, never a verification: no
// digest is compared and the output says so.
//
// Exit status: 0 = every tick matched (or evaluation completed); 1 = any
// mismatch or error. A mismatch is a determinism violation and is never
// downgraded to a warning.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay/export"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	var (
		bundle      = fs.String("bundle", "", "path to a replay bundle (manifest.json + bars-*.json + segments/)")
		ontology    = fs.String("ontology", "ontology/graph/k8s_signal_kg.json", "ontology KG release (must match the bundle's pinned graph version)")
		overlays    = fs.String("overlays", "ontology/graph/overlays", "authored overlay dir merged into the ontology")
		outDir      = fs.String("out", "", "write each replayed tick's canonical JSON here (for diffing)")
		parquetOut  = fs.String("export-parquet", "", "export the bundle's readings to this Parquet file (the Python-harness bridge)")
		quiet       = fs.Bool("q", false, "summary only (no per-tick lines)")
		showVersion = fs.Bool("version", false, "print version and exit")

		// Evaluation mode (doc 11 M4 sweeps): re-evaluate under overridden
		// sensitivity. Negative = keep the manifest's pinned value.
		evalMode    = fs.Bool("eval", false, "EVALUATION mode: re-evaluate under the -eval-* overrides; verifies nothing")
		evalBand    = fs.Float64("eval-band", -1, "override at-threshold band (with -eval)")
		evalMinComp = fs.Float64("eval-min-completeness", -1, "override degraded-surfacing completeness floor (with -eval)")
		evalWellAb  = fs.Float64("eval-well-above", -1, "override well-above factor (with -eval)")
		evalCascade = fs.Duration("eval-cascade-window", -1, "override cascade pairing window (with -eval)")

		// Forecast-evaluation pass (doc 11 M5 backtests / 09 M3): re-run the
		// REAL forecast pipeline "as of" every recorded tick against a live
		// clock, writing raw trajectories + verdicts as JSONL — the backtest
		// substrate. PROJECTED output: never digest-bearing, verifies nothing.
		fcMode     = fs.Bool("forecast", false, "FORECAST-EVALUATION pass: run the forecast pipeline at every tick; verifies nothing")
		fcClockd   = fs.String("forecast-clockd", "127.0.0.1:50051", "clockd target for the forecast pass")
		fcOut      = fs.String("forecast-out", "", "JSONL file for per-tick forecast events (required with -forecast)")
		fcHorizon  = fs.Int("forecast-horizon", 0, "override forecast.horizon_steps for the pass (0 = params default)")
		fcBudget   = fs.Int("forecast-budget", 0, "override Tier-B ceiling for the pass (0 = params default)")
		fcNoDecomp = fs.Bool("forecast-no-decompose", false, "disable 09 M5 decomposition for the pass (for the A/B exit-gate comparison)")
		fcMinCtx   = fs.Int("forecast-min-context", 0, "override forecast.min_context for the pass (0 = params default; lower for short-cycle classes)")

		// Cross-service cascade-evaluation pass (doc 15 phase D / v2 backtest
		// gate): re-run the REAL warm-path cascade at every recorded tick —
		// findings→degraded mapped via the captured bindings, walked over the
		// captured observed-flow topology, joined to the AUTHORED relation. The
		// cascade is a pure function of pinned inputs, so the chain is byte-
		// identical to live. MEASURED+AUTHORED output, verifies nothing.
		csMode     = fs.Bool("crossservice", false, "CROSS-SERVICE pass: re-compute the warm-path cascade at every tick; verifies nothing")
		csOut      = fs.String("crossservice-out", "", "JSONL file for per-tick cross-service cascade events (required with -crossservice)")
		csRelation = fs.String("crossservice-relation", "", "override: load the AUTHORED cross-service relation from this YAML file instead of the curated graph (doc 15 Phase C)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintln(out, version.String())
		return nil
	}
	if *bundle == "" {
		return fmt.Errorf("-bundle is required")
	}

	g, err := graph.LoadWithOverlays(*ontology, *overlays)
	if err != nil {
		return fmt.Errorf("load ontology: %w", err)
	}

	opts := replay.Options{BundleDir: *bundle, Graph: g, OutDir: *outDir}
	if *evalMode {
		// Start from the bundle's PINNED regime and override only the swept
		// knobs — structural pins (scrape interval, rate window, watermark)
		// always stay the capture's.
		pinned, err := replay.ReadManifest(*bundle)
		if err != nil {
			return err
		}
		regime := pinned.FPParams
		if *evalBand >= 0 {
			regime.Band = *evalBand
		}
		if *evalMinComp >= 0 {
			regime.MinCompleteness = *evalMinComp
		}
		if *evalWellAb >= 0 {
			regime.WellAboveFactor = *evalWellAb
		}
		if *evalCascade >= 0 {
			regime.CascadeWindow = *evalCascade
		}
		opts.Evaluate = &regime
	}

	var fcFile *os.File
	if *fcMode {
		if *fcOut == "" {
			return fmt.Errorf("-forecast requires -forecast-out")
		}
		p, err := params.Default()
		if err != nil {
			return err
		}
		regime := p.Forecast
		if *fcHorizon > 0 {
			regime.HorizonSteps = *fcHorizon
		}
		if *fcNoDecomp {
			regime.Decompose = false // A/B: the pre-09-M5 behaviour (full polluted window)
		}
		if *fcMinCtx > 0 {
			regime.MinContext = *fcMinCtx // per-class context (short-cycle classes need less)
		}
		budget := p.Selection.TierBBudgetPerCycle
		if *fcBudget > 0 {
			budget = *fcBudget
		}
		cl, err := clock.New(*fcClockd)
		if err != nil {
			return err
		}
		defer cl.Close()
		fcFile, err = os.Create(*fcOut)
		if err != nil {
			return err
		}
		defer fcFile.Close()
		enc := json.NewEncoder(fcFile)
		opts.Forecast = &replay.ForecastEval{
			Clock: cl, Params: regime, Budget: budget,
			Events: func(tf replay.TickForecast) { _ = enc.Encode(tf) },
		}
	}

	var csFile *os.File
	if *csMode {
		if *csOut == "" {
			return fmt.Errorf("-crossservice requires -crossservice-out")
		}
		// doc 15 Phase C: prefer the CURATED relation from the released graph; the
		// -crossservice-relation flag is an override for experimental runs only.
		var rel flow.Relation
		if *csRelation != "" {
			r, err := flow.LoadRelation(*csRelation)
			if err != nil {
				return fmt.Errorf("load cross-service relation override: %w", err)
			}
			rel = r
		} else {
			r, ok := flow.RelationFromGraph(g)
			if !ok {
				return fmt.Errorf("curated cross-service relation absent in graph %s (use -crossservice-relation to override)", short(g.Version))
			}
			rel = r
		}
		csFile, err = os.Create(*csOut)
		if err != nil {
			return err
		}
		defer csFile.Close()
		csEnc := json.NewEncoder(csFile)
		opts.CrossService = &replay.CrossServiceEval{
			Relation: rel,
			Events:   func(tc replay.TickCrossService) { _ = csEnc.Encode(tc) },
		}
	}

	rep, err := replay.Run(opts)
	if err != nil {
		return err
	}

	m := rep.Manifest
	fmt.Fprintf(out, "replay bundle %s\n", *bundle)
	fmt.Fprintf(out, "  pinned: graph %s · params v%s (%s) · cluster %s · captured %s\n",
		short(m.GraphVersion), m.ParamsVersion, m.Profile, m.ClusterID, m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(out, "  contents: %v\n  absent (stated): %v\n", m.Contents, m.Absent)
	fmt.Fprintf(out, "  segments=%d streams=%d samples=%d ticks=%d\n", rep.Segments, rep.Streams, rep.Samples, len(rep.Ticks))
	if rep.UnsealedTail {
		fmt.Fprintln(out, "  note: an unsealed .active segment was ignored (capture not closed cleanly)")
	}
	if rep.EvaluationMode {
		fmt.Fprintln(out, "  MODE: EVALUATION (overridden sensitivity) — findings re-derived, NOTHING VERIFIED")
	}
	if *fcMode {
		fmt.Fprintf(out, "  FORECAST PASS: pipeline re-ran at every tick against clockd=%s — PROJECTED output, events -> %s (verifies nothing)\n",
			*fcClockd, *fcOut)
	}
	if *csMode {
		note := ""
		if _, hasFlow := m.EdgeBudgets[string(flow.EdgeTypeFlow)]; !hasFlow {
			note = " — NOTE: bundle pins no flow budget (captured without --flow-enabled); the cascade is quiet on every tick"
		}
		fmt.Fprintf(out, "  CROSS-SERVICE PASS: warm-path cascade re-computed at every tick — MEASURED+AUTHORED output, events -> %s (verifies nothing)%s\n",
			*csOut, note)
	}
	if !*quiet {
		for _, tk := range rep.Ticks {
			mark := "ok"
			if rep.EvaluationMode {
				mark = "evaluated"
			} else if !tk.Match {
				mark = "MISMATCH"
			}
			fmt.Fprintf(out, "  tick %s  bars-epoch=%d  fingerprints=%d findings=%d  digest %s…  %s\n",
				tk.EvalNow.UTC().Format("15:04:05.000Z"), tk.BarsEpoch, tk.Fingerprints, tk.Findings,
				tk.ReplayedDigest[:16], mark)
		}
	}

	// Export runs even on a mismatching bundle (the Parquet is diagnosis input),
	// but an export failure must never MASK a determinism verdict.
	var exportErr error
	if *parquetOut != "" {
		rows, err := export.Parquet(*bundle, *parquetOut)
		if err != nil {
			exportErr = fmt.Errorf("parquet export: %w", err)
			fmt.Fprintf(out, "  parquet export FAILED: %v\n", err)
		} else {
			fmt.Fprintf(out, "  exported %d readings -> %s\n", rows, *parquetOut)
		}
	}

	if rep.Mismatches > 0 {
		return fmt.Errorf("DETERMINISM VIOLATION: %d of %d ticks did not reproduce byte-identically", rep.Mismatches, len(rep.Ticks))
	}
	if len(rep.Ticks) == 0 {
		// Zero ticks verified is not a pass — it is nothing. A capture that
		// recorded no evaluation must not print a vacuous green verdict.
		return fmt.Errorf("no evaluation ticks in the bundle: nothing was verified")
	}
	if exportErr != nil {
		return exportErr
	}
	if rep.EvaluationMode {
		fmt.Fprintf(out, "  evaluation complete: %d ticks re-derived under the override (no verification claim)\n", len(rep.Ticks))
		return nil
	}
	fmt.Fprintf(out, "  verdict: all %d ticks replayed byte-identically (doc 05 §3.5 holds)\n", len(rep.Ticks))
	return nil
}

func short(v string) string {
	if len(v) > 19 {
		return v[:19] + "…"
	}
	return v
}
