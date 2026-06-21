package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/notify"
	fstore "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

// notifyMapConfig holds the DECLARED knobs the where/when mapping reads (docs/30): the
// P1/P2 forecast lead split, the finding-staleness horizon, and the console base URL.
// None is learned.
type notifyMapConfig struct {
	shortLead  time.Duration // forecast crossing sooner than this ⇒ P1, else P2
	staleAfter time.Duration // a finding last matched longer ago than this is resolved, not firing
	consoleURL string        // base URL for deep links, or ""
}

// oomCrashPhenomena are the MEASURED phenomena that warrant a P1 mail (docs/30 §when).
var oomCrashPhenomena = map[string]bool{
	"PHEN_OOM_KILL_CGROUP":        true,
	"PHEN_OOM_KILL_SYSTEM":        true,
	"PHEN_PROBE_FAILURE_RESTART":  true,
	"PHEN_INIT_CONTAINER_FAILURE": true,
}

// oomCrashReasons are the k8s Event reasons that map to the same P1 bucket.
var oomCrashReasons = map[string]bool{
	"OOMKilled":        true,
	"CrashLoopBackOff": true,
	"BackOff":          true,
}

// notifyConfigFromEnv assembles the alert policy from env. Returns ok=false (with a
// clear log) when the mandatory secrets/recipients are absent — the lane then stays
// idle rather than crashing. The App Password is read here and never logged.
func notifyConfigFromEnv(logger *slog.Logger) (cfg notify.Config, user, pass, host string, m notifyMapConfig, interval time.Duration, ok bool) {
	user = strings.TrimSpace(os.Getenv("ALERT_SMTP_USER"))
	pass = strings.ReplaceAll(os.Getenv("ALERT_SMTP_PASSWORD"), " ", "") // Gmail prints the app pw in groups of 4
	host = strings.TrimSpace(os.Getenv("ALERT_SMTP_HOST"))
	to := splitComma(os.Getenv("ALERT_TO"))
	if user == "" || pass == "" || len(to) == 0 {
		logger.Error("alerts: --alerts-enabled needs ALERT_SMTP_USER, ALERT_SMTP_PASSWORD, ALERT_TO (comma-separated); lane idle",
			"haveUser", user != "", "havePass", pass != "", "recipients", len(to))
		return cfg, "", "", "", m, 0, false
	}
	cfg.To = to
	cfg.Cooldown = envDuration("ALERT_COOLDOWN", 30*time.Minute)
	cfg.RatePerWindow = envInt("ALERT_RATE_PER_WINDOW", 10)
	cfg.RateWindow = envDuration("ALERT_RATE_WINDOW", time.Hour)
	cfg.QuietP1Breakthrough = envBool("ALERT_QUIET_P1", true)
	cfg.QuietStartHour, cfg.QuietEndHour = parseQuietHours(os.Getenv("ALERT_QUIET_HOURS"))
	cfg.QuietLocation = time.Local
	cfg.ConsoleURL = strings.TrimRight(strings.TrimSpace(os.Getenv("ALERT_CONSOLE_URL")), "/")

	m = notifyMapConfig{
		shortLead:  envDuration("ALERT_SHORT_LEAD", 30*time.Minute),
		staleAfter: envDuration("ALERT_FINDING_STALE_AFTER", 90*time.Second),
		consoleURL: cfg.ConsoleURL,
	}
	interval = envDuration("ALERT_INTERVAL", 30*time.Second)
	return cfg, user, pass, host, m, interval, true
}

