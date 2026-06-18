# 05 — Environment & Runbook (what actually works on this machine)

> Copy-pasteable commands that **actually ran successfully** this session. The published
> `TESTING.md` has wrong commands (see [03-flaws-and-doc-bugs.md](03-flaws-and-doc-bugs.md)); use
> these instead. Re-confirm against v4.

## Machine facts
- **Cluster: k3s** (single node `fedora`, containerd `2.2.3-k3s1`, k8s `v1.35.5+k3s1`), up ~16 days.
- **No Docker daemon** → `just up` / `kind` **do not work here**. Run against the existing k3s cluster.
- **kubeconfig: `/etc/rancher/k3s/k3s.yaml`** (canonical, correct CA).
  ⚠️ `~/.kube/config` is **stale** → obsd fails with `x509: certificate signed by unknown authority`
  (kubectl tolerates it, obsd does not). Always pass the k3s.yaml path to obsd.
- **API/health port: `:9095`** (not 8080).
- **`sqlite3` CLI missing** → use `python3 -c "import sqlite3; ..."`.
- `online-boutique` namespace is already deployed (pre-existing; a couple of stale crashlooping
  replicasets — harmless for the demo, enough core pods Running).
- Repo path has a space: `/run/media/santhankumar/New Volume/Vigil-Dilijens`.

## Phase 1 — contract tests (no cluster)
```bash
just ci                          # lint + gen-check + go test -race  → "go CI gate passed"
just clockd-test                 # 11 passed, 2 skipped
just harness-test                # 131 passed
CGO_ENABLED=0 go build ./...     # exit 0 (pure-Go)
```

## Phase 2 — all 11 corpus gates (no cluster)
```bash
for g in event-detection-gate app-slo-gate departure-gate transitive-chain-gate \
         projected-transitive-gate xsvc-gate xsvc-projected-gate validate-claim-gate \
         mcp-gate incident-gate events-gate; do
  just "$g" && echo "PASS $g" || echo "FAIL $g"
done
```

## Phase 3 — live run against k3s
```bash
cd "/run/media/santhankumar/New Volume/Vigil-Dilijens"
mkdir -p data evidence

# 1. Start obsd against k3s (CORRECT flags — note kubeconfig, referee, port):
nohup bin/obsd --kubeconfig /etc/rancher/k3s/k3s.yaml \
  --db data/vigil.db --store-dir data/captures \
  --referee-enabled --health-addr ":9095" > /tmp/vigil_obsd.log 2>&1 &
echo $! > /tmp/vigil_obsd.pid
sleep 50                          # let a few 15s eval ticks accumulate

B=localhost:9095
# 2. S1 provenance firewall:
curl -s $B/api/coverage  | python3 -m json.tool | head
curl -s $B/api/findings  | python3 -m json.tool
curl -s $B/api/unexplained | python3 -m json.tool   # blindSpot present
curl -s $B/api/warnings  | python3 -m json.tool     # enabled:false + gateNote
curl -s $B/api/insights  | python3 -m json.tool

# 2b. S1 storage separation (python, since no sqlite3 CLI):
python3 - <<'PY'
import sqlite3; con=sqlite3.connect("data/vigil.db")
print(sorted(r[0] for r in con.execute("select name from sqlite_master where type='table'")))
# expect: ['findings','incidents','unexplained']  — and NO 'warnings'
PY

# 3. S8 interdependency:
curl -s $B/api/topology | python3 -m json.tool | head -40
curl -s $B/api/root-cause-chain | python3 -m json.tool   # class: MEASURED ⋈ AUTHORED
curl -s $B/api/cross-service | python3 -m json.tool

# 4. S9 claim referee:
curl -s -X POST $B/api/validate-claim -H 'Content-Type: application/json' \
  -d '{"claim":"cartservice OOM was caused by adservice CPU spike"}' | python3 -m json.tool  # flagged:true
curl -s -X POST $B/api/validate-claim -H 'Content-Type: application/json' \
  -d '{"claim":"memory usage on recommendationservice is at 280 MiB"}' | python3 -m json.tool # flagged:false

# 5. S7 overhead:
ps -o pid,pcpu,rss,comm -p "$(cat /tmp/vigil_obsd.pid)"   # RSS was ~162-176 MiB (> 150 bar)

# 6. S3 unknown-anomaly (restraint) — the signature demo:
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
kubectl create namespace chaos --dry-run=client -o yaml | kubectl apply -f -
kubectl run stress-rogue -n chaos --image=polinux/stress-ng --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"stress-rogue","image":"polinux/stress-ng","command":["stress-ng","--cpu","2","--vm","1","--vm-bytes","128M","--timeout","300s"],"resources":{"limits":{"cpu":"500m","memory":"256Mi"}}}]}}'
sleep 90                          # image pull + run + discovery
curl -s $B/api/unexplained | python3 -m json.tool
# expect openCard: mark "anomalous — investigate · not-yet-explained", loud on CPU, NO causal words

# 7. S5 replay determinism:
kill -INT "$(cat /tmp/vigil_obsd.pid)"; sleep 4     # flush captures
bin/replay -bundle data/captures > data/replay1.log 2>&1
bin/replay -bundle data/captures > data/replay2.log 2>&1
diff data/replay1.log data/replay2.log && echo "ZERO DIFF — determinism holds"

# 8. cleanup:
kubectl delete ns chaos --ignore-not-found --wait=false
```

## Gotchas / troubleshooting
- obsd exits immediately with a TLS x509 error → you used `~/.kube/config`; use the k3s.yaml path.
- `/api/...` connection refused → wrong port; it's **9095**.
- `/api/validate-claim` 404 / never flags → you forgot `--referee-enabled`.
- Empty entity inventory → you forgot `--kubeconfig`.
- Live cascade chains empty in `/api/root-cause-chain` → needs `--flow-enabled` + the conntrack
  DaemonSet (not deployed); the Phase-2 gates prove that math instead.
