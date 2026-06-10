package identity

import (
	"testing"
	"time"
)

// fixed, injected discovery time — identity must be independent of it.
var t0 = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

const cluster = "kube-system-uid-1234" // cluster id = kube-system ns UID (doc 14 A9)

func mustInstance(t *testing.T, c InstanceCoords) CEI {
	t.Helper()
	cei, err := MintInstance(c, t0)
	if err != nil {
		t.Fatalf("MintInstance(%+v): %v", c, err)
	}
	return cei
}

// MintedAt is metadata, not identity: the same coordinates minted at different
// times produce equal identities. (This is what makes late-sample joins correct.)
func TestIdentityIndependentOfMintTime(t *testing.T) {
	c := InstanceCoords{Cluster: cluster, Namespace: "default", Kind: "Pod", Name: "cart-1", UID: "uid-A"}
	a, _ := MintInstance(c, t0)
	b, _ := MintInstance(c, t0.Add(48*time.Hour))
	if !a.Same(b) {
		t.Errorf("identity changed with mint time: %q vs %q", a.Key(), b.Key())
	}
}

// Reschedule: an instance moving between nodes keeps its identity. Node placement
// is topology (doc 03 §3.5), never an identity coordinate — so the CEI has no node
// field and the key is invariant to where the pod runs.
func TestReschedulePreservesIdentity(t *testing.T) {
	// Same pod (same UID) before and after rescheduling — coordinates are identical
	// because node is not among them. The key must be stable.
	c := InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: "currencyservice-x", UID: "uid-resched"}
	before := mustInstance(t, c)
	after := mustInstance(t, c)
	if !before.Same(after) {
		t.Errorf("reschedule changed identity: %q vs %q", before.Key(), after.Key())
	}
}

// Scale: new replicas are new instances under the SAME role. Different instance
// CEIs, one shared role CEI.
func TestScaleNewInstancesShareRole(t *testing.T) {
	chain := []OwnerRef{
		{Kind: "ReplicaSet", Name: "currencyservice-abc", UID: "rs-1", Controller: true},
		{Kind: "Deployment", Name: "currencyservice", UID: "dep-1", Controller: true},
	}
	role, _ := MintRole(DeriveRole(cluster, "shop", "currencyservice-1", chain), t0)

	a := mustInstance(t, InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: "currencyservice-1", UID: "uid-1"})
	b := mustInstance(t, InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: "currencyservice-2", UID: "uid-2"})

	if a.Same(b) {
		t.Errorf("two replicas collapsed to one instance CEI: %q", a.Key())
	}
	roleB, _ := MintRole(DeriveRole(cluster, "shop", "currencyservice-2", chain), t0)
	if !role.Same(roleB) {
		t.Errorf("replicas of one workload got different role CEIs: %q vs %q", role.Key(), roleB.Key())
	}
}

// THE HONEYPOT (doc 14 §3.2): a pod deleted and recreated with the SAME name but a
// new UID must mint a DIFFERENT instance CEI (so late samples from the dead pod
// never mis-join to the new one) while keeping the SAME role CEI.
func TestRecreateSameNameIsNewInstanceSameRole(t *testing.T) {
	chainV1 := []OwnerRef{
		{Kind: "ReplicaSet", Name: "currencyservice-abc", UID: "rs-1", Controller: true},
		{Kind: "Deployment", Name: "currencyservice", UID: "dep-1", Controller: true},
	}
	// Same name, different UID (and a different ReplicaSet, as a rollout would do).
	chainV2 := []OwnerRef{
		{Kind: "ReplicaSet", Name: "currencyservice-def", UID: "rs-2", Controller: true},
		{Kind: "Deployment", Name: "currencyservice", UID: "dep-1", Controller: true},
	}

	const name = "currencyservice-x"
	old := mustInstance(t, InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: name, UID: "uid-OLD"})
	recreated := mustInstance(t, InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: name, UID: "uid-NEW"})

	if old.Same(recreated) {
		t.Fatalf("MIS-JOIN: recreated-same-name pod shares an instance CEI with the dead one: %q", old.Key())
	}

	roleOld, _ := MintRole(DeriveRole(cluster, "shop", name, chainV1), t0)
	roleNew, _ := MintRole(DeriveRole(cluster, "shop", name, chainV2), t0)
	if !roleOld.Same(roleNew) {
		t.Errorf("role identity should survive recreate/rollout: %q vs %q", roleOld.Key(), roleNew.Key())
	}
}

