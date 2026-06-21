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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
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
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/assoc"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/audit"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/departure"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/eventdetect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/governance"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/logtmpl"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/mcp"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/onset"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
	fstore "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/trace"
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
		paramsPath         = fs.String("params", "", "path to a parameters override file (overlays embedded dev defaults)")
		showVersion        = fs.Bool("version", false, "print version and exit")
		logFormat          = fs.String("log", "json", "log format: json|text")
		kubeconfig         = fs.String("kubeconfig", "", "path to a kubeconfig; when set (or --in-cluster), run the identity layer against the cluster")
		inCluster          = fs.Bool("in-cluster", false, "use in-cluster config to reach the API server")
		healthAddr         = fs.String("health-addr", ":9095", "address for the health/metrics server (/metrics, /healthz, /readyz)")
		ontology           = fs.String("ontology", "ontology/graph/k8s_signal_kg.json", "ontology KG release; with a cluster target, enables the binding compiler (doc 04)")
		overlays           = fs.String("overlays", "ontology/graph/overlays", "authored overlay dir (spans, threshold rules) merged into the ontology")
		releases           = fs.String("releases", "ontology/releases", "graph release manifests (doc 12 M1); the loaded graph self-identifies its release by hash")
		storeDir           = fs.String("store-dir", "", "directory for the qss warm tier + replay bundle (doc 14 §2.3); empty = hot rings only (replay capture off, stated)")
		dbPath             = fs.String("db", "", "SQLite findings database (doc 14 A7); empty = in-memory (findings reset on restart)")
		apiEnabled         = fs.Bool("api", true, "serve the operator surfacing API (doc 10) under /api on the health server")
		dumpBindings       = fs.String("dump-bindings", "", "write the compiled binding.Result to this JSON path once (governance migration exercise, doc 12 M4)")
		flowEnabled        = fs.Bool("flow-enabled", false, "v2 (doc 15): collect conntrack via the per-node conntrack-agent and assert observed-flow edges (OFF by default; off = byte-identical to no flow)")
		flowInterval       = fs.Duration("flow-interval", 15*time.Second, "v2: flow collector cadence")
		mcpEnabled         = fs.Bool("mcp-enabled", false, "v3 T-A: serve the read-only MCP harness at /mcp (coverage, silence-ledger, warnings, emit_advisory). OFF by default; off = byte-identical to no MCP. Auth is a separate (later) track — do not expose this beyond an isolated cluster.")
		incidentMem        = fs.Bool("incident-memory", false, "v3 T-B: fold findings into the durable cross-run incident memory (recurrence counting). OFF by default; off = byte-identical. Needs --db (a persistent store) to survive restarts.")
		eventsOn           = fs.Bool("events-enabled", false, "v3 T-C: ingest discrete k8s Events (OOMKilled, CrashLoopBackOff) as MEASURED findings joined by CEI to gauge phenomena. OFF by default; off = byte-identical (events ride off the digest).")
		eventsConds        = fs.String("events-conditions", "ontology/graph/overlays/experimental/event-conditions-v1.yaml", "v3 T-C: the authored event-corroboration conditions overlay (experimental until the events-gate promotes it)")
		appMetrics         = fs.Bool("app-metrics-enabled", false, "doc 15 cap. A: scrape application /metrics endpoints (prometheus.io/scrape pods) + bind to declared SLOs (vigil.io/slo.* annotations). OFF by default; off = byte-identical (the app overlay + app scrape are not loaded).")
		appConds           = fs.String("app-conditions", "ontology/graph/overlays/experimental/app-conditions-v1.yaml", "doc 15 cap. A: the authored application-signal overlay (experimental until the app-slo-gate promotes it)")
		eventsInt          = fs.Duration("events-interval", 15*time.Second, "v3 T-C: discrete-event collector cadence")
		refereeOn          = fs.Bool("referee-enabled", false, "v3 T-D: expose the validate_claim referee (MCP tool + /api/validate-claim) — checks an external claim against the charter + authored graph; ADVISORY, never blocks. OFF by default; off = byte-identical.")
		departureOn        = fs.Bool("departure-enabled", false, "doc 15 cap. C: the band-departure anomaly lane — a MEASURED sample leaving its own PROJECTED forecast band (classed PROJECTED, OFF the digest, never feeds governance). OFF by default; off = byte-identical. Gate-pending: surfaced only after a live step capture.")
		ksmEnabled         = fs.Bool("ksm-enabled", false, "G2 telemetry lane: scrape kube-state-metrics /metrics and ingest its kube_* object-state gauges as CEI streams in the SAME gated scrape cycle as cAdvisor (IN-digest, whole-cycle = the replay guarantee). OFF by default; off = byte-identical (no KSM scrape => no kube_* streams => the released v4 KSM checks stay unobservable, the per-tick digest is unchanged). The detect-conditions-v4 checks are part of the RELEASED graph (governance); this flag gates only the scrape that makes them observable.")
		dgxEnabled         = fs.Bool("dgx-enabled", false, "doc 20 P0: stand up the Dynamic Graph eXtension candidate staging store (candidates.db) + the read-only /api/candidates surface. OFF by default; off = byte-identical (the store is never opened; the deterministic path never reads candidates — enforced by the firewall tests). No agent in P0; this only stands up the firewalled store + surface.")
		histQuantiles      = fs.Bool("histogram-quantiles", false, "doc 20 P0.5: derive p50/p95/p99 GAUGE streams from HISTOGRAM exposition families at ingest (Prometheus bucket interpolation) instead of skipping them — unlocks p95/p99 latency for every exporter. OFF by default; off = byte-identical (histograms stay skipped + counted). MEASURED arithmetic; the derived streams ride the SAME CEI/normalize/replay path as scraped gauges.")
		assocEnabled       = fs.Bool("assoc-enabled", false, "doc 20 P2: compute the MEASURED metric-dependency graph (windowed correlation over hot series, surfaced at /api/dependency as undirected associated-with edges — never causal). OFF by default; off = byte-identical (no association computed). Off-digest; barred from detection + forecasting (enforced by the assoc import-firewall test).")
		onsetEnabled       = fs.Bool("onset-enabled", false, "doc 22 C2: compute MEASURED changepoint ONSETS over the hot gauge series (off-digest EWMA-residual CUSUM with sustained-shift confirmation) and surface the step TIMES at /api/onsets — including sub-threshold shifts the three primitives leave unmarked; the temporal-adjacency substrate for the direction-free hypotheses tab. OFF by default; off = byte-identical (no onset computed). Off-digest; never feeds detection/forecasting/governance.")
		dgxAgentEnabled    = fs.Bool("dgx-agent-enabled", false, "doc 20 P3: enable the DGX agent — an LLM PROPOSES candidate graph extensions from read-only MEASURED context (gated: grounding + evidence floor + the structural causal guard) into the candidate store. Requires --dgx-enabled and a provider key (env GROQ_API_KEY or DGX_API_KEY; DGX_MODEL/DGX_BASE_URL optional for a local OpenAI-compatible model). OFF by default; the agent authors nothing and never touches the deterministic path.")
		logsEnabled        = fs.Bool("logs-enabled", false, "doc 20 P4: mine MEASURED log templates from pod logs (a Go-native deterministic Drain) and surface them at /api/log-templates. Off-digest; regex stays the authored first layer, this is the measured second layer for the unmapped tail. OFF by default; never feeds detection or forecasting.")
		auditEnabled       = fs.Bool("audit-enabled", false, "doc 20 P4 AUDIT lane: read the apiserver audit log (JSONL at --audit-log-path) as MEASURED change records and surface them at /api/audit-changes; for each active incident, stage a direction-free co-occurrence hypothesis (arrow-of-time, never a cause) into the candidate store. OFF by default; off = byte-identical (off-digest, enforced by the audit import-firewall). Needs --audit-log-path; the change→incident hypotheses also need --dgx-enabled (the store) + --incident-memory.")
		auditLogPath       = fs.String("audit-log-path", "", "doc 20 P4 AUDIT lane: path to the apiserver audit log in JSONL (audit.k8s.io/v1 Event per line). The apiserver audit policy is OFF by default on kind; this is config-dependent (mount the audit log to obsd). Empty ⇒ the audit lane stays off even with --audit-enabled.")
		tracesEnabled      = fs.Bool("traces-enabled", false, "doc 20 P4 TRACE lane: read OTel spans (JSONL at --traces-path), build the MEASURED observed service call graph at /api/trace-graph, and stage each discovered call as a STRUCTURAL topology candidate (never causal) into the candidate store. OFF by default; off = byte-identical (off-digest, enforced by the trace import-firewall). Needs --traces-path; the topology candidates also need --dgx-enabled (the store).")
		tracesPath         = fs.String("traces-path", "", "doc 20 P4 TRACE lane: path to a spans JSONL file (one span per line: traceId/spanId/parentSpanId/service/name/startTime/endTime/error). Neither demo cluster runs an OTel/Jaeger/Tempo source; an operator wires this from an OTel file exporter (config-dependent). Empty ⇒ the trace lane stays off even with --traces-enabled.")
		forecastRoleSeries = fs.Bool("forecast-role-series", false, "doc 20 P5: churn-stable identity — forecast the WORKLOAD ROLE (a deterministic per-bin worst-member-toward-bar of its live member pods — max below an `above` bar, so it stays comparable to the per-pod bar; OwnerReference succession) instead of a single pod UID, so the series survives pod churn (HPA/rollout/OOM-restart). Requires forecast.enabled. OFF by default; off = byte-identical (the forecast feeds per-pod targets unchanged). Gate-pending like every forecast class.")
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
		// without changing the released hash when off (the app-signal precedent). The
		// KSM checks are now RELEASED (detect-conditions-v4, in the glob), so --ksm-enabled
		// gates only the SCRAPE — without it the kube_* streams are absent and the v4
		// checks stay unobservable (the per-tick digest is unchanged).
		var extra []string
		if *appMetrics {
			extra = append(extra, *appConds)
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
	return runIdentity(ctx, logger, p, *kubeconfig, *healthAddr, stdout, ontologyGraph, *storeDir, *dbPath, *apiEnabled, *dumpBindings, *flowEnabled, *flowInterval, *mcpEnabled, *incidentMem, *eventsOn, *eventsConds, *eventsInt, *refereeOn, *appMetrics, *departureOn, *ksmEnabled, *dgxEnabled, *histQuantiles, *assocEnabled, *dgxAgentEnabled, *logsEnabled, *auditEnabled, *auditLogPath, *tracesEnabled, *tracesPath, *forecastRoleSeries, *onsetEnabled)
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
func runIdentity(ctx context.Context, logger *slog.Logger, p params.Params, kubeconfig, healthAddr string, out io.Writer, ontologyGraph *graph.Graph, storeDir, dbPath string, apiEnabled bool, dumpBindings string, flowEnabled bool, flowInterval time.Duration, mcpEnabled, incidentMemory bool, eventsEnabled bool, eventsCondsPath string, eventsInterval time.Duration, refereeEnabled, appMetricsEnabled, departureEnabled, ksmEnabled, dgxEnabled, histogramQuantiles, assocEnabled, dgxAgentEnabled, logsEnabled, auditEnabled bool, auditLogPath string, tracesEnabled bool, tracesPath string, forecastRoleSeries, onsetEnabled bool) error {
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
	// doc 20 P0.5: when enabled, HISTOGRAM families ingest as derived p50/p95/p99 GAUGE
	// streams (Prometheus bucket interpolation) instead of being skipped. OFF = the
	// histogram skip is unchanged (byte-identical); the derived streams are MEASURED.
	if histogramQuantiles {
		ingestor.SetHistogramQuantiles(observe.DefaultHistogramQuantiles)
	}

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

	// doc 20 P0.b: the Dynamic Graph eXtension candidate staging store — a SEPARATE
	// candidates.db, OUTSIDE the graph loader and OFF the deterministic path (never the
	// graph-loader's extra-overlay seam, which would un-release the build). Opened only
	// with --dgx-enabled; the firewall tests prove the deterministic path can't read it.
	var candStore *candidate.Store
	if dgxEnabled {
		candPath := ""
		if dbPath != "" {
			// Isolate the candidate store PER db FILE, not per directory: each cluster runs
			// obsd with its own --db, and two clusters whose dbs share a directory must NOT
			// share one candidate store (cross-cluster candidate contamination).
			base := strings.TrimSuffix(filepath.Base(dbPath), filepath.Ext(dbPath))
			candPath = filepath.Join(filepath.Dir(dbPath), base+".candidates.db")
		}
		cs, err := candidate.Open(candPath)
		if err != nil {
			logger.Error("dgx candidate store disabled: open failed (surfacing only, non-gating)", "err", err)
		} else {
			candStore = cs
			defer candStore.Close()
			logger.Info("dgx candidate staging store enabled (doc 20 P0)", "persistent", candPath != "")
		}
	}

	// doc 20 P1: the off-digest stray-metric resolver. Capture quarantined series and,
	// each scrape interval, propose PROVISIONAL candidate nodes + associated-with edges
	// (discrete coordinate intersection, no score) into the candidate store. Reads the
	// identity inventory, writes candidates; never touches the digest or detection.
	if dgxEnabled && candStore != nil {
		ingestor.EnableQuarantineCapture()
		go dgxResolveLoop(ctx, logger, ingestor, store, candStore, graphVersion, p.Scrape.Interval.Duration())
	}

	// doc 20 P2: the off-digest metric-dependency lane — periodically associate the hot
	// series (MEASURED, associated-with, never causal) and publish /api/dependency. OFF
	// by default; never feeds detection/forecasting (the assoc firewall test enforces it).
	var depView atomic.Pointer[vapi.DependencyView]
	if assocEnabled {
		depView.Store(vapi.NewDependencyView(time.Now().UTC(), time.Time{}, time.Time{}, nil, 0, 0))
		go assocLoop(ctx, logger, ingestor, &depView, envDuration("DGX_ASSOC_INTERVAL", time.Minute), assocMaxStreams)
	}

	// doc 22 C2: the off-digest changepoint-onset lane — each interval, compute the MEASURED
	// step TIMES over the hot GAUGE series (CUSUM + sustained-shift confirmation) and publish
	// /api/onsets. OFF by default; never feeds detection/forecasting/governance (off-digest).
	var onsetView atomic.Pointer[vapi.OnsetView]
	if onsetEnabled {
		onsetView.Store(vapi.BuildOnsets(nil, true, 0, time.Now().UTC()))
		// 0 = scan ALL gauge streams: onset is O(n) per stream (linear CUSUM), so unlike the
		// O(n²) assoc lane it can afford the whole cluster — and honest coverage demands it
		// (a capped scan would silently skip most workloads).
		go onsetLoop(ctx, logger, ingestor, &onsetView, envDuration("ONSET_INTERVAL", time.Minute), 0)
	}
	var coverage atomic.Pointer[vapi.CoverageView]
	coverage.Store(vapi.BuildCoverage(clusterID, graphVersion, graphRelease, time.Now(), nil, nil, nil))
	// The deterministic absence ledger (v3 T-A): built from the SAME binding.Result
	// as coverage, each tick, off the deterministic path. The MCP harness's lead
	// feature — a provable account of what is NOT watched and why.
	var silenceView atomic.Pointer[vapi.SilenceLedgerView]
	silenceView.Store(vapi.BuildSilenceLedger(graphVersion, graphRelease, time.Now(), nil))

	// doc 20 P3: the agent harness — an LLM PROPOSES candidates from read-only MEASURED
	// context (associations + coverage gaps), gated (grounding + evidence floor + the
	// structural causal guard) and staged into the candidate store. Off-digest; the model
	// authors nothing. Needs --dgx-enabled (the store) + a provider key.
	var dgxAgent *dgx.Agent // launched below, AFTER the modality views exist (so the agent can reason over logs/traces/events)
	if dgxAgentEnabled && candStore != nil {
		apiKey := os.Getenv("GROQ_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("DGX_API_KEY")
		}
		if apiKey == "" {
			logger.Warn("dgx agent disabled: no GROQ_API_KEY / DGX_API_KEY in env (set a provider key to enable the LLM proposer)")
		} else {
			var provider dgx.Provider
			if base := os.Getenv("DGX_BASE_URL"); base != "" {
				provider = dgx.NewOpenAICompatibleProvider("custom", base, apiKey, os.Getenv("DGX_MODEL"))
			} else {
				provider = dgx.NewGroqProvider(apiKey, os.Getenv("DGX_MODEL"))
			}
			// DGX_MAX_TOKENS bumps the completion bound — a reasoning model (deepseek-v4-flash,
			// deepseek-reasoner) needs headroom for reasoning_tokens + the proposals JSON, or it
			// returns empty content (observed live). Only the ChatProvider honours it.
			if cp, ok := provider.(*dgx.ChatProvider); ok {
				if v := os.Getenv("DGX_MAX_TOKENS"); v != "" {
					if n, err := strconv.Atoi(v); err == nil {
						cp.SetMaxTokens(n)
					}
				}
				// DGX_EXTRA_BODY is a JSON object of provider-specific request fields, e.g.
				// {"thinking":{"type":"disabled"}} to turn OFF deepseek-v4-flash reasoning.
				if v := os.Getenv("DGX_EXTRA_BODY"); v != "" {
					var extra map[string]any
					if err := json.Unmarshal([]byte(v), &extra); err == nil {
						cp.SetExtraBody(extra)
					} else {
						logger.Warn("dgx: DGX_EXTRA_BODY is not valid JSON; ignoring", "err", err.Error())
					}
				}
			}
			// doc 21 §3 slice 5: opt-in DECLARED threshold floors (off by default = no regression).
			// DGX_MIN_SUPPORT raises the agreed-evidence bar a proposal must clear; DGX_MIN_CAPTURE_SAMPLE
			// requires an equiv_group pattern to absorb ≥N seen strays. Counts vs a declared integer,
			// never learned. A bad value is ignored (floor stays off).
			params := dgx.DefaultParams
			if v := os.Getenv("DGX_MIN_SUPPORT"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n >= 0 {
					params.MinSupport = n
				}
			}
			if v := os.Getenv("DGX_MIN_CAPTURE_SAMPLE"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n >= 0 {
					params.MinCaptureSample = n
				}
			}
			dgxAgent = dgx.New(provider, params)
			logger.Info("dgx agent enabled (doc 20 P3)", "provider", provider.Name(),
				"minSupport", params.MinSupport, "minCaptureSample", params.MinCaptureSample)
		}
	}

	// doc 20 P4: the log lane — mine MEASURED templates from pod logs (Go-native Drain,
	// deterministic) and publish /api/log-templates. Off-digest; regex stays the authored
	// first layer, this is the measured second layer for the unmapped tail.
	var logsView atomic.Pointer[vapi.LogTemplatesView]
	if logsEnabled {
		go logsLoop(ctx, logger, client, &logsView, envDuration("DGX_LOGS_INTERVAL", time.Minute), logsTailLines, logsMaxPods)
		logger.Info("log lane enabled (doc 20 P4)")
	}

	// doc 20 P4 AUDIT lane: read the apiserver audit log (JSONL) and publish the MEASURED
	// change feed; for each active incident, stage a direction-free arrow-of-time
	// co-occurrence hypothesis into the candidate store. Off-digest; the lane authors
	// nothing. Needs --audit-log-path; the hypotheses additionally need the candidate
	// store (--dgx-enabled) + incident memory (--incident-memory).
	var auditView atomic.Pointer[vapi.AuditView]
	if auditEnabled {
		if auditLogPath == "" {
			logger.Warn("audit lane disabled: --audit-enabled set but --audit-log-path is empty (mount the apiserver audit log to obsd)")
		} else {
			auditView.Store(vapi.WarmingAudit(time.Now().UTC())) // honest "enabled, awaiting first cycle" until the first tick
			go auditLoop(ctx, logger, store, candStore, findingsStore, &auditView,
				auditLogPath, clusterID, graphVersion, envDuration("DGX_AUDIT_INTERVAL", time.Minute), auditLookback, auditMaxLines)
			logger.Info("audit lane enabled (doc 20 P4 AUDIT)", "path", auditLogPath)
		}
	}

	// doc 20 P4 TRACE lane: read OTel spans (JSONL) and publish the MEASURED observed
	// service call graph; stage each discovered call as a STRUCTURAL topology candidate
	// (never causal). Off-digest; the lane authors nothing. Needs --traces-path; the
	// topology candidates additionally need the candidate store (--dgx-enabled).
	var traceView atomic.Pointer[vapi.TraceGraphView]
	if tracesEnabled {
		if tracesPath == "" {
			logger.Warn("trace lane disabled: --traces-enabled set but --traces-path is empty (wire a spans JSONL from an OTel file exporter)")
		} else {
			traceView.Store(vapi.WarmingTraceGraph(time.Now().UTC())) // honest "enabled, awaiting first cycle" until the first tick
			go tracesLoop(ctx, logger, store, candStore, &traceView,
				tracesPath, clusterID, graphVersion, envDuration("DGX_TRACES_INTERVAL", time.Minute), traceMaxLines)
			logger.Info("trace lane enabled (doc 20 P4 TRACE)", "path", tracesPath)
		}
	}
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

	// doc 20 P3: launch the agent here (not at its setup above) so its read-only context
	// can also reason over the MODALITY lanes — mined log templates, observed trace edges,
	// and discrete events — alongside associations + coverage gaps + unmapped strays.
	if dgxAgent != nil {
		// doc 21 Phase 2: attach the read-only retrieval tools so the agent gathers MEASURED
		// evidence on demand (and reads its own proposal ledger as memory in the loop). The
		// tools wrap the SAME atomic snapshots the api/MCP serve — one source of truth, no
		// writer in scope. DGX_TOOLS=off is the safe rollback to the Phase-1 single-shot path.
		if os.Getenv("DGX_TOOLS") != "off" {
			dgxAgent.SetTools(newDGXToolRegistry(candStore, ontologyGraph, &coverage, &silenceView, &topoView, &unexpView))
			logger.Info("dgx agent: read-only retrieval tools enabled (doc 21 Phase 2)",
				"tools", "get_strays search_equivalence_groups get_topology get_silence_ledger get_coverage get_unexplained")
		}
		// doc 21 §3 slice 4: the event-driven exploration lane. A watcher diffs the read-only
		// surfaces (unexplained cards, k8s events, operational strays) against a seen-set and
		// triggers an immediate agent run for genuinely-new items, BYPASSING the sweep cadence +
		// gap_state backoff. Bounded queue; drop-on-full (the scheduled sweep is the net).
		explorationQueue := make(chan dgx.ExplorationEvent, dgxExplorationQueueCap)
		go dgxExplorationWatcher(ctx, logger, explorationQueue, candStore, &unexpView, &eventsView,
			envDuration("DGX_EVENT_INTERVAL", dgxExplorationWatch))
		go dgxAgentLoop(ctx, logger, dgxAgent, candStore, store, graphVersion, equivGroupCatalog(ontologyGraph),
			&depView, &silenceView, &logsView, &traceView, &eventsView, explorationQueue, envDuration("DGX_AGENT_INTERVAL", dgxAgentInterval))
	}
	// doc 21 Phase 4: the anomaly→phenomenon-candidate producer. DETERMINISTIC (reads the
	// unexplained channel's recurrence reports, no LLM), so it runs whenever the candidate store
	// is open — independent of the agent. It stages recurring unexplained anomalies as
	// phenomenon_candidate proposals for human curation via governance.
	if candStore != nil {
		go phenomenonCandidateLoop(ctx, logger, candStore, &unexpView, graphVersion,
			envDuration("DGX_PHENOMENON_INTERVAL", dgxPhenomenonInterval))
		// doc 21 Phase 4 §C: the agent enriches each phenomenon candidate with a PROJECTED
		// human-readable label + non-causal description (a reviewer hint, discarded at promotion).
		// Gated on the agent being enabled (it needs the LLM); DGX_ENRICH=off is the rollback.
		if dgxAgent != nil && os.Getenv("DGX_ENRICH") != "off" {
			go phenomenonEnrichmentLoop(ctx, logger, dgxAgent, candStore,
				envDuration("DGX_ENRICHMENT_INTERVAL", dgxEnrichmentInterval))
		}
	}
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
			// doc 22 C2: the changepoint-onset surface (off-digest, MEASURED). The producer
			// runs every ONSET_INTERVAL; this just serves the latest snapshot or the OFF state.
			Onsets: func() *vapi.OnsetView {
				if v := onsetView.Load(); v != nil {
					return v
				}
				return vapi.BuildOnsets(nil, onsetEnabled, 0, time.Now().UTC())
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
			// doc 19: the blindspot registry — what Vigil CANNOT see. Derived on demand
			// from the silence ledger (the SAME binding.Result), so it reconciles with
			// get_silence_ledger by construction; the static floors need no binding.
			Blindspots: func() *vapi.BlindspotRegistryView {
				return vapi.BuildBlindspotRegistry(silenceView.Load())
			},
		}
		// doc 20 P0.b: the read-only candidate surface. main maps candidate.Candidate ->
		// vapi.CandidateRow so the api package never imports internal/candidate (the
		// read-firewall is kept by construction). nil provider ⇒ honest OFF state.
		if candStore != nil {
			providers.Candidates = func() *vapi.CandidatesView {
				rows, err := candStore.List(candidate.Filter{})
				if err != nil {
					logger.Error("dgx candidates surface: list failed (non-gating)", "err", err)
					return nil
				}
				return vapi.NewCandidatesView(time.Now().UTC(), mapCandidateRows(rows))
			}
			// doc 20 P5: reconcile the candidate store into a PROVISIONAL coverage picture
			// (strays classified/mapped/unresolved + agent/modality proposals). Reuses the
			// same rows + mapping; status=candidate, never MEASURED coverage.
			providers.ProvisionalCoverage = func() *vapi.ProvisionalCoverageView {
				rows, err := candStore.List(candidate.Filter{})
				if err != nil {
					logger.Error("dgx provisional-coverage surface: list failed (non-gating)", "err", err)
					return nil
				}
				return vapi.BuildProvisionalCoverage(time.Now().UTC(), mapCandidateRows(rows))
			}
			// doc 20 + doc 12 §3.3: the governance review queue + the human decide endpoint.
			// A candidate becomes authoritative ONLY by a named human's promotion here; main
			// maps candidate.Candidate -> vapi.GovernanceItem (api never imports candidate).
			gv := graphVersion
			providers.Governance = func() *vapi.GovernanceView {
				rows, err := candStore.List(candidate.Filter{})
				if err != nil {
					logger.Error("dgx governance surface: list failed (non-gating)", "err", err)
					return nil
				}
				return vapi.NewGovernanceView(time.Now().UTC(), gv, mapGovernanceItems(rows, gapAttemptsMap(candStore)))
			}
			// slice 3: a human rejection lengthens the candidate's exploration-gap backoff (using
			// the same base as the agent sweep), so a rejected gap is not re-examined every tick.
			gapBase := envDuration("DGX_AGENT_INTERVAL", dgxAgentInterval)
			providers.GovernanceDecide = func(req vapi.GovernanceDecisionRequest) vapi.GovernanceDecisionResult {
				return decideGovernance(candStore, gv, logger, gapBase, req)
			}
			// doc 21 §4 Phase 3 slice 2: a READ-ONLY preview of what promoting a candidate would
			// author — the overlay, plus (for equiv_group) the deterministic stray→group
			// resolution delta on a SCRATCH graph. Never mutates the store or the live graph.
			providers.GovernancePreview = func(candidateID string) *vapi.GovernancePreviewResult {
				return previewGovernance(candStore, ontologyGraph, gv, candidateID)
			}
		}
		if assocEnabled {
			providers.Dependency = func() *vapi.DependencyView { return depView.Load() }
		}
		if logsEnabled {
			providers.LogTemplates = func() *vapi.LogTemplatesView { return logsView.Load() }
		}
		if auditEnabled && auditLogPath != "" {
			providers.AuditChanges = func() *vapi.AuditView { return auditView.Load() }
		}
		if tracesEnabled && tracesPath != "" {
			providers.TraceGraph = func() *vapi.TraceGraphView { return traceView.Load() }
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
			Blindspots:        providers.Blindspots,
			// The MEASURED enrichment lanes for richer synthesis (off the digest, nil when
			// their flag is off — the tool then states the lane is off): recent log patterns
			// on a warned entity, what co-moves with its metric (associated-with, never
			// causal), and what changed shortly before onset (an antecedent, never a cause).
			LogTemplates: providers.LogTemplates,
			Dependency:   providers.Dependency,
			AuditChanges: providers.AuditChanges,
			Referee:      providers.Referee,
		}, mcpAdvisoryGatePassed, "vigil-obsd", graphRelease).HTTPHandler()
		logger.Info("MCP harness enabled (v3 T-A + T-C/T-D + v3.1 synthesis relay)", "route", "/mcp",
			"tools", "get_coverage get_silence_ledger get_warnings get_incidents get_events get_root_cause_chain get_insights get_cross_service get_topology get_unexplained get_departures get_authored_relations get_blindspots get_log_templates get_dependency get_audit_changes validate_claim emit_advisory", "advisoryGate", mcpAdvisoryGatePassed)
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
		eventsEnabled, eventsConds, eventsDets, &eventsSnap, &eventsView, forecastRoleSeries)
	if p.Forecast.Enabled {
		go forecastLoop(ctx, logger, &gate, fcIn, ingestor, graphVersion, graphRelease, p, &warningsView, cwStore, forecastRoleSeries, store)
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
	eventsEnabled bool, eventsConds []events.Corroboration, eventsDets []events.Detection, eventsSnap *atomic.Pointer[eventsSnapshot], eventsView *atomic.Pointer[vapi.EventsView],
	forecastRoleSeries bool) {
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
				if forecastRoleSeries {
					// doc 20 P5: roll pod/container targets up to their durable workload ROLE
					// so a churned pod doesn't sever the series (OwnerReference succession).
					targets = rollUpTargetsToRole(targets, store)
				}
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
// assocMaxStreams bounds the O(n^2) association (doc 20 P2); the surface states the
// bound via StreamsConsidered/StreamsTotal so the partial coverage is honest.
const assocMaxStreams = 256

// assocLoop periodically computes the MEASURED metric-dependency graph over the hot
// series and publishes /api/dependency (doc 20 P2). Off the deterministic path; it
// reads the hot store and writes only the surfacing view — never detection/forecast.
func assocLoop(ctx context.Context, logger *slog.Logger, in *observe.Ingestor, depView *atomic.Pointer[vapi.DependencyView], every time.Duration, maxStreams int) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			series, total := snapshotSeries(in, maxStreams)
			edges := assoc.Associate(now, series, assoc.DefaultParams)
			deps := make([]vapi.DependencyEdge, 0, len(edges))
			for _, e := range edges {
				deps = append(deps, vapi.DependencyEdge{A: e.A, B: e.B, Relation: e.Relation, Coefficient: e.Coefficient, Overlap: e.Overlap})
			}
			depView.Store(vapi.NewDependencyView(now, now.Add(-assoc.DefaultParams.Window), now, deps, len(series), total))
			if len(deps) > 0 {
				logger.Info("dgx: published metric-dependency associations (doc 20 P2)", "edges", len(deps), "streams", len(series), "total", total)
			}
		}
	}
}

