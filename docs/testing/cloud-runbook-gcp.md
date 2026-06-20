# Cloud runbook — single-node k3s on GCP

> Stand up the **theme-aligned** test bed — *"single-node clusters commonly used in edge
> and industrial environments"* — on one GCE VM, run Vigil's live tiers against it, and
> prove the determinism/portability dividend (capture on the cloud, replay byte-identical
> at home). One generously-sized VM packed with pods is closer to the ABB Theme-2 target
> than a managed multi-node cluster, and far cheaper.
>
> **Cost:** an `e2-standard-8` (8 vCPU / 32 GB) is ~$0.27/hr on-demand — **tear it down
> when done** (last step). **Security:** the obsd API is unauthenticated by default — this
> runbook never opens its port to the internet (SSH tunnel / `--api-token` only).

## 1. Create the VM

```bash
gcloud compute instances create vigil-edge \
  --zone=asia-south1-a \
  --machine-type=e2-standard-8 \
  --image-family=ubuntu-2404-lts --image-project=ubuntu-os-cloud \
  --boot-disk-size=80GB --boot-disk-type=pd-ssd
# NOTE: no firewall rule for obsd's :9095 — it stays private (see §6).
gcloud compute ssh vigil-edge --zone=asia-south1-a
```

> **One command for §2–§5.** After the VM exists and you've `git clone`d the repo,
> `MAX_PODS=300 RUN_TIERS=1 ./deploy/cloud/bootstrap-vigil-edge.sh` does k3s (raised
> cap) + toolchain + obsd build + node-exporter/KSM/ABB-digital-twin deploy + the
> e2e/scale/bench tiers, idempotently. The manual steps below are what it automates —
> read them to understand the rig, or run the script to skip ahead.

## 2. Install k3s (single node) with a raised pod cap

k3s defaults to `--max-pods=110`; raise it so the scale tier can pack hundreds of pods —
exactly the theme's "hundreds of pods across namespaces on a single node."

```bash
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--kubelet-arg=max-pods=300" sh -
sudo cp /etc/rancher/k3s/k3s.yaml ~/k3s.yaml && sudo chown "$USER" ~/k3s.yaml
export KUBECONFIG=$HOME/k3s.yaml VIGIL_TEST_KUBECONFIG=$HOME/k3s.yaml
kubectl get nodes        # Ready
```

## 3. Toolchain (Go, just, uv, kubectl)

```bash
# Go 1.26
curl -sL https://go.dev/dl/go1.26.4.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc && export PATH=$PATH:/usr/local/go/bin
# just + uv
curl -sL https://just.systems/install.sh | sudo bash -s -- --to /usr/local/bin
curl -LsSf https://astral.sh/uv/install.sh | sh && source ~/.bashrc
# kubectl is bundled with k3s: alias kubectl='sudo k3s kubectl'  (or install standalone)
sudo apt-get update && sudo apt-get install -y git
```

## 4. Clone + deploy the workload (a realistic edge app)

```bash
git clone https://github.com/Abishai95141/Vigil-Dilijens && cd Vigil-Dilijens
export KUBECONFIG=$HOME/k3s.yaml
just boutique                                        # Online Boutique microservices + loadgen
kubectl apply -f deploy/workloads/node-exporter.yaml # PSI -> precise THROTTLING_CASCADE (see note)
# kube-state-metrics is deployed automatically by the e2e suite; or do it manually:
kubectl create namespace monitoring
kubectl apply -f deploy/workloads/kube-state-metrics.yaml
```

> **Deploy node-exporter.** Without it node-PSI is unobservable, so `THROTTLING_CASCADE`
> can only ever be *degraded* and fires (degraded) on any throttling pod. With it, the
> cascade requires real node pressure → far more precise. (Live finding from Track 1.)

## 5. Run the live tiers

```bash
export VIGIL_TEST_KUBECONFIG=$HOME/k3s.yaml

just e2e                         # detection + restraint + live replay determinism + auth
VIGIL_SCALE_N=300 just scale     # pack ~300 pods: discovery, RSS/pod, ZERO mis-joins, churn
VIGIL_SOAK_DURATION=2h just soak # leak/stability over time (run overnight)
just bench                       # the hermetic per-tick cost curve (no cluster needed)
```

## 6. Access the API safely (never expose :9095)

```bash
# Option A — SSH tunnel (recommended): from your laptop,
gcloud compute ssh vigil-edge --zone=asia-south1-a -- -L 9095:localhost:9095
#   then locally: curl localhost:9095/api/coverage
# Option B — bearer auth if it must be reachable on the private network:
bin/obsd --kubeconfig $HOME/k3s.yaml --api-token "$(openssl rand -hex 24)" --health-addr :9095 ...
```

## 7. The determinism / portability dividend (the strong demo)

Capture on the cloud cluster, replay byte-identically on your laptop — proving the replay
guarantee holds across machines:

```bash
# on the VM: capture a window, then Ctrl-C to seal
bin/obsd --kubeconfig $HOME/k3s.yaml --db /tmp/v.db --store-dir /tmp/cap --ksm-enabled &
sleep 300; kill -INT %1; sleep 4
# pull the bundle home and replay it locally:
gcloud compute scp --recurse vigil-edge:/tmp/cap ./cloud-cap --zone=asia-south1-a
bin/replay -bundle ./cloud-cap      # "all N ticks replayed byte-identically" — same digests as the cloud run
```

## 8. Forecast gate (optional, heavy)

Capture a *slow* creep (the fast `corpus/chaos/e2e/` leaks cross in ~1 min — too fast for
forecast context); then run the real-model gate (downloads TimesFM on first run):

```bash
kubectl apply -f corpus/chaos/leak-slow.yaml
bin/obsd --kubeconfig $HOME/k3s.yaml --db /tmp/f.db --store-dir /tmp/fcap & sleep 1500; kill -INT %1; sleep 4
just forecast-gate /tmp/fcap timesfm
```

## 9. Tear down (don't forget — cost)

```bash
gcloud compute instances delete vigil-edge --zone=asia-south1-a --quiet
```

## Optional — managed multi-node (only if you need cross-node realism)

A 2–3 node **node-based** GKE Standard cluster (NOT Autopilot — serverless has no node
access, which kills the node-exporter/cAdvisor lane) lets you demo cross-node cascades. It
costs more and is less theme-aligned than the single big VM; the suite runs identically
(`VIGIL_TEST_KUBECONFIG` = the GKE kubeconfig). Prefer the single-node VM above unless the
cross-node story is specifically needed.
