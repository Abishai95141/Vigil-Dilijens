#!/usr/bin/env bash
# PSI + storage-IO + PVC-fill telemetry preflight  (doc 31).
#
# Verifies whether a TARGET cluster already EMITS the signals Vigil needs to adopt
# PSI pressure, container disk-IO, and PVC-fill observability — BEFORE any obsd wiring.
# This is the empirical gate doc 31 §2 requires ("validate before implementation").
#
# Cluster-AGNOSTIC by construction: the authoritative checks use the kubelet's own
# exposition via the apiserver node-proxy (kubectl get --raw .../proxy/metrics*), so the
# same script runs on kind, k3s, MicroK8s, or a customer's cloud cluster. The kernel /
# cgroup diagnostics are best-effort (kind/docker only) and never gate the verdict.
#
# Verdict tiers (matches doc 31):
#   PSI        -> config-only: present on any cgroup-v2 + CONFIG_PSI node, no feature gate.
#   disk-IO    -> already scraped by obsd's cAdvisor fetcher (inert until wired).
#   PVC-fill   -> needs a metrics-capable CSI provisioner; local-path emits NOTHING.
#
# Usage:  deploy/preflight/psi-storage-preflight.sh            # all nodes, current context
#         deploy/preflight/psi-storage-preflight.sh <node>     # one node
#
# Exit 0 when PSI + disk-IO are scrapable (the part Vigil can adopt with zero cluster
# change). PVC-fill absence is reported as WARN, not failure — it is a provisioner
# property, not a blocker for the PSI/disk-IO adoption.
set -uo pipefail

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
warn() { printf '  \033[33mWARN\033[0m  %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; }
info() { printf '        %s\n' "$1"; }

CTX="$(kubectl config current-context 2>/dev/null || echo '?')"
echo "=============================================================="
echo " Vigil PSI / storage telemetry preflight"
echo " context: ${CTX}"
echo "=============================================================="

if ! kubectl version >/dev/null 2>&1; then
  fail "kubectl cannot reach the cluster (is it up / is the context right?)"
  exit 2
fi

if [[ $# -ge 1 ]]; then
  NODES="$1"
else
  NODES="$(kubectl get nodes -o name 2>/dev/null | sed 's#node/##')"
fi
[[ -z "${NODES}" ]] && { fail "no nodes found"; exit 2; }

psi_ok=1 fsio_ok=1 pvc_ok=1

for n in ${NODES}; do
  echo
  echo "--- node: ${n} ------------------------------------------------"

  cadv="$(kubectl get --raw "/api/v1/nodes/${n}/proxy/metrics/cadvisor" 2>/dev/null)"
  if [[ -z "${cadv}" ]]; then
    fail "could not scrape /metrics/cadvisor on ${n}"
    psi_ok=0 fsio_ok=0
    continue
  fi

  # --- PSI (the config-only tier) ---
  psi_families="$(printf '%s\n' "${cadv}" | grep -oE '^container_pressure_[a-z_]+' | sort -u)"
  psi_count="$(printf '%s\n' "${cadv}" | grep -cE '^container_pressure_')"
  if [[ -n "${psi_families}" ]]; then
    pass "PSI emitted by cAdvisor — ${psi_count} samples, families:"
    printf '%s\n' "${psi_families}" | sed 's/^/          /'
  else
    fail "no container_pressure_* — node lacks cgroup-v2 + CONFIG_PSI (or an old cAdvisor)"
    psi_ok=0
  fi

  # --- container disk-IO (already in obsd's cadvisor scrape; inert until wired) ---
  fsio_count="$(printf '%s\n' "${cadv}" | grep -cE '^container_fs_(writes|reads)_bytes_total|^container_fs_io_time_seconds_total')"
  if [[ "${fsio_count}" -gt 0 ]]; then
    pass "container disk-IO present — container_fs_{writes,reads}_bytes_total / io_time (${fsio_count} samples)"
  else
    warn "no container_fs_* disk-IO samples on ${n}"
    fsio_ok=0
  fi

  # --- PVC fill (the genuine substrate gap) ---
  vol_metrics="$(kubectl get --raw "/api/v1/nodes/${n}/proxy/metrics" 2>/dev/null | grep -E '^kubelet_volume_stats_')"
  vol_count="$(printf '%s\n' "${vol_metrics}" | grep -c 'kubelet_volume_stats_')"
  if [[ "${vol_count}" -gt 0 ]]; then
    pass "kubelet_volume_stats_* present (${vol_count} samples) — PVC fill metric is scrapable"
    # ISOLATION CHECK: a directory-backed CSI (e.g. csi-hostpath) statfs's the NODE fs, so
    # every PVC reports the same capacity = the node disk, NOT its requested size. Detect it:
    # if PVCs with DIFFERENT requested sizes all report the SAME capacity, values are node-fs-scoped.
    distinct_caps="$(printf '%s\n' "${vol_metrics}" | grep '^kubelet_volume_stats_capacity_bytes' | grep -oE '[0-9.e+]+$' | sort -u | grep -c .)"
    distinct_reqs="$(kubectl get pvc -A -o jsonpath='{range .items[*]}{.spec.resources.requests.storage}{"\n"}{end}' 2>/dev/null | sort -u | grep -c .)"
    if [[ "${distinct_caps}" -eq 1 && "${distinct_reqs}" -ge 2 ]]; then
      warn "values are NODE-FS-SCOPED, not per-PVC — capacity is identical across PVCs of different sizes"
      info "this is a directory-backed CSI (e.g. csi-hostpath): statfs returns the node disk, so the"
      info "used-vs-requested fill bar is meaningless. For REAL per-PVC fill use a block/LVM-backed CSI"
      info "(OpenEBS LVM/ZFS, TopoLVM, Longhorn) on a dedicated disk, or a real cloud CSI cluster."
      pvc_ok=0
    fi
  else
    warn "kubelet_volume_stats_* ABSENT — provisioner does not report volume metrics"
    pvc_ok=0
  fi

  # --- best-effort kernel/cgroup diagnostics (kind/docker only; never gates) ---
  if command -v docker >/dev/null 2>&1 && docker inspect "${n}" >/dev/null 2>&1; then
    cgv="$(docker exec "${n}" stat -fc %T /sys/fs/cgroup 2>/dev/null)"
    [[ "${cgv}" == "cgroup2fs" ]] && info "cgroup: v2 (unified)" || info "cgroup: ${cgv:-unknown} (PSI needs v2)"
    docker exec "${n}" test -r /proc/pressure/io 2>/dev/null \
      && info "kernel PSI: /proc/pressure present (CONFIG_PSI=y)" \
      || info "kernel PSI: /proc/pressure MISSING (kernel lacks CONFIG_PSI)"
  fi
done

# --- storage provisioner advisory (explains a PVC WARN) ---
if [[ "${pvc_ok}" -eq 0 ]]; then
  echo
  echo "--- storage provisioner ----------------------------------------"
  kubectl get storageclass 2>/dev/null | sed 's/^/  /'
  prov="$(kubectl get sc -o jsonpath='{.items[*].provisioner}' 2>/dev/null)"
  info "provisioner(s): ${prov}"
  case "${prov}" in
    *local-path*|*hostpath*|*no-provisioner*)
      info "local-path / hostPath PVs report NO kubelet_volume_stats (kubelet does not stat them)."
      info "To validate PVC-fill on kind: deploy a metrics-capable CSI driver"
      info "(e.g. the CSI hostpath driver, OpenEBS, or Longhorn) and re-run this preflight." ;;
    *)
      info "If a real CSI driver is installed, kubelet_volume_stats should appear once a PVC is mounted by a running pod." ;;
  esac
fi

echo
echo "=============================================================="
echo " VERDICT"
[[ "${psi_ok}"  -eq 1 ]] && pass "PSI       scrapable now (config-only / zero cluster change)" || fail "PSI       NOT available — see node diagnostics above"
[[ "${fsio_ok}" -eq 1 ]] && pass "disk-IO   present (already in obsd's cAdvisor scrape; wire it)" || warn "disk-IO   not observed"
[[ "${pvc_ok}"  -eq 1 ]] && pass "PVC-fill  scrapable now" || warn "PVC-fill  needs a metrics-capable CSI provisioner (NOT a kernel/node rebuild)"
echo "=============================================================="

# Gate on the adoptable tier only (PSI). PVC-fill is an advisory WARN by design.
[[ "${psi_ok}" -eq 1 ]] && exit 0 || exit 1
