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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/departure"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/eventdetect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/mcp"
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
		mcpEnabled   = fs.Bool("mcp-enabled", false, "v3 T-A: serve the read-only MCP harness at /mcp (coverage, silence-ledger, warnings, emit_advisory). OFF by default; off = byte-identical to no MCP. Auth is a separate (later) track — do not expose this beyond an isolated cluster.")
		incidentMem  = fs.Bool("incident-memory", false, "v3 T-B: fold findings into the durable cross-run incident memory (recurrence counting). OFF by default; off = byte-identical. Needs --db (a persistent store) to survive restarts.")
		eventsOn     = fs.Bool("events-enabled", false, "v3 T-C: ingest discrete k8s Events (OOMKilled, CrashLoopBackOff) as MEASURED findings joined by CEI to gauge phenomena. OFF by default; off = byte-identical (events ride off the digest).")
		eventsConds  = fs.String("events-conditions", "ontology/graph/overlays/experimental/event-conditions-v1.yaml", "v3 T-C: the authored event-corroboration conditions overlay (experimental until the events-gate promotes it)")
		appMetrics   = fs.Bool("app-metrics-enabled", false, "doc 15 cap. A: scrape application /metrics endpoints (prometheus.io/scrape pods) + bind to declared SLOs (vigil.io/slo.* annotations). OFF by default; off = byte-identical (the app overlay + app scrape are not loaded).")
		appConds     = fs.String("app-conditions", "ontology/graph/overlays/experimental/app-conditions-v1.yaml", "doc 15 cap. A: the authored application-signal overlay (experimental until the app-slo-gate promotes it)")
		eventsInt    = fs.Duration("events-interval", 15*time.Second, "v3 T-C: discrete-event collector cadence")
		refereeOn    = fs.Bool("referee-enabled", false, "v3 T-D: expose the validate_claim referee (MCP tool + /api/validate-claim) — checks an external claim against the charter + authored graph; ADVISORY, never blocks. OFF by default; off = byte-identical.")
		departureOn  = fs.Bool("departure-enabled", false, "doc 15 cap. C: the band-departure anomaly lane — a MEASURED sample leaving its own PROJECTED forecast band (classed PROJECTED, OFF the digest, never feeds governance). OFF by default; off = byte-identical. Gate-pending: surfaced only after a live step capture.")
		ksmEnabled   = fs.Bool("ksm-enabled", false, "G2 telemetry lane: scrape kube-state-metrics /metrics and ingest its kube_* object-state gauges as CEI streams in the SAME gated scrape cycle as cAdvisor (IN-digest, whole-cycle = the replay guarantee). OFF by default; off = byte-identical (no KSM scrape, the KSM conditions overlay is not loaded). Detection wires via the authored ksm-conditions overlay.")
		ksmConds     = fs.String("ksm-conditions", "ontology/graph/overlays/experimental/ksm-conditions-v1.yaml", "G2: the authored KSM detection-conditions overlay (experimental until the ksm-gate promotes it via governance to detect-conditions-v4)")
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
		// doc 15 cap. A: with --app-metrics-enabled, merge the experimental app-signal
		// overlay ON TOP of the released overlays so the app phenomena exist in the
		// matcher's graph. Off (default), the released glob loads unchanged — the released
		// hash, and the binding/detection digest, are identical to before.
		var g *graph.Graph
		var err error
		// Experimental lane overlays are merged ON TOP of the released glob ONLY when
		// their flag is on, so each lane's checks/members exist in the matcher graph
		// without changing the released hash when off (the app-signal + KSM precedent).
		var extra []string
		if *appMetrics {
			extra = append(extra, *appConds)
		}
		if *ksmEnabled {
			extra = append(extra, *ksmConds)
		}
		if len(extra) > 0 {
			g, err = graph.LoadWithExtraOverlays(*ontology, *overlays, extra...)
		} else {
			g, err = graph.LoadWithOverlays(*ontology, *overlays)
		}
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
	return runIdentity(ctx, logger, p, *kubeconfig, *healthAddr, stdout, ontologyGraph, *storeDir, *dbPath, *apiEnabled, *dumpBindings, *flowEnabled, *flowInterval, *mcpEnabled, *incidentMem, *eventsOn, *eventsConds, *eventsInt, *refereeOn, *appMetrics, *departureOn, *ksmEnabled)
}

// mcpAdvisoryGatePassed gates whether a register-clean ADVISORY (the MCP 4th class)
// is surfaced to the caller. FALSE until the advisory backtest gate passes (doc 11
// §3.5) — the class still REFUSES banned drafts while withheld. Flip only with the
// gate's passing evidence cited, mirroring phaseECrossServiceGatePassed.
const mcpAdvisoryGatePassed = false

// phaseCDepartureGatePassed gates whether the band-departure anomaly lane (doc 15 cap. C)
// is surfaced to the operator. The DETERMINISTIC producer gate (`just departure-gate`)
// PASSES — the producer is certified (a measured sample leaving its projected band fires;
// a noisy-but-stationary series never false-fires). FALSE until a real step is captured live
// (doc 11 §3.5): the lane still COMPUTES off the digest, withheld from the surface. Mirrors
// the Phase-E / cap-D gate-pending posture.
const phaseCDepartureGatePassed = false