// onsetLoop periodically computes the MEASURED changepoint ONSETS over the hot GAUGE series
// (doc 22 C2) and publishes /api/onsets. Off the deterministic path: it reads the hot store
// and writes only the surfacing view — never detection/forecast/governance. Counters are
// skipped (a cumulative counter only steps up). Deterministic per snapshot: same samples +
// same params ⇒ same onsets.
func onsetLoop(ctx context.Context, logger *slog.Logger, in *observe.Ingestor, view *atomic.Pointer[vapi.OnsetView], every time.Duration, maxStreams int) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	p := onset.DefaultParams()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			hot := in.Hot()
			keys := hot.Keys()
			sort.Strings(keys)
			if maxStreams > 0 && len(keys) > maxStreams {
				keys = keys[:maxStreams]
			}
			var onsets []onset.Onset
			scanned := 0
			for _, id := range keys {
				if typ, ok := in.StreamType(id); ok && typ == "counter" {
					continue // a cumulative counter only steps up; onset is for gauges
				}
				cei, metric := splitStreamID(id)
				if onsetSkipMetric(metric) {
					continue // monotonic timestamp / high-water-mark gauge — onset on it is noise
				}
				samples := hot.LastN(id, qss.HotCapacity())
				if len(samples) < p.Warmup+4 {
					continue
				}
				scanned++
				onsets = append(onsets, onset.Detect(cei, metric, samples, p)...)
			}
			sort.SliceStable(onsets, func(i, j int) bool {
				if !onsets[i].At.Equal(onsets[j].At) {
					return onsets[i].At.Before(onsets[j].At)
				}
				if onsets[i].EntityCEI != onsets[j].EntityCEI {
					return onsets[i].EntityCEI < onsets[j].EntityCEI
				}
				return onsets[i].Metric < onsets[j].Metric
			})
			view.Store(vapi.BuildOnsets(onsets, true, scanned, now))
			if len(onsets) > 0 {
				logger.Info("onset: published changepoints (doc 22 C2)", "onsets", len(onsets), "gauge_streams", scanned)
			}
		}
	}
}

