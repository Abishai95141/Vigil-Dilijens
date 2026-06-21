package main

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// eventsSnapshot is one collected, role-resolved batch of discrete-event findings,
// published atomically for the eval tick to JOIN against the gauge findings. Off the
// deterministic path entirely.
type eventsSnapshot struct {
	findings    []events.EventFinding
	collectedAt time.Time
	resolved    int // events resolved to a workload role
	unresolved  int // events kept on the instance key (no role) — honest, never guessed
}

// runEventCollector is the v3 T-C discrete-event lane (doc 03 join by CEI): it lists
// Kubernetes Events filtered to the AUTHORED reasons, resolves each event's involved
// object to its durable role CEI via the identity store (the same resolution the flow
// collector and incident wiring use), and publishes a typed MEASURED EventFinding
// snapshot. Events are a DISTINCT finding source — they never pass through the
// fingerprint matcher and never enter the digest.
//
// Non-gating: this runs ONLY when --events-enabled. With it off, no events are
// ingested, no snapshot is published, and obsd is byte-identical to before. Even on,
// the snapshot rides OFF the digest (the digest hashes fingerprints/findings/
// cascades/unexplained only), so a captured bundle replays byte-identically.
func runEventCollector(ctx context.Context, logger *slog.Logger, gate *sync.RWMutex,
	client kubernetes.Interface, store *identity.Store, clusterID string,
	conds []events.Corroboration, snap *atomic.Pointer[eventsSnapshot], interval time.Duration) {

	reasons := reasonKindSet(conds)
	// A pod's lastState.terminated persists indefinitely (until the pod is recreated),
	// so a long-recovered OOM would otherwise surface as a "current" degraded finding
	// for hours/days — violating the charter's "report what is happening NOW". Only
	// surface a terminated-state finding while the termination is recent. The Events
	// stream self-limits (k8s GCs Events after ~1h) and the Waiting/CrashLoopBackOff
	// path reads the CURRENT state, so both are unaffected; this bounds the historical
	// terminated path. Operator-tunable; default keeps a recent OOM/crash visible.
	recency := envDuration("EVENT_TERMINATION_RECENCY", 15*time.Minute)
	t := time.NewTicker(interval)
	defer t.Stop()
	logger.Info("event collector started (v3 T-C)", "interval", interval.String(),
		"reasons", events.Reasons(conds), "termination_recency", recency.String())
	collectEventsOnce(ctx, logger, gate, client, store, clusterID, reasons, snap, recency) // render once immediately
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			collectEventsOnce(ctx, logger, gate, client, store, clusterID, reasons, snap, recency)
		}
	}
}

// reasonKindSet is the authored (reason, involved-kind) filter: we ingest only the
// reasons the conditions cover, on the kinds they cover — never the whole firehose.
func reasonKindSet(conds []events.Corroboration) map[string]bool {
	out := make(map[string]bool, len(conds))
	for _, c := range conds {
		out[c.Reason+"\x00"+c.InvolvedKind] = true
	}
	return out
}

