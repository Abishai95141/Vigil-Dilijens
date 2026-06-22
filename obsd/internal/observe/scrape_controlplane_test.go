package observe

import (
	"context"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// goldenAPIServer is a kube-apiserver root /metrics slice (docs/33 build 1): the
// label-dimensioned APF families the lane must SUM into clean single-series derived
// metrics. apiserver_flowcontrol_rejected_requests_total has several flow-schema rows
// (the overload symptom); apiserver_current_inflight_requests has two request_kind rows.
// A histogram family is present and must be ignored (the lane sums scalars only).
const goldenAPIServer = `# HELP apiserver_flowcontrol_rejected_requests_total APF rejections.
# TYPE apiserver_flowcontrol_rejected_requests_total counter
apiserver_flowcontrol_rejected_requests_total{flow_schema="global-default",priority_level="global-default",reason="queue-full"} 7
apiserver_flowcontrol_rejected_requests_total{flow_schema="workload-low",priority_level="workload-low",reason="queue-full"} 5
# HELP apiserver_current_inflight_requests Inflight requests.
# TYPE apiserver_current_inflight_requests gauge
apiserver_current_inflight_requests{request_kind="mutating"} 3
apiserver_current_inflight_requests{request_kind="readOnly"} 9
# HELP apiserver_request_duration_seconds latency histogram (must be ignored).
# TYPE apiserver_request_duration_seconds histogram
apiserver_request_duration_seconds_bucket{le="1",verb="GET"} 1
apiserver_request_duration_seconds_sum{verb="GET"} 1
apiserver_request_duration_seconds_count{verb="GET"} 1
`

// goldenCoreDNS is a CoreDNS :9153/metrics slice: the rcode-dimensioned responses
// family (the lane sums SERVFAIL+REFUSED into coredns_dns_responses_failed and ALL rcodes
// into coredns_dns_responses_all) and the cache hit/miss counters (summed across the two
// hit types + combined into coredns_cache_lookups).
const goldenCoreDNS = `# HELP coredns_dns_responses_total Responses by rcode.
# TYPE coredns_dns_responses_total counter
coredns_dns_responses_total{rcode="NOERROR",server="dns://:53",zone="."} 100
coredns_dns_responses_total{rcode="NXDOMAIN",server="dns://:53",zone="."} 10
coredns_dns_responses_total{rcode="SERVFAIL",server="dns://:53",zone="."} 6
coredns_dns_responses_total{rcode="REFUSED",server="dns://:53",zone="."} 4
# HELP coredns_cache_hits_total Cache hits.
# TYPE coredns_cache_hits_total counter
coredns_cache_hits_total{server="dns://:53",type="denial"} 30
coredns_cache_hits_total{server="dns://:53",type="success"} 20
# HELP coredns_cache_misses_total Cache misses.
# TYPE coredns_cache_misses_total counter
coredns_cache_misses_total{server="dns://:53"} 50
`

// Control-plane ingest (docs/33 build 1): apiserver_* aggregate to the control-plane
// NODE CEI; coredns_* aggregate to the scrape-target CoreDNS POD CEI. The lane SUMS the
// label-dimensioned families into clean single-series derived metrics — exactly one
// stream per (entity, derived metric), the form the matcher can bind.
func TestScrapeControlPlaneAggregatesByEntity(t *testing.T) {
	in, st := newTestIngestor(t)
	// Seed a CoreDNS pod (the scrape target for coredns_*).
	if _, err := st.Observe(identity.InstanceCoords{Cluster: cluster, Namespace: "kube-system", Kind: "Pod", Name: "coredns-x", UID: "coredns-u1"}, identity.CEI{}, bornAt, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	api := fixtureControlPlaneFetcher{body: goldenAPIServer, at: scrapeAt}
	pf := fixturePodFetcher{payloads: map[string]string{"kube-system/coredns-x": goldenCoreDNS}, at: scrapeAt}
	cd := []PodTarget{{Namespace: "kube-system", Name: "coredns-x", Port: "9153", Path: "metrics"}}

	sum := in.IngestPayloads(FetchControlPlaneMetrics(context.Background(), api, []string{"worker-1"}, pf, cd))

	// 6 derived streams resolve: apiserver_flowcontrol_rejected + apiserver_inflight_requests
	// (node) and coredns_dns_responses_failed + coredns_dns_responses_all + coredns_cache_misses
	// + coredns_cache_lookups (coredns pod). The admission-webhook derivation has no source
	// family here, so it emits nothing (no fabricated zero).
	if sum.SeriesResolved != 6 || sum.SamplesStored != 6 {
		t.Fatalf("resolved=%d stored=%d, want 6/6 (%s)", sum.SeriesResolved, sum.SamplesStored, sum)
	}

	// apiserver_* land on the control-plane NODE CEI (uid node-u1), one stream each.
	checkOne := func(uid, metric string, want float64, wantKind string) {
		t.Helper()
		ids := in.StreamsByUIDMetric(uid, metric)
		if len(ids) != 1 {
			t.Fatalf("%s streams = %v, want exactly 1 (aggregated single-series)", metric, ids)
		}
		s, ok := in.Latest(ids[0])
		if !ok || s.Value != want {
			t.Errorf("%s = %+v (ok=%v), want value %v", metric, s, ok, want)
		}
		if m, _ := in.Meta(ids[0]); m.Kind != wantKind {
			t.Errorf("%s CEI kind = %q, want %q", metric, m.Kind, wantKind)
		}
	}
	checkOne("node-u1", "apiserver_flowcontrol_rejected", 12, "Node") // 7 + 5
	checkOne("node-u1", "apiserver_inflight_requests", 12, "Node")    // 3 + 9
	checkOne("coredns-u1", "coredns_dns_responses_failed", 10, "Pod") // SERVFAIL 6 + REFUSED 4
	checkOne("coredns-u1", "coredns_dns_responses_all", 120, "Pod")   // 100+10+6+4
	checkOne("coredns-u1", "coredns_cache_misses", 50, "Pod")
	checkOne("coredns-u1", "coredns_cache_lookups", 100, "Pod") // misses 50 + hits (30+20)

	// The admission-webhook derivation has no source family → no stream (honest: no
	// fabricated zero, so WEBHOOK_LATENCY cannot false-fire on a webhook-less cluster).
	if ids := in.StreamsByUIDMetric("node-u1", "apiserver_admission_webhook_rejections"); len(ids) != 0 {
		t.Errorf("admission-webhook derived streams = %v, want none (source family absent)", ids)
	}
}

// A control-plane payload whose scrape-target pod is unknown to the control plane
// quarantines (never guessed) — and a node not in the control-plane view likewise.
func TestScrapeControlPlaneUnknownTargetsQuarantine(t *testing.T) {
	in, _ := newTestIngestor(t)
	api := fixtureControlPlaneFetcher{body: goldenAPIServer, at: scrapeAt}
	pf := fixturePodFetcher{payloads: map[string]string{"kube-system/coredns-ghost": goldenCoreDNS}, at: scrapeAt}
	cd := []PodTarget{{Namespace: "kube-system", Name: "coredns-ghost", Port: "9153", Path: "metrics"}}

	// apiserver attributed to an unknown node; coredns to an unknown pod.
	sum := in.IngestPayloads(FetchControlPlaneMetrics(context.Background(), api, []string{"ghost-node"}, pf, cd))
	if sum.SeriesResolved != 0 {
		t.Errorf("resolved = %d, want 0 (both targets unknown to the control plane)", sum.SeriesResolved)
	}
	if sum.SeriesQuarantine["unknown-node"] == 0 || sum.SeriesQuarantine["unknown-pod"] == 0 {
		t.Errorf("quarantines = %v, want both unknown-node and unknown-pod (never guessed)", sum.SeriesQuarantine)
	}
}

// FetchControlPlaneMetrics with the lane OFF (no fetcher) is a no-op — the byte-identical
// guarantee at the fetch layer.
func TestFetchControlPlaneNilFetcherIsNoop(t *testing.T) {
	pf := fixturePodFetcher{payloads: map[string]string{}, at: scrapeAt}
	out := FetchControlPlaneMetrics(context.Background(), nil, nil, pf, nil)
	if len(out) != 0 {
		t.Errorf("payloads = %d, want 0 (nil apiserver fetcher + no coredns targets)", len(out))
	}
}

type fixtureControlPlaneFetcher struct {
	body string
	at   time.Time
}

func (f fixtureControlPlaneFetcher) APIServerMetrics(_ context.Context) ([]byte, time.Time, error) {
	return []byte(f.body), f.at, nil
}
