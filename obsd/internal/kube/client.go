// Package kube builds Kubernetes clientsets for the runtime. It centralizes the
// in-cluster vs out-of-cluster config decision (doc 14 A2: dev runs out-of-cluster
// via kubeconfig; Phase 1 runs in-cluster). Read-only use only — we watch, never
// reconcile.
package kube

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientset builds a clientset. Resolution order:
//   - if kubeconfig is non-empty, load that file (out-of-cluster, dev);
//   - else try in-cluster config (Phase 1 Deployment);
//   - else fall back to the default kubeconfig loading rules (~/.kube/config).
func NewClientset(kubeconfig string) (*kubernetes.Clientset, error) {
	cfg, err := restConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return cs, nil
}

func restConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig %q: %w", kubeconfig, err)
		}
		return cfg, nil
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	// Out-of-cluster default loading rules (KUBECONFIG env or ~/.kube/config).
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load default kubeconfig: %w", err)
	}
	return cfg, nil
}