// onsetSkipMetric drops metrics that are monotonic timestamps or high-water marks which
// cAdvisor (and similar exporters) expose as GAUGE: a changepoint on a clock is meaningless,
// and a high-water mark's step is redundant with the live gauge (working_set already fires).
// Discovered by the live vigil-abb capture (container_last_seen produced a spurious onset).
func onsetSkipMetric(metric string) bool {
	return strings.HasPrefix(metric, "container_last_seen") ||
		strings.HasPrefix(metric, "container_start_time") ||
		strings.Contains(metric, "_max_usage_bytes")
}

// splitStreamID splits a hot-store key "CEI.Key()|metric|sub" into (cei, metric+sub): the
// CEI key is the first 6 "|"-separated fields (layer|cluster|ns|kind|name|uid), the rest is
// the metric (+ any sub-id). Fewer than 7 fields ⇒ the whole id as the entity, empty metric.
func splitStreamID(id string) (cei, metric string) {
	parts := strings.Split(id, "|")
	if len(parts) >= 7 {
		return strings.Join(parts[:6], "|"), strings.Join(parts[6:], "|")
	}
	return id, ""
}

// snapshotSeries reads up to maxStreams hot series (deterministic order) as assoc
// Points, returning the snapshot and the total stream count (for honest coverage).
func snapshotSeries(in *observe.Ingestor, maxStreams int) (map[string][]assoc.Point, int) {
	hot := in.Hot()
	keys := hot.Keys()
	sort.Strings(keys)
	total := len(keys)
	if maxStreams > 0 && len(keys) > maxStreams {
		keys = keys[:maxStreams]
	}
	out := make(map[string][]assoc.Point, len(keys))
	for _, id := range keys {
		samples := hot.LastN(id, qss.HotCapacity())
		pts := make([]assoc.Point, 0, len(samples))
		for _, smp := range samples {
			pts = append(pts, assoc.Point{At: smp.At, Value: smp.Value})
		}
		out[id] = pts
	}
	return out, total
}

