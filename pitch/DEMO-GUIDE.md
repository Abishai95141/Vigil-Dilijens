# Vigil — Live Demo Runbook (judge presentation)

Everything you need to run the demo end-to-end: what to open, what to say, what to click,
which page shows what, the exact Ask prompts, and how to fix it if anything breaks.

---

## 0. The two URLs (memorise these)

| URL | What it is | Use it for |
|---|---|---|
| **http://localhost:8501** | **Vigil Scenario Studio** (Streamlit) | Injecting the scenario + **the "💬 Ask Vigil" agent** |
| **http://localhost:5173** | **Vigil Console** (the product UI) | The 17 operator pages judges will look at |

> ⚠️ **The "Ask the agent" page is in the STUDIO (`:8501` → "💬 Ask Vigil" tab), NOT the console.**
> The console has no chat page. If you don't see the Ask page, you're on `:5173` — switch to `:8501`.

Behind them: `obsd` (the engine + MCP server) on `:9095`, `clockd` (forecast backend) on `:50051`,
and an SSH tunnel to the AWS k3s cluster on `:16443`.

---

## 1. Pre-flight (run ~10 min before the judges arrive)

### 1a. Bring the whole stack up (one command, idempotent)
```bash
bash /tmp/vigil-up.sh
```
This starts (only if not already up): the SSH tunnel, clockd, obsd+MCP, the console, and the studio.
Give it ~30 s.

### 1b. Verify everything is green
```bash
for p in 16443 50051 9095 5173 8501; do printf "%s:" $p; \
  lsof -nP -iTCP:$p -sTCP:LISTEN >/dev/null 2>&1 && echo UP || echo DOWN; done
curl -s http://localhost:9095/healthz; echo "  <- obsd"
KUBECONFIG=/tmp/aws-k3s-tunnel.yaml kubectl get pods -n abb-genix
```
You want: all 5 ports **UP**, obsd says **ok**, and **10/10 pods Ready** (9× `1/1` + `operations-dashboard` `2/2`).

### 1c. (Recommended) Pre-warm the forecast
The memory-leak forecast card needs ~10–15 min of sustained climb to mature (see §5, Act 4).
If you want the **Early-warning card visible from the start**, launch the scenario now in the studio
(§5 Act 1) and let it run while you set up. Otherwise launch it live and reach the Forecast page last.

---

## 2. If something breaks — fixes

### "I don't see the Ask page"
It's the **"💬 Ask Vigil"** tab at **http://localhost:8501**, 4th tab. If still missing:
hard-refresh (`Cmd+Shift+R`). If the studio is stale or crashed, restart it:
```bash
lsof -nP -iTCP:8501 -sTCP:LISTEN -t | xargs -r kill; sleep 2
cd /Users/abishaikc/Desktop/vigil/sim-studio
env CLUSTER_KUBECONFIG=/tmp/aws-k3s-tunnel.yaml OBSD_BASE=http://localhost:9095 \
  DGX_API_KEY=sk-fbff60a954ca4c3ca46dad3e3b05c359 DGX_BASE_URL=https://api.deepseek.com DGX_MODEL=deepseek-chat \
  /Library/Frameworks/Python.framework/Versions/3.13/bin/streamlit run studio.py \
  --server.port 8501 --server.headless true >/tmp/studio.log 2>&1 &
```

### Ask page says "DGX_API_KEY is not set"
The studio was started without the key. Restart it with the command just above (it sets the key),
or just re-run `bash /tmp/vigil-up.sh` after killing :8501.

