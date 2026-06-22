#!/usr/bin/env bash
# In-VM bootstrap for the robust all-three Vigil substrate (doc 31 §0).
# Run INSIDE the Lima Ubuntu VM:  limactl shell vigil-robust -- bash -s < deploy/lima/bootstrap.sh
#
# Turns the Ubuntu node into a single-node k3s cluster that emits PSI + disk-IO + REAL
# per-PVC fill: prepares the dedicated disk as an LVM VG, installs k3s, installs OpenEBS
# LVM LocalPV (with the operational fixes the raw manifest needs), and sets it default.
# Idempotent-ish: safe to re-run; destructive only on the dedicated disk (vigilvg).
set -euo pipefail

OPENEBS_VER="v1.9.1"
VG="vigilvg"
LVM_NS="kube-system"

log() { printf '\n=== %s ===\n' "$1"; }

# ---------------------------------------------------------------------------
log "1. dedicated disk -> raw LVM PV -> VG ${VG}"
# Lima auto-formats+mounts the additionalDisk at /mnt/lima-vigil-lvm; reclaim it as raw.
MNT=/mnt/lima-vigil-lvm
if mountpoint -q "${MNT}"; then
  DISK="/dev/$(lsblk -no PKNAME "$(findmnt -no SOURCE "${MNT}")")"
  sudo umount "${MNT}" || true
  sudo sed -i "\#${MNT}#d" /etc/fstab || true
else
  # fall back: the lone 40G disk that is not the root disk nor the cidata iso
  DISK="/dev/$(lsblk -dn -o NAME,SIZE,TYPE | awk '$3=="disk" && $1!~"vda" {print $1; exit}')"
fi
echo "dedicated disk: ${DISK}"
if ! sudo vgs "${VG}" >/dev/null 2>&1; then
  sudo wipefs -a "${DISK}"* 2>/dev/null || true
  sudo wipefs -a "${DISK}"
  sudo pvcreate -ff -y "${DISK}"
  sudo vgcreate "${VG}" "${DISK}"
fi
sudo vgs "${VG}"

# ---------------------------------------------------------------------------
log "2. install k3s (containerd, cgroup v2; Ubuntu kernel already has CONFIG_PSI=y)"
if ! sudo k3s kubectl version >/dev/null 2>&1; then
  curl -sfL https://get.k3s.io | sudo INSTALL_K3S_EXEC="--write-kubeconfig-mode=644" sh -
fi
# kubectl shim so cluster-agnostic tooling (just psi-preflight) runs unmodified in-VM.
printf '#!/bin/sh\nexec k3s kubectl "$@"\n' | sudo tee /usr/local/bin/kubectl >/dev/null
sudo chmod +x /usr/local/bin/kubectl
for i in $(seq 1 30); do sudo k3s kubectl get nodes 2>/dev/null | grep -q " Ready " && break; sleep 4; done
sudo k3s kubectl get nodes -o wide

KC() { sudo k3s kubectl "$@"; }

# ---------------------------------------------------------------------------
log "3. OpenEBS LVM LocalPV (${OPENEBS_VER}) — with the raw-manifest fixes"
KC apply -f "https://raw.githubusercontent.com/openebs/lvm-localpv/${OPENEBS_VER}/deploy/lvm-operator.yaml"
# The raw manifest ships without LVM_NAMESPACE and with a single-node-hostile controller
# podAntiAffinity. Set the env on BOTH components, drop the affinity, and skip the scheduler
# storage-capacity gate (single node). Then force a clean controller pod with the final spec.
KC -n "${LVM_NS}" set env daemonset/openebs-lvm-node       -c openebs-lvm-plugin LVM_NAMESPACE="${LVM_NS}"
KC -n "${LVM_NS}" set env deploy/openebs-lvm-controller     -c openebs-lvm-plugin LVM_NAMESPACE="${LVM_NS}"
KC -n "${LVM_NS}" patch deploy openebs-lvm-controller --type=merge \
  -p '{"spec":{"template":{"spec":{"affinity":null}}}}'
KC patch csidriver local.csi.openebs.io --type=merge -p '{"spec":{"storageCapacity":false}}' || true
KC -n "${LVM_NS}" rollout status ds/openebs-lvm-node --timeout=180s
KC -n "${LVM_NS}" delete pod -l app=openebs-lvm-controller --force --grace-period=0 || true
KC -n "${LVM_NS}" rollout status deploy/openebs-lvm-controller --timeout=180s

# ---------------------------------------------------------------------------
log "4. storageclass openebs-lvm = default (demote k3s local-path)"
cat <<EOF | KC apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: openebs-lvm
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: local.csi.openebs.io
parameters: { storage: "lvm", volgroup: "${VG}" }
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
EOF
KC patch storageclass local-path -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}' || true

log "DONE — verify with:  just psi-preflight   (run inside the VM; PSI PASS + real per-PVC)"
KC get sc