// dgxAgentInterval paces the LLM proposer (doc 20 P3) — LLM calls are costly, so the
// cadence is slow and declared.
const dgxAgentInterval = 5 * time.Minute

// dgxAgentObsCap bounds how many of each observation kind enter the prompt.
const dgxAgentObsCap = 20

// dgxAgentStrayCap bounds how many unmapped strays are offered to the agent per run
// (kept small so the prompt stays under the provider's token budget; the rest are tried
// on later cycles as the store is re-read).
const dgxAgentStrayCap = 12

// unmappedStrayObservations pulls the strays the deterministic ER could not join (a
// cei-fallback provisional NODE with no associated-with edge) and renders each as a
// "stray-metric" observation the agent may try to map to a real entity. main reads the
// candidate store; the agent only ever sees these as read-only grounding context.
func unmappedStrayObservations(cs *candidate.Store, max int) []dgx.Observation {
	if cs == nil {
		return nil
	}
	nodes, err := cs.List(candidate.Filter{Kind: candidate.KindNode})
	if err != nil {
		return nil
	}
	edges, _ := cs.List(candidate.Filter{Kind: candidate.KindEdge})
	mapped := map[string]bool{}
	for _, e := range edges {
		if e.Lineage.Source == "cei-fallback" {
			if i := strings.Index(e.Subject, " ~> "); i > 0 {
				mapped[e.Subject[:i]] = true
			}
		}
	}
	out := make([]dgx.Observation, 0, max)
	for _, n := range nodes {
		if n.Lineage.Source != "cei-fallback" || mapped[n.Subject] {
			continue
		}
		metric, _ := n.Payload["metric"].(string)
		// Don't spend the agent's bounded budget mapping pure k8s object-metadata inventory
		// (KSM kube_<object>_* state) — it is non-actionable; feed only operational strays.
		if candidate.ClassifyStrayMetric(metric) == candidate.StrayObjectMetadata {
			continue
		}
		out = append(out, dgx.Observation{
			Ref:    n.Subject,
			Kind:   "stray-metric",
			Detail: "unmapped metric " + metric + " labels{" + labelSummary(n.Payload["labels"]) + "}",
		})
		if len(out) >= max {
			break
		}
	}
	return out
}

// dgxAgentModalityCap bounds how many of each modality observation enter the prompt.
const dgxAgentModalityCap = 8

// modalityObservations renders the MEASURED modality lanes — mined log templates, observed
// trace call edges, and discrete events — as read-only agent observations, so the agent can
// reason over them (e.g. associate a loud log template or an erroring trace edge with a
// workload) instead of seeing only metric associations. Each is bounded + cited by a stable
// ref; the agent proposes only associated-with edges / direction-free hypotheses from them.
func modalityObservations(logs *vapi.LogTemplatesView, traces *vapi.TraceGraphView, events *vapi.EventsView) []dgx.Observation {
	var out []dgx.Observation
	if logs != nil && logs.Available {
		n := 0
		for _, t := range logs.Templates {
			if n >= dgxAgentModalityCap {
				break
			}
			out = append(out, dgx.Observation{
				Ref: "logtmpl:" + t.Pattern, Kind: "log-template",
				Detail: fmt.Sprintf("mined log template (count %d): %s", t.Count, t.Pattern),
			})
			n++
		}
	}
	if traces != nil && traces.Available {
		n := 0
		for _, e := range traces.Edges {
			if n >= dgxAgentModalityCap {
				break
			}
			out = append(out, dgx.Observation{
				Ref: "trace:" + e.Caller + "->" + e.Callee, Kind: "trace-edge",
				Detail: fmt.Sprintf("observed call %s -> %s (calls %d, errors %d, p95 %.0fms)", e.Caller, e.Callee, e.Calls, e.Errors, e.P95Millis),
			})
			n++
		}
	}
	if events != nil && events.Available {
		n := 0
		for _, ev := range events.Events {
			if n >= dgxAgentModalityCap {
				break
			}
			out = append(out, dgx.Observation{
				Ref: "event:" + ev.Reason + "@" + ev.Namespace + "/" + ev.Name, Kind: "k8s-event",
				Detail: fmt.Sprintf("%s on %s/%s (kind %s, count %d)", ev.Reason, ev.Namespace, ev.Name, ev.Kind, ev.Count),
			})
			n++
		}
	}
	return out
}

// labelSummary renders a stray candidate's labels (a map[string]any from JSON) as a
// compact, deterministic "k=v,k=v" string for the agent prompt.
func labelSummary(v any) string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ",")
}

// dgxExplorationQueueCap bounds the event channel; on backpressure events are dropped (the
// scheduled sweep is the net, so a drop is never a missed gap, only a delayed one).
const dgxExplorationQueueCap = 100

// dgxExplorationWatch is the default cadence the seen-set watcher diffs the read-only surfaces.
const dgxExplorationWatch = 30 * time.Second

// dgxExplorationWatcher diffs the read-only surfaces against a content-keyed seen-set every
// interval and emits an ExplorationEvent for each NEWLY-seen unexplained card, k8s event, or
// operational stray (doc 21 §3 slice 4). The FIRST tick seeds the seen-set WITHOUT firing (the
// existing backlog is not "new"). Newness is a set DIFFERENCE, never a novelty score (charter:
// no learned trigger). Reads the SAME atomic snapshots the api/MCP serve — one source of truth.
func dgxExplorationWatcher(ctx context.Context, logger *slog.Logger, queue chan<- dgx.ExplorationEvent, cs *candidate.Store,
	unexpView *atomic.Pointer[vapi.UnexplainedView], eventsView *atomic.Pointer[vapi.EventsView], interval time.Duration) {
	if interval <= 0 {
		interval = dgxExplorationWatch
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	seen := map[string]bool{}
	primed := false
	droppedLogged := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			fresh := diffNewExplorationEvents(seen, unexpView.Load(), eventsView.Load(), unmappedStrayObservations(cs, dgxAgentStrayCap), now)
			if !primed {
				// The first sweep seeds the seen-set with the existing backlog — those are not
				// "new", so they must not all fire at once. Genuinely-new items fire next tick.
				primed = true
				continue
			}
			for _, e := range fresh {
				select {
				case queue <- e:
				default:
					if !droppedLogged {
						logger.Warn("dgx exploration: event queue full, dropping (the scheduled sweep is the net)", "kind", e.Kind)
						droppedLogged = true
					}
				}
			}
			if len(fresh) > 0 {
				logger.Info("dgx exploration: new triggers queued (doc 21 §3 slice 4)", "count", len(fresh))
			}
		}
	}
}

// diffNewExplorationEvents folds the current read-only surfaces against the seen-set and returns
// the NEWLY-seen items as exploration events, recording each into `seen` so it never re-fires.
// Pure given (seen, snapshots, now) — the testable core of the watcher.
func diffNewExplorationEvents(seen map[string]bool, unexp *vapi.UnexplainedView, events *vapi.EventsView, strays []dgx.Observation, now time.Time) []dgx.ExplorationEvent {
	var fresh []dgx.ExplorationEvent
	add := func(kind, ref, detail string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		fresh = append(fresh, dgx.ExplorationEvent{Kind: kind, Ref: ref, Detail: detail, EmittedAt: now})
	}
	if unexp != nil {
		for _, f := range unexp.OpenCards {
			// Only a genuinely NEW unexplained card (not an aging/superseded one) is a trigger.
			if string(f.Status) != "new" {
				continue
			}
			add("unexplained", "unexplained:"+f.Scope+"|"+f.Namespace+"|"+f.Name+"|"+f.Kind,
				"new unexplained loud card on "+f.Kind+" "+f.Name+" ("+f.Namespace+")")
		}
	}
	if events != nil && events.Available {
		for _, e := range events.Events {
			add("event", "event:"+e.Reason+"@"+e.Namespace+"/"+e.Name,
				"k8s event "+e.Reason+" on "+e.Kind+" "+e.Name)
		}
	}
	for _, s := range strays {
		// s.Ref is the operational stray subject ("stray:<metric>"); object-metadata strays are
		// already filtered out by unmappedStrayObservations.
		add("stray", s.Ref, s.Detail)
	}
	return fresh
}

// gapRefPrefixes are the observation Refs the agent EXPLORES (vs reasons WITH): each is a
// "gap" scheduled by gap_state's deterministic backoff (doc 21 §3). An association (assoc:)
// or a valid-entity ref is a FACT the agent reasons with, never a gap to back off.
var gapRefPrefixes = []string{"stray:", "silence:", "unexplained:", "event:", "logtmpl:", "trace:"}