### **The AWS IP changed** (cluster was restarted → new public IP)
The tunnel targets a hardcoded IP. Only one file needs editing:
```bash
# 1. edit the IP in the tunnel keeper:
#    open /tmp/vigil-tunnel-keeper.sh and change   ubuntu@3.108.191.127   to   ubuntu@<NEW_IP>
# 2. kill the old tunnel + restart it:
pkill -f vigil-tunnel-keeper; lsof -tiTCP:16443 | xargs -r kill
bash /tmp/vigil-up.sh          # re-creates the tunnel, leaves the rest up
# 3. obsd dies when its tunnel drops — restart obsd too:
lsof -tiTCP:9095 | xargs -r kill; bash /tmp/vigil-up.sh
```
The kubeconfig (`/tmp/aws-k3s-tunnel.yaml`) points at `127.0.0.1:16443` and **does not change** —
the k3s API cert is valid for `127.0.0.1`, so only the SSH target IP matters.
SSH key is `~/Downloads/vigil-cloud.pem`, user `ubuntu`.

### Console pages are empty / "vigil MCP not running" / Ask returns nothing
**This always means `obsd` is down** (it exits if its tunnel drops). Restart it:
```bash
lsof -tiTCP:9095 | xargs -r kill; bash /tmp/vigil-up.sh
# wait ~20s, then confirm:
curl -s http://localhost:9095/healthz
```
After obsd restarts it needs ~1–2 min to re-scrape before pages fill.

### `kubectl` hangs / "connection refused" to the cluster
The tunnel dropped. Check + restart:
```bash
nc -z 127.0.0.1 16443 && echo "tunnel UP" || echo "tunnel DOWN"
pkill -f vigil-tunnel-keeper; bash /tmp/vigil-up.sh
```

### The cluster is dirty from a previous run (stray findings)
Reset to a clean 10/10 baseline:
```bash
cd /Users/abishaikc/Desktop/vigil/sim-studio
CLUSTER_KUBECONFIG=/tmp/aws-k3s-tunnel.yaml /Library/Frameworks/Python.framework/Versions/3.13/bin/python3 \
  -c "import faults; [print(l, r.rc) for l,r in faults.global_reset()]"
KUBECONFIG=/tmp/aws-k3s-tunnel.yaml kubectl delete pod genix-historian-0 -n abb-genix   # force a clean re-pull
```
Or just click **HEAL ALL / RESET** in the studio sidebar.

### DeepSeek is slow / rate-limited
The agent makes ~5–20 tool calls then 1–2 synthesis calls; a full answer takes ~20–60 s. That's
normal. If it errors, just ask again — it's idempotent (read-only).

---

## 3. The map — what each surface contains

### Studio (`:8501`) — 6 tabs
| Tab | Contains |
|---|---|
| 🧭 Working Principle | One screen: the provenance tiers (MEASURED/PROJECTED/AUTHORED/ASSOCIATION/ADVISORY) + the charter. **Open this first to frame the pitch.** |
| 🎬 Scenarios | The scenario cards. **🎷 The Grand Tour** is first — Launch / Heal buttons + the exact kubectl preview. |
| 📊 Vigil Live | A quick read of how Vigil currently sees the cluster (findings, root-cause, forecast, right-sizing). |
| **💬 Ask Vigil** | **The DeepSeek agent.** Type a question → it calls Vigil's MCP tools live → returns a structured advisory card (WHAT/WHEN/WHY/HOW) + narrative + every tool call shown for audit. |
| 🤖 Ask (per-question) | The one-shot per-scenario answer (fixed tools). Use the chat tab instead for the wow factor. |
| 🩺 Cluster | Live pods, armed faults, dependency reference. |

