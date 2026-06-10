package identity

import "k8s.io/apimachinery/pkg/labels"

// The Watcher is the join audit's independent control-plane TruthSource (doc 03 §6):
// it answers from the raw informer listers — the actual cluster state — NOT from the
// lifecycle Store. The audit cross-checks our identity model against this truth, so a
// divergence is a genuine model bug rather than a self-consistent echo.
var _ TruthSource = (*Watcher)(nil)

// PodUID returns the current UID of the pod named (namespace, name) per the cluster.
func (w *Watcher) PodUID(namespace, name string) (string, bool) {
	pod, err := w.podLister.Pods(namespace).Get(name)
	if err != nil {
		return "", false
	}
	return string(pod.UID), true
}

// NodeUID returns the current UID of the node named `name` per the cluster.
func (w *Watcher) NodeUID(name string) (string, bool) {
	node, err := w.nodeLister.Get(name)
	if err != nil {
		return "", false
	}
	return string(node.UID), true
}

// PodExistsByUID reports whether a pod with this UID currently exists.
func (w *Watcher) PodExistsByUID(uid string) bool {
	if w.podIndexer == nil {
		return false
	}
	objs, err := w.podIndexer.ByIndex(podUIDIndex, uid)
	return err == nil && len(objs) > 0
}

// ListPods enumerates all current pods.
func (w *Watcher) ListPods() []EntityRef {
	pods, err := w.podLister.List(labels.Everything())
	if err != nil {
		return nil
	}
	out := make([]EntityRef, 0, len(pods))
	for _, p := range pods {
		out = append(out, EntityRef{Namespace: p.Namespace, Name: p.Name, UID: string(p.UID)})
	}
	return out
}

// ListNodes enumerates all current nodes.
func (w *Watcher) ListNodes() []EntityRef {
	nodes, err := w.nodeLister.List(labels.Everything())
	if err != nil {
		return nil
	}
	out := make([]EntityRef, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, EntityRef{Name: n.Name, UID: string(n.UID)})
	}
	return out
}

// AuditConsistencyNow runs the consistency audit against current control-plane truth
// at the watcher's clock — the live counterpart of AuditConsistency, used by the
// metrics collector and the Phase-0a exit-gate check. Before the informer caches
// sync, the listers read empty; rather than mistake that for a clean empty cluster,
// it returns a NOT-READY report (which can never pass the gate).
func (w *Watcher) AuditConsistencyNow() ConsistencyReport {
	if !w.HasSynced() {
		return ConsistencyReport{Ready: false, At: w.clock()}
	}
	return AuditConsistency(w.store, w, w.clock())
}
