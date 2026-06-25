# Vigil Demo — Onboarding (try it in 5 minutes)

The fast path to bring everything up from a cold stop and run the demo yourself.
For the full act-by-act script, page reference, Ask prompts, and troubleshooting, see
[DEMO-GUIDE.md](DEMO-GUIDE.md).

---

## What you're starting
Five local processes talk to a real AWS k3s cluster over an SSH tunnel:

| Process | Port | What it is |
|---|---|---|
| SSH tunnel | 16443 | reaches the AWS k3s API (the cluster lives in AWS) |
| clockd | 50051 | TimesFM forecast backend |
| **obsd** | 9095 | the Vigil engine + MCP server (this is "the product") |
| console | 5173 | the Vigil operator UI (17 pages) |
| studio | 8501 | the demo console — inject scenarios + **Ask Vigil** agent |

> The two URLs you open: **console → http://localhost:5173**, **studio → http://localhost:8501**.
> The DeepSeek "Ask" agent is the **💬 Ask Vigil** tab in the **studio (8501)** — not the console.

---

## 1. Start everything (one command)
```bash
bash /tmp/vigil-up.sh
```
Idempotent — starts only what's down. Wait ~30–40 s. (This script + the tunnel keeper live in
`/tmp`; if the machine rebooted and they're gone, ping me to regenerate them — they hold the AWS IP
and the DeepSeek key, which is why they're not in the repo.)

## 2. Check it's up + the cluster is reachable
```bash
for p in 16443 50051 9095 5173 8501; do printf "%s:" $p; \
  lsof -nP -iTCP:$p -sTCP:LISTEN >/dev/null 2>&1 && echo UP || echo DOWN; done
curl -s http://localhost:9095/healthz; echo "  <- obsd (want: ok)"
KUBECONFIG=/tmp/aws-k3s-tunnel.yaml kubectl get pods -n abb-genix
```
Want: all 5 **UP**, obsd **ok**, **10/10 pods Ready** (9× `1/1` + `operations-dashboard` `2/2`).
After a fresh start, give obsd ~1–2 min to scrape before the console pages fill.

## 3. Run the demo
1. Open the **studio**: http://localhost:8501
2. **Sidebar → HEAL ALL / RESET** → wait for 10/10 (a clean baseline to start from).
3. **🎬 Scenarios tab → 🎷 The Grand Tour → Launch.** (One click injects two real faults.)
4. Watch it unfold (measured timing):
   - **~t+1 min** — historian `WORKLOAD_UNAVAILABLE` + cross-service cascade → console `/insights`, `/root-cause`
   - **~t+7 min** — memory-leak **onset** (anomaly) → console `/anomalies`
   - **~t+13 min** — **forecast early-warning card** → console `/forecast` (lead time counts down as it climbs)
5. **💬 Ask Vigil tab** → ask:
   `What is happening in our cluster right now — what, when, why, and how do I fix it?`
   → you get a structured advisory (WHAT / WHEN / WHY / HOW + blast radius + evidence + blind spots).
6. When done: **Sidebar → HEAL ALL / RESET** to return to a clean baseline.

> **Timing tip:** the forecast card is the slow one (~13 min). Click Grand Tour ~13–15 min before you
> want to show `/forecast`, or pre-warm it before judges arrive. Everything else is live by ~t+3 min.

## 4. Stop everything
```bash
for p in 8501 5173 9095 50051 16443; do lsof -tiTCP:$p | xargs -r kill; done
pkill -f vigil-tunnel-keeper
```

---

## If something's off (quick fixes)
| Symptom | Fix |
|---|---|
| Don't see the "Ask" page | It's the **💬 Ask Vigil** tab in the **studio (8501)**, not the console. Hard-refresh if needed. |
| Console pages empty / "MCP not running" | obsd is down → `lsof -tiTCP:9095 \| xargs -r kill; bash /tmp/vigil-up.sh` |
| `kubectl` hangs | tunnel dropped → `pkill -f vigil-tunnel-keeper; bash /tmp/vigil-up.sh` |
| HEAL ALL leaves historian down | (fixed now) — if it ever sticks: `KUBECONFIG=/tmp/aws-k3s-tunnel.yaml kubectl delete pod genix-historian-0 -n abb-genix` |
| AWS cluster IP changed | edit the IP in `/tmp/vigil-tunnel-keeper.sh`, then restart tunnel + obsd (see DEMO-GUIDE §2) |

Full troubleshooting + the verbatim demo narration: [DEMO-GUIDE.md](DEMO-GUIDE.md).
