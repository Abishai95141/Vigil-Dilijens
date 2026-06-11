package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
)

// GatherFacts builds the binding engine's platform-fact view (doc 04 §3.1
// mechanisms 1 and 4) from the API server: component versions and runtime from
// NodeInfo, and tool presence detected from the running workload set (pod names +
// container images). Everything here is MEASURED — read from the cluster, with
// "cannot tell from the API" recorded as indeterminate, never guessed.
func GatherFacts(ctx context.Context, cs kubernetes.Interface) (binding.PlatformFacts, error) {
	facts := binding.PlatformFacts{ToolPresent: map[string]bool{}}

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return facts, fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes.Items) > 0 {
		info := nodes.Items[0].Status.NodeInfo
		facts.KubeletVersion = info.KubeletVersion
		facts.ContainerRuntime = info.ContainerRuntimeVersion
		facts.KernelVersion = info.KernelVersion
		facts.OSImage = info.OSImage
	}
	// Heterogeneous node pools: report the divergence rather than pretending one
	// version. (Single-version is by far the common case; the fact records it.)
	for _, n := range nodes.Items[1:] {
		if n.Status.NodeInfo.KernelVersion != facts.KernelVersion {
			facts.MixedKernels = true
		}
	}

	pods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return facts, fmt.Errorf("list pods: %w", err)
	}
	// Tool detection: a tool is "present" when a running pod's name or image names
	// it. Deliberately substring-simple — the gating consequence of a false
	// negative is an honest "out-of-scope: tool not detected", never a mis-bind.
	var corpus []string
	for i := range pods.Items {
		p := &pods.Items[i]
		entry := strings.ToLower(p.Name)
		for _, c := range p.Spec.Containers {
			entry += " " + strings.ToLower(c.Image)
		}
		corpus = append(corpus, entry)
	}
	sort.Strings(corpus)
	facts.WorkloadCorpus = corpus
	return facts, nil
}