### Console (`:5173`) — the 17 pages, in demo order
| Page | Route | Shows | One-liner to say |
|---|---|---|---|
| Cluster graph | `/` | Workload topology + observed-flow edges | "This is the live dependency graph — measured from real traffic, not declared." |
| Overview | `/overview` | The at-a-glance health summary | "Everything Vigil currently asserts, in one place." |
| Insights | `/insights` | The firing phenomena, ranked | "Each is a MEASURED match against an AUTHORED pattern — provenance shown, never blurred." |
| Root cause | `/root-cause` | The cross-service cascade chain | "It roots the chain on the most-upstream *measured-degraded* node, oriented only by an authored relation." |
| Events | `/events` | K8s events (ImagePullBackOff, OOMKilled) | "Discrete facts, joined to phenomena — never fused." |
| Forecast | `/forecast` | Early-warning BandBar cards | "It forecasts each series to its OWN bar with a mandatory uncertainty band + a lead time." |
| Anomalies | `/anomalies` | CUSUM onsets (the inbox) | "Off-digest onset detection: it marks *when* a step started, keyed to the exact container." |
| Incidents | `/incidents` | Grouped incidents | "Two independent faults stay TWO incidents — it refuses to merge unrelated problems." |
| Timeline | `/timeline` | Matches + unexplained + projected over time | "The whole story on one axis." |
| Coverage | `/coverage` | What Vigil can/can't detect + why | "It's honest about its own reach — declared bars, rule-bound metrics, strays." |
| Silence | `/silence` | The silence ledger (sensors with no signal) | "Absence is a first-class signal — 'I looked and saw nothing' is different from 'I didn't look.'" |
| Governance | `/governance` | Candidate proposals + AI-triage | "The agent proposes; a named human promotes. The system never approves itself." |
| Causal hypotheses | `/causal-hypotheses` | Direction-free association edges | "Co-moving series — a LEAD, not a cause. The operator authors the arrow." |
| Right-sizing | `/right-sizing` | Reclaim / resize-up advisories | "p95-vs-request, always-on. Advisory only — a human acts." |
| Referee | `/referee` | The determinism/replay audit | "Same inputs → byte-identical outputs. It's a deterministic engine." |
| Config | `/config` | Profile, params, graph release/hash | "Pinned to an immutable release — `v0.18.0`, hash `f13e5101`." |
| Integrations (MCP) | `/integrations` | The MCP tool surface | "Everything you saw is also exposed over MCP — any AI can consume it." |

---

## 4. The 60-second framing (say this before you click anything)

> "Vigil is a **deterministic** Kubernetes observability engine. Three rules make it different:
> it only uses **borrowed-normativity bars** — it never invents a threshold; it **never invents
> causal direction** — only a human-authored relation is called a cause, everything else is a
> labelled association; and it's **honest about what it can't see**. The result: an engine that's
> smarter at *seeing and admitting* failures than tools that confidently guess. Let me show you a
> real incident, live, on a real cluster."

Open the **🧭 Working Principle** tab to anchor this, then go to **🎬 Scenarios**.

---

## 4b. MEASURED timeline (from a full clean rehearsal — what to expect after you click)

Verified live, click → fire time:

| Signal | Page | Fires at |
|---|---|---|
| `WORKLOAD_UNAVAILABLE` (historian) | `/insights`, `/root-cause` | **~t+1 min** |
| Cross-service cascade | `/root-cause` | **~t+2 min** |
| Memory-leak **onset** (anomaly) | `/anomalies` | **~t+7 min** |
| **Forecast early-warning CARD** | `/forecast` | **~t+13 min** (≈34-min lead, then it **counts down** as it climbs) |
| OOM (if left running) | — | ~t+30 min |

So: **click Grand Tour ~13–15 min before you want to land on `/forecast`** (or pre-warm it before the judges sit down). Everything except the forecast is solid by ~t+3 min. The forecast card persists from ~t+13 min until OOM (~t+30 min) — a ~15-min window — and the **shrinking lead time** ("34 min → 10 min") is a great thing to point at live. Ask the agent **after** the card is up and it folds the forecast into its answer (verified).

## 5. The demo flow (act by act)

### Act 1 — Inject the incident (Studio → 🎬 Scenarios)
- Click the **🎷 The Grand Tour** card → read the one-line preview (the exact kubectl ops) → click **Launch**.
- Say: *"I'm injecting TWO independent real faults at once: the historian database goes down via an
  image-pull failure, and a memory leak starts on the analyzer. Watch Vigil keep them separate."*
- Both faults are now real kubectl operations on the live cluster.

