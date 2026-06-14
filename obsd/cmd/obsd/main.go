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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
	fstore "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/version"
)

// phase0aCoverageTarget is the join-coverage threshold for the Phase-0a exit gate.
// Mis-joins must be zero regardless; coverage tolerates transient observe lag.
const phase0aCoverageTarget = 0.99

// Curation-feedback thresholds (doc 08 §3.6 / §8 open question): a recurring
// unexplained signature surfaces as a candidate-phenomenon report once it has
// persisted this many evaluation windows OR appeared on this many distinct
// entities — recurrence across TIME or across ENTITIES is each a growth signal.
// Off the deterministic path; tunable policy, held here until governance owns it.
const (
	unexplainedCurationWindows  = 20 // ~5 min at the 15s dev tick
	unexplainedCurationEntities = 3
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
		paramsPath   = fs.String("params", "", "path to a parameters override file (overlays embedded dev defaults)")
		showVersion  = fs.Bool("version", false, "print version and exit")
		logFormat    = fs.String("log", "json", "log format: json|text")
		kubeconfig   = fs.String("kubeconfig", "", "path to a kubeconfig; when set (or --in-cluster), run the identity layer against the cluster")
		inCluster    = fs.Bool("in-cluster", false, "use in-cluster config to reach the API server")
		healthAddr   = fs.String("health-addr", ":9095", "address for the health/metrics server (/metrics, /healthz, /readyz)")
		ontology     = fs.String("ontology", "ontology/graph/k8s_signal_kg.json", "ontology KG release; with a cluster target, enables the binding compiler (doc 04)")
		overlays     = fs.String("overlays", "ontology/graph/overlays", "authored overlay dir (spans, threshold rules) merged into the ontology")
		releases     = fs.String("releases", "ontology/releases", "graph release manifests (doc 12 M1); the loaded graph self-identifies its release by hash")
		storeDir     = fs.String("store-dir", "", "directory for the qss warm tier + replay bundle (doc 14 §2.3); empty = hot rings only (replay capture off, stated)")
		dbPath       = fs.String("db", "", "SQLite findings database (doc 14 A7); empty = in-memory (findings reset on restart)")
		apiEnabled   = fs.Bool("api", true, "serve the operator surfacing API (doc 10) under /api on the health server")
		dumpBindings = fs.String("dump-bindings", "", "write the compiled binding.Result to this JSON path once (governance migration exercise, doc 12 M4)")
		flowEnabled  = fs.Bool("flow-enabled", false, "v2 (doc 15): collect conntrack via the per-node conntrack-agent and assert observed-flow edges (OFF by default; off = byte-identical to no flow)")
		flowInterval = fs.Duration("flow-interval", 15*time.Second, "v2: flow collector cadence")
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

	// Load the ontology release (with authored overlays) for the binding compiler.
	// Binding is NON-GATING (doc 01): identity runs identically whether the
	// ontology is present, defective, or absent — absence is stated, never fatal.
	var ontologyGraph *graph.Graph
	if *ontology != "" {
		g, err := graph.LoadWithOverlays(*ontology, *overlays)
		if err != nil {
			logger.Warn("binding disabled: ontology release not loadable (identity is unaffected)", "path", *ontology, "err", err)
		} else {
			ontologyGraph = g
			// Self-identify the release (doc 12 M1): match the loaded content hash
			// against committed manifests. An unmatched hash is an UNRELEASED dev
			// build — stated, never silently presented as a release.
			g.Release = graph.IdentifyRelease(g, *releases)
			if g.Release == "" {
				logger.Warn("ontology is an UNRELEASED dev build — no release manifest matches its hash (cut one with `just graph-version`, doc 12 M1)",
					"version", g.Version[:sha256PreviewLen])
			} else {
				logger.Info("ontology release loaded",
					"release", g.Release, "version", g.Version[:sha256PreviewLen], "nodes", g.NodeCount(),
					"edges", len(g.Edges), "threshold_rules", len(g.Rules), "overlays", len(g.Overlays))
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runIdentity(ctx, logger, p, *kubeconfig, *healthAddr, stdout, ontologyGraph, *storeDir, *dbPath, *apiEnabled, *dumpBindings, *flowEnabled, *flowInterval)
}

// sha256PreviewLen truncates "sha256:<64 hex>" for log lines; the full pin stays on
// the Result and the coverage report.
const sha256PreviewLen = len("sha256:") + 12

// runIdentity wires and runs the identity & correlation layer against the cluster,
// serving health/metrics and printing a live entity inventory + join-audit verdict.
func runIdentity(ctx context.Context, logger *slog.Logger, p params.Params, kubeconfig, healthAddr string, out io.Writer, ontologyGraph *graph.Graph, storeDir, dbPath string, apiEnabled bool, dumpBindings string, flowEnabled bool, flowInterval time.Duration) error {
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
	// v2 (doc 15 §3.4): the OPTIONAL flow edge budget is injected here, never via
	// params.requiredEdgeBudgets (which validates the fixed structural set every
	// cluster must have). When --flow-enabled it is pinned into the EdgeStore and the
	// replay manifest; when off, flow is wholly absent and obsd is unchanged.
	if flowEnabled {
		budgets[flow.EdgeTypeFlow] = flowEdgeBudget
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
	// Observation ingest (doc 05 M1, doc 14 A1): we own the scraper — cAdvisor per
	// node via the API-server proxy, every series through the identity normalizer,
	// CEI-stamped samples into the hot store. Non-gating like everything else:
	// identity runs identically if scraping fails (failures are stated, counted).
	hot := qss.NewHotStore()
	ingestor := observe.NewIngestor(identity.NewNormalizer(clusterID, store), hot)

	fpParams := observe.FPParams{
		ScrapeInterval:     p.Scrape.Interval.Duration(),
		RateWindow:         p.Observation.RateWindow.Duration(),
		Watermark:          p.Observation.Watermark.Duration(),
		Band:               p.Observation.AtThresholdBand,
		WellAboveFactor:    p.Observation.WellAboveFactor,
		CooccurrenceWindow: p.Observation.DefaultCooccurrenceWindow.Duration(),
		MinCompleteness:    p.Detection.MinCompleteness,
		CascadeWindow:      p.Detection.CascadeWindow.Duration(),
	}

	// Replay capture (doc 05 M5, doc 14 §2.3): the qss warm tier + bundle writer.
	// Off when no --store-dir (hot rings only — replay capture off, stated above).
	// Capture requires the pinned ontology: a bundle without its graph version
	// could not be replayed exactly, so we refuse to write a half-bundle.
	var capture *replay.Capture
	if storeDir != "" {
		if ontologyGraph == nil {
			logger.Warn("replay capture disabled: --store-dir set but no ontology release loaded (a bundle must pin its graph version)")
		} else {
			c, err := replay.NewCapture(storeDir, qss.WarmConfig{
				SegmentDuration: p.Store.SegmentDuration.Duration(),
				Retention:       p.Store.WarmRetention.Duration(),
				FsyncBatch:      p.Store.FsyncBatch.Duration(),
			})
			if err != nil {
				return fmt.Errorf("replay capture: %w", err)
			}
			stringBudgets := make(map[string]time.Duration, len(p.Identity.EdgeBudgets))
			for k, v := range p.Identity.EdgeBudgets {
				stringBudgets[k] = v.Duration()
			}
			if flowEnabled { // pin the flow budget so replay walks under the capture's budget
				stringBudgets[string(flow.EdgeTypeFlow)] = flowEdgeBudget
			}
			if err := c.WriteManifest(replay.Manifest{
				CreatedAt: time.Now().UTC(), ClusterID: clusterID,
				GraphVersion: ontologyGraph.Version, ParamsVersion: p.Version, Profile: p.Profile,
				FPParams: fpParams, ScrapeInterval: p.Scrape.Interval.Duration(),
				HotRingCapacity: qss.HotCapacity(), ObsdVersion: version.Version,
				EdgeBudgets: stringBudgets,
				Contents: []string{"readings (qss segments, arrival-ordered)", "resolved bars per epoch",
					"topology snapshot per tick (edges with validity, doc 07 M2)", "evaluation ticks with digests"},
				Absent: []string{"time-shifted evaluation (lands with harness suites)"},
			}); err != nil {
				return fmt.Errorf("replay capture: %w", err)
			}
			capture = c
			defer func() {
				if err := capture.Close(); err != nil {
					logger.Error("replay capture: seal on shutdown failed", "err", err)
				} else {
					logger.Info("replay bundle sealed", "dir", storeDir)
				}
			}()
			warm := c.Warm()
			if warm.Recovered.Segments > 0 || warm.Recovered.TruncatedBytes > 0 {
				logger.Warn("warm tier crash recovery", "sealed_segments", warm.Recovered.Segments, "truncated_bytes", warm.Recovered.TruncatedBytes)
			}
			ingestor.SetTap(func(def qss.StreamDef, recv time.Time, s qss.Sample) {
				if err := warm.Append(def, recv, s); err != nil {
					logger.Error("warm tier append failed (capture is now incomplete)", "err", err)
				}
			})
			logger.Info("replay capture active (doc 05 M5)", "dir", storeDir,
				"segment", p.Store.SegmentDuration.Duration().String(), "retention", p.Store.WarmRetention.Duration().String())
		}
	}

	// Surfacing back end (doc 10): the SQLite findings store (A7) and the
	// published Coverage Report snapshot the API serves. Both are OFF the
	// deterministic path — a surfacing failure never perturbs detection/replay.
	graphVersion, graphRelease := "", ""
	if ontologyGraph != nil {
		graphVersion = ontologyGraph.Version
		graphRelease = ontologyGraph.Release
	}
	var findingsStore *fstore.Store
	if apiEnabled {
		s, err := fstore.Open(dbPath)
		if err != nil {
			logger.Error("findings store disabled: open failed (surfacing only, non-gating)", "err", err)
		} else {
			findingsStore = s
			defer findingsStore.Close()
		}
	}
	var coverage atomic.Pointer[vapi.CoverageView]
	coverage.Store(vapi.BuildCoverage(clusterID, graphVersion, graphRelease, time.Now(), nil, nil, nil))
	// The operator surfaces (doc 10 M2–M4), published each tick like coverage —
	// off the deterministic path, race-free via atomics.
	var unexpView atomic.Pointer[vapi.UnexplainedView]
	var insightsView atomic.Pointer[vapi.InsightsView]
	var topoView atomic.Pointer[vapi.TopologyView]
	// The early-warning lane (doc 10 M5 / 09 M4): dark unless forecast.enabled
	// — the gate rule (no class is operator-visible before its backtest gate,
	// doc 11 §3.5) is enforced by configuration, stated by the surface.
	var warningsView atomic.Pointer[vapi.WarningsView]
	// v2 cross-service cascade (doc 15 phase D/F), warm path: published each tick
	// off the deterministic digest, surfaced (gated) at /api/cross-service.
	var crossSvcView atomic.Pointer[flow.Chain]
	var fcIn *atomic.Pointer[forecastInputs]
	if p.Forecast.Enabled {
		fcIn = new(atomic.Pointer[forecastInputs])
	}

	// The store gate: scrape cycles write under the write lock; evaluation ticks
	// read under the read lock. Ticks therefore always observe whole scrape
	// cycles — the visibility property the replay digest depends on (doc 05 §3.5).
	// Fetching (network) happens OUTSIDE the lock; only parse+ingest holds it.
	var gate sync.RWMutex

	go scrapeLoop(ctx, logger, &gate, ingestor, kube.NewProxyFetcher(client), watcher, p.Scrape.Interval.Duration())

	// v2 flow lane (doc 15): observe conntrack from the per-node agent and assert
	// observed-flow edges into the same EdgeStore the tick snapshots. Off by default;
	// non-gating; the matcher does not yet walk flow edges, so the digest is unchanged.
	// The eval tick additionally composes a WARM-PATH cross-service cascade (phase D)
	// from this tick's findings + the flow edges + the authored relation — off the
	// digest, gated (logged, not yet a deterministic finding).
	var flowRel flow.Relation
	if flowEnabled {
		if r, rerr := flow.LoadRelation(flowRelationPath); rerr != nil {
			logger.Warn("flow: authored relation unavailable; cross-service cascade off", "path", flowRelationPath, "err", rerr)
		} else {
			flowRel = r
		}
		go runFlowCollector(ctx, logger, &gate, client, store, edges, clusterID, flowInterval)
	}

	// The operator context-window store (10 M6) is shared: the API serves/accepts
	// windows, and the forecast loop reads their boundaries as decomposition splice
	// points (09 M5 §3.4). Created once so both see the same windows.
	cwStore := vapi.NewContextWindowStore()

	var providers *vapi.Providers
	if apiEnabled {
		providers = &vapi.Providers{
			Coverage:    func() *vapi.CoverageView { return coverage.Load() },
			Unexplained: func() *vapi.UnexplainedView { return unexpView.Load() },
			Insights:    func() *vapi.InsightsView { return insightsView.Load() },
			Topology:    func() *vapi.TopologyView { return topoView.Load() },
			Warnings:    func() *vapi.WarningsView { return warningsView.Load() },
			// v2 cross-service cascade surface (doc 15 phase F): the warm-path chain,
			// or the honest OFF/quiet state. flowEnabled drives the OFF-vs-quiet split.
			CrossService: func() *vapi.CrossServiceView {
				return vapi.BuildCrossService(crossSvcView.Load(), flowEnabled, time.Now().UTC())
			},
		}
		if findingsStore != nil {
			providers.Findings = findingsStore.ActiveFindings
			// The durable store can hold a finding whose last match was many ticks
			// ago; serve anything older than ~3 eval ticks as stale, not firing-now.
			providers.FindingsStaleAfter = 3 * p.Observation.EvaluationTick.Duration()
			// The anomaly timeline (doc 10 M4) composes the durable match +
			// unexplained history; reads the store on request (off the hot path).
			providers.Timeline = func() (*vapi.TimelineView, error) {
				fr, err := findingsStore.ActiveFindings(200)
				if err != nil {
					return nil, err
				}
				ur, err := findingsStore.ActiveUnexplained(200)
				if err != nil {
					return nil, err
				}
				// Projected lane (10 M5): the current early-warning bands,
				// only when the lane is enabled (the gate rule).
				var pw []vapi.WarningCard
				laneOn := false
				if wv := warningsView.Load(); wv != nil && wv.Enabled {
					pw = wv.Warnings
					laneOn = true
				}
				return vapi.BuildTimeline(time.Now().UTC(), fr, ur, pw, laneOn), nil
			}
		}
		// Context windows (doc 10 M6, begun) + register-guarded chat (10 M7, begun).
		providers.ContextWindows = cwStore
		providers.Chat = func() *vapi.ChatSnapshot {
			snap := &vapi.ChatSnapshot{CoverageNote: "see /api/coverage for the full visibility map"}
			if cv := coverage.Load(); cv != nil {
				snap.GraphRelease = cv.GraphRelease
				snap.CoverageTierA = cv.Summary.TierA
				snap.CoverageNote = fmt.Sprintf("%d full / %d partial / %d none phenomena observable.",
					cv.Summary.PhenomenaFull, cv.Summary.PhenomenaPartial, cv.Summary.PhenomenaNone)
			}
			if iv := insightsView.Load(); iv != nil {
				for _, f := range iv.Findings {
					m := vapi.ChatMatch{Phenomenon: f.Phenomenon, Label: f.Label, Entity: f.Name, Quality: f.Quality}
					if len(f.Members) > 0 {
						m.AuthoredNote = f.Members[0].Note
					}
					// NOTE: blast radius is the DOWNSTREAM (T0+) at-risk set, not
					// precursors (T0-) — feeding it into Precursors would mislabel a
					// consequence as a cause. Precursors are an authored T0- relation
					// the insight card does not currently carry; left empty (the chat
					// then cites the authored note, never an invented precursor).
					snap.Matches = append(snap.Matches, m)
				}
			}
			if wv := warningsView.Load(); wv != nil && wv.Enabled {
				for _, c := range wv.Warnings {
					snap.Warnings = append(snap.Warnings, vapi.ChatWarning{
						Entity: c.Name, Metric: c.Metric, EarliestAt: c.EarliestAt, LatestAt: c.LatestAt,
						OpenEnded: c.LatestBeyondHorizon, Confidence: c.Confidence,
					})
				}
			}
			if uv := unexpView.Load(); uv != nil {
				snap.UnexplainedN = len(uv.OpenCards)
			}
			return snap
		}
		// Config (doc 10 M6): runtime configuration + the forecast lane's gate
		// posture, so an operator can read the system's settings (and WHY the
		// soon-lane is dark) without inferring it from the warnings tab.
		providers.Config = func() *vapi.ConfigView {
			fcNote := "Early warnings ON: the shipped class(es) passed the backtest calibration gate (doc 11 §3.5)."
			if !p.Forecast.Enabled {
				fcNote = vapi.ForecastGateNote()
			}
			return &vapi.ConfigView{
				GeneratedAt:    time.Now().UTC(),
				ClusterID:      clusterID,
				Profile:        p.Profile,
				ParamsVersion:  p.Version,
				GraphRelease:   graphRelease,
				GraphVersion:   graphVersion,
				ScrapeInterval: p.Scrape.Interval.Duration().String(),
				EvaluationTick: p.Observation.EvaluationTick.Duration().String(),
				TierBBudget:    p.Selection.TierBBudgetPerCycle,
				Forecast: vapi.ForecastConfigView{
					Enabled:              p.Forecast.Enabled,
					GateNote:             fcNote,
					ClockdTarget:         p.Forecast.ClockdTarget,
					Interval:             p.Forecast.Interval.Duration().String(),
					HorizonSteps:         p.Forecast.HorizonSteps,
					MinContext:           p.Forecast.MinContext,
					Decompose:            p.Forecast.Decompose,
					ResetDropFraction:    p.Forecast.ResetDropFraction,
					MaxExplainedFraction: p.Forecast.MaxExplainedFraction,
				},
				Note: "System configuration (not a provenance-classed finding). " +
					"Detection runs every evaluation tick regardless of the forecast lane (non-gating, doc 01).",
			}
		}
		logger.Info("operator surfacing API enabled (doc 10 M1–M7-begun + doc 15 F)",
			"routes", "/api/coverage /api/findings /api/insights /api/topology /api/unexplained /api/timeline /api/warnings /api/cross-service /api/context-windows /api/chat /api/config")
	}
	go serveHealth(ctx, logger, ln, registry, watcher, providers)
	go inventoryLoop(ctx, out, logger, &gate, store, edges, watcher, clusterID, graphVersion, graphRelease, p.Observation.EvaluationTick.Duration(),
		&binder{graph: ontologyGraph, client: client, logger: logger, ingestor: ingestor, fpParams: fpParams, dumpPath: dumpBindings},
		capture, &coverage, &unexpView, &insightsView, &topoView, findingsStore, budgets,
		fcIn, p.Selection.TierBBudgetPerCycle, &warningsView, flowEnabled, flowRel, &crossSvcView)
	if p.Forecast.Enabled {
		go forecastLoop(ctx, logger, &gate, fcIn, ingestor, graphVersion, graphRelease, p, &warningsView, cwStore)
	}

	logger.Info("running identity & correlation layer (doc 03) — Ctrl-C to stop", "health_addr", ln.Addr().String())
	return watcher.Run(ctx)
}

// serveHealth exposes /metrics (Prometheus), /healthz (liveness), and /readyz
// (informer sync) — the health-metrics endpoints of doc 03 §6 — on an already-bound
// listener (so bind failures are surfaced by the caller, not swallowed here).
func serveHealth(ctx context.Context, logger *slog.Logger, ln net.Listener, registry *prometheus.Registry, watcher *identity.Watcher, providers *vapi.Providers) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if providers != nil {
		// The operator surfacing API (doc 10) shares the health listener so the web
		// app's /api proxy target is the one bound port.
		vapi.Register(mux, *providers)
	}
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
func inventoryLoop(ctx context.Context, out io.Writer, logger *slog.Logger, gate *sync.RWMutex, store *identity.Store, edges *identity.EdgeStore, watcher *identity.Watcher, clusterID, graphVersion, graphRelease string, every time.Duration, bnd *binder, capture *replay.Capture, coverage *atomic.Pointer[vapi.CoverageView], unexpView *atomic.Pointer[vapi.UnexplainedView], insightsView *atomic.Pointer[vapi.InsightsView], topoView *atomic.Pointer[vapi.TopologyView], findingsStore *fstore.Store, budgets map[identity.EdgeType]time.Duration, fcIn *atomic.Pointer[forecastInputs], tierBBudget int, warningsView *atomic.Pointer[vapi.WarningsView], flowEnabled bool, flowRel flow.Relation, crossSvcView *atomic.Pointer[flow.Chain]) {
	// Per-edge-type budgets as the topology builder wants them (string-keyed).
	strBudgets := make(map[string]time.Duration, len(budgets))
	for k, v := range budgets {
		strBudgets[string(k)] = v
	}
	if every <= 0 {
		every = 15 * time.Second
	}
	render := func() {
		// Hold the store gate's read side for the whole evaluation so the tick sees
		// whole scrape cycles, never a half-ingested one (the replay guarantee).
		gate.RLock()
		defer gate.RUnlock()
		now := time.Now().UTC()
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

		// The human-facing artifacts (stdout): the named, correctly-joined
		// inventory, then the bound customer graph's coverage report (doc 04) —
		// presented adjacently, each from its own source, never fused.
		active := store.ActiveInstances()
		renderInventory(out, active, edges, clusterID, now, rep, g)
		var fps []observe.Fingerprint
		var findings []detect.Finding
		var cascades []detect.Cascade
		var unexp []unexplained.Finding
		barsEpoch := 0
		barsOK := true
		// Topology snapshot (doc 07 M2/§3.7): ONE canonical snapshot per tick
		// drives evaluation AND the capture record. The matcher walks a store
		// REBUILT from the snapshot — never the live store, which informers keep
		// mutating — so recorded == evaluated by construction.
		topoSnap := edges.Snapshot()
		var topo detect.Topology
		var selTopo selection.Topology
		topoOK := true
		if ts, terr := identity.NewEdgeStoreFromSnapshot(topoSnap, budgets); terr != nil {
			// Cannot happen for a store's own snapshot; if it ever does, the tick
			// is honestly un-walkable: entity-local only, capture suppressed
			// (recording a topology the evaluation did not use would be a lie).
			logger.Error("topology snapshot rebuild failed; first-order matching skipped this tick", "err", terr)
			topoOK = false
		} else {
			topo, selTopo = ts, ts
		}
		evalWindow := identity.TimeWindow{Start: now.Add(-bnd.fpParams.CooccurrenceWindow), End: now}
		// NOTE: compile holds the gate's read side through its discovery-time kube
		// List calls (it must: binding QA reads live stream evidence under the same
		// gate). A scrape cycle's ingest can therefore wait on a re-bind tick for
		// the duration of those calls — a stated latency trade-off, not a
		// correctness issue: the delayed cycle lands whole, after this tick, in
		// both the rings and the capture log.
		if bd := bnd.compile(ctx, active, now); bd != nil {
			renderBinding(out, bd, boutiqueNamespace)
			// Monitoring selection (doc 06 M1+M2): gates 1–2 + reason codes, plus
			// neighbourhood closure over the same snapshot detection walks. The
			// deterministic Tier-A core feeds detection; the full records (incl.
			// the none-list and closure gaps) are the audit surface.
			selected := selection.TierASet(bd.Result, bnd.graph)
			selResult := selection.Select(active, bd.Result, bnd.graph, bd.Stale, now, "evaluation-tick", selTopo, evalWindow)
			renderSelection(out, selResult)
			// Live fingerprints (doc 05 M3): the first MEASURED "what is happening
			// now" — re-materialized each tick against fresh samples.
			fps = bnd.fingerprints(now)
			renderFingerprints(out, fps, boutiqueNamespace)
			// Phenomenon matches (doc 07 M1–M3) over the SELECTED fingerprints
			// (doc 06 provides the Tier-A set to detection), then cascade
			// recognition over the authored relations (doc 07 M5).
			findings = bnd.detectFindings(fps, selected, topo, evalWindow)
			renderFindings(out, findings, bnd.lastObs)
			cascades = bnd.cascades(now, findings, topo, evalWindow)
			renderCascades(out, cascades)
			// Unexplained channel (doc 08): loud-but-unmatched routing — the
			// blind-spot patch, after detection so coverage sees this tick's
			// matches. Part of the digest (windowed, deterministic).
			unexp = bnd.routeUnexplained(now, fps, findings)
			renderUnexplained(out, unexp, bnd.unexp)
			// Surfacing (doc 10 M1): publish the Coverage Report snapshot the API
			// serves, and persist the findings feed (A7). Off the deterministic
			// path — failures are logged, never allowed to perturb the tick.
			if coverage != nil {
				coverage.Store(vapi.BuildCoverage(clusterID, graphVersion, graphRelease, now, bd.Result, bnd.lastObs, selResult))
			}
			// The unexplained-channel snapshot (doc 08): open cards + curation
			// candidates + the residual blind-spot notice, surfaced to /api.
			if unexpView != nil && bnd.unexp != nil {
				unexpView.Store(&vapi.UnexplainedView{
					GeneratedAt: now, GraphVersion: graphVersion,
					OpenCards:  bnd.unexp.OpenCards(),
					Candidates: bnd.unexp.Candidates(unexplainedCurationWindows, unexplainedCurationEntities),
					BlindSpot:  unexplained.BlindSpotNotice,
				})
			}
			// The "now" insight surface (doc 10 M2) + the topology surface (doc 10
			// M3): live snapshots, current marks only (predictive marks are a
			// separate visual language added in M5/Phase 2).
			if insightsView != nil {
				insightsView.Store(vapi.BuildInsights(clusterID, graphVersion, graphRelease, now, findings, cascades))
			}
			// v2 cross-service cascade (doc 15 phase D), WARM PATH — off the digest,
			// gated (logged, not yet a deterministic finding). Map this tick's findings
			// to their workloads (the MEASURED degraded callees) via the identity store,
			// then walk the observed-flow edges to the impacted callers + join the one
			// AUTHORED relation. A pure consequence of (findings, captured flow topology),
			// so it never perturbs the tick or the replay digest.
			if flowEnabled && crossSvcView != nil {
				var degraded []flow.DegradedWorkload
				seen := map[string]bool{}
				for i := range findings {
					rec, ok := store.Get(findings[i].EntityCEI)
					if !ok || rec.RoleCEI.RoleKey == "" {
						continue
					}
					k := rec.RoleCEI.Key()
					if seen[k] {
						continue
					}
					seen[k] = true
					degraded = append(degraded, flow.DegradedWorkload{
						CEI: rec.RoleCEI, Label: flow.RoleLabel(rec.RoleCEI),
						Phenomenon: findings[i].Phenomenon, Detail: findings[i].Phenomenon + " finding (degraded callee)",
					})
				}
				if chain, ok := flow.CrossServiceChain(edges, degraded, flowRel, evalWindow, now); ok {
					c := chain
					crossSvcView.Store(&c)
					logger.Info("cross-service cascade (v2 phase D, warm path)",
						"root", chain.MostUpstreamDegradedNode, "impacted_callers", len(chain.Links),
						"why_class", "AUTHORED", "edge_class", "MEASURED observed flow")
				} else {
					crossSvcView.Store(nil)
				}
			}
			if topoView != nil {
				// Predictive marks (10 M5): the warned set from the latest
				// forecast cycle, a SEPARATE visual language from current
				// marks; lags the warm path by at most one tick.
				warned := map[string]bool{}
				if wv := warningsView.Load(); wv != nil {
					for i := range wv.Warnings {
						warned[wv.Warnings[i].EntityCEI] = true
					}
				}
				topoView.Store(vapi.BuildTopology(clusterID, graphVersion, now, active, topoSnap, strBudgets, findings, unexp, selected, warned))
			}
			// Freeze the warm path's inputs (doc 09 M4): Tier-B targets under
			// the budget (doc 06 M5) plus the context the blast-radius walk
			// needs. The forecast loop only reads this snapshot — published
			// here so its view of bindings/topology is exactly one tick's.
			if fcIn != nil {
				targets, _ := forecast.EligibleTargets(bd.Result, bnd.graph)
				sel := selection.SelectTierB(targets, tierBBudget)
				fcIn.Store(&forecastInputs{
					targets:    selection.BudgetedTargets(sel),
					unbudgeted: len(sel.Unbudgeted),
					selected:   selected,
					topo:       topo,
					matcher:    bnd.matcher,
					inventory:  active,
					evalWindow: evalWindow,
				})
			}
			if findingsStore != nil {
				if err := findingsStore.UpsertFindings(now, findings); err != nil {
					logger.Error("findings store: upsert failed (surfacing only)", "err", err)
				}
				// Persist the unexplained cards too, so the anomaly timeline (doc
				// 10 M4) survives restarts and shows closed aging spans.
				if err := findingsStore.UpsertUnexplained(now, unexp); err != nil {
					logger.Error("findings store: unexplained upsert failed (surfacing only)", "err", err)
				}
			}
			if capture != nil {
				epoch, err := capture.SetBars(now, bd.Result.Bindings)
				if err != nil {
					// Without a durable bars file this tick's frame would reference an
					// epoch that does not describe the bars actually used — replay
					// would report a false determinism violation. Drop the tick from
					// the capture instead (stated), never record a lie.
					logger.Error("replay capture: bars write failed; this tick will not be captured", "err", err)
					barsOK = false
				}
				barsEpoch = epoch
			}
		}
		// The tick digest: the canonical hash of this tick's complete deterministic
		// output (doc 05 §3.5) — the value replay must reproduce byte-identically.
		digest, _, derr := replay.Digest(now, fps, findings, cascades, unexp)
		if derr != nil {
			// Unreachable while the ingest gate drops non-finite values; if it ever
			// fires, the tick is honestly uncapturable — stated, not invented.
			logger.Error("tick digest unavailable (non-canonical value in output)", "err", derr)
			fmt.Fprintf(out, " tick digest UNAVAILABLE: %v\n\n", derr)
			return
		}
		fmt.Fprintf(out, " tick digest sha256:%s…  (full digest in the replay bundle)\n\n", digest[:16])
		if capture != nil {
			if werr := capture.Warm().Err(); werr != nil {
				// The warm tier latched a write failure: the bundle is incomplete
				// from that point on. Say so on the same surface that advertises
				// the bundle, not only in the logs.
				fmt.Fprintf(out, " CAPTURE FAILED — replay bundle incomplete: %v\n\n", werr)
				return
			}
			if !barsOK || !topoOK {
				return
			}
			if err := capture.Tick(replay.TickRecord{
				EvalNow: now, BarsEpoch: barsEpoch, Topology: topoSnap, Digest: digest,
				Fingerprints: len(fps), Findings: len(findings),
			}); err != nil {
				logger.Error("replay capture: tick write failed", "err", err)
			}
		}
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

// scrapeLoop runs the observation ingest cycle (doc 05 M1): scrape every node's
// cAdvisor endpoint each scrape interval, log the honest summary (resolved /
// dropped / quarantined / node errors). The first cycle fires as soon as the
// informers sync so identity joins are warm. Fetching happens outside the store
// gate (network); ingest holds the write side so evaluation ticks never observe
// a half-ingested cycle (the replay guarantee, doc 05 §3.5).
func scrapeLoop(ctx context.Context, logger *slog.Logger, gate *sync.RWMutex, in *observe.Ingestor, f observe.Fetcher, watcher *identity.Watcher, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	cycle := func() {
		nodes := watcher.ListNodes()
		names := make([]string, 0, len(nodes))
		for _, n := range nodes {
			names = append(names, n.Name)
		}
		payloads := observe.FetchCAdvisor(ctx, f, names)
		// node-exporter lane (doc 14 §3.2 row 3): scraped only on nodes where the
		// exporter is detected RUNNING — presence is read from the cluster, never
		// configured, so an undeployed exporter produces no fetch noise.
		neNodes := watcher.NodesRunning("node-exporter")
		if len(neNodes) > 0 {
			payloads = append(payloads, observe.FetchNodeExporter(ctx, f, neNodes)...)
		}
		gate.Lock()
		sum := in.IngestPayloads(payloads)
		gate.Unlock()
		logger.Info("observation ingest", "families", fmt.Sprintf("cadvisor:%d node-exporter:%d", len(names), len(neNodes)),
			"summary", sum.String(), "streams", in.Hot().Streams())
	}
	if waitForSync(ctx, watcher, every) {
		cycle()
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycle()
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
