package identity

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Self-observability of the identity layer (doc 03 §6, techstack §12): a Prometheus
// collector that publishes the layer's health signals — join accuracy, orphan and
// quarantine rates, edge staleness distribution per type, tombstone tiers, and the
// Phase-0a exit-gate verdict (mis-joins MUST be zero). The collector reads the live
// stores on each scrape, so the numbers are always current.

const metricNS = "vigil_identity"

// Collector implements prometheus.Collector over the identity stores and the live
// join-consistency audit.
type Collector struct {
	store          *Store
	edges          *EdgeStore
	auditor        *Auditor
	consistency    func() ConsistencyReport // live audit; may be nil (e.g. no cluster)
	targetCoverage float64

	active        *prometheus.Desc
	tombstones    *prometheus.Desc
	discovered    *prometheus.Desc
	terminated    *prometheus.Desc
	successions   *prometheus.Desc
	degradedJoins *prometheus.Desc
	evicted       *prometheus.Desc

	edgeStatus     *prometheus.Desc
	edgeAsserts    *prometheus.Desc
	edgeConfirms   *prometheus.Desc
	edgeRetracts   *prometheus.Desc
	edgeReasserts  *prometheus.Desc
	edgeSuspectTrv *prometheus.Desc
	edgeAbsentTrv  *prometheus.Desc

	streamsResolved    *prometheus.Desc
	streamsDropped     *prometheus.Desc
	streamsQuarantined *prometheus.Desc
	quarantineByReason *prometheus.Desc
	orphanRate         *prometheus.Desc
	streamJoinAccuracy *prometheus.Desc

	joinAccuracy  *prometheus.Desc
	joinCoverage  *prometheus.Desc
	misjoins      *prometheus.Desc
	entitiesCheck *prometheus.Desc
	entitiesMiss  *prometheus.Desc
	gatePassed    *prometheus.Desc
}

