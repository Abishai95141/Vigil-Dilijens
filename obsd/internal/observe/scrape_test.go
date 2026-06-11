package observe

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

const cluster = "test-cluster"

var (
	scrapeAt = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	bornAt   = scrapeAt.Add(-time.Hour)
)

// goldenCAdvisor is a realistic cAdvisor exposition snippet exercising every trap
// the normalizer encodes: a real container row (with a sample timestamp), the
// pod-level aggregate (container=""), the pause sandbox (container="POD"), a
// system-slice cgroup row, a machine_ row, an unknown pod, and a histogram
// family (skipped, stated).
const goldenCAdvisor = `# HELP container_memory_working_set_bytes Current working set in bytes.
# TYPE container_memory_working_set_bytes gauge
container_memory_working_set_bytes{container="server",id="/kubepods/pod-x",image="img",name="x",namespace="shop",pod="web-a"} 1.34217728e+08 1781524785000
container_memory_working_set_bytes{container="",id="/kubepods/pod-x",image="",name="",namespace="shop",pod="web-a"} 1.40000000e+08 1781524785000
container_memory_working_set_bytes{container="POD",id="/kubepods/pod-x/pause",image="pause",name="",namespace="shop",pod="web-a"} 5.0e+05 1781524785000
container_memory_working_set_bytes{container="",id="/system.slice/sshd.service",image="",name="",namespace="",pod=""} 9.9e+06 1781524785000
container_memory_working_set_bytes{container="server",id="/kubepods/pod-ghost",image="img",name="g",namespace="shop",pod="ghost"} 7.7e+07 1781524785000
# HELP container_cpu_usage_seconds_total Cumulative cpu time consumed in seconds.
# TYPE container_cpu_usage_seconds_total counter
container_cpu_usage_seconds_total{container="server",id="/kubepods/pod-x",image="img",name="x",namespace="shop",pod="web-a"} 42.5 1781524785000
# HELP machine_cpu_cores Number of CPU cores on the machine.
# TYPE machine_cpu_cores gauge
machine_cpu_cores{boot_id="b",machine_id="m",system_uuid="s"} 8
# HELP container_cpu_load_d_total histogram-ish family to skip
# TYPE container_cpu_load_d_total histogram
container_cpu_load_d_total_bucket{le="1",container="server",namespace="shop",pod="web-a"} 1
container_cpu_load_d_total_sum{container="server",namespace="shop",pod="web-a"} 1
container_cpu_load_d_total_count{container="server",namespace="shop",pod="web-a"} 1
`

type fixtureFetcher struct {
	payloads map[string]string
	at       time.Time
}

func (f fixtureFetcher) NodeMetrics(_ context.Context, node, path string) ([]byte, time.Time, error) {
	body, ok := f.payloads[node+"/"+path]
	if !ok {
		return nil, f.at, context.DeadlineExceeded
	}
	return []byte(body), f.at, nil
}

