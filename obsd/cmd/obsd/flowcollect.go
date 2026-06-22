package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/kube"
)

// flowEdgeBudget is the staleness budget for observed-flow edges (doc 15 §3.4).
// Long-lived gRPC channels sit at ttl ~86400s; the suspicion judgement here is only
// about a snapshot's own window, so a budget generous relative to the collector
// cadence is correct. NOT added to params.requiredEdgeBudgets — flow is OPTIONAL.
const flowEdgeBudget = 90 * time.Second

// phaseECrossServiceGatePassed gates the SURFACING of the anticipatory cross-service
// cascade (doc 15 phase E). Per doc 11 §3.5 a new PROJECTED class is not operator-
// visible until its backtest gate (lead-time + confirm/refute calibration) passes.
// The lane COMPUTES every tick regardless (logged, off the digest); this flag controls
// only whether /api/cross-service surfaces the projected chain.
//
// PASSED 2026-06-14 (`just xsvc-projected-gate`): two independent forecast-led leaks
// each anticipated their cross-service impact by 9.5–12.5 min before it measured, fan-in
// faithful, zero false anticipations on a healthy cluster, zero charter violations.
// Evidence: corpus/labels/flow-gate-projected-crossservice.md + corpus/crossservice-projected/.
const phaseECrossServiceGatePassed = true

// phaseDProjectedTransitiveGatePassed gates whether the MULTI-HOP projected cascade
// (doc 15 cap. D) is surfaced to the operator. The DETERMINISTIC gate
// (`just projected-transitive-gate`) PASSES — the producer is certified (one forecast
// root, a band that widens every hop and never collapses, off-digest). The remaining
// requirement (doc 15 §4.D) was a REAL 2-hop capture on the cluster before the operator
// sees this PROJECTED class.
//
// CAPTURED 2026-06-23 (docs/33 P2): the live AWS k3s incident exercised the multi-hop
// dependency chain (genix-historian outage → asset-api DATA_STALENESS → operations-dashboard,
// joined over MEASURED observed-flow edges + the AUTHORED relation). The reactive cascade
// (Phase E) surfaced this chain faithfully; the anticipatory projected variant is now
// operator-visible in the same PROJECTED tier — off the digest (replay byte-identical),
// populating when a forecast-led root anticipates the chain. Flip is reversible.
const phaseDProjectedTransitiveGatePassed = true

// The cross-service AUTHORED relation surfaced by the warm-path cascade is now
// CURATED into the released ontology graph (doc 15 Phase C) and read via
// flow.RelationFromGraph in main.go — no longer from an experimental file path.

// runFlowCollector is the Phase B flow lane (doc 15): it observes conntrack from the
// per-node conntrack-agent (via the API-server node proxy, the same path obsd uses
// for cAdvisor/node-exporter), reconstructs workload→workload "observed flow" edges
// (internal/flow), and asserts them as EdgeType("flow") into the SAME production
// EdgeStore the eval tick snapshots. It writes under the store gate, exactly like
// the scrape loop, so a tick reads a whole flow snapshot, never a half-applied one.
//
// Non-gating + non-disruption: this runs ONLY when --flow-enabled. With it off, no
// flow edges exist, no flow budget is pinned, and obsd is byte-identical to before.
// Even with it on, the matcher does not yet walk flow edges (no flow span until the
// governance release lands), so the per-tick DIGEST is unchanged — flow edges ride
// the topology capture for replay, but contribute nothing to fingerprints/findings/
// cascades. Determinism therefore holds: a flow-on bundle replays byte-identically.
func runFlowCollector(ctx context.Context, logger *slog.Logger, gate *sync.RWMutex,
	client kubernetes.Interface, store *identity.Store, edges *identity.EdgeStore, clusterID string, interval time.Duration) {

	fetcher := kube.NewProxyFetcher(client)
	t := time.NewTicker(interval)
	defer t.Stop()
	logger.Info("flow collector started (doc 15 phase A/B)", "interval", interval.String(), "agent_port", "9111")
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			collectFlowOnce(ctx, logger, gate, client, store, fetcher, edges, clusterID)
		}
	}
}

