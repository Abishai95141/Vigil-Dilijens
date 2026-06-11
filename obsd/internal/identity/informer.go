package identity

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	batchlisters "k8s.io/client-go/listers/batch/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	discoverylisters "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/tools/cache"
)

// Watcher wires client-go SharedInformers for the identity-bearing objects (pods,
// nodes) and their controllers (ReplicaSets, Jobs, needed to resolve role chains)
// into the lifecycle Store. We WATCH; we never reconcile cluster state (doc 14 A2):
// no controller-runtime, no CRDs, no write verbs. Informer events are the primary
// confirmation channel (doc 14 §1.1).
type Watcher struct {
	cluster     string
	store       *Store
	edges       *EdgeStore
	clock       func() time.Time
	gcEvery     time.Duration
	factory     informers.SharedInformerFactory
	rsLister    appslisters.ReplicaSetLister
	jobLister   batchlisters.JobLister
	pvcLister   corelisters.PersistentVolumeClaimLister
	svcLister   corelisters.ServiceLister
	sliceLister discoverylisters.EndpointSliceLister
	podLister   corelisters.PodLister
	nodeLister  corelisters.NodeLister
	podIndexer  cache.Indexer // pods indexed by UID (for the join audit's PodExistsByUID)
	synced      []cache.InformerSynced
	logger      *slog.Logger
}

// podUIDIndex is the informer index name for looking up pods by their UID.
const podUIDIndex = "byUID"

// NewWatcher constructs a Watcher over the given clientset, feeding the identity
// Store (lifecycle) and the EdgeStore (topology). resync is the informer relist
// period (doc 14 §1.1: 30 s dev / 5 min prod), which also drives edge-confirmation
// reconciliation; 0 disables periodic resync. The returned Watcher is inert until
// Run is called.
func NewWatcher(client kubernetes.Interface, store *Store, edges *EdgeStore, cluster string, resync time.Duration, logger *slog.Logger) (*Watcher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	f := informers.NewSharedInformerFactory(client, resync)
	w := &Watcher{
		cluster:     cluster,
		store:       store,
		edges:       edges,
		clock:       store.clock,
		gcEvery:     store.fullRetention, // sweep at least once per full-tombstone window
		factory:     f,
		rsLister:    f.Apps().V1().ReplicaSets().Lister(),
		jobLister:   f.Batch().V1().Jobs().Lister(),
		pvcLister:   f.Core().V1().PersistentVolumeClaims().Lister(),
		svcLister:   f.Core().V1().Services().Lister(),
		sliceLister: f.Discovery().V1().EndpointSlices().Lister(),
		podLister:   f.Core().V1().Pods().Lister(),
		nodeLister:  f.Core().V1().Nodes().Lister(),
		logger:      logger,
	}
	if w.gcEvery <= 0 {
		w.gcEvery = time.Minute
	}

	// Lister-only informers (no handlers) must be instantiated so their caches fill:
	// RS/Job for role chains, PVC for mounts edge targets.
	rsInformer := f.Apps().V1().ReplicaSets().Informer()
	jobInformer := f.Batch().V1().Jobs().Informer()
	pvcInformer := f.Core().V1().PersistentVolumeClaims().Informer()

	podInformer := f.Core().V1().Pods().Informer()
	nodeInformer := f.Core().V1().Nodes().Informer()
	sliceInformer := f.Discovery().V1().EndpointSlices().Informer()
	leaseInformer := f.Coordination().V1().Leases().Informer()
	svcInformer := f.Core().V1().Services().Informer()

	// Index pods by UID so the join audit can verify a container CEI's owning pod
	// exists (doc 03 §6) without a full scan.
	if err := podInformer.AddIndexers(cache.Indexers{
		podUIDIndex: func(obj any) ([]string, error) {
			if pod, ok := obj.(*corev1.Pod); ok {
				return []string{string(pod.UID)}, nil
			}
			return nil, nil
		},
	}); err != nil {
		return nil, fmt.Errorf("add pod uid indexer: %w", err)
	}
	w.podIndexer = podInformer.GetIndexer()

	handlers := []struct {
		name     string
		informer cache.SharedIndexInformer
		funcs    cache.ResourceEventHandlerFuncs
	}{
		{"pod", podInformer, cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj any) { w.upsertPod(obj) },
			UpdateFunc: func(_, obj any) { w.upsertPod(obj) },
			DeleteFunc: w.deletePod,
		}},
		{"node", nodeInformer, cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj any) { w.upsertNode(obj) },
			UpdateFunc: func(_, obj any) { w.upsertNode(obj) },
			DeleteFunc: w.deleteNode,
		}},
		{"endpointslice", sliceInformer, cache.ResourceEventHandlerFuncs{
			AddFunc:    w.onEndpointSliceAdd,
			UpdateFunc: w.onEndpointSliceUpdate,
			DeleteFunc: w.deleteEndpointSlice,
		}},
		{"service", svcInformer, cache.ResourceEventHandlerFuncs{
			AddFunc:    w.onServiceUpsert,
			UpdateFunc: func(_, obj any) { w.onServiceUpsert(obj) },
			DeleteFunc: w.deleteService,
		}},
		{"lease", leaseInformer, cache.ResourceEventHandlerFuncs{
			AddFunc:    w.upsertLease,
			UpdateFunc: func(_, obj any) { w.upsertLease(obj) },
			DeleteFunc: w.deleteLease,
		}},
	}
	for _, h := range handlers {
		if _, err := h.informer.AddEventHandler(h.funcs); err != nil {
			return nil, fmt.Errorf("add %s handler: %w", h.name, err)
		}
	}

	w.synced = []cache.InformerSynced{
		rsInformer.HasSynced, jobInformer.HasSynced, pvcInformer.HasSynced, svcInformer.HasSynced,
		podInformer.HasSynced, nodeInformer.HasSynced, sliceInformer.HasSynced, leaseInformer.HasSynced,
	}
	return w, nil
}