// sha256PreviewLen truncates "sha256:<64 hex>" for log lines; the full pin stays on
// the Result and the coverage report.
const sha256PreviewLen = len("sha256:") + 12

// refereePhenomena extracts the validate_claim referee's static ground from the loaded
// graph (v3 T-D): the phenomenon vocabulary (id -> ALIAS tails for endpoint mapping) and
// the AUTHORED phenomenon_relation edges (the only legitimate causal basis). Each
// phenomenon gets BOTH the natural id-derived tail ("oom kill cgroup") AND the cleaned
// graph label ("oom kill", from "OOM kill (cgroup-level)") so the referee recognises
// either phrasing in live prose. Pure; nil graph yields empty context (the referee then
// runs only its substring backstop).
func refereePhenomena(g *graph.Graph) (map[string][]string, []vapi.AuthoredLink) {
	if g == nil {
		return nil, nil
	}
	phen := make(map[string][]string, len(g.Phenomena))
	for id, p := range g.Phenomena {
		set := map[string]bool{}
		if t := strings.TrimSpace(strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(id, "PHEN_"), "_", " "))); t != "" {
			set[t] = true
		}
		if p != nil {
			if t := cleanPhenLabel(p.Label); t != "" {
				set[t] = true
			}
		}
		aliases := make([]string, 0, len(set))
		for a := range set {
			aliases = append(aliases, a)
		}
		sort.Strings(aliases)
		phen[id] = aliases
	}
	var links []vapi.AuthoredLink
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.Type == "phenomenon_relation" {
			links = append(links, vapi.AuthoredLink{Src: e.Src, Dst: e.Dst, Why: e.Why})
		}
	}
	return phen, links
}

// cleanPhenLabel normalises a graph label into a prose-matchable tail: lowercased,
// parentheticals dropped ("OOM kill (cgroup-level)" -> "oom kill"), separators spaced.
func cleanPhenLabel(label string) string {
	s := strings.ToLower(strings.TrimSpace(label))
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[:i]
	}
	for _, sep := range []string{"→", "->", "/"} {
		s = strings.ReplaceAll(s, sep, " ")
	}
	return strings.Join(strings.Fields(s), " ")
}

