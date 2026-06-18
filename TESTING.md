# Testing Vigil

The full strategy — tiers, how-to-run, the ABB Theme-2 question→test mapping, and the
honest proven-vs-roadmap framing — lives in **[`docs/testing/`](docs/testing/README.md)**.
This is the 60-second version.

## Run it

```bash
# Tier 1 — contract/unit (no cluster, ~10 min)
just ci                 # lint + gen-check + go test -race ./...
just clockd-test        # forecasting clock suite
just harness-test       # validation/backtest harness + gate scorers + meta-gate

# Tier 2 — corpus gates (no cluster): the real engine vs independent oracles
just event-detection-gate app-slo-gate departure-gate transitive-chain-gate \
     projected-transitive-gate validate-claim-gate mcp-gate incident-gate \
     events-gate xsvc-gate xsvc-projected-gate
#   (honest tier of each: docs/testing/gate-rigor.md)

# Tier 3 — live (a single-node cluster + kubectl). Point at the kubeconfig;
# on k3s use the canonical path (obsd rejects a stale ~/.kube/config CA):
VIGIL_TEST_KUBECONFIG=/etc/rancher/k3s/k3s.yaml just e2e     # detection + restraint + replay determinism + auth
VIGIL_TEST_KUBECONFIG=/etc/rancher/k3s/k3s.yaml just scale   # hundreds of pods: discovery, RSS, 0 mis-joins, churn

# Scale benchmarks (no cluster) · forecast gate (on-demand, real TimesFM)
just bench
just forecast-gate <bundle>      # docs/testing/forecast-gate.md
```

A fresh single-node k3s on GCP: **[docs/testing/cloud-runbook-gcp.md](docs/testing/cloud-runbook-gcp.md)**.

## What's proven vs. roadmap (the honest line)

- **Proven & verified live:** provenance separation · join-never-fuse · **live replay
  determinism (capture-here, replay-anywhere)** · identity mis-join defense · must-NOT-fire
  restraint · dependency mapping · KSM object-state lane (PVC/eviction/disk) · an automated
  live e2e · a measured scale envelope · bearer auth (deny-by-default) · a DB-migration path.
- **Validated, reproducible, on-demand (not routine CI):** the TimesFM forecast gate
  (`just forecast-gate`; campaign evidence in `corpus/labels/forecast-gate-09M3.md`).
- **Roadmap, platform-layer:** mTLS / per-caller authz (ingress/mesh) · the hundreds-of-pods
  live run (a node with a raised pod cap — see the cloud runbook).

Don't present forecasting or enterprise security/scale as *done*; present them as
architected-with-gates. Lead with the proven core. Full detail + the gate-rigor tier list:
[docs/testing/](docs/testing/README.md).