// HasSynced reports whether every informer's initial cache list has completed.
// Useful to gate startup (and to make role resolution deterministic in tests:
// the RS/Job listers must be synced before pods resolve their full role chain).
// Returns false until Run has started the informers.
func (w *Watcher) HasSynced() bool {
	for _, s := range w.synced {
		if !s() {
			return false
		}
	}
	return len(w.synced) > 0
}

// Run starts the informers, waits for the initial cache sync, then blocks until
// ctx is cancelled, periodically GC'ing the store. Returns nil on clean shutdown.
func (w *Watcher) Run(ctx context.Context) error {
	w.factory.Start(ctx.Done())
	for typ, ok := range w.factory.WaitForCacheSync(ctx.Done()) {
		if !ok {
			return fmt.Errorf("identity watcher: cache sync failed for %v", typ)
		}
	}
	w.logger.Info("identity watcher synced", "cluster", w.cluster)

	ticker := time.NewTicker(w.gcEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.store.GC()
			w.edges.GC()
		}
	}
}

// ControllerOf implements ControllerResolver using the cache listers.
func (w *Watcher) ControllerOf(kind, namespace, name string) (OwnerRef, bool) {
	switch kind {
	case "ReplicaSet":
		rs, err := w.rsLister.ReplicaSets(namespace).Get(name)
		if err != nil {
			return OwnerRef{}, false
		}
		return controllerOwnerRef(rs.OwnerReferences)
	case "Job":
		job, err := w.jobLister.Jobs(namespace).Get(name)
		if err != nil {
			return OwnerRef{}, false
		}
		return controllerOwnerRef(job.OwnerReferences)
	default:
		return OwnerRef{}, false
	}
}