// notifyLoop is the off-digest alert lane (docs/30). Each interval it reads the CLASSED
// views the inventory/forecast loops already published, maps the new facts to alerts, and
// hands them to the dispatcher (which applies the fatigue controls + sends). NON-GATING: a
// send failure is logged and retried next tick; nothing here ever blocks detection.
func notifyLoop(ctx context.Context, logger *slog.Logger, disp *notify.Dispatcher, m notifyMapConfig,
	warningsView *atomic.Pointer[vapi.WarningsView], transitiveChainView *atomic.Pointer[[]flow.Chain],
	crossSvcView *atomic.Pointer[flow.Chain], eventsView *atomic.Pointer[vapi.EventsView],
	findingsStore *fstore.Store, interval time.Duration) {

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now().UTC()
			var warnings *vapi.WarningsView
			if warningsView != nil {
				warnings = warningsView.Load()
			}
			var transitive []flow.Chain
			if transitiveChainView != nil {
				if p := transitiveChainView.Load(); p != nil {
					transitive = *p
				}
			}
			var crossSvc *flow.Chain
			if crossSvcView != nil {
				crossSvc = crossSvcView.Load()
			}
			var events *vapi.EventsView
			if eventsView != nil {
				events = eventsView.Load()
			}
			var findings []fstore.FindingRow
			if findingsStore != nil {
				if fs, err := findingsStore.ActiveFindings(200); err == nil {
					findings = fs
				}
			}
			alerts := notifyBuildAlerts(now, m, warnings, transitive, crossSvc, events, findings)
			if len(alerts) == 0 {
				continue
			}
			res, err := disp.Dispatch(ctx, now, alerts)
			if err != nil {
				logger.Warn("alert dispatch: send failed (non-gating, retries next tick)", "err", err)
				continue
			}
			if res.Sent {
				logger.Info("alerts emailed", "selected", res.Selected, "suppressed", res.Suppressed)
			}
		}
	}
}

// notifyBuildAlerts is the where/when matrix: it maps the per-tick CLASSED views to a
// deduplicated []notify.Alert, citing each clause's provenance class and copying any
// authored "why" VERBATIM (it never paraphrases or fabricates a reason). Order:
// cascade chains, OOM/crash findings, OOM/crash events, forecast crossings; later
// duplicates of an already-seen dedup key are dropped (one fact = one alert).
func notifyBuildAlerts(now time.Time, m notifyMapConfig, warnings *vapi.WarningsView,
	transitive []flow.Chain, crossSvc *flow.Chain, events *vapi.EventsView, findings []fstore.FindingRow) []notify.Alert {

	var out []notify.Alert
	seen := map[string]bool{}
	add := func(a notify.Alert, ok bool) {
		if !ok || a.DedupKey == "" || seen[a.DedupKey] {
			return
		}
		seen[a.DedupKey] = true
		a.At = now
		out = append(out, a)
	}

	// 1. Cascade chains (P1) — MEASURED degraded workloads stitched over the AUTHORED relation.
	chains := append([]flow.Chain(nil), transitive...)
	if crossSvc != nil {
		chains = append(chains, *crossSvc)
	}
	for _, c := range chains {
		add(alertFromChain(c, m.consoleURL))
	}

	// 2. OOM/crash findings (P1) — the authoritative MEASURED phenomenon match, non-stale.
	for i := range findings {
		f := findings[i]
		if !oomCrashPhenomena[f.Phenomenon] {
			continue
		}
		f.MarkFreshness(now, m.staleAfter)
		if f.Stale {
			continue // last matched long ago ⇒ resolved, not firing now
		}
		add(alertFromOOMFinding(f, m.consoleURL))
	}

	// 3. OOM/crash events (P1) — the discrete k8s Event; collapses onto a finding for the
	//    same workload via the shared dedup bucket (ns/name + oom|crash), so one OOM = one mail.
	if events != nil && events.Available {
		for _, e := range events.Events {
			if !oomCrashReasons[e.Reason] {
				continue
			}
			add(alertFromOOMEvent(e, m.consoleURL))
		}
	}

	// 4. Forecast crossings (PROJECTED) — only when the early-warning lane is enabled (its
	//    gate passed). Short lead ⇒ P1, comfortable lead or open band ⇒ P2.
	if warnings != nil && warnings.Enabled {
		for _, w := range warnings.Warnings {
			add(alertFromForecast(w, m.shortLead, m.consoleURL))
		}
	}
	return out
}