func isGapRef(ref string) bool {
	for _, p := range gapRefPrefixes {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

// gapObservationIDs returns the distinct gap Refs among the observations (deterministic order).
func gapObservationIDs(obs []dgx.Observation) []string {
	var out []string
	seen := map[string]bool{}
	for _, o := range obs {
		if isGapRef(o.Ref) && !seen[o.Ref] {
			seen[o.Ref] = true
			out = append(out, o.Ref)
		}
	}
	return out
}

// filterDueObservations keeps every non-gap observation plus the gaps that are DUE this sweep;
// the backed-off gaps are dropped (they return once overdue). Order-preserving.
func filterDueObservations(obs []dgx.Observation, due map[string]bool) []dgx.Observation {
	out := make([]dgx.Observation, 0, len(obs))
	for _, o := range obs {
		if !isGapRef(o.Ref) || due[o.Ref] {
			out = append(out, o)
		}
	}
	return out
}

// gapIDForCandidate returns the exploration gap a candidate addresses — its subject if that is
// a gap ref, else its first gap-prefixed evidence ref — or "" if none. Feeds the Recurrence
// support count (slice 1) and the human-rejection backoff (decideGovernance).
func gapIDForCandidate(c candidate.Candidate) string {
	if isGapRef(c.Subject) {
		return c.Subject
	}
	for _, e := range c.Evidence {
		if isGapRef(e.Ref) {
			return e.Ref
		}
	}
	return ""
}

// gapAttemptsMap snapshots the gap-state attempt counts (gap_id → attempts) so the governance
// surface can fold each candidate's recurrence into its deterministic Support. Best-effort:
// a read error yields an empty map (recurrence simply shows 0).
func gapAttemptsMap(cs *candidate.Store) map[string]int {
	states, err := cs.ListGapStates()
	if err != nil {
		return nil
	}
	m := make(map[string]int, len(states))
	for _, g := range states {
		m[g.GapID] = g.AttemptCount
	}
	return m
}

// dgxPhenomenonInterval is the cadence the deterministic phenomenon-candidate producer
// re-reads the unexplained curation reports. The reports change slowly (recurrence
// aggregation), so a relaxed cadence is plenty; re-Put is idempotent (content-id dedup).
const dgxPhenomenonInterval = 60 * time.Second

// phenomenonCandidateLoop is the anomaly→phenomenon producer (doc 21 Phase 4): each interval it
// reads the unexplained channel's RECURRING-anomaly curation reports (a DETERMINISTIC recurrence
// aggregation, doc 08 §3.6 — never a model invention) and stages each as a phenomenon_candidate
// in the firewalled candidate store, so a NAMED HUMAN can curate a recurring anomaly into a real
// phenomenon via governance. Off the deterministic path; the system proposes, it never authors.
func phenomenonCandidateLoop(ctx context.Context, logger *slog.Logger, cs *candidate.Store,
	unexpView *atomic.Pointer[vapi.UnexplainedView], graphVersion string, every time.Duration) {
	if every <= 0 {
		every = dgxPhenomenonInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			uv := unexpView.Load()
			if uv == nil || len(uv.Candidates) == 0 {
				continue
			}
			now := time.Now().UTC()
			// DEDUP BY SIGNATURE (subject): a recurring anomaly is ONE decision. The recurrence
			// `windows` count grows every eval tick, which would otherwise change the content id and
			// stage a NEW row each cycle. Skip any signature already present (any status) so the
			// queue holds exactly one candidate per recurring anomaly — and a rejected/promoted one
			// is never re-proposed.
			seen := map[string]bool{}
			if rows, err := cs.List(candidate.Filter{Kind: candidate.KindPhenomenonCandidate}); err == nil {
				for _, r := range rows {
					seen[r.Subject] = true
				}
			}
			staged := 0
			for _, rep := range uv.Candidates {
				subj := candidate.PhenomenonCandidateSubject(phenomenonSignature(rep))
				if seen[subj] {
					continue
				}
				if err := stagePhenomenonCandidate(cs, now, rep, graphVersion); err != nil {
					logger.Warn("dgx phenomenon-candidate: stage failed (non-gating)", "err", err)
					continue
				}
				seen[subj] = true
				staged++
			}
			if staged > 0 {
				logger.Info("dgx phenomenon-candidate producer (doc 21 Phase 4)", "staged", staged,
					"note", "recurring unexplained anomalies proposed for human curation")
			}
		}
	}
}

// stagePhenomenonCandidate converts ONE deterministic recurrence report into a staged
// phenomenon_candidate. The signature (entity kind + sorted metric set) mirrors the unexplained
// channel's recurrence key, so re-staging the same recurrence updates the row in place. Evidence
// = the recurrence fact + each distinct entity that exhibited it (so the support chip reflects
// the cross-entity reach). The report's Rationale is the recurrence fact, never a causal claim.
// phenomenonSignature is the stable recurrence key (entity kind + sorted metric set) — mirrors
// the unexplained channel's signatureKey, and is the dedup subject for a recurring anomaly.
func phenomenonSignature(rep unexplained.CandidateReport) string {
	metrics := append([]string(nil), rep.Metrics...)
	sort.Strings(metrics)
	return rep.EntityKind + "\x1f" + strings.Join(metrics, ",")
}

func stagePhenomenonCandidate(cs *candidate.Store, now time.Time, rep unexplained.CandidateReport, graphVersion string) error {
	metrics := append([]string(nil), rep.Metrics...)
	sort.Strings(metrics)
	sig := phenomenonSignature(rep)
	prop := candidate.PhenomenonCandidateProposal{
		Signature: sig, EntityKind: rep.EntityKind, Metrics: metrics,
		Windows: rep.Windows, Entities: rep.Entities,
	}
	if err := candidate.ValidatePhenomenonCandidateProposal(prop); err != nil {
		return err // a malformed report (no metrics / no recurrence) is skipped, never staged
	}
	payload := candidate.PhenomenonCandidatePayload(prop)
	payload["rationale"] = rep.Rationale // the deterministic recurrence fact (no causal vocabulary)
	ev := []candidate.EvidenceRef{{
		Kind: "context:unexplained", Ref: "unexplained:" + sig,
		Detail: fmt.Sprintf("recurring unexplained loudness across %d evaluation windows", rep.Windows),
	}}
	for _, e := range rep.Entities {
		ev = append(ev, candidate.EvidenceRef{Kind: "context:entity", Ref: "entity:" + e, Detail: "exhibited the recurring loudness"})
	}
	c := candidate.Candidate{
		Kind:     candidate.KindPhenomenonCandidate,
		Subject:  candidate.PhenomenonCandidateSubject(sig),
		Evidence: ev,
		Lineage:  candidate.Lineage{Source: "unexplained-curation", Method: "recurrence-aggregation", GraphVersion: graphVersion},
		Payload:  payload,
	}
	_, err := cs.Put(now, c)
	return err
}

// dgxEnrichmentInterval / dgxEnrichmentPerCycle bound the agent-enrichment pass: a relaxed
// cadence, a few LLM calls per cycle, so the PROJECTED hints accrue without burning the provider.
const (
	dgxEnrichmentInterval = 90 * time.Second
	dgxEnrichmentPerCycle = 5
)

// phenomenonEnrichmentLoop is the agent-enrichment pass (doc 21 Phase 4 §C). For each PENDING
// phenomenon candidate that has no model hint yet, it asks the agent for a human-readable label +
// a non-causal description and attaches it as a PROJECTED Suggestion — a reviewer aid that is
// DISCARDED at promotion (the named human authors the real label + detection). It NEVER touches
// the deterministic candidate's identity/evidence (SetSuggestion is content-id-stable) and NEVER
// authors detection. Gated on the agent being enabled (it needs the LLM); off the deterministic
// path; honours the provider rate-limit; bounded calls per cycle.
func phenomenonEnrichmentLoop(ctx context.Context, logger *slog.Logger, agent *dgx.Agent, cs *candidate.Store, every time.Duration) {
	if every <= 0 {
		every = dgxEnrichmentInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var rateLimitedUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			if now.Before(rateLimitedUntil) {
				continue
			}
			rows, err := cs.List(candidate.Filter{Kind: candidate.KindPhenomenonCandidate, Status: candidate.StatusCandidate})
			if err != nil {
				logger.Warn("dgx phenomenon enrichment: list failed (non-gating)", "err", err)
				continue
			}
			enriched := 0
			for _, c := range rows {
				if enriched >= dgxEnrichmentPerCycle {
					break
				}
				if c.Suggestion != nil {
					continue // already enriched — one hint per candidate
				}
				prop, perr := candidate.ParsePhenomenonCandidatePayload(c.Payload)
				if perr != nil || len(prop.Metrics) == 0 {
					continue
				}
				sg, serr := agent.SuggestPhenomenon(ctx, prop.Metrics, prop.EntityKind)
				if serr != nil {
					if isRateLimited(serr) {
						rateLimitedUntil = now.Add(dgxAgentRateLimitCooldown)
						logger.Warn("dgx phenomenon enrichment rate-limited; backing off (non-gating)",
							"cooldown", dgxAgentRateLimitCooldown.String(), "err", truncStr(serr.Error(), 200))
						break
					}
					logger.Warn("dgx phenomenon enrichment failed (non-gating)", "subject", c.Subject, "err", truncStr(serr.Error(), 200))
					continue
				}
				if err := cs.SetSuggestion(now, c.ID, &sg); err != nil {
					logger.Warn("dgx phenomenon enrichment: set suggestion failed (non-gating)", "err", err)
					continue
				}
				enriched++
			}
			if enriched > 0 {
				logger.Info("dgx phenomenon enrichment (doc 21 Phase 4 §C)", "enriched", enriched,
					"note", "PROJECTED label/description hints — discarded at promotion")
			}
		}
	}
}

// dgxAgentLoop runs the LLM proposer over read-only context each interval and stages
// the gated survivors (doc 20 P3). Off the deterministic path; the agent authors
// nothing — it proposes, the gates filter, a human promotes later.
func dgxAgentLoop(ctx context.Context, logger *slog.Logger, agent *dgx.Agent, cs *candidate.Store, store *identity.Store, graphVersion string, knownGroups map[string]string,
	depView *atomic.Pointer[vapi.DependencyView], silenceView *atomic.Pointer[vapi.SilenceLedgerView],
	logsView *atomic.Pointer[vapi.LogTemplatesView], traceView *atomic.Pointer[vapi.TraceGraphView], eventsView *atomic.Pointer[vapi.EventsView],
	explore <-chan dgx.ExplorationEvent, every time.Duration) {
	if every <= 0 {
		every = 5 * time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var rateLimitedUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// SCHEDULED SWEEP: read every surface, skip backed-off gaps (slice 3), explore the rest.
			now := time.Now().UTC()
			if now.Before(rateLimitedUntil) {
				continue // backing off after a provider rate-limit; no per-tick log spam
			}
			c := buildAgentContext(graphVersion, knownGroups, depView.Load(), silenceView.Load(), buildEntityRefs(store), unmappedStrayObservations(cs, dgxAgentStrayCap))
			c.Observations = append(c.Observations, modalityObservations(logsView.Load(), traceView.Load(), eventsView.Load())...)
			if len(c.Observations) == 0 {
				continue
			}
			// doc 21 §3 Phase 3 slice 3: skip exploration gaps still inside their backoff window —
			// re-examine a gap (an unmapped stray, a silence row, a modality lane) only once it is
			// OVERDUE. A new gap (no gap_state row) is always due; an examined gap backs off
			// base·2^(attempts-1). This bounds repeated LLM spend on gaps that keep yielding
			// nothing, deterministically (no learned interval). Non-gap facts (associations) stay.
			if gapIDs := gapObservationIDs(c.Observations); len(gapIDs) > 0 {
				if due, derr := cs.DueGaps(now, gapIDs); derr != nil {
					logger.Warn("dgx gap-state: due check failed (examining all gaps this sweep)", "err", derr)
				} else {
					c.Observations = filterDueObservations(c.Observations, due)
				}
			}
			dueGaps := gapObservationIDs(c.Observations)
			if len(dueGaps) == 0 {
				continue // every exploration gap is backing off — nothing new to examine this sweep
			}
			runAgentOnce(ctx, logger, agent, cs, now, c, dueGaps, every, &rateLimitedUntil, "sweep")
		case ev := <-explore:
			// EVENT TRIGGER (slice 4): a newly-seen unexplained card / k8s event / operational stray
			// asks the agent to explore NOW — BYPASSING gap_state backoff (a genuinely new thing is
			// worth a look even if its gap recently backed off). We still honour the provider
			// rate-limit, and record a gap attempt after, so future SWEEPS back the gap off normally.
			now := time.Now().UTC()
			if now.Before(rateLimitedUntil) {
				// Provider still in cooldown: drop this event (it is NOT re-queued — already
				// dequeued). Non-gating: the underlying observation persists in the read-only
				// surfaces and the next scheduled sweep re-examines it. Logged so the drop is never
				// silent (honest partial coverage).
				logger.Debug("dgx exploration: event dropped (provider rate-limited; the next sweep is the net)",
					"kind", ev.Kind, "ref", ev.Ref, "cooldownUntil", rateLimitedUntil.UTC().Format(time.RFC3339))
				continue
			}
			c := buildEventContext(graphVersion, knownGroups, buildEntityRefs(store), ev)
			runAgentOnce(ctx, logger, agent, cs, now, c, []string{ev.Ref}, every, &rateLimitedUntil, "event:"+ev.Kind)
		}
	}
}