func collectEventsOnce(ctx context.Context, logger *slog.Logger, gate *sync.RWMutex,
	client kubernetes.Interface, store *identity.Store, clusterID string,
	reasons map[string]bool, snap *atomic.Pointer[eventsSnapshot], recency time.Duration) {

	now := time.Now().UTC()

	// Two discrete-fact sources, both read from the API server (network, no lock):
	//   1. The Events stream (CoreV1().Events) — kubelet/control-plane Event objects.
	//   2. Pod containerStatuses — the AUTHORITATIVE representation of OOMKilled and
	//      CrashLoopBackOff. On kind, an OOM emits NO "OOMKilled" Event at all (the
	//      event reasons are BackOff/Started/…); the fact lives ONLY in
	//      lastState.terminated.reason == "OOMKilled" (exitCode 137). Sourcing pod
	//      status is therefore not a proxy — it is the canonical signal, and the one
	//      that actually closes the cAdvisor blind spot this lane exists for.
	// Each source is best-effort: one failing never blanks the other's findings.
	el, evErr := client.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	pl, podErr := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if evErr != nil {
		logger.Warn("event collector: list events (continuing with pod status)", "err", evErr)
	}
	if podErr != nil {
		logger.Warn("event collector: list pods (continuing with events)", "err", podErr)
	}
	if evErr != nil && podErr != nil {
		return // both sources down: keep the prior snapshot rather than blank it
	}

	// Role resolution reads the identity store (independently locked); the outer gate is
	// held read-side for symmetry with the scrape path, never during the network lists.
	gate.RLock()
	byKey := make(map[string]events.EventFinding)
	put := func(ef events.EventFinding) {
		k := ef.EntityCEI + "\x00" + ef.Reason + "\x00" + ef.Kind
		if prior, ok := byKey[k]; ok && prior.LastTimestamp.After(ef.LastTimestamp) && prior.Count >= ef.Count {
			return // keep the most recent / highest-count occurrence
		}
		byKey[k] = ef
	}
	resolve := func(ns, name, kind, uid string) (string, string, bool) {
		return events.ResolveEventRole(store, clusterID, events.Involved{Namespace: ns, Name: name, Kind: kind, UID: uid})
	}
	// Source 1: the Events stream.
	if el != nil {
		for i := range el.Items {
			ev := &el.Items[i]
			io := ev.InvolvedObject
			if !reasons[ev.Reason+"\x00"+io.Kind] {
				continue
			}
			entity, role, unresolved := resolve(io.Namespace, io.Name, io.Kind, string(io.UID))
			first, last := eventTimes(ev)
			put(events.EventFinding{
				Reason: ev.Reason, EntityCEI: entity, RoleCEI: role, RoleUnresolved: unresolved,
				Namespace: io.Namespace, Name: io.Name, Kind: io.Kind, Count: eventCount(ev),
				FirstTimestamp: first, LastTimestamp: last,
			})
		}
	}
	// Source 2: pod containerStatuses — terminated.reason (OOMKilled, …) and
	// waiting.reason (CrashLoopBackOff, …). The authoritative, reliable source.
	if pl != nil {
		for i := range pl.Items {
			p := &pl.Items[i]
			for j := range p.Status.ContainerStatuses {
				cs := &p.Status.ContainerStatuses[j]
				// Recency gate on the HISTORICAL terminated path: lastState.terminated
				// persists for the life of the pod, so a termination older than the window
				// is not "happening now" (the container has been running since) and must not
				// surface as a live finding. A zero/unset FinishedAt is treated as not-recent.
				// The Waiting/CrashLoopBackOff check below reads the CURRENT state and is kept
				// independent (no early continue), so a recovered OOM and a live crash-loop on
				// the same container are judged separately.
				if t := cs.LastTerminationState.Terminated; t != nil && reasons[t.Reason+"\x00Pod"] {
					if fin := t.FinishedAt.Time.UTC(); !fin.IsZero() && now.Sub(fin) <= recency {
						entity, role, unresolved := resolve(p.Namespace, p.Name, "Pod", string(p.UID))
						put(events.EventFinding{
							Reason: t.Reason, EntityCEI: entity, RoleCEI: role, RoleUnresolved: unresolved,
							Namespace: p.Namespace, Name: p.Name, Kind: "Pod", Count: max1(cs.RestartCount),
							FirstTimestamp: t.StartedAt.Time.UTC(), LastTimestamp: fin,
						})
					}
				}
				if w := cs.State.Waiting; w != nil && reasons[w.Reason+"\x00Pod"] {
					entity, role, unresolved := resolve(p.Namespace, p.Name, "Pod", string(p.UID))
					last := time.Time{}
					if t := cs.LastTerminationState.Terminated; t != nil {
						last = t.FinishedAt.Time.UTC()
					}
					put(events.EventFinding{
						Reason: w.Reason, EntityCEI: entity, RoleCEI: role, RoleUnresolved: unresolved,
						Namespace: p.Namespace, Name: p.Name, Kind: "Pod", Count: max1(cs.RestartCount),
						LastTimestamp: last,
					})
				}
			}
		}
	}
	gate.RUnlock()

	findings := make([]events.EventFinding, 0, len(byKey))
	resolved, unresolved := 0, 0
	for _, ef := range byKey {
		findings = append(findings, ef)
		if ef.RoleUnresolved {
			unresolved++
		} else {
			resolved++
		}
	}
	// Deterministic order so the published snapshot is stable across collections with
	// the same inputs (mirrors the corroboration join's sort).
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		if a.RoleCEI != b.RoleCEI {
			return a.RoleCEI < b.RoleCEI
		}
		return a.EntityCEI < b.EntityCEI
	})
	snap.Store(&eventsSnapshot{
		findings: findings, collectedAt: now, resolved: resolved, unresolved: unresolved,
	})
	logger.Info("event collector: discrete events ingested (v3 T-C)",
		"events", len(findings), "role_resolved", resolved, "role_unresolved", unresolved)
}

// max1 clamps a restart count to at least 1 (a container in a terminated/waiting
// state has occurred at least once even if restartCount has not yet ticked).
func max1(n int32) int32 {
	if n < 1 {
		return 1
	}
	return n
}

// eventCount returns the Event's occurrence count, defaulting to 1 (the new events
// API may carry the count in Series; a single occurrence has Count 0 in some clients).
func eventCount(ev *corev1.Event) int32 {
	if ev.Series != nil && ev.Series.Count > 0 {
		return ev.Series.Count
	}
	if ev.Count > 0 {
		return ev.Count
	}
	return 1
}

// eventTimes returns (first, last) occurrence times in UTC, robust to the two Event
// schemas: the legacy First/LastTimestamp and the newer EventTime/Series.LastObserved.
func eventTimes(ev *corev1.Event) (time.Time, time.Time) {
	first := firstNonZero(ev.FirstTimestamp.Time, ev.EventTime.Time, ev.LastTimestamp.Time)
	last := ev.LastTimestamp.Time
	if ev.Series != nil && !ev.Series.LastObservedTime.Time.IsZero() {
		last = ev.Series.LastObservedTime.Time
	}
	last = firstNonZero(last, ev.EventTime.Time, ev.FirstTimestamp.Time)
	return first.UTC(), last.UTC()
}

func firstNonZero(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}