// alertFromChain maps a flow.Chain to a P1 cascade alert. The headline + flow edges are
// MEASURED scaffolding (no causal verb); the relation "why" is AUTHORED, quoted verbatim.
func alertFromChain(c flow.Chain, consoleURL string) (notify.Alert, bool) {
	if len(c.Links) == 0 && len(c.Path) == 0 {
		return notify.Alert{}, false // no asserted edge ⇒ not a chain
	}
	nodes := chainNodes(c)
	root := c.MostUpstreamDegradedNode
	downstream := ""
	if n := len(c.Path); n > 0 {
		downstream = c.Path[n-1].Downstream
	} else if len(c.Links) > 0 {
		downstream = c.Links[0].Impacted
	}
	headline := "Degradation cascade: " + root
	if downstream != "" && downstream != root {
		headline += " → " + downstream
	}

	var lines []notify.Line
	// MEASURED: the observed-flow edges (ordered transitive path, else the one-hop fan-in).
	if len(c.Path) > 0 {
		for _, p := range c.Path {
			lines = append(lines, notify.Line{Class: notify.ClassMeasured,
				Text: fmt.Sprintf("observed flow %s → %s (%s); %s=%s, %s=%s",
					p.Upstream, p.Downstream, p.EdgeTraversal, p.Upstream, p.UpstreamPhenomenon, p.Downstream, p.DownstreamPhenomenon)})
		}
	} else {
		for _, l := range c.Links {
			lines = append(lines, notify.Line{Class: notify.ClassMeasured,
				Text: fmt.Sprintf("observed flow %s → %s (%s)", l.Degraded, l.Impacted, l.EdgeTraversal)})
		}
	}
	// MEASURED: each node's own symptom.
	for i, s := range c.Symptoms {
		if i >= 6 {
			lines = append(lines, notify.Line{Class: notify.ClassMeasured, Text: fmt.Sprintf("… +%d more symptoms", len(c.Symptoms)-i)})
			break
		}
		txt := s.Workload + ": " + s.Phenomenon
		if s.Detail != "" {
			txt += " (" + s.Detail + ")"
		}
		lines = append(lines, notify.Line{Class: notify.ClassMeasured, Text: txt})
	}
	// AUTHORED: the curated relation note(s), verbatim, de-duplicated.
	for _, why := range uniqueWhys(c) {
		lines = append(lines, notify.Line{Class: notify.ClassAuthored, Text: why})
	}

	return notify.Alert{
		Priority: notify.P1, Kind: "cascade-chain", Headline: headline, Entity: root,
		DedupKey: "cascade|" + strings.Join(nodes, ","),
		Lines:    lines, ConsoleLink: link(consoleURL, "/root-cause"),
	}, true
}

// alertFromOOMFinding maps an OOM/crash MEASURED finding to a P1 alert.
func alertFromOOMFinding(f fstore.FindingRow, consoleURL string) (notify.Alert, bool) {
	wl := f.Namespace + "/" + f.Name
	txt := fmt.Sprintf("%s on %s (%s); quality %s, completeness %.0f%% (%d/%d required members observed)",
		f.Label, wl, f.Kind, f.Quality, f.Completeness*100, f.RequiredMet, f.RequiredTotal)
	lines := []notify.Line{{Class: notify.ClassMeasured, Text: txt}}
	if f.Quality == "degraded" && f.RequiredUnobserved > 0 {
		lines = append(lines, notify.Line{Class: notify.ClassMeasured,
			Text: fmt.Sprintf("degraded match: %d required member(s) unobservable — surfaced, never fabricated", f.RequiredUnobserved)})
	}
	return notify.Alert{
		Priority: notify.P1, Kind: "oom-crash", Headline: f.Label + " on " + f.Name, Entity: wl,
		DedupKey: "oomcrash|" + wl + "|" + oomBucket(f.Phenomenon),
		Lines:    lines, ConsoleLink: link(consoleURL, "/incidents"),
	}, true
}

// alertFromOOMEvent maps a discrete OOM/crash k8s Event to a P1 alert (collapses onto a
// finding for the same workload via the shared bucket key).
func alertFromOOMEvent(e vapi.EventCard, consoleURL string) (notify.Alert, bool) {
	wl := e.Namespace + "/" + e.Name
	lines := []notify.Line{{Class: notify.ClassMeasured,
		Text: fmt.Sprintf("k8s event %s ×%d on %s (%s)", e.Reason, e.Count, wl, e.Kind)}}
	if e.Corroborated && e.CorroborationWhy != "" {
		lines = append(lines, notify.Line{Class: notify.ClassAuthored, Text: e.CorroborationWhy})
	}
	return notify.Alert{
		Priority: notify.P1, Kind: "oom-crash", Headline: e.Reason + " on " + e.Name, Entity: wl,
		DedupKey: "oomcrash|" + wl + "|" + oomBucket(e.Reason),
		Lines:    lines, ConsoleLink: link(consoleURL, "/events"),
	}, true
}