// runAgentOnce runs ONE agent pass over context c (a scheduled sweep or an event trigger),
// handling provider rate-limits (extending *rateLimitedUntil) and advancing the gap_state
// backoff for the examined gaps (slice 3). `trigger` labels the structured log. Non-gating:
// any failure is logged and swallowed — detection/forecast never wait on the agent.
func runAgentOnce(ctx context.Context, logger *slog.Logger, agent *dgx.Agent, cs *candidate.Store, now time.Time, c dgx.Context, examinedGaps []string, every time.Duration, rateLimitedUntil *time.Time, trigger string) {
	// doc 21 §2.2c: the agent reads its OWN prior proposals (promoted/rejected/pending) as memory
	// so it does not re-propose what was already decided.
	led := buildLedger(cs)
	rep, err := agent.RunOnce(ctx, cs, now, c, led)
	if err != nil {
		if isRateLimited(err) {
			// Provider quota exhausted (e.g. Groq tokens-per-day). Back off so we do NOT re-fire
			// every tick for the whole quota window; log ONCE at WARN. Non-gating: detection,
			// forecasting, and every other lane are unaffected, and the staged candidates remain.
			*rateLimitedUntil = now.Add(dgxAgentRateLimitCooldown)
			logger.Warn("dgx agent rate-limited; backing off (non-gating — detection/forecast unaffected)",
				"cooldown", dgxAgentRateLimitCooldown.String(), "trigger", trigger, "err", truncStr(err.Error(), 200))
			return
		}
		logger.Error("dgx agent run failed (non-gating)", "trigger", trigger, "err", err)
		return
	}
	// slice 3: advance each examined gap's backoff (deterministic; injected `now`). The Recurrence
	// count this builds surfaces in candidate Support (the review ranking).
	for _, id := range examinedGaps {
		if id == "" || !isGapRef(id) {
			continue
		}
		if _, e := cs.RecordGapAttempt(now, id, "", every, candidate.GapBackoffCap); e != nil {
			logger.Warn("dgx gap-state: record attempt failed (non-gating)", "gap", id, "err", e)
		}
	}
	logger.Info("dgx agent run (doc 20 P3 + doc 21 Phase 2-3)", "provider", rep.Provider, "trigger", trigger,
		"proposed", rep.Proposed, "accepted", rep.Accepted, "rejected", len(rep.Rejected), "thresholdGated", rep.ThresholdGated,
		"toolCalls", rep.ToolCalls, "iterations", rep.Iterations, "gapsExamined", len(examinedGaps), "note", rep.Note)
}

// buildEventContext seeds a minimal agent context from one exploration event: the event as a
// single observation plus the valid entity keys + the equivalence-group catalog. The agent's
// read-only tools (Phase 2) gather any further evidence on demand, so the seed stays small.
func buildEventContext(graphVersion string, knownGroups map[string]string, entities []candidate.EntityRef, ev dgx.ExplorationEvent) dgx.Context {
	c := dgx.Context{GraphVersion: graphVersion, ValidEntities: make(map[string]bool, len(entities)), KnownGroups: knownGroups}
	for _, e := range entities {
		c.ValidEntities[e.Key] = true
	}
	c.Observations = []dgx.Observation{{Ref: ev.Ref, Kind: ev.Kind, Detail: ev.Detail}}
	return c
}

// dgxAgentRateLimitCooldown pauses the LLM proposer after a provider rate-limit so it does
// not hammer the quota every tick (the agent is non-gating; nothing else waits on it).
const dgxAgentRateLimitCooldown = 10 * time.Minute

// isRateLimited reports whether a provider error is a rate-limit / quota rejection (HTTP
// 429), so the loop can back off instead of retrying every tick.
func isRateLimited(err error) bool {
	s := err.Error()
	return strings.Contains(s, "status 429") || strings.Contains(s, "rate limit") || strings.Contains(s, "Rate limit")
}

func truncStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// buildAgentContext maps the read-only surfaces into the agent's grounding context
// (doc 20 P3): associations + coverage gaps as MEASURED observations the agent may
// cite, and the valid entity keys. main does the mapping so dgx never imports api.
func buildAgentContext(graphVersion string, knownGroups map[string]string, dep *vapi.DependencyView, silence *vapi.SilenceLedgerView, entities []candidate.EntityRef, strayObs []dgx.Observation) dgx.Context {
	c := dgx.Context{GraphVersion: graphVersion, ValidEntities: make(map[string]bool, len(entities)), KnownGroups: knownGroups}
	for _, e := range entities {
		c.ValidEntities[e.Key] = true
	}
	// doc 20: the unmapped strays the deterministic ER could not join (their names didn't
	// reach the ≥2-coordinate floor). The agent may PROPOSE an associated-with edge mapping
	// a stray to a real entity when its labels identify one — a semantic join the discrete
	// ER cannot make, staged as a candidate for human verification (never authored).
	c.Observations = append(c.Observations, strayObs...)
	if dep != nil && dep.Available {
		for i, e := range dep.Edges {
			if i >= dgxAgentObsCap {
				break
			}
			c.Observations = append(c.Observations, dgx.Observation{
				Ref:    "assoc:" + e.A + "~" + e.B,
				Kind:   "association",
				Detail: fmt.Sprintf("%s associated-with %s (r=%.2f, overlap=%d)", e.A, e.B, e.Coefficient, e.Overlap),
			})
		}
	}
	if silence != nil {
		n := 0
		for _, row := range silence.Silent {
			if n >= dgxAgentObsCap {
				break
			}
			c.Observations = append(c.Observations, dgx.Observation{Ref: "silence:" + row.Metric, Kind: "coverage-gap", Detail: row.Metric + ": " + row.Reason})
			n++
		}
	}
	return c
}

// equivGroupCatalog builds the read-only equivalence-group catalog (id → canonical OTel)
// the agent maps operational strays into (doc 21 §5). Computed ONCE from the immutable bound
// graph; nil-safe (an empty catalog when binding is disabled simply means the agent proposes
// no equiv_group mappings that cycle).
func equivGroupCatalog(g *graph.Graph) map[string]string {
	if g == nil {
		return nil
	}
	out := make(map[string]string, len(g.EquivalenceGroups))
	for id, eg := range g.EquivalenceGroups {
		out[id] = eg.CanonicalOTel
	}
	return out
}

// envDuration reads a duration from env (e.g. tuning a lane cadence), falling back to
// def when unset or unparseable. Operability + lets a live test run a tight cycle.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// log-lane bounds (doc 20 P4): sample at most this many running pods, tailing this many
// lines each — keeps the off-digest mining cheap and the surface honest about its sample.
const logsTailLines = 120
const logsMaxPods = 24

// audit-lane bounds (doc 20 P4 AUDIT): how far back a change may be an antecedent (the
// arrow-of-time lookback) and how many tail lines of the audit log to read per cycle.
const auditLookback = 30 * time.Minute
const auditMaxLines = 4000

// auditLoop reads the apiserver audit log (JSONL) each interval, publishes the MEASURED
// change feed, and — for each active incident — stages a direction-free arrow-of-time
// co-occurrence hypothesis into the candidate store (doc 20 P4 AUDIT). Off the
// deterministic path; non-gating. candStore/findingsStore may be nil (then only the
// change feed is published, with no hypotheses).
func auditLoop(ctx context.Context, logger *slog.Logger, store *identity.Store, candStore *candidate.Store,
	findingsStore *fstore.Store, auditView *atomic.Pointer[vapi.AuditView],
	path, clusterID, graphVersion string, every, lookback time.Duration, maxLines int) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			lines, truncated := readTail(path, maxLines, tailReadMaxBytes)
			changes := audit.Resolve(audit.ParseEvents(lines), buildAuditResolver(store, clusterID))
			staged := 0
			if candStore != nil && findingsStore != nil {
				incs := activeAuditIncidents(findingsStore, graphVersion)
				n, err := audit.HypothesizeAndStage(candStore, now, changes, incs, lookback)
				if err != nil {
					logger.Error("dgx: stage audit hypotheses failed (non-gating)", "err", err)
				}
				staged = n
			}
			auditView.Store(vapi.NewAuditView(now, staged, len(lines), truncated, mapAuditRows(changes)))
			if truncated {
				logger.Warn("dgx: audit log exceeded the read cap; older lines dropped this cycle (partial coverage, surfaced at /api/audit-changes)", "linesRead", len(lines), "maxLines", maxLines)
			}
			if len(changes) > 0 {
				logger.Info("dgx: audit change feed (doc 20 P4 AUDIT)", "changes", len(changes), "hypothesesStaged", staged)
			}
		}
	}
}

// tailReadMaxBytes bounds the bytes read per cycle by the audit/trace JSONL tail reader,
// so a huge log cannot blow memory regardless of the line cap. Older content beyond this
// is dropped — and the truncation is SURFACED + logged, never hidden (honest partial
// coverage). A production deployment would stream a rotated log; this is the kind/demo path.
const tailReadMaxBytes int64 = 8 << 20 // 8 MiB

// readTail reads the last lines of a JSONL file, bounded by BOTH a byte cap (memory is
// bounded by the cap, NOT the file size) and a line cap, returning the lines and whether
// it truncated (dropped older content). Best-effort: an unreadable path yields no lines,
// never fatal. When the file exceeds the byte cap, the first (partial) line of the window
// is dropped — harmless, since the parsers skip malformed lines.
func readTail(path string, maxLines int, maxBytes int64) (lines []string, truncated bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false
	}
	var data []byte
	if st.Size() > maxBytes {
		truncated = true
		buf := make([]byte, maxBytes)
		n, err := f.ReadAt(buf, st.Size()-maxBytes)
		if err != nil && err != io.EOF {
			return nil, false
		}
		data = buf[:n]
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:] // drop the partial first line of the window
		}
	} else {
		if data, err = io.ReadAll(f); err != nil {
			return nil, false
		}
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > maxLines {
		all = all[len(all)-maxLines:]
		truncated = true
	}
	return all, truncated
}

// buildAuditResolver snapshots the active role CEIs and returns a resolver that maps a
// change object (namespace, resource, name) to a durable role CEI by constructing the
// role key from the authored resource→Kind map and checking membership. main does this
// so the audit core imports neither identity nor client-go. A miss ⇒ ("", false) ⇒ the
// change is honestly RoleUnresolved (never a guessed role).
func buildAuditResolver(store *identity.Store, clusterID string) audit.Resolver {
	active := map[string]bool{}
	if store != nil {
		for _, r := range store.ActiveInstances() {
			if r.RoleCEI.RoleKey != "" {
				active[r.RoleCEI.Key()] = true
			}
		}
	}
	return func(ns, resource, name string) (string, bool) {
		kind, ok := audit.KindForResource(resource)
		if !ok {
			return "", false
		}
		key := identity.CEI{Layer: identity.LayerRole, Cluster: clusterID, Namespace: ns, Kind: kind, RoleKey: kind + "/" + name}.Key()
		if active[key] {
			return key, true
		}
		return "", false
	}
}

// activeAuditIncidents maps the durable incident memory into audit.Incidents (the join
// targets). The incident's onset is its FirstSeen; its namespace is parsed from the role
// CEI key. main does the mapping so the audit core imports neither store nor identity.
func activeAuditIncidents(fs *fstore.Store, graphVersion string) []audit.Incident {
	rows, err := fs.ActiveIncidents(200)
	if err != nil {
		return nil
	}
	out := make([]audit.Incident, 0, len(rows))
	for _, r := range rows {
		gv := r.GraphVersion
		if gv == "" {
			gv = graphVersion
		}
		out = append(out, audit.Incident{
			ID: r.Key, RoleCEI: r.RoleCEI, Namespace: namespaceFromCEIKey(r.RoleCEI),
			Onset: r.FirstSeen, GraphVersion: gv,
		})
	}
	return out
}

