// Command flowprobe is the EXPERIMENTAL hypothesis-validation spike for
// service-dependency discovery + cross-service cascade tracing (internal/flow).
// It is NOT part of obsd's deterministic path: it builds its own flow graph from
// Linux conntrack and emits a charter-clean chain to stdout. See internal/flow/doc.go.
//
// Modes:
//
//	flowprobe                               # graph mode: reconstruct + print the flow graph (C1)
//	flowprobe -degraded productcatalogservice -degraded-detail "cpu throttle 0.42>0.25"
//	                                        # chain mode: seed a MEASURED degradation, walk to a root (C2)
//
// Sources: live (`docker exec <node> cat /proc/net/nf_conntrack`) or offline
// (`-conntrack-dir` of conntrack-<node>.txt files). Pods come from `kubectl get pods`.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

func main() {
	fs := flag.NewFlagSet("flowprobe", flag.ExitOnError)
	nodes := fs.String("nodes", "vigil-control-plane,vigil-worker,vigil-worker2", "comma-separated kind node container names")
	conntrackDir := fs.String("conntrack-dir", "", "offline: read conntrack-<node>.txt from this dir instead of docker exec")
	snapshots := fs.Int("snapshots", 1, "number of conntrack snapshots to fold in")
	interval := fs.Duration("interval", 15*time.Second, "delay between live snapshots")
	relationPath := fs.String("relation", "ontology/graph/overlays/experimental/flow-relation-v0.yaml", "authored relation YAML")
	degraded := fs.String("degraded", "", "workload to seed as MEASURED-degraded (chain mode); empty = graph mode")
	degradedDetail := fs.String("degraded-detail", "measured degradation (basis supplied by obsd findings)", "the measured basis for the degradation")
	namespace := fs.String("namespace", "online-boutique", "namespace of the degraded workload / scoring scope")
	cluster := fs.String("cluster", "kind-vigil", "cluster id (internal consistency only)")
	out := fs.String("out", "", "write JSON here (default stdout)")
	_ = fs.Parse(os.Args[1:])

	pods, err := loadPods()
	if err != nil {
		fmt.Fprintf(os.Stderr, "flowprobe: load pods: %v\n", err)
		os.Exit(1)
	}
	resolver := flow.NewResolver(*cluster, pods)
	g := flow.NewGraph(resolver, time.Now)

	nodeList := strings.Split(*nodes, ",")
	var lastAt time.Time
	for i := 0; i < *snapshots; i++ {
		lastAt = time.Now().UTC()
		for _, n := range nodeList {
			n = strings.TrimSpace(n)
			raw, err := readConntrack(n, *conntrackDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "flowprobe: conntrack %s: %v\n", n, err)
				continue
			}
			conns, _ := flow.ParseConntrack(bytes.NewReader(raw))
			g.Observe(conns, lastAt)
		}
		if i < *snapshots-1 {
			time.Sleep(*interval)
		}
	}

	if *degraded == "" {
		emitGraph(g, *out)
		return
	}

	rel, err := flow.LoadRelation(*relationPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flowprobe: relation: %v\n", err)
		os.Exit(1)
	}
	root := identity.CEI{Layer: identity.LayerRole, Cluster: *cluster, Namespace: *namespace, Kind: "Pod", RoleKey: "Deployment/" + *degraded}
	sym := flow.Symptom{
		Workload: root, Label: *namespace + "/" + *degraded,
		Phenomenon: rel.Trigger, Class: "MEASURED", Detail: *degradedDetail,
	}
	chain := flow.Walk(g, []flow.Symptom{sym}, rel, lastAt)
	b, _ := chain.JSON()
	write(*out, b)
}

// graphJSON is the machine-readable graph-mode output (for C1 scoring).
type graphJSON struct {
	Edges     []edgeJSON    `json:"edges"`
	Coverage  flow.Coverage `json:"coverage"`
	Snapshots int           `json:"snapshots"`
}