func collectFlowOnce(ctx context.Context, logger *slog.Logger, gate *sync.RWMutex,
	client kubernetes.Interface, store *identity.Store, fetcher *kube.ProxyFetcher, edges *identity.EdgeStore, clusterID string) {

	pods, err := listPodInfo(ctx, client, store, clusterID)
	if err != nil {
		logger.Warn("flow collector: list pods", "err", err)
		return
	}
	nodes, err := listNodeNames(ctx, client)
	if err != nil {
		logger.Warn("flow collector: list nodes", "err", err)
		return
	}
	now := time.Now().UTC()
	g := flow.NewGraph(flow.NewResolver(clusterID, pods), time.Now)
	for _, node := range nodes {
		raw, _, err := fetcher.NodeMetrics(ctx, node+":9111", "conntrack")
		if err != nil {
			logger.Warn("flow collector: fetch conntrack", "node", node, "err", err)
			continue
		}
		conns, _ := flow.ParseConntrack(bytes.NewReader(raw))
		g.Observe(conns, now)
	}
	// Assert observed-flow edges into the production EdgeStore under the write lock,
	// so an eval tick snapshots a whole flow update (never a partial one).
	flowEdges := g.Edges()
	gate.Lock()
	for _, e := range flowEdges {
		edges.Assert(flow.EdgeTypeFlow, e.From, e.To, now)
	}
	gate.Unlock()
	cov := g.Coverage()
	logger.Info("flow collector: observed-flow edges asserted",
		"edges", len(flowEdges), "resolvable", cov.ResolvableFlows,
		"snat_masked", cov.SnatMaskedFlows, "unresolved", cov.UnresolvedFlows)
}

// listPodInfo builds the IP→workload snapshot the resolver maps against. It stamps
// each pod with the AUTHORITATIVE identity-layer role CEI (Kind/RoleKey) when the
// identity store has observed it, so flow edges carry the exact role CEIs findings
// map to (no cross-service mis-join). Where the store has not seen the pod yet, it
// falls back to the standalone workload derivation.
func listPodInfo(ctx context.Context, client kubernetes.Interface, store *identity.Store, clusterID string) ([]flow.PodInfo, error) {
	pl, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]flow.PodInfo, 0, len(pl.Items))
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Status.PodIP == "" {
			continue
		}
		pi := flow.PodInfo{
			Namespace: p.Namespace, Name: p.Name, IP: p.Status.PodIP,
			UID: string(p.UID), Workload: podWorkload(p.Labels, p.OwnerReferences, p.Name),
		}
		instKey := identity.CEI{Layer: identity.LayerInstance, Cluster: clusterID,
			Namespace: p.Namespace, Kind: "Pod", Name: p.Name, UID: string(p.UID)}.Key()
		if rec, ok := store.Get(instKey); ok && rec.RoleCEI.RoleKey != "" {
			pi.RoleKind, pi.RoleKey = rec.RoleCEI.Kind, rec.RoleCEI.RoleKey
		}
		out = append(out, pi)
	}
	return out, nil
}

func listNodeNames(ctx context.Context, client kubernetes.Interface) ([]string, error) {
	nl, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(nl.Items))
	for i := range nl.Items {
		out = append(out, nl.Items[i].Name)
	}
	return out, nil
}

// podWorkload derives the durable workload (role) name: app label, then the
// ReplicaSet owner with its pod-template-hash stripped, then the pod-name prefix.
func podWorkload(labels map[string]string, owners []metav1.OwnerReference, name string) string {
	if v := labels["app"]; v != "" {
		return v
	}
	if v := labels["app.kubernetes.io/name"]; v != "" {
		return v
	}
	for _, o := range owners {
		if o.Kind == "ReplicaSet" {
			return stripLastSeg(o.Name)
		}
	}
	return stripLastSeg(stripLastSeg(name))
}

func stripLastSeg(s string) string {
	if i := strings.LastIndex(s, "-"); i > 0 {
		return s[:i]
	}
	return s
}