// alertFromForecast maps a PROJECTED early-warning card to a P1 (short lead) or P2
// (comfortable lead / open band) alert. Register: "projected to cross", never "will".
func alertFromForecast(w vapi.WarningCard, shortLead time.Duration, consoleURL string) (notify.Alert, bool) {
	wl := w.Namespace + "/" + w.Name
	if wl == "/" {
		wl = w.EntityCEI
	}
	prio := notify.P2
	if !w.LatestBeyondHorizon && w.TimeToCrossSeconds < shortLead.Seconds() {
		prio = notify.P1 // imminent or short-lead crossing
	}
	latest := "open (beyond horizon)"
	if !w.LatestBeyondHorizon {
		latest = w.LatestAt.UTC().Format(time.RFC3339)
	}
	lines := []notify.Line{{Class: notify.ClassProjected,
		Text: fmt.Sprintf("%s projected to cross %s%s at %s; band [%s, %s], confidence %s, lead %s",
			w.Metric, trimFloat(w.BarValue), unitSuffix(w.BarUnit), w.CrossAt.UTC().Format(time.RFC3339),
			w.EarliestAt.UTC().Format(time.RFC3339), latest, orDash(w.Confidence), humanizeLead(w.TimeToCrossSeconds))}}
	// Graph-authored precursors — JOIN never FUSE: a MEASURED framing line, then the authored
	// phenomenon ids VERBATIM on their own AUTHORED line (the framing is system text; the ids
	// are cited, never paraphrased; the line's class never over-claims its weakest input).
	if len(w.PrecursorPhenomena) > 0 {
		lines = append(lines, notify.Line{Class: notify.ClassMeasured, Text: "graph-declared precursor phenomena for this metric (cited below):"})
		lines = append(lines, notify.Line{Class: notify.ClassAuthored, Text: strings.Join(w.PrecursorPhenomena, ", ")})
	}
	// Blast radius — the authored relation walked over MEASURED topology: a MEASURED framing
	// line, then the curator's note VERBATIM on its own AUTHORED line. Never fused.
	for i, r := range w.AtRisk {
		if i >= 4 {
			break
		}
		lines = append(lines, notify.Line{Class: notify.ClassMeasured,
			Text: fmt.Sprintf("blast radius: %s at risk on %s (authored relation over measured topology)", r.Phenomenon, r.CEIKey)})
		if r.Why != "" {
			lines = append(lines, notify.Line{Class: notify.ClassAuthored, Text: r.Why})
		}
	}
	return notify.Alert{
		Priority: prio, Kind: "forecast-crossing",
		Headline: "Forecast crossing (PROJECTED): " + w.Metric + " on " + w.Name, Entity: wl,
		DedupKey: "forecast|" + w.EntityCEI + "|" + w.Metric,
		Lines:    lines, ConsoleLink: link(consoleURL, "/forecast"),
	}, true
}

// --- small helpers ---

func chainNodes(c flow.Chain) []string {
	set := map[string]bool{}
	add := func(s string) {
		if s != "" {
			set[s] = true
		}
	}
	add(c.MostUpstreamDegradedNode)
	for _, l := range c.Links {
		add(l.Impacted)
		add(l.Degraded)
	}
	for _, p := range c.Path {
		add(p.Upstream)
		add(p.Downstream)
	}
	for _, s := range c.Symptoms {
		add(s.Workload)
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func uniqueWhys(c flow.Chain) []string {
	seen := map[string]bool{}
	var out []string
	addWhy := func(w string) {
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	for _, l := range c.Links {
		addWhy(l.Why)
	}
	for _, p := range c.Path {
		addWhy(p.Why)
	}
	return out
}

// oomBucket collapses the distinct OOM/crash phenomena + event reasons into the two
// dedup buckets so a finding and its event for the same workload coalesce.
func oomBucket(phenomenonOrReason string) string {
	s := strings.ToUpper(phenomenonOrReason)
	if strings.Contains(s, "OOM") {
		return "oom"
	}
	return "crash" // PROBE_FAILURE_RESTART, INIT_CONTAINER_FAILURE, CrashLoopBackOff, BackOff
}

func link(base, path string) string {
	if base == "" {
		return ""
	}
	return base + path
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// parseQuietHours reads "START-END" 24h hours (e.g. "22-7"); empty/invalid ⇒ (0,0) = off.
func parseQuietHours(s string) (start, end int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	a, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	b, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || a < 0 || a > 23 || b < 0 || b > 23 {
		return 0, 0
	}
	return a, b
}

func humanizeLead(seconds float64) string {
	if seconds <= 0 {
		return "imminent"
	}
	return (time.Duration(seconds) * time.Second).Round(time.Minute).String()
}

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func unitSuffix(u string) string {
	if u == "" {
		return ""
	}
	return " " + u
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
