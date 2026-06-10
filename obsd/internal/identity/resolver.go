package identity

// Role resolution (doc 03 §3.1): a pod's role anchors on the TOPMOST controller in
// its ownership chain, so the role survives Deployment rollouts and reschedules.
// A pod's own OwnerReferences only name its immediate controller (a ReplicaSet for
// a Deployment, a Job for a CronJob); resolving the rest of the chain needs a
// lookup one level up, abstracted behind ControllerResolver so it is unit-testable
// without informers.

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
