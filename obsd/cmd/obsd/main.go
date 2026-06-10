// Command obsd is the Vigil runtime core: the modular monolith that runs identity,
// binding, observation, selection, detection, the unexplained channel, and the
// clock client (doc 00 §5, techstack §4). One binary, strict internal package
// boundaries mirroring the blueprint docs.
//
// Phase 0a: it loads and validates the parameters file (doc 14 §5), and — when
// pointed at a cluster — runs the identity & correlation layer (doc 03), printing
// a live, correctly-joined entity inventory ("prerequisite zero, observable"). It
// does not yet scrape, bind, or detect.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
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
		kubeconfig  = fs.String("kubeconfig", "", "path to a kubeconfig; when set (or --in-cluster), run the identity layer against the cluster")
		inCluster   = fs.Bool("in-cluster", false, "use in-cluster config to reach the API server")
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

	if *kubeconfig == "" && !*inCluster {
		// No cluster target: there is nothing truthful to report about a live
		// cluster, so say exactly that rather than inventing output.
		logger.Warn("no cluster target — pass --kubeconfig or --in-cluster to run the identity layer (doc 03)")
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runIdentity(ctx, logger, p, *kubeconfig)
}

// runIdentity wires and runs the identity & correlation layer against the cluster,
// printing a live entity inventory.
func runIdentity(ctx context.Context, logger *slog.Logger, p params.Params, kubeconfig string) error {
	client, err := kube.NewClientset(kubeconfig)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	clusterID, err := identity.ClusterID(ctx, client)
	if err != nil {
		return fmt.Errorf("resolve cluster id: %w", err)
	}
	logger.Info("resolved cluster identity", "cluster_id", clusterID)

	store := identity.NewStore(
		time.Now,
		p.Identity.Tombstone.FullRetention.Duration(),
		p.Identity.Tombstone.StubRetention.Duration(),
		p.Identity.Tombstone.MaxEntries,
	)
	// Convert the parameters file's string-keyed edge budgets (doc 14 §1.2) to the
	// typed map the edge store wants. Kept here so the identity package stays free
	// of a params dependency.
	budgets := make(map[identity.EdgeType]time.Duration, len(p.Identity.EdgeBudgets))
	for k, v := range p.Identity.EdgeBudgets {
		budgets[identity.EdgeType(k)] = v.Duration()
	}
	edges := identity.NewEdgeStore(time.Now, budgets, p.Identity.RetractedEdgeHorizon.Duration())

	watcher, err := identity.NewWatcher(client, store, edges, clusterID, p.Identity.Reconciliation.Duration(), logger)
	if err != nil {
		return fmt.Errorf("identity watcher: %w", err)
	}

	go inventoryLoop(ctx, logger, store, edges, p.Observation.EvaluationTick.Duration())

	logger.Info("running identity & correlation layer (doc 03) — Ctrl-C to stop")
	return watcher.Run(ctx)
}

// inventoryLoop periodically logs the identity inventory: the live, correctly-
// joined entity counts and the layer's health metrics (doc 03 §6).
func inventoryLoop(ctx context.Context, logger *slog.Logger, store *identity.Store, edges *identity.EdgeStore, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m := store.Metrics()
			e := edges.Metrics()
			logger.Info("identity inventory",
				"active", m.Active,
				"full_tombstones", m.FullTombstones,
				"stub_tombstones", m.StubTombstones,
				"discovered_total", m.Discovered,
				"terminated_total", m.Terminated,
				"successions", m.Successions,
				"degraded_joins", m.DegradedJoins,
				"evicted_before_horizon", m.EvictedBeforeHorizon,
				"edges_live", e.Live,
				"edges_suspect", e.Suspect,
				"edges_retracted", e.Retracted,
			)
		}
	}
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