// namespaceFromCEIKey extracts the namespace from a role/instance CEI key
// ("r|cluster|namespace|…" or "i|cluster|namespace|…"). Returns "" if the shape is
// unexpected (never a guessed namespace).
func namespaceFromCEIKey(key string) string {
	parts := strings.Split(key, "|")
	if len(parts) >= 3 && (parts[0] == "r" || parts[0] == "i") {
		return parts[2]
	}
	return ""
}

// mapAuditRows projects audit.ChangeEvents into the surfacing row type (doc 20 P4 AUDIT).
// main does the mapping so the api package never imports internal/audit (read-firewall).
func mapAuditRows(changes []audit.ChangeEvent) []vapi.AuditChangeRow {
	out := make([]vapi.AuditChangeRow, 0, len(changes))
	for _, c := range changes {
		out = append(out, vapi.AuditChangeRow{
			AuditID: c.AuditID, Verb: c.Verb, Resource: c.Resource, Namespace: c.Namespace,
			Name: c.Name, User: c.User, RoleCEI: c.RoleCEI, RoleUnresolved: c.RoleUnresolved,
			Timestamp: c.Timestamp,
		})
	}
	return out
}

// trace-lane bound (doc 20 P4 TRACE): read at most this many tail lines of the spans
// JSONL per cycle, keeping the off-digest call-graph build cheap + the census honest.
const traceMaxLines = 20000

// tracesLoop reads OTel spans (JSONL) each interval, publishes the MEASURED observed call
// graph, and stages each discovered service-to-service call as a STRUCTURAL topology
// candidate (doc 20 P4 TRACE). Off the deterministic path; non-gating. candStore may be
// nil (then only the call graph is published, with no candidates).
func tracesLoop(ctx context.Context, logger *slog.Logger, store *identity.Store, candStore *candidate.Store,
	traceView *atomic.Pointer[vapi.TraceGraphView], path, clusterID, graphVersion string, every time.Duration, maxLines int) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			lines, truncated := readTail(path, maxLines, tailReadMaxBytes) // same bounded JSONL tail reader
			g := trace.BuildCallGraph(trace.ParseSpans(lines))
			staged := 0
			if candStore != nil {
				n, err := trace.ProposeAndStage(candStore, now, g, buildServiceResolver(store, clusterID), clusterID, graphVersion)
				if err != nil {
					logger.Error("dgx: stage trace topology candidates failed (non-gating)", "err", err)
				}
				staged = n
			}
			rows := make([]vapi.TraceEdgeRow, 0, len(g.Edges))
			for _, e := range g.Edges {
				rows = append(rows, vapi.TraceEdgeRow{
					Caller: e.Caller, Callee: e.Callee, Calls: e.Calls, Errors: e.Errors,
					P50Millis: e.P50Millis, P95Millis: e.P95Millis, MaxMillis: e.MaxMillis,
				})
			}
			traceView.Store(vapi.NewTraceGraphView(now, g.SpansObserved, g.Traces, g.OrphanSpans, staged, truncated, rows))
			if truncated {
				logger.Warn("dgx: spans file exceeded the read cap; older spans dropped this cycle (extra census incompleteness, surfaced at /api/trace-graph)", "linesRead", len(lines), "maxLines", maxLines)
			}
			if len(g.Edges) > 0 {
				logger.Info("dgx: observed call graph (doc 20 P4 TRACE)", "edges", len(g.Edges), "spans", g.SpansObserved, "orphans", g.OrphanSpans, "candidatesStaged", staged)
			}
		}
	}
}

// buildServiceResolver maps a span's service.name to a workload role CEI by matching it
// against the active inventory's role names (the role key's last segment). main does this
// so the trace core imports neither identity nor client-go. A miss ⇒ ("", false) ⇒ the
// candidate references the raw service name (flagged), never a guessed CEI.
func buildServiceResolver(store *identity.Store, clusterID string) trace.ServiceResolver {
	byName := map[string]string{}
	ambiguous := map[string]bool{}
	if store != nil {
		for _, r := range store.ActiveInstances() {
			if r.RoleCEI.RoleKey == "" {
				continue
			}
			// role key form "Kind/name" → index by the bare name (the OTel service.name).
			i := strings.LastIndex(r.RoleCEI.RoleKey, "/")
			if i < 0 {
				continue
			}
			name := r.RoleCEI.RoleKey[i+1:]
			key := r.RoleCEI.Key()
			// A bare name shared by two DIFFERENT workloads (different Kind/namespace) is
			// ambiguous: don't guess which one the span means. Mark it unresolved — the SAME
			// honest-when-ambiguous discipline the stray-ER ≥2-coordinate floor uses. This is
			// also order-independent (ActiveInstances iterates a Go map in unspecified order).
			if existing, ok := byName[name]; ok && existing != key {
				ambiguous[name] = true
				continue
			}
			byName[name] = key
		}
	}
	return func(service string) (string, bool) {
		if ambiguous[service] {
			return "", false // ambiguous service name → unresolved, never a guessed CEI
		}
		cei, ok := byName[service]
		return cei, ok
	}
}

// logsLoop mines MEASURED log templates from a bounded sample of pod logs each interval
// and publishes /api/log-templates (doc 20 P4). Off the deterministic path.
func logsLoop(ctx context.Context, logger *slog.Logger, client kubernetes.Interface, logsView *atomic.Pointer[vapi.LogTemplatesView], every time.Duration, tail, maxPods int) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			pods := runningPods(ctx, client, maxPods)
			lines, sampled := fetchPodLogLines(ctx, client, pods, int64(tail))
			tmpls := logtmpl.Mine(lines, logtmpl.DefaultParams)
			rows := make([]vapi.LogTemplateRow, 0, len(tmpls))
			for _, tp := range tmpls {
				rows = append(rows, vapi.LogTemplateRow{Pattern: tp.Pattern, Count: tp.Count})
			}
			logsView.Store(vapi.NewLogTemplatesView(now, sampled, len(lines), rows))
			if len(rows) > 0 {
				logger.Info("dgx: mined log templates (doc 20 P4)", "templates", len(rows), "pods", sampled, "lines", len(lines))
			}
		}
	}
}

func runningPods(ctx context.Context, client kubernetes.Interface, maxPods int) []corev1.Pod {
	list, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]corev1.Pod, 0, maxPods)
	for i := range list.Items {
		if list.Items[i].Status.Phase != corev1.PodRunning {
			continue
		}
		out = append(out, list.Items[i])
		if len(out) >= maxPods {
			break
		}
	}
	return out
}

// fetchPodLogLines tails recent log lines from each pod (best-effort; a pod whose logs
// are unreadable is skipped, never fatal). Returns the lines + how many pods were read.
func fetchPodLogLines(ctx context.Context, client kubernetes.Interface, pods []corev1.Pod, tail int64) ([]string, int) {
	var lines []string
	sampled := 0
	for i := range pods {
		req := client.CoreV1().Pods(pods[i].Namespace).GetLogs(pods[i].Name, &corev1.PodLogOptions{TailLines: &tail})
		rc, err := req.Stream(ctx)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		rc.Close()
		sampled++
	}
	return lines, sampled
}

