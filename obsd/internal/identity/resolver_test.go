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