func (w *Watcher) upsertPod(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	inst := InstanceCoords{
		Cluster: w.cluster, Namespace: pod.Namespace, Kind: "Pod",
		Name: pod.Name, UID: string(pod.UID),
	}
	roleCoords := ResolvePodRole(w.cluster, pod.Namespace, pod.Name, pod.Spec.NodeName, pod.Annotations, ownerRefs(pod.OwnerReferences), w)
	role, err := MintRole(roleCoords, pod.CreationTimestamp.Time)
	if err != nil {
		w.logger.Warn("identity: pod role minting failed", "pod", pod.Namespace+"/"+pod.Name, "err", err)
		return
	}
	if _, err := w.store.Observe(inst, role, pod.CreationTimestamp.Time, podState(pod)); err != nil {
		w.logger.Warn("identity: pod observe failed", "pod", pod.Namespace+"/"+pod.Name, "err", err)
		return
	}
	w.upsertPodEdges(pod)
}

func (w *Watcher) deletePod(obj any) {
	pod := podFromTombstone(obj)
	if pod == nil {
		return
	}
	inst := InstanceCoords{
		Cluster: w.cluster, Namespace: pod.Namespace, Kind: "Pod",
		Name: pod.Name, UID: string(pod.UID),
	}
	w.store.TerminateInstance(inst, deletionTime(pod.DeletionTimestamp, w.clock))
	w.deletePodEdges(pod)
}

func (w *Watcher) upsertNode(obj any) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		return
	}
	inst := InstanceCoords{
		Cluster: w.cluster, Kind: "Node", Name: node.Name, UID: string(node.UID),
	}
	state := StateActive
	if !nodeReady(node) {
		state = StateDiscovered
	}
	// Nodes have no role layer (no ownership chain); RoleCEI stays zero.
	if _, err := w.store.Observe(inst, CEI{}, node.CreationTimestamp.Time, state); err != nil {
		w.logger.Warn("identity: node observe failed", "node", node.Name, "err", err)
	}
}

func (w *Watcher) deleteNode(obj any) {
	node := nodeFromTombstone(obj)
	if node == nil {
		return
	}
	inst := InstanceCoords{Cluster: w.cluster, Kind: "Node", Name: node.Name, UID: string(node.UID)}
	w.store.TerminateInstance(inst, deletionTime(node.DeletionTimestamp, w.clock))
	w.deleteNodeEdges(node)
}

// --- helpers -----------------------------------------------------------------

func podState(pod *corev1.Pod) LifecycleState {
	if pod.Status.Phase == corev1.PodRunning {
		return StateActive
	}
	return StateDiscovered
}

func nodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// deletionTime prefers the object's own DeletionTimestamp (event time, doc 14 A12);
// when absent it falls back to receive time.
func deletionTime(ts *metav1.Time, clock func() time.Time) time.Time {
	if ts != nil && !ts.Time.IsZero() {
		return ts.Time
	}
	return clock()
}

func ownerRefs(refs []metav1.OwnerReference) []OwnerRef {
	out := make([]OwnerRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, OwnerRef{
			Kind: r.Kind, Name: r.Name, UID: string(r.UID),
			Controller: r.Controller != nil && *r.Controller,
		})
	}
	return out
}

func controllerOwnerRef(refs []metav1.OwnerReference) (OwnerRef, bool) {
	for _, r := range refs {
		if r.Controller != nil && *r.Controller {
			return OwnerRef{Kind: r.Kind, Name: r.Name, UID: string(r.UID), Controller: true}, true
		}
	}
	return OwnerRef{}, false
}

// podFromTombstone extracts a Pod from a delete event, unwrapping the
// DeletedFinalStateUnknown tombstone the informer delivers on a missed delete.
func podFromTombstone(obj any) *corev1.Pod {
	if pod, ok := obj.(*corev1.Pod); ok {
		return pod
	}
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		if pod, ok := t.Obj.(*corev1.Pod); ok {
			return pod
		}
	}
	return nil
}

func nodeFromTombstone(obj any) *corev1.Node {
	if node, ok := obj.(*corev1.Node); ok {
		return node
	}
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		if node, ok := t.Obj.(*corev1.Node); ok {
			return node
		}
	}
	return nil
}