// rollUpTargetsToRole maps pod/container forecast targets to their durable workload ROLE
// (doc 20 P5 churn-stable identity): a target keyed on a single pod UID dies when the pod
// is replaced, so we re-key it on the role CEI (the OwnerReference-derived RoleCEI the
// identity store already tracks) and dedup per (role, container, metric). The role's
// series is then the deterministic per-bin worst-member-toward-bar of its live members
// (RoleSeriesReader — max for an `above` bar, min for `below`, so it stays comparable to
// the per-pod bar; doc 22 C1), so it survives churn without the replica-sum mis-bar that
// would falsely read "already crossed". Node/PVC targets and unresolvable-role targets
// pass through unchanged (honest). Deterministic: output sorted by (CEIKey, Metric).
func rollUpTargetsToRole(targets []forecast.Target, store *identity.Store) []forecast.Target {
	seen := map[string]bool{}
	out := make([]forecast.Target, 0, len(targets))
	for _, t := range targets {
		if t.Entity != "Pod" && t.Entity != "Container" {
			out = append(out, t)
			continue
		}
		rec, ok := store.Get(t.CEIKey)
		if !ok || rec.RoleCEI.RoleKey == "" {
			out = append(out, t) // role unresolvable ⇒ keep per-instance, never guess
			continue
		}
		roleKey := rec.RoleCEI.Key()
		streamUID := roleKey
		if t.Entity == "Container" && t.Container != "" {
			streamUID = roleKey + "\x1f" + t.Container // role + container (the role-stream uid)
		}
		dedup := streamUID + "\x1f" + t.Metric
		if seen[dedup] {
			continue
		}
		seen[dedup] = true
		rt := t
		rt.CEIKey = roleKey
		rt.StreamUID = streamUID
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CEIKey != out[j].CEIKey {
			return out[i].CEIKey < out[j].CEIKey
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

// buildRoleMembers groups the active inventory by role key and returns a resolver from a
// role-stream uid (roleKey, or roleKey+"\x1f"+container) to its member STREAM uids (the
// pod UID, or podUID/container) — the join keys observe.StreamsByUIDMetric matches. main
// does this so the forecast package imports neither identity nor client-go.
func buildRoleMembers(store *identity.Store) forecast.MemberResolver {
	byRole := map[string][]string{}
	for _, r := range store.ActiveInstances() {
		if r.RoleCEI.RoleKey == "" || r.UID == "" {
			continue
		}
		byRole[r.RoleCEI.Key()] = append(byRole[r.RoleCEI.Key()], r.UID)
	}
	return func(roleUID string) []string {
		roleKey, container := roleUID, ""
		if i := strings.Index(roleUID, "\x1f"); i >= 0 {
			roleKey, container = roleUID[:i], roleUID[i+1:]
		}
		members := byRole[roleKey]
		out := make([]string, 0, len(members))
		for _, uid := range members {
			if container != "" {
				out = append(out, uid+"/"+container)
			} else {
				out = append(out, uid)
			}
		}
		return out
	}
}

// buildEntityRefs snapshots the active identity inventory as candidate.EntityRefs for
// the stray-metric ER (doc 20 P1). main does the mapping so the candidate package never
// imports identity.
func buildEntityRefs(store *identity.Store) []candidate.EntityRef {
	inst := store.ActiveInstances()
	refs := make([]candidate.EntityRef, 0, len(inst))
	for _, r := range inst {
		refs = append(refs, candidate.EntityRef{
			Key: r.CEI.Key(), Kind: r.Kind, Namespace: r.Namespace, Name: r.Name, UID: r.UID,
		})
	}
	return refs
}

// dgxResolveLoop drains quarantined strays each interval and stages candidate proposals
// (off the deterministic path; non-gating). It is the first candidate PRODUCER (doc 20
// P1) — it proposes PROVISIONAL nodes + associated-with edges, never authors.
func dgxResolveLoop(ctx context.Context, logger *slog.Logger, in *observe.Ingestor, store *identity.Store, cs *candidate.Store, graphVersion string, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			strays := in.DrainQuarantined()
			if len(strays) == 0 {
				continue
			}
			obs := make([]candidate.StrayObservation, 0, len(strays))
			for _, q := range strays {
				obs = append(obs, candidate.StrayObservation{
					Family: string(q.Family), Metric: q.Metric, Labels: q.Labels, Node: q.Node,
					Reason: q.Reason, StreamRef: q.Metric + "@" + q.Node, GraphVersion: graphVersion,
				})
			}
			staged, err := candidate.ResolveAndStage(cs, time.Now().UTC(), obs, buildEntityRefs(store))
			if err != nil {
				logger.Error("dgx: stage stray candidates failed (non-gating)", "err", err)
				continue
			}
			if staged > 0 {
				logger.Info("dgx: staged stray-metric candidates (doc 20 P1)", "strays", len(strays), "candidates", staged)
			}
		}
	}
}

// mapGovernanceItems projects candidate rows into the governance review surface (doc 20 +
// doc 12 §3.3) — the full provenance a human needs to decide, including the cited evidence
// and (when decided) who decided + their authored note. main maps so api never imports
// internal/candidate.
func mapGovernanceItems(cs []candidate.Candidate, gapAttempts map[string]int) []vapi.GovernanceItem {
	out := make([]vapi.GovernanceItem, 0, len(cs))
	for _, c := range cs {
		ev := make([]vapi.GovernanceEvidence, 0, len(c.Evidence))
		for _, e := range c.Evidence {
			ev = append(ev, vapi.GovernanceEvidence{Kind: e.Kind, Ref: e.Ref, Detail: e.Detail})
		}
		rationale, _ := c.Payload["rationale"].(string)
		// Deterministic MEASURED support (doc 21 §4): a struct of counts, computed in main
		// (api never imports candidate). Recurrence = how many times the agent re-examined this
		// candidate's exploration gap (gap_state attempts, slice 3) — a count, never a confidence.
		sup := candidate.Score(c, gapAttempts[gapIDForCandidate(c)])
		it := vapi.GovernanceItem{
			ID: c.ID, Kind: string(c.Kind), Status: string(c.Status), Subject: c.Subject,
			Relation: c.Relation, Source: c.Lineage.Source, Method: c.Lineage.Method,
			GraphVersion: c.Lineage.GraphVersion, Rationale: rationale, Evidence: ev,
			Support: vapi.GovernanceSupport{
				EvidenceCount: sup.EvidenceCount, CaptureSample: sup.CaptureSample,
				Recurrence: sup.Recurrence, DistinctEntities: sup.DistinctEntities, AgeSeconds: sup.AgeSeconds,
			},
			DecidedBy: c.DecidedBy, Note: c.Note, CreatedAt: c.CreatedAt,
			// A pure k8s object-metadata stray is non-actionable: counted, but kept out of the
			// human review queue (the deterministic classifier owns this; api never imports us).
			Actionable: candidate.StrayCandidateActionable(c.Subject),
		}
		if !c.DecidedAt.IsZero() {
			t := c.DecidedAt
			it.DecidedAt = &t
		}
		// PROJECTED agent enrichment (doc 21 Phase 4 §C): surface the model's suggested label +
		// description as a HINT. It is discarded at promotion (never AUTHORED) — the UI labels it.
		if c.Suggestion != nil {
			it.SuggestedLabel = c.Suggestion.Label
			it.SuggestedDescription = c.Suggestion.Description
			it.SuggestedSeverity = c.Suggestion.Severity
			it.SuggestedBy = c.Suggestion.Model
		}
		out = append(out, it)
	}
	return out
}

// decideGovernance records a NAMED HUMAN's promote/reject decision and, on a promotion,
// renders the committable AUTHORED overlay artifact (doc 12 §3.3 — the human owns the call;
// the system never approves). It never mutates the released graph (the firewall holds).
func decideGovernance(cs *candidate.Store, graphVersion string, logger *slog.Logger, gapBase time.Duration, req vapi.GovernanceDecisionRequest) vapi.GovernanceDecisionResult {
	var st candidate.Status
	switch req.Decision {
	case "promote":
		st = candidate.StatusPromoted
	case "reject":
		st = candidate.StatusRejected
	default:
		return vapi.GovernanceDecisionResult{OK: false, CandidateID: req.CandidateID, Message: "decision must be 'promote' or 'reject'"}
	}
	now := time.Now().UTC()
	if err := cs.Decide(now, req.CandidateID, st, req.DecidedBy, req.Note); err != nil {
		return vapi.GovernanceDecisionResult{OK: false, CandidateID: req.CandidateID, Message: err.Error()}
	}
	res := vapi.GovernanceDecisionResult{OK: true, CandidateID: req.CandidateID, Status: string(st)}
	if st == candidate.StatusPromoted {
		if c, found, err := cs.Get(req.CandidateID); err == nil && found {
			if y, err := candidate.PromotedOverlayYAML(*c, graphVersion); err == nil {
				res.OverlayYAML = y
			}
		}
		res.Message = "promoted by " + req.DecidedBy + " — commit the overlay artifact through the release governance gate (it does not auto-load)"
		logger.Info("governance: candidate promoted (doc 12 §3.3 — named human owns the call)", "id", req.CandidateID, "by", req.DecidedBy)
	} else {
		// slice 3: a named human's rejection lengthens this candidate's exploration-gap backoff,
		// so the agent does not re-surface the same rejected gap every sweep. Best-effort + non-gating.
		if gapBase > 0 {
			if c, found, gerr := cs.Get(req.CandidateID); gerr == nil && found {
				if gap := gapIDForCandidate(*c); gap != "" {
					if e := cs.RecordGapRejection(now, gap, "human rejected: "+truncStr(req.Note, 80), gapBase, candidate.GapBackoffCap); e != nil {
						logger.Warn("dgx gap-state: record rejection failed (non-gating)", "gap", gap, "err", e)
					}
				}
			}
		}
		res.Message = "rejected by " + req.DecidedBy
		logger.Info("governance: candidate rejected", "id", req.CandidateID, "by", req.DecidedBy)
	}
	return res
}

// previewAuthorPlaceholder / previewNotePlaceholder stand in for the named human's
// attribution while PREVIEWING a still-pending candidate's overlay. They are clearly
// non-authoritative: the real author + rationale are supplied only at promotion (the human
// authors the note; the model's rationale is discarded). They exist only so the overlay
// renderer — which rightly REQUIRES an author + note on a real promotion — can render the
// structure for a read-only look.
const (
	previewAuthorPlaceholder = "‹you — the named human authors this at promotion›"
	previewNotePlaceholder   = "‹you author the rationale at promotion (the model's is discarded)›"
)

// previewGovernance renders a READ-ONLY preview of what promoting a candidate would author
// (doc 21 §4, Phase 3 slice 2): the exact overlay artifact, and — for an equivalence-group
// candidate — the deterministic stray→group RESOLUTION delta computed on a scratch graph. It
// NEVER mutates the candidate store or the live graph (it works on a COPY of the candidate
// and a scratch copy of the graph's equivalence groups). The api never imports candidate, so
// main does the parsing and hands governance plain inputs.
func previewGovernance(cs *candidate.Store, g *graph.Graph, graphVersion, candidateID string) *vapi.GovernancePreviewResult {
	c, found, err := cs.Get(candidateID)
	if err != nil {
		return &vapi.GovernancePreviewResult{OK: false, CandidateID: candidateID, Message: "lookup failed: " + err.Error()}
	}
	if !found {
		return &vapi.GovernancePreviewResult{OK: false, CandidateID: candidateID, Message: "no such candidate"}
	}
	res := &vapi.GovernancePreviewResult{OK: true, CandidateID: candidateID, Kind: string(c.Kind)}

	// Render the EXACT overlay a promotion would author, on a COPY (the store row is untouched).
	// Author + note are PLACEHOLDERS the named human fills at promotion — the overlay renderer
	// rightly requires them, but a preview is not an authored act.
	preview := *c
	preview.Status = candidate.StatusPromoted
	if preview.DecidedBy == "" {
		preview.DecidedBy = previewAuthorPlaceholder
	}
	if strings.TrimSpace(preview.Note) == "" {
		preview.Note = previewNotePlaceholder
	}
	if y, yerr := candidate.PromotedOverlayYAML(preview, graphVersion); yerr == nil {
		res.OverlayYAML = y
	} else {
		res.Message = "overlay preview unavailable: " + yerr.Error()
	}

	if c.Kind == candidate.KindEquivGroup {
		res.MovesCoverage = true
		res.Caveat = "Promoting authors a dialect regex into the equivalence group — the metric (and the others the " +
			"pattern captures) stop being strays and resolve to the canonical variable. This is the one promotion that " +
			"moves MEASURED coverage (doc 21 §5). Full per-phenomenon observability impact appears in the binding diff after you commit + reload."
		if prop, perr := candidate.ParseEquivGroupPayload(c.Payload); perr == nil {
			in := governance.EquivGroupPreviewInput{Pattern: prop.Pattern}
			if prop.GroupID != "" {
				in.TargetGroupID = prop.GroupID
			} else if prop.NewGroup != nil {
				in.NewGroupID = prop.NewGroup.ID
				in.NewCanonical = prop.NewGroup.CanonicalOTel
				in.NewLabel = prop.NewGroup.Label
			}
			// Scope = the focal metric + the strays the pattern captured at proposal time.
			in.ScopeMetrics = append([]string{prop.Metric}, prop.CaptureSample...)
			if pv, gerr := governance.PreviewEquivGroupPromotion(g, in); gerr == nil {
				res.Equiv = &vapi.GovernanceEquivPreview{
					GroupID: pv.GroupID, DefinesNewGroup: pv.DefinesNewGroup, Canonical: in.NewCanonical,
					Pattern: pv.Pattern, NewlyResolved: pv.NewlyResolved,
					AlreadyResolved: pv.AlreadyResolved, StillUnresolved: pv.StillUnresolved,
				}
			} else {
				// Honest partial coverage: APPEND, never overwrite — if the overlay render also
				// failed (res.Message already set), surface BOTH so no failure is masked.
				if res.Message == "" {
					res.Message = "coverage preview unavailable: " + gerr.Error()
				} else {
					res.Message += "; also coverage preview unavailable: " + gerr.Error()
				}
			}
		}
	} else if c.Kind == candidate.KindPhenomenonCandidate {
		// A phenomenon candidate authors a CorrelationGroup NODE (a starting skeleton). No detection
		// condition is implied — the human authors the member roles + a check + a DECLARED bar before
		// it can ever fire, so promotion alone moves NO MEASURED coverage.
		res.Caveat = "Promoting authors a candidate phenomenon NODE (a CorrelationGroup skeleton) from this recurring " +
			"anomaly. It implies NO detection: you author the member roles, a detection check, and a DECLARED bar before it " +
			"can fire. On its own it moves no MEASURED coverage — it opens a phenomenon for you to curate. The system proposes; it never authors detection."
	} else {
		// Every other promotion authors a graph RELATIONSHIP (identity/topology/co-occurrence);
		// it never binds a metric to a bar, so it does not move MEASURED coverage on its own.
		res.Caveat = "Promoting authors a graph relationship (identity / topology / co-occurrence). It does NOT bind any " +
			"metric to a bar, so on its own it does not move MEASURED coverage or make any metric watched — that needs a separate authored detection rule."
	}
	return res
}

// mapCandidateRows projects the DGX candidate store's rows into the surfacing row
// type (doc 20 P0.b). main does the mapping so the api package never imports
// internal/candidate — keeping the read-firewall intact.
func mapCandidateRows(cs []candidate.Candidate) []vapi.CandidateRow {
	out := make([]vapi.CandidateRow, 0, len(cs))
	for _, c := range cs {
		out = append(out, vapi.CandidateRow{
			ID:            c.ID,
			Kind:          string(c.Kind),
			Status:        string(c.Status),
			Subject:       c.Subject,
			Relation:      c.Relation,
			Source:        c.Lineage.Source,
			Method:        c.Lineage.Method,
			GraphVersion:  c.Lineage.GraphVersion,
			EvidenceCount: len(c.Evidence),
			Reason:        c.Reason,
			CreatedAt:     c.CreatedAt,
			UpdatedAt:     c.UpdatedAt,
		})
	}
	return out
}

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
