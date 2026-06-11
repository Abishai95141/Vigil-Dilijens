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
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/version"
)

// phase0aCoverageTarget is the join-coverage threshold for the Phase-0a exit gate.
// Mis-joins must be zero regardless; coverage tolerates transient observe lag.
const phase0aCoverageTarget = 0.99

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
		healthAddr  = fs.String("health-addr", ":9095", "address for the health/metrics server (/metrics, /healthz, /readyz)")
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
	return runIdentity(ctx, logger, p, *kubeconfig, *healthAddr, stdout)
}

// runIdentity wires and runs the identity & correlation layer against the cluster,
// serving health/metrics and printing a live entity inventory + join-audit verdict.
func runIdentity(ctx context.Context, logger *slog.Logger, p params.Params, kubeconfig, healthAddr string, out io.Writer) error {
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

	// Join-audit + health metrics (doc 03 §6, M5): the collector reads the stores
	// and runs the live consistency audit (the Phase-0a exit gate) on each scrape.
	auditor := identity.NewAuditor()
	registry := prometheus.NewRegistry()
	registry.MustRegister(identity.NewCollector(store, edges, auditor, watcher.AuditConsistencyNow, phase0aCoverageTarget))

	// Bind the health/metrics listener SYNCHRONOUSLY so a bind failure (port in use,
	// no permission) fails the process loudly instead of leaving it running gate-blind
	// with no /metrics and no exit-gate signal (doc 11: a gate that can vanish
	// unnoticed is out of process).
	ln, err := net.Listen("tcp", healthAddr)
	if err != nil {
		return fmt.Errorf("bind health server on %s: %w", healthAddr, err)
	}
	go serveHealth(ctx, logger, ln, registry, watcher)
	go inventoryLoop(ctx, out, logger, store, edges, watcher, clusterID, p.Observation.EvaluationTick.Duration())

	logger.Info("running identity & correlation layer (doc 03) — Ctrl-C to stop", "health_addr", ln.Addr().String())
	return watcher.Run(ctx)
}

// serveHealth exposes /metrics (Prometheus), /healthz (liveness), and /readyz
// (informer sync) — the health-metrics endpoints of doc 03 §6 — on an already-bound
// listener (so bind failures are surfaced by the caller, not swallowed here).
func serveHealth(ctx context.Context, logger *slog.Logger, ln net.Listener, registry *prometheus.Registry, watcher *identity.Watcher) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if watcher.HasSynced() {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ready")
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "syncing")
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	logger.Info("health server listening", "addr", ln.Addr().String(), "endpoints", "/metrics /healthz /readyz")
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Error("health server failed after bind", "err", err)
	}
}

// inventoryLoop periodically surfaces the identity inventory: the operational health
// summary + Phase-0a gate verdict to the logger (stderr), and the live, correctly-
// joined per-service entity inventory table to out (stdout) — "prerequisite zero,
// observable" (doc 03 §6, CLAUDE.md demo target). It renders once as soon as the
// informers sync, then on every evaluation tick.
func inventoryLoop(ctx context.Context, out io.Writer, logger *slog.Logger, store *identity.Store, edges *identity.EdgeStore, watcher *identity.Watcher, clusterID string, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	render := func() {
		now := time.Now()
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

		// The Phase-0a exit gate, live: join accuracy with zero mis-joins.
		rep := watcher.AuditConsistencyNow()
		g := identity.EvaluateGate(rep, phase0aCoverageTarget)
		logger.Info("join audit (phase-0a gate)",
			"join_accuracy", g.JoinAccuracy,
			"coverage", g.Coverage,
			"misjoins", rep.Misjoins,
			"missing", rep.Missing,
			"checked", rep.CheckedPods+rep.CheckedNodes,
			"gate_passed", g.Passed,
		)
		if rep.Misjoins > 0 {
			logger.Error("MIS-JOINS detected (the silent killer) — Phase-0a gate fails", "count", rep.Misjoins, "details", rep.Details)
		}

		// The human-facing artifact: the named, correctly-joined inventory (stdout).
		renderInventory(out, store, edges, clusterID, now, rep, g)
	}

	// Render promptly once the informer caches have synced rather than waiting a full
	// tick, so the demo shows a populated inventory within a second or two.
	if waitForSync(ctx, watcher, every) {
		render()
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			render()
		}
	}
}

// waitForSync blocks until the watcher's informers have synced, ctx is cancelled, or
// the bound elapses; it reports whether the caches are synced. Polling lives in the
// display loop (cmd), never in identity logic, so the no-time.Now-in-logic rule holds.
func waitForSync(ctx context.Context, watcher *identity.Watcher, bound time.Duration) bool {
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for {
		if watcher.HasSynced() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return watcher.HasSynced()
		case <-poll.C:
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
