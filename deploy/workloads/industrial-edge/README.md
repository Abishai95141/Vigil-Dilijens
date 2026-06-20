# industrial-edge — ABB OT/edge digital twin

A second digital twin for Vigil, modeling an **ABB industrial-edge gateway** rather
than e-commerce. Target: a single-node **k3s** cluster on a constrained (~15 GiB)
node. It exercises the same epistemic machinery the Online Boutique twin does
(doc 14 §3.3) — most importantly the **resolvability-hole policy** (doc 04 §3.4):
config-sourced limits make a bar resolvable; one workload here is deliberately
**unbounded** so Vigil must list it as Tier-B-ineligible rather than invent a bar.

## Topology

```
  sim-temperature ─┐
  sim-current     ─┤ publish (MQTT 1883)        ┌─ edge-inference (subscribe edge/#)
  sim-power       ─┼────────────►  mqtt-broker ─┤
  sim-vibration   ─┘  (fan-in)                  └─ (historian-writer path*)
                                                        │ write
                                                        ▼
                          scada-dashboard ───read──►  historian (InfluxDB + PVC)
```

\* In this minimal manifest the simulators publish and `edge-inference` subscribes;
the historian carries a PVC and is polled by the SCADA dashboard. The PVC mount is
the deliberate I/O surface (see "Why the PVC matters" below).

## Workloads and resource posture

| Workload | Kind | Limits | Vigil meaning |
|---|---|---|---|
| `mqtt-broker` (mosquitto) | Deployment | tight | Tier-A; near-threshold candidate under fan-in |
| `historian` (InfluxDB) | StatefulSet **+ PVC** | bounded | Tier-A; marquee working-set climber; PVC-fill lane |
| `sim-temperature` | Deployment | bounded | Tier-A, forecastable |
| `sim-current` | Deployment | bounded | Tier-A, forecastable |
| `sim-power` | Deployment | bounded | Tier-A, forecastable |
| `sim-vibration` | Deployment | **NONE (unbounded)** | **Tier-B-INELIGIBLE — the resolvability hole** |
| `edge-inference` | Deployment | tight | Tier-A; broker consumer |
| `scada-dashboard` | Deployment | tight | Tier-A; historian reader |

### The deliberately unbounded workload

`sim-vibration` declares `requests` but **no `limits` block**. There is therefore no
config-sourced bar, no crossable limit, and it is **ineligible for forecasting**.
Vigil must surface it on the coverage report's unbounded-workload list
("unbounded: no early-warning eligibility"), never silently skipped (doc 04 §3.4).
Do not add a `limits:` stanza to it — that would close the hole the twin exists to
exercise.

### Why the PVC matters

The historian is a StatefulSet with a `volumeClaimTemplate`, so its data path is a
**PersistentVolume**, not `emptyDir`. That is the point: it drives the Pod→PVC
`mounts` edge (doc 03), the PVC-fill / `PHEN_DISK_FILLING` lane, and the
`kubelet_volume_stats` stream (doc 19 "PVC dark-bar").

> **Caveat (doc 16):** on k3s's default `local-path` storageClass the kubelet emits
> **zero** `kubelet_volume_stats`, so PVC-fill is a *dark bar* there (silence class
> `no-stream-key` — never a fabricated reading). PVC **PENDING** is still observable
> via KSM. To watch live PVC-fill, point the claim at a CSI storageClass that reports
> volume stats (override `storageClassName` in `20-historian.yaml`).

## Deploy

With kustomize (applies everything in order, into the `industrial-edge` namespace):

```sh
kubectl apply -k deploy/workloads/industrial-edge/
```

Or per-file (same order):

```sh
kubectl apply -f deploy/workloads/industrial-edge/00-namespace.yaml
kubectl apply -f deploy/workloads/industrial-edge/10-mqtt-broker.yaml
kubectl apply -f deploy/workloads/industrial-edge/20-historian.yaml
kubectl apply -f deploy/workloads/industrial-edge/30-device-simulators.yaml
kubectl apply -f deploy/workloads/industrial-edge/40-edge-inference.yaml
kubectl apply -f deploy/workloads/industrial-edge/50-scada-dashboard.yaml
```

Wait for readiness:

```sh
kubectl -n industrial-edge rollout status statefulset/historian
kubectl -n industrial-edge get pods
```

## Scale the device fleet toward hundreds of pods

Each replica is one "device". Scale a simulator family up (mind the ~15 GiB node —
each pod requests ~12 Mi, so a few hundred pods is on the order of GiBs):

```sh
kubectl -n industrial-edge scale deploy/sim-temperature --replicas=60
kubectl -n industrial-edge scale deploy/sim-current     --replicas=60
kubectl -n industrial-edge scale deploy/sim-power       --replicas=60
kubectl -n industrial-edge scale deploy/sim-vibration   --replicas=60
```

The fan-in to the single `mqtt-broker` Service is the intended stress.

## Teardown

```sh
kubectl delete -k deploy/workloads/industrial-edge/
```

Or:

```sh
kubectl delete namespace industrial-edge
```

> Deleting the namespace removes the historian's PVC and its data. If you provisioned
> a non-default storageClass, confirm the PV reclaim policy before relying on this.

## Client-side validation (no cluster state)

Every manifest validates client-side. Preferred:

```sh
for f in deploy/workloads/industrial-edge/*.yaml; do
  kubectl apply --dry-run=client -f "$f" -o yaml >/dev/null && echo "OK  $f"
done
```

Pure-offline fallback (structural YAML parse, no schema):

```sh
for f in deploy/workloads/industrial-edge/*.yaml; do
  python3 -c "import yaml,sys;[*yaml.safe_load_all(open(sys.argv[1]))]" "$f" && echo "PARSED $f"
done
```

## Images (pinned — doc 14: no floating tags)

- `eclipse-mosquitto:2.0.20` — broker + `mosquitto_pub`/`mosquitto_sub` for sims and inference
- `influxdb:2.7-alpine` — historian
- `curlimages/curl:8.11.1` — SCADA dashboard poller
