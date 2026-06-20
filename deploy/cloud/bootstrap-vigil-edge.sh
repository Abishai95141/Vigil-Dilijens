#!/usr/bin/env bash
# bootstrap-vigil-edge.sh — one-command stand-up of the single-node cloud test bed.
#
# Packages docs/testing/cloud-runbook-gcp.md §2–§5 into one idempotent script. Run it
# ON a fresh Ubuntu VM (GCP e2-standard-8, AWS m6i.2xlarge, Azure D8s, or any 8 vCPU /
# 32 GB box) after `git clone` — it installs k3s with a RAISED pod cap, the toolchain,
# builds obsd, and deploys the ABB industrial-edge digital twin WITH node-exporter
# (so node-PSI is observable → precise STORAGE_SATURATION / THROTTLING_CASCADE — the
# gap a bare k3s box can't surface). Then it prints how to run the live tiers.
#
# Why cloud, not the laptop k3s: the laptop's pod cap (110) and missing node-exporter
# cap the "hundreds of pods + PSI saturation" story. This VM lifts both. The strong
# demo is the determinism dividend — capture here, replay BYTE-IDENTICAL at home
# (runbook §7). Tear the VM down when done (runbook §9) — it costs ~$0.27/hr.
#
# Usage:
#   ./deploy/cloud/bootstrap-vigil-edge.sh            # stand up the rig (no tiers)
#   MAX_PODS=300 RUN_TIERS=1 ./deploy/cloud/bootstrap-vigil-edge.sh   # + run e2e/scale/bench
#
# Env knobs:
#   MAX_PODS   (default 300)  kubelet --max-pods (the theme's "hundreds of pods")
#   SCALE_N    (default 300)  VIGIL_SCALE_N for the scale tier (when RUN_TIERS=1)
#   SOAK_DUR   (default 2h)   VIGIL_SOAK_DURATION for the soak tier (when RUN_TIERS=1)
#   RUN_TIERS  (default 0)    1 = run e2e + scale + bench at the end (soak is separate)
#   GO_VERSION (default 1.26.4)
set -euo pipefail

MAX_PODS="${MAX_PODS:-300}"
SCALE_N="${SCALE_N:-300}"
SOAK_DUR="${SOAK_DUR:-2h}"
RUN_TIERS="${RUN_TIERS:-0}"
GO_VERSION="${GO_VERSION:-1.26.4}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

log() { printf '\n\033[1;36m== %s\033[0m\n' "$*"; }

# --- 1. k3s with a raised pod cap (idempotent) ------------------------------------
if ! command -v k3s >/dev/null 2>&1; then
  log "installing k3s (single node, --max-pods=${MAX_PODS})"
  curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--kubelet-arg=max-pods=${MAX_PODS}" sh -
else
  log "k3s already installed — ensuring --max-pods=${MAX_PODS}"
  sudo mkdir -p /etc/rancher/k3s
  # Append the kubelet-arg if absent, then restart so it takes effect.
  if ! sudo grep -qs "max-pods=${MAX_PODS}" /etc/rancher/k3s/config.yaml 2>/dev/null; then
    printf 'kubelet-arg:\n  - "max-pods=%s"\n' "${MAX_PODS}" | sudo tee -a /etc/rancher/k3s/config.yaml >/dev/null
    sudo systemctl restart k3s
  fi
fi
sudo cp /etc/rancher/k3s/k3s.yaml "$HOME/k3s.yaml" && sudo chown "$USER" "$HOME/k3s.yaml"
export KUBECONFIG="$HOME/k3s.yaml" VIGIL_TEST_KUBECONFIG="$HOME/k3s.yaml"
log "waiting for the node to be Ready"
until kubectl get nodes 2>/dev/null | grep -q ' Ready '; do sleep 3; done
kubectl get nodes -o wide
echo "max-pods: $(kubectl get node -o jsonpath='{.items[0].status.capacity.pods}')"

# --- 2. toolchain (idempotent) ----------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
  log "installing Go ${GO_VERSION}"
  curl -sL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | sudo tar -C /usr/local -xz
  export PATH="$PATH:/usr/local/go/bin"
  grep -qs '/usr/local/go/bin' "$HOME/.bashrc" || echo 'export PATH=$PATH:/usr/local/go/bin' >> "$HOME/.bashrc"
fi
command -v just >/dev/null 2>&1 || { log "installing just"; curl -sL https://just.systems/install.sh | sudo bash -s -- --to /usr/local/bin; }
command -v uv   >/dev/null 2>&1 || { log "installing uv";   curl -LsSf https://astral.sh/uv/install.sh | sh; }

# --- 3. build obsd (CGO-free — the portability contract) --------------------------
log "building obsd (CGO_ENABLED=0)"
cd "$REPO_ROOT"
CGO_ENABLED=0 go build -o bin/obsd ./obsd/cmd/obsd
CGO_ENABLED=0 go build -o bin/replay ./obsd/cmd/replay

# --- 4. deploy the rig: PSI source + KSM + the ABB digital twin --------------------
log "deploying node-exporter (node-PSI → STORAGE_SATURATION/THROTTLING_CASCADE observable)"
kubectl apply -f deploy/workloads/node-exporter.yaml
log "deploying kube-state-metrics (OOM/restart object-state lane)"
kubectl create namespace monitoring 2>/dev/null || true
kubectl apply -f deploy/workloads/kube-state-metrics.yaml
log "deploying the ABB industrial-edge digital twin"
kubectl apply -k deploy/workloads/industrial-edge/
kubectl -n industrial-edge rollout status deploy/mqtt-broker --timeout=120s || true

cat <<EOF

$(printf '\033[1;32m== rig up.\033[0m')  Single node, max-pods=${MAX_PODS}, node-exporter + KSM + ABB digital twin live.

Run the live tiers (PSI now observable, so STORAGE_SATURATION can fire for real):
  export VIGIL_TEST_KUBECONFIG=\$HOME/k3s.yaml
  just e2e                              # detection + restraint + live replay determinism + auth
  VIGIL_SCALE_N=${SCALE_N} just scale   # pack ~${SCALE_N} pods: discovery, RSS/pod, ZERO mis-joins, churn
  VIGIL_SOAK_DURATION=${SOAK_DUR} just soak   # leak/stability over time (run overnight)
  just bench                            # hermetic per-tick cost curve

Brutal chaos (ABB-shaped) — apply against the live twin and watch Vigil's findings:
  kubectl apply -f corpus/chaos/industrial/abb-mqtt-leak-oom-reconnect-cpu.yaml
  kubectl apply -f corpus/chaos/industrial/abb-historian-io-scada-restart.yaml
  bin/obsd --kubeconfig \$HOME/k3s.yaml --ksm-enabled --app-metrics-enabled --health-addr :9095 &
  # then SSH-tunnel :9095 home (runbook §6) — NEVER expose it publicly.

Determinism dividend: capture here, replay byte-identical at home (runbook §6/§7).
Tear down when done (it costs money): \033[1;33mGCP\033[0m gcloud compute instances delete vigil-edge --quiet
                                     \033[1;33mAWS\033[0m aws ec2 terminate-instances --instance-ids <id>
                                     \033[1;33mAzure\033[0m az group delete -n vigil-edge-rg --yes
EOF

if [ "$RUN_TIERS" = "1" ]; then
  log "RUN_TIERS=1 → e2e + scale (N=${SCALE_N}) + bench (soak is separate; run it overnight)"
  export VIGIL_TEST_KUBECONFIG="$HOME/k3s.yaml"
  just e2e
  VIGIL_SCALE_N="${SCALE_N}" just scale
  just bench
fi