// runIdentity wires and runs the identity & correlation layer against the cluster,
// serving health/metrics and printing a live entity inventory + join-audit verdict.
func runIdentity(ctx context.Context, logger *slog.Logger, p params.Params, kubeconfig, healthAddr string, out io.Writer, ontologyGraph *graph.Graph, storeDir, dbPath string, apiEnabled bool, dumpBindings string, flowEnabled bool, flowInterval time.Duration, mcpEnabled, incidentMemory bool, eventsEnabled bool, eventsCondsPath string, eventsInterval time.Duration, refereeEnabled, appMetricsEnabled, departureEnabled, ksmEnabled bool) error {
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
	// The deterministic absence ledger (v3 T-A): built from the SAME binding.Result
	// as coverage, each tick, off the deterministic path. The MCP harness's lead
	// feature — a provable account of what is NOT watched and why.
	var silenceView atomic.Pointer[vapi.SilenceLedgerView]
	silenceView.Store(vapi.BuildSilenceLedger(graphVersion, graphRelease, time.Now(), nil))
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
	var transitiveChainView atomic.Pointer[[]flow.Chain]     // doc 15 cap. B: the transitive root-cause chains
	var projectedTransitiveView atomic.Pointer[[]flow.Chain] // doc 15 cap. D: the multi-hop projected cascades
	var departureView atomic.Pointer[[]departure.Departure]  // doc 15 cap. C: band-departure anomalies (gate-pending; producer hook is a follow-up)
	// v2 ANTICIPATORY cross-service cascade (doc 15 phase E): seeded from the gated
	// early-warning lane, propagating a PROJECTED downstream-impact hypothesis along
	// the MEASURED flow edges. Computed-but-DARK until its own backtest gate passes
	// (doc 11 §3.5): a new PROJECTED class is not operator-visible before its gate.
	var projectedCrossSvcView atomic.Pointer[flow.Chain]
	// v3 T-C discrete-event lane: the collector publishes a role-resolved EventFinding
	// snapshot (eventsSnap); the eval tick JOINS it to the gauge findings into
	// eventsView. Both ride OFF the deterministic digest. Unavailable until
	// --events-enabled; loaded conditions are validated against the graph below.
	var eventsSnap atomic.Pointer[eventsSnapshot]
	var eventsView atomic.Pointer[vapi.EventsView]
	eventsView.Store(vapi.BuildEvents(graphVersion, time.Now(), nil))
	var eventsConds []events.Corroboration
	var eventsDets []events.Detection // graph-robustness #2 G1: event-driven detection conditions (validated referential)
	var fcIn *atomic.Pointer[forecastInputs]
	if p.Forecast.Enabled {
		fcIn = new(atomic.Pointer[forecastInputs])
	}

	// The store gate: scrape cycles write under the write lock; evaluation ticks
	// read under the read lock. Ticks therefore always observe whole scrape
	// cycles — the visibility property the replay digest depends on (doc 05 §3.5).
	// Fetching (network) happens OUTSIDE the lock; only parse+ingest holds it.
	var gate sync.RWMutex

	proxyFetcher := kube.NewProxyFetcher(client)
	// doc 15 cap. A: with --app-metrics-enabled, the scrape cycle also fetches app
	// /metrics from pods that opt in via prometheus.io/scrape, in the SAME gated cycle.
	var appTargets func(context.Context) []observe.PodTarget
	if appMetricsEnabled {
		appTargets = func(c context.Context) []observe.PodTarget { return discoverAppTargets(c, client, logger) }
		logger.Info("app-metrics lane enabled (doc 15 cap. A)",
			"discovery", "prometheus.io/scrape pods", "slo_source", "vigil.io/slo.* annotations")
	}
	// G2 KSM lane: with --ksm-enabled, the scrape cycle ALSO fetches kube-state-metrics'
	// /metrics (the cluster object-state gauges) in the SAME gated cycle (whole-cycle
	// ingest = the replay guarantee). KSM is discovered by its own workload identity —
	// the SAME needle binding's tool-detection uses (obtain.go) — so the lane is
	// cluster-agnostic: any KSM deployment is found, no service name is hard-coded.
	var ksmTargets func(context.Context) []observe.PodTarget
	if ksmEnabled {
		ksmTargets = func(c context.Context) []observe.PodTarget { return discoverKSMTargets(c, client, logger) }
		logger.Info("ksm lane enabled (G2 telemetry expansion)",
			"discovery", "kube-state-metrics workload", "ingest", "kube_* object-state gauges -> CEI streams (in-digest)")
	}
	go scrapeLoop(ctx, logger, &gate, ingestor, proxyFetcher, watcher, p.Scrape.Interval.Duration(),
		appMetricsEnabled, proxyFetcher, appTargets, ksmEnabled, ksmTargets)

	// v2 flow lane (doc 15): observe conntrack from the per-node agent and assert
	// observed-flow edges into the same EdgeStore the tick snapshots. Off by default;
	// non-gating; the matcher does not yet walk flow edges, so the digest is unchanged.
	// The eval tick additionally composes a WARM-PATH cross-service cascade (phase D)
	// from this tick's findings + the flow edges + the authored relation — off the
	// digest, gated (logged, not yet a deterministic finding).
	var flowRel flow.Relation
	if flowEnabled {
		// doc 15 Phase C: the cross-service relation is now CURATED into the released
		// ontology graph (a phenomenon_relation edge), read here from the loaded graph
		// — not the experimental overlay file. Surfaced verbatim with graph provenance.
		if r, ok := flow.RelationFromGraph(ontologyGraph); ok {
			flowRel = r
			logger.Info("flow: cross-service relation loaded from curated graph (doc 15 phase C)",
				"trigger", r.Trigger, "downstream", r.Downstream, "graph", r.Version)
		} else {
			logger.Warn("flow: curated cross-service relation absent in graph; cross-service cascade off")
		}
		go runFlowCollector(ctx, logger, &gate, client, store, edges, clusterID, flowInterval)
	}

	// v3 T-C discrete-event lane (doc 03 join by CEI): load the AUTHORED corroboration
	// conditions and, when --events-enabled, start the collector. Honest degradation:
	// a condition that corroborates a phenomenon absent from the released graph has no
	// authored basis — the lane stays off rather than surface it (mirrors
	// flow.RelationFromGraph's ok=false). Events ride off the digest; non-gating.
	if eventsEnabled {
		cs, err := events.LoadEventConditions(eventsCondsPath)
		if err != nil {
			logger.Warn("events lane: conditions not loadable; discrete-event lane off", "path", eventsCondsPath, "err", err)
		} else {
			valid := true
			for _, target := range events.CorroborationTargets(cs) {
				if ontologyGraph == nil || ontologyGraph.Phenomena[target] == nil {
					logger.Warn("events lane: condition corroborates a phenomenon absent from the graph; lane off", "phenomenon", target)
					valid = false
				}
			}
			if valid {
				eventsConds = cs
				// Event-driven phenomenon detection (graph-robustness #2 G1): a discrete
				// event that IS a required member of a phenomenon produces a degraded
				// MEASURED finding. Validate referentially — the member_signal must be a
				// role=required member of the named phenomenon in the released graph —
				// else drop that detection (honest degradation, never a fabricated member).
				if dets, derr := events.LoadEventDetections(eventsCondsPath); derr != nil {
					logger.Warn("events lane: detections not loadable; event-driven detection off", "err", derr)
				} else {
					var ok []events.Detection
					for _, d := range dets {
						p := ontologyGraph.Phenomena[d.Phenomenon]
						if p == nil {
							logger.Warn("events detection: phenomenon absent from graph; dropped", "phenomenon", d.Phenomenon)
							continue
						}
						isReq := false
						for _, mem := range p.Members {
							if mem.SignalID == d.MemberSignal && mem.Role == "required" {
								isReq = true
								break
							}
						}
						if !isReq {
							logger.Warn("events detection: member_signal is not a required member; dropped (no authored basis)",
								"phenomenon", d.Phenomenon, "member", d.MemberSignal)
							continue
						}
						ok = append(ok, d)
					}
					eventsDets = ok
				}
				go runEventCollector(ctx, logger, &gate, client, store, clusterID, eventsConds, &eventsSnap, eventsInterval)
				logger.Info("events lane enabled (v3 T-C)", "route", "/api/events",
					"reasons", events.Reasons(cs), "conditions", eventsCondsPath,
					"event_detections", len(eventsDets))
			}
		}
	}

	// The operator context-window store (10 M6) is shared: the API serves/accepts
	// windows, and the forecast loop reads their boundaries as decomposition splice
	// points (09 M5 §3.4). Created once so both see the same windows.
	cwStore := vapi.NewContextWindowStore()

	// v3 T-D validate-claim referee: a pure check of an EXTERNAL claim against the
	// charter + the AUTHORED graph relations + the current PROJECTED/MEASURED state.
	// Advisory — never blocks, never gates detection. Default off ⇒ not constructed.
	var refereeFn func(string) vapi.ClaimVerdict
	if refereeEnabled {
		phen, links := refereePhenomena(ontologyGraph)
		refereeFn = func(claim string) vapi.ClaimVerdict {
			cc := vapi.ClaimContext{Phenomena: phen, AuthoredLinks: links}
			// PROJECTED subjects: the current early-warning entities (the names a
			// crossing claim would reference); MEASURED subjects: the current findings.
			if wv := warningsView.Load(); wv != nil {
				for i := range wv.Warnings {
					if n := wv.Warnings[i].Name; n != "" {
						cc.Projected = append(cc.Projected, n)
					}
				}
			}
			if findingsStore != nil {
				if rows, err := findingsStore.ActiveFindings(200); err == nil {
					for i := range rows {
						if rows[i].Name != "" {
							cc.Measured = append(cc.Measured, rows[i].Name)
						}
					}
				}
			}
			return vapi.ValidateClaim(claim, cc, vapi.ClaimOpts{})
		}
		logger.Info("validate-claim referee enabled (v3 T-D)",
			"routes", "/api/validate-claim + MCP validate_claim", "authored_relations", len(links))
	}

	var providers *vapi.Providers
	if apiEnabled {
		providers = &vapi.Providers{
			Coverage:      func() *vapi.CoverageView { return coverage.Load() },
			SilenceLedger: func() *vapi.SilenceLedgerView { return silenceView.Load() },
			Unexplained:   func() *vapi.UnexplainedView { return unexpView.Load() },
			Insights:      func() *vapi.InsightsView { return insightsView.Load() },
			Topology:      func() *vapi.TopologyView { return topoView.Load() },
			Warnings:      func() *vapi.WarningsView { return warningsView.Load() },
			// v2 cross-service cascade surface (doc 15 phase F): the warm-path chain,
			// or the honest OFF/quiet state. flowEnabled drives the OFF-vs-quiet split.
			CrossService: func() *vapi.CrossServiceView {
				return vapi.BuildCrossService(crossSvcView.Load(), projectedCrossSvcView.Load(), flowEnabled, phaseECrossServiceGatePassed, time.Now().UTC())
			},
			// doc 15 cap. B: the transitive root-cause chain surface — the warm-path
			// chains, or the honest OFF/quiet state (flowEnabled drives OFF-vs-quiet).
			RootCauseChain: func() *vapi.RootCauseChainView {
				var chains, projected []flow.Chain
				if p := transitiveChainView.Load(); p != nil {
					chains = *p
				}
				if p := projectedTransitiveView.Load(); p != nil {
					projected = *p
				}
				return vapi.BuildRootCauseChain(chains, projected, flowEnabled, phaseDProjectedTransitiveGatePassed, time.Now().UTC())
			},
			// doc 15 cap. C: the band-departure anomaly surface — gate-pending (the producer
			// + anti-FP gate pass; the live stateful forecast-band hook + step capture flip it).
			Departures: func() *vapi.DepartureView {
				var deps []departure.Departure
				if p := departureView.Load(); p != nil {
					deps = *p
				}
				return vapi.BuildDepartures(deps, departureEnabled, phaseCDepartureGatePassed, time.Now().UTC())
			},
			// v3 T-C discrete-event lane: the joined EventsView, or the honest
			// unavailable state when --events-enabled is off.
			Events: func() *vapi.EventsView {
				if !eventsEnabled {
					return nil
				}
				return eventsView.Load()
			},
			// v3 T-D validate-claim referee (nil ⇒ /api/validate-claim reports off).
			Referee: refereeFn,
			// v3.1 the curated causal map — class AUTHORED. Computed on demand from the
			// loaded graph (small + static); the SAME phenomenon_relation edges + aliases
			// the referee checks against, so the catalog and the referee never disagree.
			AuthoredRelations: func() *vapi.AuthoredRelationsView {
				ph, lk := refereePhenomena(ontologyGraph)
				return vapi.BuildAuthoredRelations(ph, lk, time.Now().UTC())
			},
		}
		if findingsStore != nil {
			providers.Findings = findingsStore.ActiveFindings
			// The durable store can hold a finding whose last match was many ticks
			// ago; serve anything older than ~3 eval ticks as stale, not firing-now.
			providers.FindingsStaleAfter = 3 * p.Observation.EvaluationTick.Duration()
			// The durable cross-run incident memory (v3 T-B): read on request, off
			// the hot path. Empty (not unavailable) when --incident-memory is off.
			providers.Incidents = func() (*vapi.IncidentsView, error) {
				rows, err := findingsStore.ActiveIncidents(200)
				if err != nil {
					return nil, err
				}
				return vapi.BuildIncidents(graphVersion, time.Now().UTC(), rows), nil
			}
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
	var mcpHandler http.Handler
	if mcpEnabled && providers != nil {
		// v3 T-A: the read-only MCP harness reuses the SAME provider snapshot funcs —
		// no new computation, no writer in scope (non-gating + no-write-back by
		// construction). Auth is a later track; keep this to an isolated cluster.
		mcpHandler = mcp.New(mcp.Sources{
			Coverage:      providers.Coverage,
			SilenceLedger: providers.SilenceLedger,
			Warnings:      providers.Warnings,
			Incidents: func() *vapi.IncidentsView {
				if providers.Incidents == nil {
					return nil
				}
				v, _ := providers.Incidents()
				return v
			},
			Events: providers.Events,
			// v3.1 synthesis relay: the full classed picture so an MCP-connected AI can
			// synthesize a cause + remediation from grounded facts. Same snapshot funcs as
			// the web /api — no new computation, no writer in scope (no-write-back holds).
			Insights:          providers.Insights,
			RootCauseChain:    providers.RootCauseChain,
			CrossService:      providers.CrossService,
			Topology:          providers.Topology,
			Unexplained:       providers.Unexplained,
			Departures:        providers.Departures,
			AuthoredRelations: providers.AuthoredRelations,
			Referee:           providers.Referee,
		}, mcpAdvisoryGatePassed, "vigil-obsd", graphRelease).HTTPHandler()
		logger.Info("MCP harness enabled (v3 T-A + T-C/T-D + v3.1 synthesis relay)", "route", "/mcp",
			"tools", "get_coverage get_silence_ledger get_warnings get_incidents get_events get_root_cause_chain get_insights get_cross_service get_topology get_unexplained get_departures get_authored_relations validate_claim emit_advisory", "advisoryGate", mcpAdvisoryGatePassed)
	}
	if incidentMemory {
		if findingsStore == nil {
			logger.Warn("incident memory enabled but no --db: incidents fold in memory and reset on restart (pass --db for the durable cross-run memory)")
		} else {
			logger.Info("incident memory enabled (v3 T-B)", "resolve_gap", p.Incident.ResolveGap.String(), "window_bucket", p.Incident.WindowBucket.String())
		}
	}
	go serveHealth(ctx, logger, ln, registry, watcher, providers, mcpHandler)
	go inventoryLoop(ctx, out, logger, &gate, store, edges, watcher, clusterID, graphVersion, graphRelease, p.Observation.EvaluationTick.Duration(),
		&binder{graph: ontologyGraph, client: client, logger: logger, ingestor: ingestor, fpParams: fpParams, dumpPath: dumpBindings},
		capture, &coverage, &silenceView, &unexpView, &insightsView, &topoView, findingsStore, budgets,
		fcIn, p.Selection.TierBBudgetPerCycle, &warningsView, flowEnabled, flowRel, &crossSvcView, &projectedCrossSvcView, &transitiveChainView, &projectedTransitiveView,
		incidentMemory, p.Incident.ResolveGap.Duration(), p.Incident.WindowBucket.Duration(),
		eventsEnabled, eventsConds, eventsDets, &eventsSnap, &eventsView)
	if p.Forecast.Enabled {
		go forecastLoop(ctx, logger, &gate, fcIn, ingestor, graphVersion, graphRelease, p, &warningsView, cwStore)
	}

	logger.Info("running identity & correlation layer (doc 03) — Ctrl-C to stop", "health_addr", ln.Addr().String())
	return watcher.Run(ctx)
}

// serveHealth exposes /metrics (Prometheus), /healthz (liveness), and /readyz
// (informer sync) — the health-metrics endpoints of doc 03 §6 — on an already-bound
// listener (so bind failures are surfaced by the caller, not swallowed here).
func serveHealth(ctx context.Context, logger *slog.Logger, ln net.Listener, registry *prometheus.Registry, watcher *identity.Watcher, providers *vapi.Providers, mcpHandler http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if providers != nil {
		// The operator surfacing API (doc 10) shares the health listener so the web
		// app's /api proxy target is the one bound port.
		vapi.Register(mux, *providers)
	}
	if mcpHandler != nil {
		// v3 T-A: the read-only MCP harness (JSON-RPC over HTTP), behind --mcp-enabled.
		mux.Handle("/mcp", mcpHandler)
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
func inventoryLoop(ctx context.Context, out io.Writer, logger *slog.Logger, gate *sync.RWMutex, store *identity.Store, edges *identity.EdgeStore, watcher *identity.Watcher, clusterID, graphVersion, graphRelease string, every time.Duration, bnd *binder, capture *replay.Capture, coverage *atomic.Pointer[vapi.CoverageView], silenceView *atomic.Pointer[vapi.SilenceLedgerView], unexpView *atomic.Pointer[vapi.UnexplainedView], insightsView *atomic.Pointer[vapi.InsightsView], topoView *atomic.Pointer[vapi.TopologyView], findingsStore *fstore.Store, budgets map[identity.EdgeType]time.Duration, fcIn *atomic.Pointer[forecastInputs], tierBBudget int, warningsView *atomic.Pointer[vapi.WarningsView], flowEnabled bool, flowRel flow.Relation, crossSvcView *atomic.Pointer[flow.Chain], projectedCrossSvcView *atomic.Pointer[flow.Chain], transitiveChainView *atomic.Pointer[[]flow.Chain], projectedTransitiveView *atomic.Pointer[[]flow.Chain], incidentEnabled bool, incidentResolveGap, incidentBucket time.Duration,
	eventsEnabled bool, eventsConds []events.Corroboration, eventsDets []events.Detection, eventsSnap *atomic.Pointer[eventsSnapshot], eventsView *atomic.Pointer[vapi.EventsView]) {
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
			// Event-driven phenomenon detection (graph-robustness #2 G1): a real
			// discrete event that IS a required member of a phenomenon (OOMKilled →
			// OOM_KILL_CGROUP, CrashLoopBackOff → PROBE_FAILURE_RESTART) produces a
			// DEGRADED MEASURED finding. These JOIN the fingerprint findings ONLY for the
			// live surfaces below + cascade recognition — they ride strictly OFF the
			// deterministic digest (the fp `findings`/`cascades` used by routeUnexplained
			// + capture are NEVER mutated), so replay stays byte-identical and the events
			// lane non-gating. With --events-enabled off this block is skipped entirely.
			surfFindings, surfCascades := findings, cascades
			if eventsEnabled && len(eventsDets) > 0 && bnd.matcher != nil {
				var raw []events.EventFinding
				if snap := eventsSnap.Load(); snap != nil {
					raw = snap.findings
				}
				if ef := eventdetect.Findings(bnd.graph, raw, eventsDets, now); len(ef) > 0 {
					surfFindings = append(append([]detect.Finding{}, findings...), ef...)
					surfCascades = bnd.augmentedCascades(now, surfFindings, topo, evalWindow)
					renderFindings(out, ef, bnd.lastObs)
					renderCascades(out, surfCascades)
				}
			}
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
			if silenceView != nil {
				// Same binding.Result, same tick — the two surfaces can never disagree.
				silenceView.Store(vapi.BuildSilenceLedger(graphVersion, graphRelease, now, bd.Result))
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
				insightsView.Store(vapi.BuildInsights(clusterID, graphVersion, graphRelease, now, surfFindings, surfCascades))
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

				// Transitive root-cause chain (doc 15 cap. B), WARM PATH — the one-hop
				// cascade above made TRANSITIVE. Reuses the SAME degraded set + authored
				// relation; a pure function of (flow topology, degraded set, relation,
				// window), so it never perturbs the tick or the replay digest. Direction
				// is taken ONLY from the authored relation; silent intermediates are stated
				// gaps, never bridged; independent faults never merge into one chain.
				if transitiveChainView != nil {
					chains := flow.TransitiveChains(edges, degraded, flowRel, evalWindow, now, flow.TransitiveMaxHops)
					if len(chains) > 0 {
						transitiveChainView.Store(&chains)
						for i := range chains {
							logger.Info("transitive root-cause chain (doc 15 cap. B, warm path)",
								"root", chains[i].MostUpstreamDegradedNode, "hops", len(chains[i].Path),
								"gaps", len(chains[i].Gaps), "why_class", "AUTHORED", "edge_class", "MEASURED observed flow")
						}
					} else {
						transitiveChainView.Store(nil)
					}
				}
			}
			// v2 ANTICIPATORY cross-service cascade (doc 15 phase E), WARM PATH — seeded
			// from the GATED early-warning lane (warningsView.Warnings, never raw
			// forecasts), so it inherits the forecast lane's own gate. Each upstream
			// callee FORECAST to cross soon propagates a PROJECTED downstream-impact
			// hypothesis to its callers over the MEASURED flow edge + the AUTHORED
			// relation (weakest-input rule). Off the digest; computed every tick;
			// surfaced only once its own backtest gate passes (doc 11 §3.5).
			if flowEnabled && projectedCrossSvcView != nil {
				var projected []flow.ProjectedDegradedWorkload
				if wv := warningsView.Load(); wv != nil && wv.Enabled {
					seen := map[string]bool{}
					for i := range wv.Warnings {
						c := &wv.Warnings[i]
						rec, ok := store.Get(c.EntityCEI)
						if !ok || rec.RoleCEI.RoleKey == "" {
							continue
						}
						k := rec.RoleCEI.Key()
						if seen[k] {
							continue
						}
						seen[k] = true
						projected = append(projected, flow.ProjectedDegradedWorkload{
							CEI: rec.RoleCEI, Label: flow.RoleLabel(rec.RoleCEI), Metric: c.Metric,
							PrecursorPhenomena: c.PrecursorPhenomena, Confidence: c.Confidence,
							CrossAt: c.CrossAt, EarliestAt: c.EarliestAt, LatestAt: c.LatestAt,
							LatestBeyondHorizon: c.LatestBeyondHorizon,
						})
					}
				}
				chain, ok := flow.ProjectedCrossServiceChain(edges, projected, flowRel, evalWindow, now)
				if ok {
					c := chain
					projectedCrossSvcView.Store(&c)
					logger.Info("anticipatory cross-service cascade (v2 phase E, warm path)",
						"root", chain.MostUpstreamDegradedNode, "projected_callers", len(chain.Links),
						"class", "PROJECTED upstream+downstream · MEASURED edge · AUTHORED why",
						"surfaced", phaseECrossServiceGatePassed)
				} else {
					projectedCrossSvcView.Store(nil)
				}

				// Multi-hop projected cascade (doc 15 cap. D), WARM PATH — the one-hop
				// anticipatory cascade above made TRANSITIVE: from each forecast root, the
				// ripple propagates to its TRANSITIVE callers, each carrying the root's band
				// INHERITED and WIDENED per hop (the clock is run once, at the root). Reuses
				// the SAME warned `projected` set; off-digest. COMPUTED every tick, withheld
				// from the operator until a real 2-hop lead+confirm capture (a new PROJECTED
				// class, gate-pending — phaseDProjectedTransitiveGatePassed).
				if projectedTransitiveView != nil {
					pchains := flow.ProjectedTransitiveChains(edges, projected, flowRel, evalWindow, now, flow.TransitiveMaxHops)
					if len(pchains) > 0 {
						projectedTransitiveView.Store(&pchains)
						for i := range pchains {
							logger.Info("multi-hop projected cascade (doc 15 cap. D, warm path)",
								"root", pchains[i].MostUpstreamDegradedNode, "hops", len(pchains[i].Path),
								"class", "PROJECTED impact · MEASURED edge · AUTHORED why · band widens per hop",
								"surfaced", phaseDProjectedTransitiveGatePassed)
						}
					} else {
						projectedTransitiveView.Store(nil)
					}
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
				topoView.Store(vapi.BuildTopology(clusterID, graphVersion, now, active, topoSnap, strBudgets, surfFindings, unexp, selected, warned))
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
				// v3 T-B: fold this tick's findings into the durable incident memory
				// (cross-run phenomenon recurrence). Keyed on the DURABLE role CEI
				// (doc 03) — when the role is unresolvable the incident is recorded
				// under the instance key, stated, never assigned a guessed role.
				// Off the deterministic path; the replay digest never reads it.
				if incidentEnabled {
					for i := range findings {
						roleKey, unresolved := findings[i].EntityCEI, true
						if rec, ok := store.Get(findings[i].EntityCEI); ok && rec.RoleCEI.RoleKey != "" {
							roleKey, unresolved = rec.RoleCEI.Key(), false
						}
						if err := findingsStore.UpsertIncident(now, findings[i].Phenomenon, roleKey,
							unresolved, graphVersion, incidentResolveGap, incidentBucket); err != nil {
							logger.Error("incident memory: upsert failed (surfacing only)", "err", err)
						}
					}
				}
			}
			// v3 T-C discrete-event lane: JOIN this tick's gauge findings to the
			// collected events on the shared role CEI (corroborate, never fuse) and
			// publish the EventsView. Each gauge finding is resolved to its role CEI
			// exactly as the incident upsert does (store.Get); the collector resolved
			// the events the same way. Runs whether or not a db is present — events do
			// not persist. OFF the deterministic digest: the events snapshot never
			// touched fps/findings, so this cannot perturb the tick's output.
			if eventsEnabled && eventsView != nil {
				gaugeRoles := make(map[string]map[string]bool)
				for i := range findings {
					rec, ok := store.Get(findings[i].EntityCEI)
					if !ok || rec.RoleCEI.RoleKey == "" {
						continue
					}
					roleKey := rec.RoleCEI.Key()
					if gaugeRoles[findings[i].Phenomenon] == nil {
						gaugeRoles[findings[i].Phenomenon] = make(map[string]bool)
					}
					gaugeRoles[findings[i].Phenomenon][roleKey] = true
				}
				var raw []events.EventFinding
				if snap := eventsSnap.Load(); snap != nil {
					raw = snap.findings
				}
				eventsView.Store(vapi.BuildEvents(graphVersion, now, events.Corroborate(raw, gaugeRoles, eventsConds)))
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

// discoverAppTargets lists pods that opt into app-metrics scraping via the standard
// prometheus.io/scrape annotation (doc 15 cap. A) and returns their scrape targets:
// port from prometheus.io/port (default 8080), path from prometheus.io/path (default
// metrics). Discovery is config-declared on the customer's OWN pods, never inferred; a
// failed list degrades the lane this cycle (logged), never fatal.
func discoverAppTargets(ctx context.Context, cs kubernetes.Interface, logger *slog.Logger) []observe.PodTarget {
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Warn("app-metrics: pod discovery failed this cycle (lane degraded, never fatal)", "err", err)
		return nil
	}
	var out []observe.PodTarget
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Annotations["prometheus.io/scrape"] != "true" || p.Status.Phase != corev1.PodRunning {
			continue
		}
		port := p.Annotations["prometheus.io/port"]
		if port == "" {
			port = "8080"
		}
		path := strings.TrimPrefix(p.Annotations["prometheus.io/path"], "/")
		if path == "" {
			path = "metrics"
		}
		out = append(out, observe.PodTarget{Namespace: p.Namespace, Name: p.Name, Port: port, Path: path})
	}
	return out
}

// ksmImageNeedle is the substring that identifies a kube-state-metrics workload —
// the SAME needle binding's tool-detection uses (obtain.go toolNeedles). Selecting
// by the workload's own identity (not a hard-coded namespace/name) keeps the lane
// cluster-agnostic: any KSM deployment, in any namespace, is found.
const ksmImageNeedle = "kube-state-metrics"

// discoverKSMTargets finds running kube-state-metrics pods (by container image, the
// cluster-agnostic identity) and returns their /metrics scrape targets. Port/path
// honour the standard prometheus.io annotations when present, else KSM's defaults
// (:8080/metrics). A failed list degrades the lane this cycle (logged), never fatal.
func discoverKSMTargets(ctx context.Context, cs kubernetes.Interface, logger *slog.Logger) []observe.PodTarget {
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Warn("ksm: pod discovery failed this cycle (lane degraded, never fatal)", "err", err)
		return nil
	}
	var out []observe.PodTarget
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		isKSM := false
		for _, c := range p.Spec.Containers {
			if strings.Contains(c.Image, ksmImageNeedle) {
				isKSM = true
				break
			}
		}
		if !isKSM {
			continue
		}
		port := p.Annotations["prometheus.io/port"]
		if port == "" {
			port = "8080"
		}
		path := strings.TrimPrefix(p.Annotations["prometheus.io/path"], "/")
		if path == "" {
			path = "metrics"
		}
		out = append(out, observe.PodTarget{Namespace: p.Namespace, Name: p.Name, Port: port, Path: path})
	}
	return out
}

// scrapeLoop runs the observation ingest cycle (doc 05 M1): scrape every node's
// cAdvisor endpoint each scrape interval, log the honest summary (resolved /
// dropped / quarantined / node errors). The first cycle fires as soon as the
// informers sync so identity joins are warm. Fetching happens outside the store
// gate (network); ingest holds the write side so evaluation ticks never observe
// a half-ingested cycle (the replay guarantee, doc 05 §3.5).
func scrapeLoop(ctx context.Context, logger *slog.Logger, gate *sync.RWMutex, in *observe.Ingestor, f observe.Fetcher, watcher *identity.Watcher, every time.Duration, appEnabled bool, podFetcher observe.PodFetcher, appTargets func(context.Context) []observe.PodTarget, ksmEnabled bool, ksmTargets func(context.Context) []observe.PodTarget) {
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
		// app-metrics lane (doc 15 cap. A): app /metrics from opt-in pods, in the SAME
		// gated cycle (so eval ticks see whole cycles — the replay guarantee). Only when
		// the flag is on; off ⇒ this block never runs and obsd is byte-identical.
		appCount := 0
		if appEnabled && appTargets != nil {
			tg := appTargets(ctx)
			appCount = len(tg)
			if len(tg) > 0 {
				payloads = append(payloads, observe.FetchPodMetrics(ctx, podFetcher, tg)...)
			}
		}
		// G2 KSM lane (kube-state-metrics): the cluster object-state gauges, in the
		// SAME gated cycle so eval ticks see whole cycles. KSM identity rides on the
		// series labels (normalize.ksm), not the scrape target — FetchKSM stamps
		// FamilyKSM. Only when the flag is on; off ⇒ this block never runs.
		ksmCount := 0
		if ksmEnabled && ksmTargets != nil {
			tg := ksmTargets(ctx)
			ksmCount = len(tg)
			if len(tg) > 0 {
				payloads = append(payloads, observe.FetchKSM(ctx, podFetcher, tg)...)
			}
		}
		gate.Lock()
		sum := in.IngestPayloads(payloads)
		gate.Unlock()
		logger.Info("observation ingest", "families", fmt.Sprintf("cadvisor:%d node-exporter:%d app:%d ksm:%d", len(names), len(neNodes), appCount, ksmCount),
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
