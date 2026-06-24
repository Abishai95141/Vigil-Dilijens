# Vigil Scenario Studio

A **scenario-first** showcase console for the live `abb-genix` cluster. Where the original
`simulator/` is a fault catalog, this studio is built around **six operator questions** — each
scenario injects a real fault, shows which Vigil pages light up, and lets you **ask the
MCP-connected agent** the question and get an answer grounded only in what Vigil measured.

It is a fresh, self-contained app (it does not depend on `simulator/`), but it reuses the same
honest principle: every fault is a genuine `kubectl` operation, shown verbatim before it runs.

## Why it exists

The point of the studio is to make Vigil's **reasoning discipline** legible:

- **MEASURED / PROJECTED / AUTHORED / ASSOCIATION / ADVISORY** — every fact is labelled, never blurred.
- **Association ≠ cause.** Co-moving series surface as a *direction-free hypothesis*; a named
  operator authors the arrow. Vigil refuses to invent causal direction.
- **It admits what it can't see** (Coverage / Silence-ledger / Blindspots).

## The six scenarios → questions

| Scenario | Question it answers | Real fault(s) | Vigil surfaces |
|---|---|---|---|
| 🌋 Multi-level cascade → root cause | *What's the cause of a failure in our cluster?* | historian outage (+ stream queue) | Root-cause chain, Cross-service, Insights |
| 📈 Which pod is spiking CPU? | *Which pod is causing unexpected CPU spikes?* | CPU burn on stream-processor | Onsets, Findings (THROTTLING), Insights |
| 🔀 Services influencing each other | *Are different services influencing each other's resource consumption?* | CPU burn (noisy aggressor) | Dependency, Causal-hypotheses, Onsets |
| 💽 PVC I/O ↔ pod restarts | *How are PVC I/O patterns linked to pod restarts?* | PVC I/O storm + memory squeeze → OOM | Dependency, Causal-hypotheses, Events, Findings (PVC_FILLING) |
| 🔮 Early warning — long vs short lead | *Is there any early warning, and how could I resolve it?* | memory leak @ 48 / 512 / 4096 KiB | Early-warnings (BandBar), Departures, Insights |
| 🧮 Which workloads need optimization? | *Which workloads need optimization?* | none — always-on | Right-sizing |

> **On this AWS EBS cluster the kind PVC blind spot is CLOSED**: `kubelet_volume_stats_used_bytes`,
> `container_fs_writes_bytes_total` and `container_pressure_io_*` are all MEASURED, so PVC fill
> and I/O patterns are real signals here — only the *direction* of the I/O→restart link stays
> operator-authored.

## Run

```bash
cd sim-studio
pip install -r requirements.txt           # streamlit + pandas (kubectl must be on PATH)
export DGX_API_KEY=<deepseek key>          # same key obsd uses; enables the agent pane
streamlit run studio.py                    # opens http://localhost:8501
```

Environment (all have sensible defaults for the live AWS setup):

| Var | Default | Purpose |
|---|---|---|
| `CLUSTER_KUBECONFIG` | `/tmp/aws-k3s.yaml` | kubeconfig for the faults |
| `CLUSTER_CONTEXT` | `default` | kube-context |
| `CLUSTER_NAMESPACE` | `abb-genix` | target namespace |
| `OBSD_BASE` | `http://localhost:9095` | obsd `/api` + `/mcp` |
| `DGX_API_KEY` / `DGX_BASE_URL` / `DGX_MODEL` | — / `api.deepseek.com` / `deepseek-chat` | the agent pane |

## Layout

| Tab | Purpose |
|---|---|
| 🧭 Working Principle | one screen explaining provenance tiers + the charter discipline |
| 🎬 Scenarios | the six question-mapped cards: Launch / Inject / Heal, with the exact command preview |
| 📊 Vigil Live | how Vigil currently sees the cluster (findings, root-cause, cross-service, forecast, right-sizing) |
| 🤖 Ask the Agent | pick a question → gather the mapped MCP tools → DeepSeek answer + raw grounding |
| 🩺 Cluster | live pods, armed sim faults, dependency reference |

## Safety

Every fault has a **Heal**; the sidebar **HEAL ALL / RESET** returns the namespace to a clean
baseline (clears sim flags, restores patched limits, removes PVC ballast, scales the backbone
back to 1). The DeepSeek key is read from the environment only — never hard-coded, never logged.

## Module map

- `studio.py` — the Streamlit app (5 tabs).
- `scenarios.py` — the six question-mapped scenarios (compose `faults.py`).
- `faults.py` — the fault primitives + metadata (single source of truth; what's shown == what runs).
- `cluster.py` — transparent `kubectl` wrappers (AWS-aware; each returns the exact command it ran).
- `vigil.py` — read-only obsd `/api` + `/mcp` client.
- `agent.py` — the MCP-grounded DeepSeek "Ask the agent" brain (one-shot, charter-disciplined).