### Act 2 — The instant story: WHAT + WHY (Console)
Give it ~60–90 s, then walk these pages:
1. **`/` Cluster graph** — point at `genix-historian` going unhealthy and its edges.
2. **`/insights`** — `PHEN_WORKLOAD_UNAVAILABLE` on the historian. *"MEASURED — ready replicas below desired."*
3. **`/root-cause`** — the cascade: historian (the root) → its callers (asset-api, operations-dashboard).
   *"It names the upstream cause, not the nearest symptom."*
4. **`/events`** — the `ImagePullBackOff` event, joined to the phenomenon.
5. **`/incidents`** — note there are **two separate incidents**, not one merged blob.

### Act 3 — Governance: safe self-extension (Console → `/governance`)
- Show a **pending candidate** (a stray metric proposed to attach to an entity).
- Click **AI-triage** → the DeepSeek agent recommends *promote/reject* with a rationale and a confidence.
- Say: *"The agent proposes; it does NOT apply. A named human promotes — and every promotion is
  audited and reversible. The system never approves itself."* (Leave it as a recommendation, or promote
  one to show the audit entry.)

### Act 4 — WHEN: the forecast (Console → `/forecast`)
- By now (~10–15 min in, or pre-warmed in §1c) the **pdm-analyzer memory** early-warning card is up:
  a projected crossing of its ~243 Mi bar, an uncertainty band, and a **lead time**.
- Say: *"This is the part that matters at 3 a.m. — Vigil told us this WILL breach, with a lead time,
  BEFORE it pages anyone. The band is mandatory; it never gives a false-precision point estimate."*
- If the card hasn't matured yet: show **`/anomalies`** instead — the CUSUM onset already marks
  *when the leak started* and that it's climbing. (The forecaster needs sustained history to project a
  crossing — that's honest, not a gap; the agent can still give the ETA from the trajectory.)

### Act 5 — The closer: ASK THE AGENT (Studio → 💬 Ask Vigil)
This is the wow moment. Paste the flagship prompt (§6) and let it run (~30–60 s while it calls tools).
- Point at the **live tool-call trace** as it streams: *"It's not guessing — it's calling Vigil's
  read-only tools and grounding every claim."*
- When it lands, read the **advisory card**: WHAT / WHEN / WHY / HOW / blast-radius / confidence,
  then the evidence (each tagged MEASURED/AUTHORED/ASSOCIATION/HYPOTHESIS), then the blind-spots.
- Say: *"Two problems, not conflated. A prioritized P0/P1/P2 runbook. And it tells you what it
  CAN'T see. That's an answer you can act on."*

### Act 6 — Reset
- Studio sidebar → **HEAL ALL / RESET** (or §2 "cluster is dirty"). Confirm 10/10 Ready.

---

## 6. The Ask prompts (paste these — they produce actionable answers)

Type these into **💬 Ask Vigil** (`:8501`). Each is grounded; the agent calls the tools itself.

1. **The flagship (use this one):**
   > `What is happening in our cluster right now — what, when, why, and how do I fix it?`
   → Full incident report: both faults, named entities + numbers, a P0/P1/P2 action plan, blind-spots.

2. **Root cause + blast radius:**
   > `What is the root cause of the historian outage, and which services are affected downstream?`
   → Names genix-historian (image-pull), the cross-service callers impacted, the remediation steps.

3. **Pre-incident / early warning:**
   > `Is there anything trending toward failure that I should act on before it pages someone?`
   → Surfaces the pdm-analyzer memory climb / forecast, with the lead time and a fix.

4. **Charter discipline (great for a skeptical judge):**
   > `Are any services affecting each other's resources? Be clear about what's correlation vs a real cause.`
   → Shows association edges labelled as LEADS, and refuses to assert direction Vigil hasn't authored.

5. **Optimisation (always answers, even with no incident):**
   > `Which workloads are over- or under-provisioned, and what should I set?`
   → A right-sizing table: reclaim vs resize-up vs honestly-unstable (no guess).