func newTestIngestor(t *testing.T) (*Ingestor, *identity.Store) {
	t.Helper()
	st := identity.NewStore(func() time.Time { return scrapeAt }, 15*time.Minute, 24*time.Hour, 1000)
	// Seed identity: the pod web-a (uid pod-x) and the node, both alive.
	if _, err := st.Observe(identity.InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: "web-a", UID: "pod-x"}, identity.CEI{}, bornAt, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Observe(identity.InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-1", UID: "node-u1"}, identity.CEI{}, bornAt, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	return NewIngestor(identity.NewNormalizer(cluster, st), qss.NewHotStore()), st
}

// The golden scrape: real rows land CEI-stamped, traps route correctly, the
// histogram family is skipped and counted, and the unknown pod quarantines.
func TestScrapeCAdvisorGolden(t *testing.T) {
	in, _ := newTestIngestor(t)
	f := fixtureFetcher{payloads: map[string]string{"worker-1/metrics/cadvisor": goldenCAdvisor}, at: scrapeAt}
	sum := in.ScrapeCAdvisor(context.Background(), f, []string{"worker-1"})

	// 4 resolved: container ws, pod-aggregate ws, container cpu counter, machine_ row (node).
	if sum.SeriesResolved != 4 || sum.SamplesStored != 4 {
		t.Fatalf("resolved=%d stored=%d, want 4/4 (%s)", sum.SeriesResolved, sum.SamplesStored, sum)
	}
	if sum.SeriesDropped["pause-sandbox-row"] != 1 || sum.SeriesDropped["non-pod-cgroup"] != 1 {
		t.Errorf("drops = %v, want pause-sandbox-row:1 non-pod-cgroup:1", sum.SeriesDropped)
	}
	if sum.SeriesQuarantine["unknown-pod"] != 1 {
		t.Errorf("quarantines = %v, want unknown-pod:1", sum.SeriesQuarantine)
	}
	if sum.SkippedFamilies != 1 {
		t.Errorf("skipped families = %d, want 1 (histogram)", sum.SkippedFamilies)
	}

	// The container stream: CEI kind Container, UID podUID/container (the QA join
	// key), exposition type preserved, value exact.
	ids := in.StreamsByUIDMetric("pod-x/server", "container_memory_working_set_bytes")
	if len(ids) != 1 {
		t.Fatalf("container ws streams = %v, want exactly 1", ids)
	}
	meta, _ := in.Meta(ids[0])
	if meta.Kind != "Container" || meta.Type != "gauge" || meta.Cadence != "scrape" || meta.Node != "worker-1" {
		t.Errorf("meta = %+v", meta)
	}
	latest, ok := in.Hot().Latest(ids[0])
	if !ok || latest.Value != 1.34217728e+08 {
		t.Errorf("latest = %+v, want 134217728", latest)
	}
	// The sample's own timestamp (1781524785000 ms) is the stored time, not receive time.
	if want := time.UnixMilli(1781524785000).UTC(); !latest.At.Equal(want) {
		t.Errorf("sample at = %v, want event time %v", latest.At, want)
	}

	// The counter stream keeps its exposition type (QA's character-check input).
	cpu := in.StreamsByUIDMetric("pod-x/server", "container_cpu_usage_seconds_total")
	if len(cpu) != 1 {
		t.Fatalf("cpu streams = %v", cpu)
	}
	if m, _ := in.Meta(cpu[0]); m.Type != "counter" {
		t.Errorf("cpu type = %s, want counter", m.Type)
	}

	// The pod aggregate landed on the POD CEI (uid pod-x), never a container CEI.
	agg := in.StreamsByUIDMetric("pod-x", "container_memory_working_set_bytes")
	if len(agg) != 1 {
		t.Errorf("pod-aggregate streams = %v, want 1", agg)
	}
	if m, _ := in.Meta(agg[0]); m.Kind != "Pod" {
		t.Errorf("aggregate kind = %s, want Pod", m.Kind)
	}

	// machine_ row landed on the Node CEI.
	node := in.StreamsByUIDMetric("node-u1", "machine_cpu_cores")
	if len(node) != 1 {
		t.Errorf("node streams = %v, want 1", node)
	}
}

// A failing node is a stated partial scrape, never silent — and the healthy
// node's ingest is unaffected.
func TestScrapePartialFailureStated(t *testing.T) {
	in, st := newTestIngestor(t)
	if _, err := st.Observe(identity.InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-2", UID: "node-u2"}, identity.CEI{}, bornAt, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	f := fixtureFetcher{payloads: map[string]string{"worker-1/metrics/cadvisor": goldenCAdvisor}, at: scrapeAt}
	sum := in.ScrapeCAdvisor(context.Background(), f, []string{"worker-2", "worker-1"})
	if len(sum.NodeErrors) != 1 || !strings.Contains(sum.NodeErrors[0], "worker-2") {
		t.Errorf("node errors = %v, want exactly worker-2's failure stated", sum.NodeErrors)
	}
	if sum.SeriesResolved != 4 {
		t.Errorf("healthy node resolved %d, want 4 despite the other node failing", sum.SeriesResolved)
	}
}

// Re-scraping appends to the same streams (stable stream identity), and the ring
// returns history oldest-first.
func TestRescrapeAppendsToSameStream(t *testing.T) {
	in, _ := newTestIngestor(t)
	f1 := fixtureFetcher{payloads: map[string]string{"worker-1/metrics/cadvisor": goldenCAdvisor}, at: scrapeAt}
	in.ScrapeCAdvisor(context.Background(), f1, []string{"worker-1"})
	bumped := strings.ReplaceAll(goldenCAdvisor, "1.34217728e+08 1781524785000", "1.35e+08 1781524800000")
	f2 := fixtureFetcher{payloads: map[string]string{"worker-1/metrics/cadvisor": bumped}, at: scrapeAt.Add(15 * time.Second)}
	in.ScrapeCAdvisor(context.Background(), f2, []string{"worker-1"})

	ids := in.StreamsByUIDMetric("pod-x/server", "container_memory_working_set_bytes")
	if len(ids) != 1 {
		t.Fatalf("stream identity not stable across scrapes: %v", ids)
	}
	hist := in.Hot().LastN(ids[0], 10)
	if len(hist) != 2 || hist[0].Value != 1.34217728e+08 || hist[1].Value != 1.35e+08 {
		t.Errorf("history = %+v, want 2 samples oldest-first", hist)
	}
}