type edgeJSON struct {
	From         string `json:"from"`
	To           string `json:"to"`
	ServicePorts []int  `json:"service_ports"`
	ConnCount    int    `json:"conn_count"`
	TickPresence int    `json:"tick_presence"`
}

func emitGraph(g *flow.Graph, out string) {
	gj := graphJSON{Coverage: g.Coverage(), Snapshots: g.Ticks()}
	for _, e := range g.Edges() {
		ports := make([]int, 0, len(e.ServicePorts))
		for p := range e.ServicePorts {
			ports = append(ports, p)
		}
		sort.Ints(ports)
		gj.Edges = append(gj.Edges, edgeJSON{From: e.FromLabel, To: e.ToLabel, ServicePorts: ports, ConnCount: e.ConnCount, TickPresence: e.TickPresence})
	}
	b, _ := json.MarshalIndent(gj, "", "  ")
	write(out, b)
	// human summary to stderr
	fmt.Fprintf(os.Stderr, "\nreconstructed %d observed-flow edges over %d snapshot(s):\n", len(gj.Edges), gj.Snapshots)
	for _, e := range gj.Edges {
		fmt.Fprintf(os.Stderr, "  %-45s -> %-40s ports=%v conn=%d presence=%d\n", e.From, e.To, e.ServicePorts, e.ConnCount, e.TickPresence)
	}
	c := gj.Coverage
	fmt.Fprintf(os.Stderr, "\ncoverage: resolvable=%d snat_masked=%d unresolved=%d infra=%d unreplied=%d\n",
		c.ResolvableFlows, c.SnatMaskedFlows, c.UnresolvedFlows, c.InfraFlows, c.UnrepliedFlows)
}

func write(out string, b []byte) {
	if out == "" {
		fmt.Println(string(b))
		return
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "flowprobe: write %s: %v\n", out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", out)
}

func readConntrack(node, dir string) ([]byte, error) {
	if dir != "" {
		return os.ReadFile(filepath.Join(dir, "conntrack-"+node+".txt"))
	}
	cmd := exec.Command("docker", "exec", node, "cat", "/proc/net/nf_conntrack")
	var buf, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	return buf.Bytes(), nil
}

// --- pod snapshot via kubectl ---

type kPodList struct {
	Items []kPod `json:"items"`
}
type kPod struct {
	Metadata struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		UID             string            `json:"uid"`
		Labels          map[string]string `json:"labels"`
		OwnerReferences []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"ownerReferences"`
	} `json:"metadata"`
	Status struct {
		PodIP string `json:"podIP"`
	} `json:"status"`
}

func loadPods() ([]flow.PodInfo, error) {
	cmd := exec.Command("kubectl", "get", "pods", "-A", "-o", "json")
	var buf, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	var list kPodList
	if err := json.Unmarshal(buf.Bytes(), &list); err != nil {
		return nil, err
	}
	out := make([]flow.PodInfo, 0, len(list.Items))
	for _, p := range list.Items {
		if p.Status.PodIP == "" {
			continue
		}
		out = append(out, flow.PodInfo{
			Namespace: p.Metadata.Namespace,
			Name:      p.Metadata.Name,
			IP:        p.Status.PodIP,
			UID:       p.Metadata.UID,
			Workload:  workloadOf(p),
		})
	}
	return out, nil
}

// workloadOf derives the durable workload (role) name: prefer the app label, then
// the ReplicaSet owner with its pod-template-hash stripped, then the pod-name prefix.
func workloadOf(p kPod) string {
	if v := p.Metadata.Labels["app"]; v != "" {
		return v
	}
	if v := p.Metadata.Labels["app.kubernetes.io/name"]; v != "" {
		return v
	}
	for _, o := range p.Metadata.OwnerReferences {
		if o.Kind == "ReplicaSet" {
			return stripLast(o.Name) // frontend-759775d795 -> frontend
		}
	}
	// pod name: <wl>-<rshash>-<podhash>
	return stripLast(stripLast(p.Metadata.Name))
}

func stripLast(s string) string {
	if i := strings.LastIndex(s, "-"); i > 0 {
		return s[:i]
	}
	return s
}