6. **Honesty surface:**
   > `What can Vigil NOT see in this cluster right now?`
   → The blind-spots: logs, DNS failures, registry internals, intra-container attribution, audit (off).

7. **On-call closer:**
   > `Give me a prioritized action plan for on-call right now, most urgent first.`
   → A ranked runbook with concrete kubectl commands.

> Tip: the agent is conversational — you can follow up ("tell me more about the asset-api errors",
> "what's the kubectl command for #1") and it keeps context.

### ✅ These prompts were live-validated against the mid-incident cluster
All scored **5/5 actionable + charter-clean** (grounded entities/numbers, provenance labelled, no invented cause, runnable kubectl). What each actually returned:

| # | Prompt | Tool calls | What it returned |
|---|---|---|---|
| 1 | what/when/why/how | 20 | "Two independent faults: genix-historian down (ImagePullBackOff) + asset-api stale → operations-dashboard." MEASURED/AUTHORED/HYPOTHESIS labelled, 5 kubectl steps. |
| 2 | root cause + downstream | 16 | Names historian (0/1, image-pull) + the asset-api→operations-dashboard cascade; labels the pull reason "unobservable" instead of inventing it; 7 steps. |
| 3 | trending toward failure | 19 | Historian down + recurring CPU throttling that escalated to probe-failure/OOM yesterday; flags historian→asset-api as HYPOTHESIS; 6 steps. |
| 4 | correlation vs cause | 18 | Cleanly separates the one AUTHORED cross-service cause from independent faults + node co-occurrences; 5 steps. |
| 5 | over/under-provisioned | 6 | "31 over-provisioned, 0 under, **do not touch 32 unstable**" with p95-vs-request numbers + patch commands. |
| 6 | what Vigil can't see | 11 | 10 critical phenomena invisible, 146/493 pairs silent, etcd/scheduler/CNI/logs unmonitored — 23 blind spots, all grounded. |

**Important demo nuance:** the agent leads with **firing phenomena** (historian down, asset-api staleness, throttling). The **pdm-analyzer memory leak** only becomes prominent once its **forecast card fires** (or it OOMs) — so ask the WHAT/WHEN/WHY/HOW prompt **after** you've shown the `/forecast` page, and the agent will fold the early-warning into its answer. Ask it earlier and you still get a strong multi-fault answer (historian + asset-api + throttling), just without the leak headline.

**Expect chronic background faults** (asset-api↔asset-registry staleness, node CPU throttling) — they're real conditions of this small single-node cluster, not bugs. Narrate them as a *strength*: "Vigil sees all of these and keeps them as **separate incidents** — it never merges unrelated faults." The agent does exactly that in every answer above.

---

## 7. Honest answers to likely judge questions

- **"Did you fake any of this?"** No — every fault is a real `kubectl` op (shown before it runs), every
  number is scraped from the live cluster, and the agent only relays grounded facts.
- **"Why are the Trace and Audit pages off?"** Those lanes need an OTel/audit source this cluster
  doesn't have, and **Vigil says so plainly instead of fabricating** — the call-graph value is already
  delivered live by the Topology + Dependency pages. That honesty is the product.
- **"Is the AI making up causes?"** No. It labels its own causal guesses as `HYPOTHESIS`, calls
  correlations associations, and only an AUTHORED relation is called a cause.
- **"Is it reproducible?"** Yes — `/referee` shows same-inputs → byte-identical outputs; the graph is
  pinned to immutable release `v0.18.0` (hash `f13e5101`).

---

## 8. Teardown (after the demo)
```bash
cd /Users/abishaikc/Desktop/vigil/sim-studio
CLUSTER_KUBECONFIG=/tmp/aws-k3s-tunnel.yaml /Library/Frameworks/Python.framework/Versions/3.13/bin/python3 \
  -c "import faults; [print(l, r.rc) for l,r in faults.global_reset()]"
# leave services up, or stop them:
for p in 8501 5173 9095 50051 16443; do lsof -tiTCP:$p | xargs -r kill; done
pkill -f vigil-tunnel-keeper
```
