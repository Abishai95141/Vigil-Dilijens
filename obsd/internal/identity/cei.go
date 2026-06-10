package identity

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Canonical Entity Identity (CEI) — doc 03 §3.2. The single normalized identity
// key minted at discovery for every instance and every role, built from stable
// coordinates. It is the ONLY join key in the system: every graph instance node
// carries its CEI, every time-series stream carries its CEI (stamped at ingest),
// and the graph<->store join is EXACT match on CEI, never fuzzy, never at read time.

// Layer is the identity layer a CEI names (doc 03 §3.1).
type Layer uint8

const (
	// LayerInstance is the physical object (this pod, UID-anchored). Mortal: born,
	// lives, dies, is replaced.
	LayerInstance Layer = iota + 1
	// LayerRole is the logical function (the `currencyservice` workload member).
	// Durable across restarts and rescheduling.
	LayerRole
)

func (l Layer) String() string {
	switch l {
	case LayerInstance:
		return "instance"
	case LayerRole:
		return "role"
	default:
		return "unknown"
	}
}

// sep is the field delimiter in a CEI key. Kubernetes coordinates (cluster UID,
// namespace, kind, name, object UID) never contain it; minting rejects any field
// that does, so the key is unambiguous and the join stays exact.
const sep = "|"

// CEI is a canonical entity identity. Two CEIs name the same entity iff their
// Key() values are equal; MintedAt is provenance metadata and is NOT part of
// identity (so re-minting the same coordinates yields an equal identity).
type CEI struct {
	Layer     Layer
	Cluster   string // cluster id = kube-system namespace UID (doc 14 A9)
	Namespace string // empty for cluster-scoped kinds (e.g. Node)
	Kind      string // Pod, Node, Container, Service, PersistentVolumeClaim, ...

	// Instance-layer coordinates.
	Name string // object name (instance only)
	UID  string // object UID — the per-instance anchor (instance only)

	// Role-layer coordinate.
	RoleKey string // derived stable key, e.g. "Deployment/currencyservice" (role only)
	Bare    bool   // role derived from a bare (ownerless) pod fallback (doc 14 A10)

	MintedAt time.Time // receive-time wall clock at discovery (doc 14 A12); metadata only
}

// Key returns the canonical, stable join key. It excludes MintedAt and Bare
// (provenance, not identity). Instance and role keys live in disjoint namespaces
// ("i" vs "r") so they can never collide.
func (c CEI) Key() string {
	switch c.Layer {
	case LayerInstance:
		return strings.Join([]string{"i", c.Cluster, c.Namespace, c.Kind, c.Name, c.UID}, sep)
	case LayerRole:
		return strings.Join([]string{"r", c.Cluster, c.Namespace, c.Kind, c.RoleKey}, sep)
	default:
		return strings.Join([]string{"?", c.Cluster, c.Namespace, c.Kind}, sep)
	}
}

// Same reports whether two CEIs name the same entity (exact key match).
func (c CEI) Same(other CEI) bool { return c.Key() == other.Key() }

func (c CEI) String() string { return c.Key() }

// InstanceCoords are the stable coordinates of a physical object, supplied by the
// identity layer after label normalization (doc 03 §3.3).
type InstanceCoords struct {
	Cluster   string
	Namespace string // empty for cluster-scoped kinds
	Kind      string
	Name      string
	UID       string
}

// RoleCoords are the coordinates of a logical role.
type RoleCoords struct {
	Cluster   string
	Namespace string
	Kind      string
	RoleKey   string
	Bare      bool
}

// OwnerRef is a minimal mirror of the fields of metav1.OwnerReference that role
// derivation needs. Kept local so the CEI scheme carries no Kubernetes dependency;
// the informer wiring (M3) maps the real type onto this one.
type OwnerRef struct {
	Kind       string
	Name       string
	UID        string
	Controller bool
}

