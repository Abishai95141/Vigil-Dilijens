package flow

import "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"

// PodInfo is one row of the frozen identity snapshot the resolver maps against:
// a pod's IP and the workload (role) it belongs to. Built once, out of band, from
// the live pod list — never re-read during a reconstruction, so the mapping is a
// pure function of the snapshot (replayable).
type PodInfo struct {
	Namespace string
	Name      string
	IP        string
	Workload  string // the role name, e.g. "productcatalogservice"
	UID       string
}

// Resolver maps a conntrack IP to a CEI, reusing the identity CEI scheme. Edges in
// this spike are workload→workload (role layer), so an unstable per-pod IP folds to
// the durable role the moment it is observed.
type Resolver struct {
	cluster string
	byIP    map[string]PodInfo
}

// NewResolver freezes the snapshot into an IP index.
func NewResolver(cluster string, pods []PodInfo) *Resolver {
	idx := make(map[string]PodInfo, len(pods))
	for _, p := range pods {
		if p.IP != "" {
			idx[p.IP] = p
		}
	}
	return &Resolver{cluster: cluster, byIP: idx}
}

// Instance returns the instance CEI (this pod) for an IP, if known.
func (r *Resolver) Instance(ip string) (identity.CEI, bool) {
	p, ok := r.byIP[ip]
	if !ok {
		return identity.CEI{}, false
	}
	return identity.CEI{
		Layer: identity.LayerInstance, Cluster: r.cluster,
		Namespace: p.Namespace, Kind: "Pod", Name: p.Name, UID: p.UID,
	}, true
}

// Role returns the role (workload) CEI for an IP, if known — the join unit for
// flow edges. RoleKey mirrors the identity convention ("Deployment/<name>").
func (r *Resolver) Role(ip string) (identity.CEI, bool) {
	p, ok := r.byIP[ip]
	if !ok {
		return identity.CEI{}, false
	}
	return identity.CEI{
		Layer: identity.LayerRole, Cluster: r.cluster,
		Namespace: p.Namespace, Kind: "Pod", RoleKey: "Deployment/" + p.Workload,
	}, true
}

// Label returns a short human label (namespace/workload) for an IP — for the
// surfacing output only; never an identity.
func (r *Resolver) Label(ip string) (string, bool) {
	p, ok := r.byIP[ip]
	if !ok {
		return "", false
	}
	return p.Namespace + "/" + p.Workload, true
}

// Workload returns the workload name for an IP.
func (r *Resolver) Workload(ip string) (string, bool) {
	p, ok := r.byIP[ip]
	if !ok {
		return "", false
	}
	return p.Workload, true
}
