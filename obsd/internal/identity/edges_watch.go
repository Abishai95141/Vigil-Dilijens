package identity

import (
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// Edge derivation from informer events (doc 03 §3.5, doc 14 §1.1). Informer
// Add/Update assert/confirm edges (resync re-delivery is the reconciliation
// backstop, doc 14 §1.1 ch2); deletes and observed changes retract. Edge times use
// receive time (w.clock(), doc 14 A12) except node-lease, which uses the lease's
// own RenewTime when present. When a referenced entity (node, PVC, service) is not
// yet in cache the edge is skipped and retried on the next resync — never guessed.

const nodeLeaseNamespace = "kube-node-lease"

func (w *Watcher) podInstanceCEI(pod *corev1.Pod) (CEI, bool) {
	cei, err := MintInstance(InstanceCoords{
		Cluster: w.cluster, Namespace: pod.Namespace, Kind: "Pod", Name: pod.Name, UID: string(pod.UID),
	}, w.clock())
	return cei, err == nil
}

// upsertPodEdges asserts the pod's topology edges: runs-on (single node, reconciled
// so a reschedule retracts the old node) and mounts (its PVCs; the volume set is
// immutable for a pod's life, so we assert/confirm).
func (w *Watcher) upsertPodEdges(pod *corev1.Pod) {
	now := w.clock()
	podCEI, ok := w.podInstanceCEI(pod)
	if !ok {
		return
	}

	if pod.Spec.NodeName != "" {
		if nodeUID, ok := w.store.NodeUID(pod.Spec.NodeName, now); ok {
			if nodeCEI, err := MintInstance(InstanceCoords{
				Cluster: w.cluster, Kind: "Node", Name: pod.Spec.NodeName, UID: nodeUID,
			}, now); err == nil {
				w.edges.ReconcileOut(EdgeRunsOn, podCEI, []CEI{nodeCEI}, now)
			}
		}
		// Node not yet known: skip; the next resync/update retries once it syncs.
	} else {
		w.edges.ReconcileOut(EdgeRunsOn, podCEI, nil, now) // unscheduled -> no runs-on
	}

	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim == nil {
			continue
		}
		if pvcCEI, ok := w.pvcCEI(pod.Namespace, v.PersistentVolumeClaim.ClaimName, now); ok {
			w.edges.Assert(EdgeMounts, podCEI, pvcCEI, now)
		}
	}
}

func (w *Watcher) deletePodEdges(pod *corev1.Pod) {
	if podCEI, ok := w.podInstanceCEI(pod); ok {
		w.edges.RetractAllFrom(podCEI.Key(), w.clock())
	}
}

func (w *Watcher) pvcCEI(namespace, name string, at time.Time) (CEI, bool) {
	pvc, err := w.pvcLister.PersistentVolumeClaims(namespace).Get(name)
	if err != nil {
		return CEI{}, false
	}
	cei, err := MintInstance(InstanceCoords{
		Cluster: w.cluster, Namespace: namespace, Kind: "PersistentVolumeClaim", Name: name, UID: string(pvc.UID),
	}, at)
	return cei, err == nil
}

// --- selects (service -> pod), from EndpointSlice ----------------------------

func (w *Watcher) onEndpointSliceAdd(obj any)       { w.onEndpointSlice(obj) }
func (w *Watcher) onEndpointSliceUpdate(_, obj any) { w.onEndpointSlice(obj) }

func (w *Watcher) onEndpointSlice(obj any) {
	if slice, ok := obj.(*discoveryv1.EndpointSlice); ok {
		w.reconcileService(slice.Namespace, slice.Labels[discoveryv1.LabelServiceName])
	}
}

func (w *Watcher) deleteEndpointSlice(obj any) {
	if slice := endpointSliceFromTombstone(obj); slice != nil {
		// The informer updates its store before notifying, so the lister no longer
		// contains the deleted slice — reconcile sees the remaining slices.
		w.reconcileService(slice.Namespace, slice.Labels[discoveryv1.LabelServiceName])
	}
}

func (w *Watcher) onServiceUpsert(obj any) {
	if svc, ok := obj.(*corev1.Service); ok {
		w.reconcileService(svc.Namespace, svc.Name)
	}
}

func (w *Watcher) deleteService(obj any) {
	svc := serviceFromTombstone(obj)
	if svc == nil {
		return
	}
	cei, err := MintInstance(InstanceCoords{
		Cluster: w.cluster, Namespace: svc.Namespace, Kind: "Service", Name: svc.Name, UID: string(svc.UID),
	}, w.clock())
	if err == nil {
		w.edges.RetractAllFrom(cei.Key(), w.clock()) // all selects from this service
	}
}