func nonEmpty(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must be non-empty", field)
	}
	if strings.Contains(value, sep) {
		return fmt.Errorf("%s must not contain the reserved separator %q: %q", field, sep, value)
	}
	return nil
}

// MintInstance mints an instance-layer CEI, validating that every identity-bearing
// coordinate is present and separator-free. Namespace may be empty (cluster-scoped
// kinds like Node). mintedAt is injected (no time.Now in logic).
func MintInstance(coords InstanceCoords, mintedAt time.Time) (CEI, error) {
	var errs []error
	for field, value := range map[string]string{
		"cluster": coords.Cluster,
		"kind":    coords.Kind,
		"name":    coords.Name,
		"uid":     coords.UID,
	} {
		if err := nonEmpty(field, value); err != nil {
			errs = append(errs, err)
		}
	}
	// Namespace is optional but, if present, must be separator-free.
	if coords.Namespace != "" && strings.Contains(coords.Namespace, sep) {
		errs = append(errs, fmt.Errorf("namespace must not contain %q", sep))
	}
	if err := errors.Join(errs...); err != nil {
		return CEI{}, fmt.Errorf("mint instance CEI: %w", err)
	}
	return CEI{
		Layer:     LayerInstance,
		Cluster:   coords.Cluster,
		Namespace: coords.Namespace,
		Kind:      coords.Kind,
		Name:      coords.Name,
		UID:       coords.UID,
		MintedAt:  mintedAt,
	}, nil
}

// MintRole mints a role-layer CEI.
func MintRole(coords RoleCoords, mintedAt time.Time) (CEI, error) {
	var errs []error
	for field, value := range map[string]string{
		"cluster":  coords.Cluster,
		"kind":     coords.Kind,
		"role key": coords.RoleKey,
	} {
		if err := nonEmpty(field, value); err != nil {
			errs = append(errs, err)
		}
	}
	if coords.Namespace != "" && strings.Contains(coords.Namespace, sep) {
		errs = append(errs, fmt.Errorf("namespace must not contain %q", sep))
	}
	if err := errors.Join(errs...); err != nil {
		return CEI{}, fmt.Errorf("mint role CEI: %w", err)
	}
	return CEI{
		Layer:     LayerRole,
		Cluster:   coords.Cluster,
		Namespace: coords.Namespace,
		Kind:      coords.Kind,
		RoleKey:   coords.RoleKey,
		Bare:      coords.Bare,
		MintedAt:  mintedAt,
	}, nil
}

// DeriveRole computes the role coordinates for a pod from its resolved ownership
// chain (doc 03 §3.1). The chain is ordered immediate -> top (e.g. ReplicaSet then
// Deployment); the role anchors on the TOPMOST controller, so the role is stable
// across Deployment rollouts (a new ReplicaSet does not mint a new role) and across
// restarts/rescheduling. A pod with no controller in its chain is a bare pod: the
// fallback role key is "Pod/<name>", flagged bare (doc 14 A10).
//
// Note: resolving the chain (Pod -> ReplicaSet -> Deployment) from the informer
// cache is M3's job; this function defines the role semantics over an already
// resolved chain and is the unit the churn tests pin.
func DeriveRole(cluster, namespace, podName string, chain []OwnerRef) RoleCoords {
	anchor, ok := topController(chain)
	if !ok {
		return RoleCoords{
			Cluster:   cluster,
			Namespace: namespace,
			Kind:      "Pod",
			RoleKey:   "Pod/" + podName,
			Bare:      true,
		}
	}
	return RoleCoords{
		Cluster:   cluster,
		Namespace: namespace,
		Kind:      anchor.Kind,
		RoleKey:   anchor.Kind + "/" + anchor.Name,
	}
}

// topController returns the topmost controller in an immediate->top ordered chain.
func topController(chain []OwnerRef) (OwnerRef, bool) {
	var anchor OwnerRef
	found := false
	for _, o := range chain {
		if o.Controller {
			anchor = o
			found = true
		}
	}
	return anchor, found
}