// NewCollector builds the collector. consistency runs the live join-consistency
// audit (e.g. Watcher.AuditConsistencyNow); pass nil when no cluster is attached.
func NewCollector(store *Store, edges *EdgeStore, auditor *Auditor, consistency func() ConsistencyReport, targetCoverage float64) *Collector {
	d := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(metricNS+"_"+name, help, labels, nil)
	}
	return &Collector{
		store: store, edges: edges, auditor: auditor,
		consistency: consistency, targetCoverage: targetCoverage,

		active:        d("active_entities", "Currently live instances in the identity store."),
		tombstones:    d("tombstones", "Tombstoned instances by tier.", "tier"),
		discovered:    d("discovered_total", "Total instances discovered."),
		terminated:    d("terminated_total", "Total instances terminated."),
		successions:   d("successions_total", "Total succession links recorded."),
		degradedJoins: d("degraded_joins_total", "Late-sample joins that landed on a stub-tier tombstone."),
		evicted:       d("tombstones_evicted_before_horizon_total", "Tombstones LRU-evicted before their retention horizon (a join-risk signal)."),

		edgeStatus:     d("edges", "Topology edges by type and status.", "type", "status"),
		edgeAsserts:    d("edge_asserts_total", "Total edge assertions."),
		edgeConfirms:   d("edge_confirms_total", "Total edge confirmations."),
		edgeRetracts:   d("edge_retractions_total", "Total edge retractions."),
		edgeReasserts:  d("edge_reasserts_total", "Total edge re-assertions after retraction."),
		edgeSuspectTrv: d("edge_suspect_traversals_total", "Traversals that hit a suspect (stale) edge."),
		edgeAbsentTrv:  d("edge_absent_traversals_total", "Traversals that found no valid edge."),

		streamsResolved:    d("streams_resolved_total", "Streams normalized to a CEI."),
		streamsDropped:     d("streams_dropped_total", "Streams deliberately dropped (recognized non-pod rows)."),
		streamsQuarantined: d("streams_quarantined_total", "Streams quarantined (unjoinable, never guessed)."),
		quarantineByReason: d("quarantine_total", "Quarantined streams by reason.", "reason"),
		orphanRate:         d("orphan_rate", "Quarantined / (resolved + quarantined)."),
		streamJoinAccuracy: d("stream_join_accuracy", "Resolved streams verified correct against truth / verified."),

		joinAccuracy:  d("join_accuracy", "Consistency-audit: correctly-resolved / resolved entities (1.0 iff no mis-joins)."),
		joinCoverage:  d("join_coverage", "Consistency-audit: resolved / current entities."),
		misjoins:      d("misjoins", "Consistency-audit mis-joins — the silent killer; MUST be 0 for the Phase-0a gate."),
		entitiesCheck: d("entities_checked", "Current entities checked by the consistency audit."),
		entitiesMiss:  d("entities_missing", "Current entities the store does not yet resolve (coverage lag)."),
		gatePassed:    d("phase0a_gate_passed", "Phase-0a exit gate verdict (1 = pass: no mis-joins and coverage >= target)."),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.active
	ch <- c.tombstones
	ch <- c.discovered
	ch <- c.terminated
	ch <- c.successions
	ch <- c.degradedJoins
	ch <- c.evicted
	ch <- c.edgeStatus
	ch <- c.edgeAsserts
	ch <- c.edgeConfirms
	ch <- c.edgeRetracts
	ch <- c.edgeReasserts
	ch <- c.edgeSuspectTrv
	ch <- c.edgeAbsentTrv
	ch <- c.streamsResolved
	ch <- c.streamsDropped
	ch <- c.streamsQuarantined
	ch <- c.quarantineByReason
	ch <- c.orphanRate
	ch <- c.streamJoinAccuracy
	ch <- c.joinAccuracy
	ch <- c.joinCoverage
	ch <- c.misjoins
	ch <- c.entitiesCheck
	ch <- c.entitiesMiss
	ch <- c.gatePassed
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counter := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
	}

	m := c.store.Metrics()
	gauge(c.active, float64(m.Active))
	gauge(c.tombstones, float64(m.FullTombstones), "full")
	gauge(c.tombstones, float64(m.StubTombstones), "stub")
	counter(c.discovered, float64(m.Discovered))
	counter(c.terminated, float64(m.Terminated))
	counter(c.successions, float64(m.Successions))
	counter(c.degradedJoins, float64(m.DegradedJoins))
	counter(c.evicted, float64(m.EvictedBeforeHorizon))

	e := c.edges.Metrics()
	for typ, pt := range e.PerType {
		gauge(c.edgeStatus, float64(pt.Live), string(typ), "valid")
		gauge(c.edgeStatus, float64(pt.Suspect), string(typ), "suspect")
		gauge(c.edgeStatus, float64(pt.Retracted), string(typ), "retracted")
	}
	counter(c.edgeAsserts, float64(e.Asserted))
	counter(c.edgeConfirms, float64(e.Confirmed))
	counter(c.edgeRetracts, float64(e.Retractions))
	counter(c.edgeReasserts, float64(e.Reasserted))
	counter(c.edgeSuspectTrv, float64(e.SuspectTraversals))
	counter(c.edgeAbsentTrv, float64(e.AbsentTraversals))

	if c.auditor != nil {
		s := c.auditor.Snapshot()
		counter(c.streamsResolved, float64(s.Resolved))
		counter(c.streamsDropped, float64(s.Dropped))
		counter(c.streamsQuarantined, float64(s.Quarantined))
		for reason, n := range s.QuarantineByReason {
			counter(c.quarantineByReason, float64(n), reason)
		}
		gauge(c.orphanRate, s.OrphanRate)
		gauge(c.streamJoinAccuracy, s.JoinAccuracy)
	}

	if c.consistency != nil {
		rep := c.consistency()
		g := EvaluateGate(rep, c.targetCoverage)
		gauge(c.joinAccuracy, g.JoinAccuracy)
		gauge(c.joinCoverage, g.Coverage)
		gauge(c.misjoins, float64(rep.Misjoins))
		gauge(c.entitiesCheck, float64(rep.CheckedPods+rep.CheckedNodes))
		gauge(c.entitiesMiss, float64(rep.Missing))
		gauge(c.gatePassed, boolToFloat(g.Passed))
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
