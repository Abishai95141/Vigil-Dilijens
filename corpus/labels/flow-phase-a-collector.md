# Phase A — production flow collector (conntrack DaemonSet) — validated 2026-06-13

The spike read conntrack via `docker exec` (host access). Phase A (doc 15 §4) replaces
that with a real in-cluster collector: a per-node DaemonSet exposes the node's conntrack
table over HTTP, and the consumer fetches it via the **API-server node proxy** — the same
`nodes/<name>:<port>/proxy/` mechanism obsd already uses for cAdvisor and node-exporter.
No host access, no `docker exec` in the data path.

## What was built (branch `v2`)
- `obsd/cmd/conntrack-agent/` — a tiny CGO-free HTTP exposer: `GET /conntrack` serves
  `/proc/net/nf_conntrack`, `GET /healthz` checks readability. Does NOT parse/map — the
  thinnest privileged surface (read a file, write the wire). Parsing + DNAT + IP→CEI stay
  in obsd (`internal/flow`), where the identity informer lives.
- `deploy/flow/Dockerfile` — `FROM scratch` over the static binary (no registry pull;
  /proc is a runtime kernel mount).
- `deploy/flow/conntrack-agent.yaml` — hostNetwork (so `/proc/net/nf_conntrack` is the
  node's own table), privileged (v1; tighten to CAP_NET_ADMIN later), `:9111`, tolerates
  control-plane. 3/3 pods Running on kind `vigil` (arm64).
- `obsd/cmd/flowprobe` — new `-conntrack-source proxy`: fetches via
  `kubectl get --raw /api/v1/nodes/<node>:9111/proxy/conntrack`.

## Live verification (Online Boutique, production collector path)
- **The proxy path is FAITHFUL:** on vigil-worker the agent-via-proxy returned **109**
  conntrack rows vs **109** via `docker exec` — identical. When cartservice was down
  (OOMKilled), both showed `dport=7070: 0` — the collector reflects reality, it does not
  lose or invent.
- **C1 graph accuracy via the in-cluster collector (healthy cluster, 6 snapshots):**
  **Precision 1.000 · Recall 0.938 · F1 0.968** — 15/16 edges, **0 spurious**, decoy
  (`shoppingassistantservice`) absent, coverage honest (snat_masked + unresolved counted).
  The one miss (`checkoutservice→cartservice`) is a **request-scoped purchase-path edge**
  — present only while a checkout runs — correctly surfaced as coverage, never faked.
- **Chain mode via proxy:** `-degraded productcatalogservice` → root =
  `productcatalogservice`, impacted = {checkoutservice, frontend, recommendationservice}.

## Verdict
The production collector mechanism works and is faithful to the host-level read. obsd
can now obtain per-node conntrack through its existing scrape-via-node-proxy pattern, with
no host access — ready for Phase B to assert the resulting flow edges into the EdgeStore.
Deferred to v2 (eBPF): cross-node SNAT caller recovery (still counted as `snat_masked`,
never guessed) and tightening privileged→CAP_NET_ADMIN. Boutique left healthy (13/13).
