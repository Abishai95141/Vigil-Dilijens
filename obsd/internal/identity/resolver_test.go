package identity

import "testing"

// fakeResolver maps (kind|ns|name) -> controlling owner, standing in for the
// cache listers.
type fakeResolver struct{ m map[string]OwnerRef }

func (f fakeResolver) ControllerOf(kind, namespace, name string) (OwnerRef, bool) {
	o, ok := f.m[kind+"|"+namespace+"|"+name]
	return o, ok
}

func ctrl(kind, name, uid string) OwnerRef {
	return OwnerRef{Kind: kind, Name: name, UID: uid, Controller: true}
}

func TestResolveChainDeployment(t *testing.T) {
	r := fakeResolver{m: map[string]OwnerRef{
		"ReplicaSet|shop|web-abc": ctrl("Deployment", "web", "dep-uid"),
	}}
	chain := ResolveChain("shop", "web-x", []OwnerRef{ctrl("ReplicaSet", "web-abc", "rs-uid")}, r)
	role := DeriveRole(cluster, "shop", "web-x", chain)
	if role.RoleKey != "Deployment/web" || role.Bare {
		t.Errorf("role = %+v, want Deployment/web (not bare)", role)
	}
}

func TestResolveChainCronJob(t *testing.T) {
	r := fakeResolver{m: map[string]OwnerRef{
		"Job|shop|backup-123": ctrl("CronJob", "backup", "cj-uid"),
	}}
	chain := ResolveChain("shop", "backup-123-xyz", []OwnerRef{ctrl("Job", "backup-123", "job-uid")}, r)
	role := DeriveRole(cluster, "shop", "backup-123-xyz", chain)
	if role.RoleKey != "CronJob/backup" {
		t.Errorf("role = %q, want CronJob/backup", role.RoleKey)
	}
}

func TestResolveChainStatefulSetDirect(t *testing.T) {
	// A StatefulSet owns pods directly; nothing to resolve upward.
	chain := ResolveChain("shop", "redis-0", []OwnerRef{ctrl("StatefulSet", "redis", "ss-uid")}, fakeResolver{})
	role := DeriveRole(cluster, "shop", "redis-0", chain)
	if role.RoleKey != "StatefulSet/redis" {
		t.Errorf("role = %q, want StatefulSet/redis", role.RoleKey)
	}
}

func TestResolveChainBarePod(t *testing.T) {
	chain := ResolveChain("shop", "lonely", nil, fakeResolver{})
	if chain != nil {
		t.Errorf("bare pod chain = %v, want nil", chain)
	}
	role := DeriveRole(cluster, "shop", "lonely", chain)
	if role.RoleKey != "Pod/lonely" || !role.Bare {
		t.Errorf("role = %+v, want bare Pod/lonely", role)
	}
}

func TestResolveChainNonControllerIgnored(t *testing.T) {
	// An owner reference that is not the controller does not anchor the role.
	chain := ResolveChain("shop", "x", []OwnerRef{{Kind: "ReplicaSet", Name: "rs", Controller: false}}, fakeResolver{})
	role := DeriveRole(cluster, "shop", "x", chain)
	if !role.Bare {
		t.Errorf("non-controller owner should yield a bare role, got %+v", role)
	}
}

// When the intermediate controller is not yet in cache, the role falls back to the
// immediate controller (degraded but not wrong) and self-corrects on re-observe.
func TestResolveChainDeploymentDegradedFallback(t *testing.T) {
	chain := ResolveChain("shop", "web-x", []OwnerRef{ctrl("ReplicaSet", "web-abc", "rs-uid")}, fakeResolver{})
	role := DeriveRole(cluster, "shop", "web-x", chain)
	if role.RoleKey != "ReplicaSet/web-abc" {
		t.Errorf("degraded role = %q, want ReplicaSet/web-abc fallback", role.RoleKey)
	}
}

// A static (mirror) pod is detected by the mirror annotation OR a controller
// ownerReference to a Node; either signal alone is sufficient, and a normal pod with
// neither is not a mirror pod.
func TestIsMirrorPod(t *testing.T) {
	nodeOwner := []OwnerRef{ctrl("Node", "cp-1", "node-uid")}
	rsOwner := []OwnerRef{ctrl("ReplicaSet", "web-abc", "rs-uid")}
	cases := []struct {
		name string
		ann  map[string]string
		own  []OwnerRef
		want bool
	}{
		{"annotation only", map[string]string{MirrorPodAnnotation: "deadbeef"}, nil, true},
		{"node owner only", nil, nodeOwner, true},
		{"both signals", map[string]string{MirrorPodAnnotation: "deadbeef"}, nodeOwner, true},
		{"normal controller", nil, rsOwner, false},
		{"bare pod", nil, nil, false},
		// A non-controller Node owner is not a mirror signal (mirror owners are controllers).
		{"non-controller node owner", nil, []OwnerRef{{Kind: "Node", Name: "cp-1", Controller: false}}, false},
	}
	for _, c := range cases {
		if got := IsMirrorPod(c.ann, c.own); got != c.want {
			t.Errorf("%s: IsMirrorPod = %v, want %v", c.name, got, c.want)
		}
	}
}

