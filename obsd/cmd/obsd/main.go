// Command obsd is the Vigil runtime core: the modular monolith that runs identity,
// binding, observation, selection, detection, the unexplained channel, and the
// clock client (doc 00 §5, techstack §4). One binary, strict internal package
// boundaries mirroring the blueprint docs.
//
// This is the Phase-0a skeleton. It loads and validates the parameters file
// (doc 14 §5) and prints a startup banner. It does NOT yet stand up informers,
// scraping, or the identity join — those are the next deliverables on the
// critical path (doc 03, prerequisite zero). The skeleton is deliberately honest
// about what is not yet implemented rather than printing a fake inventory.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "obsd:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("obsd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		paramsPath  = fs.String("params", "", "path to a parameters override file (overlays embedded dev defaults)")
		showVersion = fs.Bool("version", false, "print version and exit")
		logFormat   = fs.String("log", "json", "log format: json|text")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, version.String())
		return nil
	}

	logger := newLogger(stderr, *logFormat)
	slog.SetDefault(logger)

	p, err := params.Load(*paramsPath)
	if err != nil {
		return fmt.Errorf("load parameters: %w", err)
	}

	logger.Info("obsd starting",
		"version", version.Version,
		"profile", p.Profile,
		"params_version", p.Version,
		"scrape_interval", p.Scrape.Interval.String(),
		"evaluation_tick", p.Observation.EvaluationTick.String(),
		"tier_b_budget", p.Selection.TierBBudgetPerCycle,
	)

	// Prerequisite zero (doc 03): the identity & correlation layer is the next
	// thing built here. Until it exists, obsd has nothing truthful to report
	// about a live cluster, so it says exactly that instead of inventing output.
	logger.Warn("identity layer not yet implemented — obsd has no live cluster join to report",
		"next", "doc 03 M1: CEI scheme + normalization maps",
	)
	return nil
}

func newLogger(w *os.File, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts))
	default:
		return slog.New(slog.NewJSONHandler(w, opts))
	}
}
