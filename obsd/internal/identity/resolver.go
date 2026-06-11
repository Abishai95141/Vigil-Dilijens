package identity

import "strings"

// Role resolution (doc 03 §3.1): a pod's role anchors on the TOPMOST controller in
// its ownership chain, so the role survives Deployment rollouts and reschedules.
// A pod's own OwnerReferences only name its immediate controller (a ReplicaSet for
// a Deployment, a Job for a CronJob); resolving the rest of the chain needs a
// lookup one level up, abstracted behind ControllerResolver so it is unit-testable
// without informers.
//
// Static (mirror) pods are the one exception to "anchor on the topmost controller":
// the kubelet owns a static pod's API-server mirror with an ownerReference to the
// Node it runs on (controller=true), so the generic rule would group every static
// pod on a control-plane node (etcd, kube-apiserver, kube-controller-manager,
// kube-scheduler) under a single Node/<node> role. They are distinct logical
// functions, so they resolve to distinct StaticPod/<component> roles instead.

// ControllerResolver resolves the controlling owner of an intermediate controller
// object one level up the chain (e.g. ReplicaSet -> Deployment, Job -> CronJob).
// The informer wiring backs this with cache listers; tests back it with a fake.
type ControllerResolver interface {
	// ControllerOf returns the controlling OwnerRef of the named object of the
	// given kind, if it is known. A miss (object not yet in cache, or no controller)
	// returns ok=false and the chain stops there.
	ControllerOf(kind, namespace, name string) (OwnerRef, bool)
}

// maxChainDepth bounds the walk, guarding against pathological or cyclic owner
// graphs (CRD operators can nest controllers).
const maxChainDepth = 6

// MirrorPodAnnotation is the annotation the kubelet stamps on the API-server mirror
// of a static pod (the value is the mirror hash). Its presence identifies a static
// (mirror) pod.
const MirrorPodAnnotation = "kubernetes.io/config.mirror"

// ResolvePodRole resolves a pod's role coordinates (doc 03 §3.1) — the single role
// entry point used by the informer wiring. Static (mirror) pods resolve to a distinct
// StaticPod/<component> role; every other pod anchors on the topmost controller in its
// ownership chain (a bare, ownerless pod falls back to Pod/<name>, doc 14 A10). For a
// non-mirror pod this is exactly ResolveChain followed by DeriveRole.
func ResolvePodRole(cluster, namespace, podName, nodeName string, annotations map[string]string, podOwners []OwnerRef, r ControllerResolver) RoleCoords {
	if IsMirrorPod(annotations, podOwners) {
		return StaticPodRole(cluster, namespace, podName, nodeName)
	}
	chain := ResolveChain(namespace, podName, podOwners, r)
	return DeriveRole(cluster, namespace, podName, chain)
}

// IsMirrorPod reports whether a pod is a static pod's API-server mirror, from its
// annotations and owner references. Either signal is sufficient: the kubelet stamps
// the mirror annotation AND owns the mirror with a controller ownerReference to the
// Node it runs on.
func IsMirrorPod(annotations map[string]string, owners []OwnerRef) bool {
	if _, ok := annotations[MirrorPodAnnotation]; ok {
		return true
	}
	for _, o := range owners {
		if o.Controller && o.Kind == "Node" {
			return true
		}
	}
	return false
}

// StaticPodRole returns the role coordinates for a static (mirror) pod: a distinct
// StaticPod/<component> role rather than the Node owner. The component is the pod name
// with its "-<nodeName>" mirror suffix removed — the kubelet names a mirror pod
// "<manifest-name>-<nodeName>" — so across an HA control plane "kube-apiserver" is one
// role with one instance per node (like a DaemonSet), not N node-specific roles. A
// static pod is not bare (it has an owner), so Bare stays false.
func StaticPodRole(cluster, namespace, podName, nodeName string) RoleCoords {
	component := podName
	if nodeName != "" {
		component = strings.TrimSuffix(podName, "-"+nodeName)
	}
	return RoleCoords{
		Cluster:   cluster,
		Namespace: namespace,
		Kind:      "StaticPod",
		RoleKey:   "StaticPod/" + component,
	}
}

// ResolveChain builds the ownership chain (immediate -> top) for a pod from its
// own OwnerReferences plus upward resolution. Returns nil for a bare (ownerless)
// pod. The chain is what DeriveRole anchors the role on.
func ResolveChain(namespace, podName string, podOwners []OwnerRef, r ControllerResolver) []OwnerRef {
	cur, ok := controllerRef(podOwners)
	if !ok {
		return nil // bare pod (doc 14 A10)
	}
	chain := []OwnerRef{cur}
	for depth := 0; depth < maxChainDepth; depth++ {
		if !resolvableFurther(cur.Kind) || r == nil {
			break
		}
		next, ok := r.ControllerOf(cur.Kind, namespace, cur.Name)
		if !ok {
			// Cannot resolve further (e.g. the intermediate controller is not yet
			// in cache): anchor on what we have. A Deployment pod whose ReplicaSet
			// is unresolved falls back to a ReplicaSet-anchored role — degraded but
			// not wrong; it self-corrects once the cache populates and the pod is
			// re-observed.
			break
		}
		chain = append(chain, next)
		cur = next
	}
	return chain
}

// resolvableFurther reports whether a controller kind has a controller above it
// that we resolve. ReplicaSet -> Deployment and Job -> CronJob are the standard
// two-level chains; StatefulSet, DaemonSet, and CRD controllers own pods directly
// and are their own role anchor.
func resolvableFurther(kind string) bool {
	return kind == "ReplicaSet" || kind == "Job"
}

func controllerRef(owners []OwnerRef) (OwnerRef, bool) {
	for _, o := range owners {
		if o.Controller {
			return o, true
		}
	}
	return OwnerRef{}, false
}