// StaticPodRole strips the "-<nodeName>" mirror suffix to yield the component role,
// so etcd/kube-apiserver/... are distinct roles; it falls back safely when the suffix
// is absent or the node name is unknown.
func TestStaticPodRole(t *testing.T) {
	cases := []struct {
		pod, node, wantKey string
	}{
		{"kube-apiserver-vigil-control-plane", "vigil-control-plane", "StaticPod/kube-apiserver"},
		{"etcd-vigil-control-plane", "vigil-control-plane", "StaticPod/etcd"},
		// HA control plane: the same component on a different node is the SAME role.
		{"kube-apiserver-cp-2", "cp-2", "StaticPod/kube-apiserver"},
		// Unknown node name → keep the full pod name, still distinct (not Node-grouped).
		{"kube-scheduler-cp-1", "", "StaticPod/kube-scheduler-cp-1"},
		// Suffix not present → no-op trim, full name retained.
		{"weird-static", "other-node", "StaticPod/weird-static"},
	}
	for _, c := range cases {
		got := StaticPodRole(cluster, "kube-system", c.pod, c.node)
		if got.Kind != "StaticPod" || got.RoleKey != c.wantKey || got.Bare {
			t.Errorf("StaticPodRole(%q,%q) = %+v, want Kind=StaticPod RoleKey=%q Bare=false", c.pod, c.node, got, c.wantKey)
		}
	}
}

// ResolvePodRole routes mirror pods to a distinct StaticPod role (via either signal)
// while resolving normal pods exactly as ResolveChain+DeriveRole would — the two
// control-plane static pods no longer collapse onto one Node role.
func TestResolvePodRoleMirrorVsNormal(t *testing.T) {
	r := fakeResolver{m: map[string]OwnerRef{
		"ReplicaSet|shop|web-abc": ctrl("Deployment", "web", "dep-uid"),
	}}
	node := "vigil-control-plane"
	nodeOwner := []OwnerRef{ctrl("Node", node, "node-uid")}

	// Two distinct static pods on the same node must yield two distinct roles, not one
	// shared Node/<node> role (the bug being fixed).
	api := ResolvePodRole(cluster, "kube-system", "kube-apiserver-"+node, node, map[string]string{MirrorPodAnnotation: "h1"}, nodeOwner, r)
	etcd := ResolvePodRole(cluster, "kube-system", "etcd-"+node, node, nil, nodeOwner, r)
	if api.RoleKey != "StaticPod/kube-apiserver" {
		t.Errorf("apiserver role = %q, want StaticPod/kube-apiserver", api.RoleKey)
	}
	if etcd.RoleKey != "StaticPod/etcd" {
		t.Errorf("etcd role = %q, want StaticPod/etcd", etcd.RoleKey)
	}
	if api.RoleKey == etcd.RoleKey {
		t.Error("distinct static pods collapsed onto one role")
	}

	// A normal Deployment pod is unaffected: same result as the direct chain path.
	normal := ResolvePodRole(cluster, "shop", "web-x", "worker-1", nil, []OwnerRef{ctrl("ReplicaSet", "web-abc", "rs-uid")}, r)
	if normal.RoleKey != "Deployment/web" || normal.Bare {
		t.Errorf("normal pod role = %+v, want Deployment/web (not bare)", normal)
	}
}

// A cyclic owner graph must terminate at the depth cap, not loop forever.
func TestResolveChainCycleTerminates(t *testing.T) {
	r := fakeResolver{m: map[string]OwnerRef{
		"ReplicaSet|shop|a": ctrl("ReplicaSet", "a", "uid-a"), // self-cycle
	}}
	chain := ResolveChain("shop", "p", []OwnerRef{ctrl("ReplicaSet", "a", "uid-a")}, r)
	if len(chain) > 1+maxChainDepth {
		t.Errorf("chain length %d exceeds depth cap", len(chain))
	}
}