// Role anchors on the TOPMOST controller, so it is stable across Deployment
// rollouts (the ReplicaSet changes, the role does not).
func TestRoleStableAcrossRollout(t *testing.T) {
	rsA := []OwnerRef{
		{Kind: "ReplicaSet", Name: "web-aaa", UID: "rs-a", Controller: true},
		{Kind: "Deployment", Name: "web", UID: "dep", Controller: true},
	}
	rsB := []OwnerRef{
		{Kind: "ReplicaSet", Name: "web-bbb", UID: "rs-b", Controller: true},
		{Kind: "Deployment", Name: "web", UID: "dep", Controller: true},
	}
	a := DeriveRole(cluster, "shop", "web-1", rsA)
	b := DeriveRole(cluster, "shop", "web-2", rsB)
	if a.RoleKey != b.RoleKey || a.RoleKey != "Deployment/web" {
		t.Errorf("role not stable across rollout: %q vs %q", a.RoleKey, b.RoleKey)
	}
}

func TestRoleDerivation(t *testing.T) {
	cases := []struct {
		name     string
		chain    []OwnerRef
		wantKey  string
		wantBare bool
	}{
		{
			name:    "deployment-managed anchors on the deployment",
			chain:   []OwnerRef{{Kind: "ReplicaSet", Name: "x-1", UID: "rs", Controller: true}, {Kind: "Deployment", Name: "x", UID: "d", Controller: true}},
			wantKey: "Deployment/x",
		},
		{
			name:    "statefulset",
			chain:   []OwnerRef{{Kind: "StatefulSet", Name: "redis", UID: "ss", Controller: true}},
			wantKey: "StatefulSet/redis",
		},
		{
			name:     "bare pod falls back to Pod/name, flagged bare",
			chain:    nil,
			wantKey:  "Pod/lonely",
			wantBare: true,
		},
		{
			name:     "non-controller owners are ignored (bare)",
			chain:    []OwnerRef{{Kind: "Service", Name: "svc", UID: "s", Controller: false}},
			wantKey:  "Pod/lonely",
			wantBare: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveRole(cluster, "shop", "lonely", tc.chain)
			if got.RoleKey != tc.wantKey {
				t.Errorf("RoleKey = %q, want %q", got.RoleKey, tc.wantKey)
			}
			if got.Bare != tc.wantBare {
				t.Errorf("Bare = %v, want %v", got.Bare, tc.wantBare)
			}
		})
	}
}

// Cluster-scoped kinds (Node) carry no namespace; identity still works.
func TestClusterScopedKind(t *testing.T) {
	node, err := MintInstance(InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-1", UID: "node-uid"}, t0)
	if err != nil {
		t.Fatalf("mint node: %v", err)
	}
	if node.Namespace != "" {
		t.Errorf("node namespace should be empty, got %q", node.Namespace)
	}
	if node.Key() != "i|"+cluster+"||Node|worker-1|node-uid" {
		t.Errorf("unexpected node key: %q", node.Key())
	}
}

// Instance and role key spaces are disjoint and never collide.
func TestInstanceAndRoleKeysDisjoint(t *testing.T) {
	inst := mustInstance(t, InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: "x", UID: "u"})
	role, _ := MintRole(RoleCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", RoleKey: "Pod/x"}, t0)
	if inst.Key() == role.Key() {
		t.Errorf("instance and role keys collided: %q", inst.Key())
	}
}

func TestMintValidation(t *testing.T) {
	t.Run("instance requires uid", func(t *testing.T) {
		if _, err := MintInstance(InstanceCoords{Cluster: cluster, Kind: "Pod", Name: "x"}, t0); err == nil {
			t.Error("expected error for missing UID")
		}
	})
	t.Run("instance requires name", func(t *testing.T) {
		if _, err := MintInstance(InstanceCoords{Cluster: cluster, Kind: "Pod", UID: "u"}, t0); err == nil {
			t.Error("expected error for missing name")
		}
	})
	t.Run("separator injection rejected", func(t *testing.T) {
		if _, err := MintInstance(InstanceCoords{Cluster: cluster, Kind: "Pod", Name: "a|b", UID: "u"}, t0); err == nil {
			t.Error("expected error for separator in name")
		}
	})
	t.Run("role requires role key", func(t *testing.T) {
		if _, err := MintRole(RoleCoords{Cluster: cluster, Kind: "Pod"}, t0); err == nil {
			t.Error("expected error for missing role key")
		}
	})
}