// reconcileService recomputes the full selects(service -> pod) edge set from ALL of
// the service's EndpointSlices at once and ReconcileOut's it. A single slice is NOT
// authoritative: the EndpointSlice controller caps slices (~100 endpoints) and
// repacks/splits/merges them, so a pod's endpoint moves between slices of the same
// service. Reconciling against the union across slices (the lister's post-event
// state) is correct under that churn — a per-slice diff would tear down a still-live
// edge on rebalance (the trust failure of suppressing a real service->pod match).
func (w *Watcher) reconcileService(namespace, svcName string) {
	if svcName == "" {
		return
	}
	now := w.clock()
	svcCEI, ok := w.serviceCEIByName(namespace, svcName, now)
	if !ok {
		return
	}
	sel := labels.SelectorFromSet(labels.Set{discoveryv1.LabelServiceName: svcName})
	slices, err := w.sliceLister.EndpointSlices(namespace).List(sel)
	if err != nil {
		return
	}
	var pods []CEI
	seen := make(map[string]struct{})
	for _, slice := range slices {
		for _, ep := range slice.Endpoints {
			podCEI, ok := w.podCEIFromEndpoint(namespace, ep, now)
			if !ok {
				continue
			}
			if _, dup := seen[podCEI.Key()]; dup {
				continue
			}
			seen[podCEI.Key()] = struct{}{}
			pods = append(pods, podCEI)
		}
	}
	w.edges.ReconcileOut(EdgeSelects, svcCEI, pods, now)
}

func (w *Watcher) serviceCEIByName(namespace, svcName string, at time.Time) (CEI, bool) {
	svc, err := w.svcLister.Services(namespace).Get(svcName)
	if err != nil {
		return CEI{}, false
	}
	cei, err := MintInstance(InstanceCoords{
		Cluster: w.cluster, Namespace: namespace, Kind: "Service", Name: svcName, UID: string(svc.UID),
	}, at)
	return cei, err == nil
}

func (w *Watcher) podCEIFromEndpoint(namespace string, ep discoveryv1.Endpoint, at time.Time) (CEI, bool) {
	if ep.TargetRef == nil || ep.TargetRef.Kind != "Pod" {
		return CEI{}, false
	}
	cei, err := MintInstance(InstanceCoords{
		Cluster: w.cluster, Namespace: namespace, Kind: "Pod", Name: ep.TargetRef.Name, UID: string(ep.TargetRef.UID),
	}, at)
	return cei, err == nil
}

// --- node-lease (node liveness), from Lease ----------------------------------

func (w *Watcher) upsertLease(obj any) {
	lease, ok := obj.(*coordinationv1.Lease)
	if !ok || lease.Namespace != nodeLeaseNamespace {
		return
	}
	nodeCEI, ok := w.nodeCEIByName(lease.Name)
	if !ok {
		return
	}
	// A node-lease is a self-edge whose freshness IS node liveness; confirm it at the
	// lease's own renew time (its 40s budget mirrors the node-monitor grace period).
	w.edges.Assert(EdgeNodeLease, nodeCEI, nodeCEI, leaseRenewTime(lease, w.clock()))
}

func (w *Watcher) deleteLease(obj any) {
	lease := leaseFromTombstone(obj)
	if lease == nil || lease.Namespace != nodeLeaseNamespace {
		return
	}
	if nodeCEI, ok := w.nodeCEIByName(lease.Name); ok {
		w.edges.Retract(EdgeNodeLease, nodeCEI.Key(), nodeCEI.Key(), w.clock())
	}
}

// deleteNodeEdges retracts everything touching a deleted node: runs-on edges into it
// and its node-lease self-edge.
func (w *Watcher) deleteNodeEdges(node *corev1.Node) {
	now := w.clock()
	nodeCEI, err := MintInstance(InstanceCoords{Cluster: w.cluster, Kind: "Node", Name: node.Name, UID: string(node.UID)}, now)
	if err != nil {
		return
	}
	w.edges.RetractAllTo(nodeCEI.Key(), now)
	w.edges.RetractAllFrom(nodeCEI.Key(), now)
}

func (w *Watcher) nodeCEIByName(name string) (CEI, bool) {
	now := w.clock()
	nodeUID, ok := w.store.NodeUID(name, now)
	if !ok {
		return CEI{}, false
	}
	cei, err := MintInstance(InstanceCoords{Cluster: w.cluster, Kind: "Node", Name: name, UID: nodeUID}, now)
	return cei, err == nil
}

func leaseRenewTime(lease *coordinationv1.Lease, fallback time.Time) time.Time {
	if lease.Spec.RenewTime != nil && !lease.Spec.RenewTime.Time.IsZero() {
		return lease.Spec.RenewTime.Time
	}
	return fallback
}

func endpointSliceFromTombstone(obj any) *discoveryv1.EndpointSlice {
	if s, ok := obj.(*discoveryv1.EndpointSlice); ok {
		return s
	}
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		if s, ok := t.Obj.(*discoveryv1.EndpointSlice); ok {
			return s
		}
	}
	return nil
}

func leaseFromTombstone(obj any) *coordinationv1.Lease {
	if l, ok := obj.(*coordinationv1.Lease); ok {
		return l
	}
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		if l, ok := t.Obj.(*coordinationv1.Lease); ok {
			return l
		}
	}
	return nil
}

func serviceFromTombstone(obj any) *corev1.Service {
	if s, ok := obj.(*corev1.Service); ok {
		return s
	}
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		if s, ok := t.Obj.(*corev1.Service); ok {
			return s
		}
	}
	return nil
}
