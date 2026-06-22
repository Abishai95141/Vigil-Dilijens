# Robust single-node Vigil substrate (Lima + Ubuntu + k3s)

The **only** cluster that emits all three signal classes with **real values on one node**:

| Signal | how it's real here |
|---|---|
| **PSI** `container_pressure_{cpu,io,memory}_{stalled,waiting}` | Ubuntu 6.8 kernel has `CONFIG_PSI=y` (default-enabled) + cgroup v2 → k3s's cAdvisor emits all 6 families, no feature gate |
| **container disk-IO** `container_fs_*` | cgroup v2 (universal) |
| **PVC fill** `kubelet_volume_stats_*` | **per-PVC** via OpenEBS LVM on a dedicated disk — each PVC is a real sized logical volume (a 5Gi PVC reports `capacity≈5.2GB`, not the node disk) |

Why not kind or minikube: kind/LinuxKit has PSI but its directory-backed CSI reports the node
disk for every PVC; minikube's buildroot kernel has `# CONFIG_PSI is not set` (PSI impossible).
Ubuntu satisfies both PSI and LVM. See `docs/31` §0.

## Bring-up (≈5–8 min, no sudo on the host, no extra hypervisor)

```bash
brew install lima                                   # Apple Virtualization.framework backend
limactl disk create vigil-lvm --size 40GiB          # dedicated disk for the LVM VG (persists)
limactl start --name vigil-robust deploy/lima/vigil-robust.yaml --tty=false
limactl shell vigil-robust -- bash -s < deploy/lima/bootstrap.sh   # k3s + LVM + OpenEBS LVM
```

Verify (run inside the VM — the bootstrap installed a `kubectl` shim → `k3s kubectl`):

```bash
limactl shell vigil-robust -- bash -s < deploy/preflight/psi-storage-preflight.sh
# expect: PSI PASS, disk-IO PASS, PVC-fill PASS (no node-fs-scoped warning — values are per-PVC)
```

## Deploy the reference workload (real dependency graph + real per-PVC usage)

The abb-genix stack's stateful stores land on the LVM storageclass (real per-PVC fill that
grows as InfluxDB/services write); the deployments form the service dependency graph. The sim
image is built on the host and imported into k3s (k3s has no `kind load`):

```bash
docker build -t vigil-abb-sim:0.1 deploy/workloads/abb-genix/sim
docker save vigil-abb-sim:0.1 -o /tmp/sim.tar
limactl copy /tmp/sim.tar vigil-robust:/tmp/sim.tar
limactl shell vigil-robust -- sudo k3s ctr images import /tmp/sim.tar
NODE=lima-vigil-robust
limactl shell vigil-robust -- sudo k3s kubectl label node $NODE vigil.io/sim-node=abb-genix --overwrite
for m in 00-namespace-config 10-backbone 20-sims; do
  cat deploy/workloads/abb-genix/$m.yaml | limactl shell vigil-robust -- sudo k3s kubectl apply -f -
done
```

## Teardown

```bash
limactl delete -f vigil-robust          # remove the VM
limactl disk delete vigil-lvm           # remove the dedicated disk (optional)
```

## Notes / robustness

- **vz networking:** the k3s API is on the VM's internal IP; drive the cluster via
  `limactl shell vigil-robust -- sudo k3s kubectl …` (or copy `/etc/rancher/k3s/k3s.yaml`
  out and rewrite the server to a forwarded port). The preflight runs in-VM by design.
- **LVM survives reboots:** the VG is an LVM2 signature on the raw disk; Ubuntu's lvm2 systemd
  auto-activates it on boot, and Lima won't reformat a disk that already carries a signature.
- **OpenEBS raw-manifest fixes** (baked into `bootstrap.sh`, learned the hard way): the v1.9.1
  `lvm-operator.yaml` ships without `LVM_NAMESPACE` (node plugin crash-loops) and with a
  single-node-hostile controller `podAntiAffinity`; it also leaves the scheduler storage-capacity
  gate on. The script sets the env on node+controller, clears the affinity, sets
  `CSIDriver.storageCapacity=false`, and force-recreates the controller pod.
