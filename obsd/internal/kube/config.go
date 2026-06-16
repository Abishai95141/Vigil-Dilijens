package kube

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
)

// sloAnnotationDomain is the workload-annotation key domain carrying customer-declared
// application SLO bars (doc 15 cap. A): "vigil.io/slo.queue.max_depth: 1000" declares the
// config-path slo.queue.max_depth = 1000 (the key minus the domain IS the config path).
// Borrowed normativity — the customer's own number on the customer's own object, read
// verbatim, never learned.
const sloAnnotationDomain = "vigil.io/"

// ConfigSnapshot is a point-in-time, read-only view of the customer's declared
// configuration (limits, allocatable, storage requests) — the borrowed-normativity
// source the binding engine resolves bars from (doc 04 §3.4). Taken via List calls
// at discovery/re-binding time, never in the hot path; a snapshot is immutable, so
// one Compile sees one consistent config world (determinism per compile).
type ConfigSnapshot struct {
	pods  map[string]binding.PodConfig
	nodes map[string]binding.NodeConfig
	pvcs  map[string]binding.PVCConfig
}

var _ binding.EntityConfig = (*ConfigSnapshot)(nil)

// SnapshotConfig lists pods, nodes, and PVCs and extracts their declared config.
func SnapshotConfig(ctx context.Context, cs kubernetes.Interface) (*ConfigSnapshot, error) {
	snap := &ConfigSnapshot{
		pods:  map[string]binding.PodConfig{},
		nodes: map[string]binding.NodeConfig{},
		pvcs:  map[string]binding.PVCConfig{},
	}

	pods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		pc := binding.PodConfig{}
		for _, c := range p.Spec.Containers {
			cc := binding.ContainerConfig{Name: c.Name}
			if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				cc.MemLimitBytes = q.Value()
			}
			if q, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
				cc.CPULimitMilli = q.MilliValue()
			}
			pc.Containers = append(pc.Containers, cc)
		}
		// Customer-declared application SLO bars (doc 15 cap. A) from vigil.io/slo.*
		// annotations. The value is read verbatim; an unparseable value is SKIPPED so
		// the app pair stays unbounded/listed — never a fabricated bar.
		for k, v := range p.Annotations {
			path, ok := strings.CutPrefix(k, sloAnnotationDomain)
			if !ok || !strings.HasPrefix(path, "slo.") {
				continue
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				continue
			}
			if pc.SLOs == nil {
				pc.SLOs = map[string]float64{}
			}
			pc.SLOs[path] = f
		}
		snap.pods[p.Namespace+"/"+p.Name] = pc
	}

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	for i := range nodes.Items {
		n := &nodes.Items[i]
		nc := binding.NodeConfig{}
		if q, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			nc.AllocatableMemoryBytes = q.Value()
		}
		snap.nodes[n.Name] = nc
	}

	pvcs, err := cs.CoreV1().PersistentVolumeClaims(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pvcs: %w", err)
	}
	for i := range pvcs.Items {
		c := &pvcs.Items[i]
		pc := binding.PVCConfig{}
		if q, ok := c.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			pc.RequestedStorageBytes = q.Value()
		}
		snap.pvcs[c.Namespace+"/"+c.Name] = pc
	}
	return snap, nil
}

func (s *ConfigSnapshot) Pod(namespace, name string) (binding.PodConfig, bool) {
	c, ok := s.pods[namespace+"/"+name]
	return c, ok
}

func (s *ConfigSnapshot) Node(name string) (binding.NodeConfig, bool) {
	c, ok := s.nodes[name]
	return c, ok
}

func (s *ConfigSnapshot) PVC(namespace, name string) (binding.PVCConfig, bool) {
	c, ok := s.pvcs[namespace+"/"+name]
	return c, ok
}

// MaxNodeAllocatableMemory returns the largest node allocatable in the snapshot —
// the cluster's own declared capacity ceiling, used as QA's plausibility bound.
func (s *ConfigSnapshot) MaxNodeAllocatableMemory() int64 {
	var max int64
	for _, n := range s.nodes {
		if n.AllocatableMemoryBytes > max {
			max = n.AllocatableMemoryBytes
		}
	}
	return max
}

// PVCs enumerates the snapshot's claims in deterministic order.
func (s *ConfigSnapshot) PVCs() []binding.PVCRef {
	out := make([]binding.PVCRef, 0, len(s.pvcs))
	for k := range s.pvcs {
		var ns, name string
		for i := 0; i < len(k); i++ {
			if k[i] == '/' {
				ns, name = k[:i], k[i+1:]
				break
			}
		}
		out = append(out, binding.PVCRef{Namespace: ns, Name: name})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}
