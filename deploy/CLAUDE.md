# deploy — dev cluster, RBAC, add-ons, workloads

Runs on the **Linux/Ubuntu** box (kind needs Docker). Versions pinned here, bumped
deliberately (techstack §14).

```
kind/cluster.yaml      3-node cluster: 1 control-plane + 2 workers (doc 14 §3.1)
rbac/clusterrole.yaml  read-only RBAC for the in-cluster obsd Deployment (doc 14 A2)
helm/                  pinned values for kube-state-metrics · node-exporter · Chaos Mesh
workloads/             Online Boutique (Phase 0) · OTel Demo (Phase 1) manifests
```

- **Two workers is the floor** — `runs-on` topology, node-pressure phenomena, and the
  2-hop noisy-neighbour walk are unrepresentable on one node.
- We **watch, never reconcile**: get/list/watch only, no CRDs, no controller-runtime.
  kubelet cAdvisor metrics are reached via the API-server proxy in dev (`nodes/proxy`).
- Honest caveat (doc 14 §3.1): kind nodes share the host kernel — container-scoped
  phenomena are real, node-level pressure realism is limited. Stand up the k3s-on-VMs
  staging cluster before the node-pressure falsification corpus (doc 11 M3) is
  treated as authoritative.

`just up` / `just down` / `just rbac`. Online Boutique limits get deliberately
customized at install (doc 14 §3.3): one service tuned near-threshold, one stripped of
limits, so the resolvability-hole policy is exercised from day one.
