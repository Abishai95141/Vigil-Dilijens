# Flow-edge spike — service-dependency discovery + cross-service cascade tracing

**Hypothesis-validation result (2026-06-13). Verdict: CONFIRMED on the recoverable
subgraph.** An EXPERIMENTAL spike (`obsd/internal/flow` + `obsd/cmd/flowprobe`),
fully isolated from the deterministic path, tested whether service→service
dependency edges can be DISCOVERED automatically from Linux conntrack (a MEASURED
fact) and whether the cascade machinery, given those edges + ONE authored
cross-service relation, can trace interdependent failures to a named root — without
the user declaring any topology.

## Method (chosen + adversarially hardened)

Snapshot `/proc/net/nf_conntrack` per node → recover the real callee behind
kube-proxy ClusterIP DNAT via the **reply tuple** (caller = orig src, callee = reply
src, service port = orig dport — correct for both DNAT'd and direct flows) → map both
ends to workload CEIs (reusing the identity scheme) → emit MEASURED "observed flow"
edges → walk them BACKWARD (impact propagates callee→caller) under one AUTHORED
relation to name a structural root. Zero app changes, no mesh, no eBPF programs, no
learned model. SNAT-masked cross-node callers are counted (`snat_masked_flows`), never
guessed; the recoverable subgraph rides the durable ESTABLISHED gRPC channels.

## Evidence (live `kind-vigil`, Online Boutique)

### C1 — dependency-graph accuracy: **PASS (perfect)**
Live reconstruction over 9 node-snapshots vs the 16-edge realizable answer key
(extracted from boutique `*_SERVICE_ADDR` env vars; `shoppingassistantservice` decoy
excluded):
- **Precision 1.000 · Recall 1.000 · F1 1.000** — all 16 real edges, 0 spurious, 0 missed.
- Decoy `shoppingassistantservice` correctly absent. Every direction correct.
- Coverage honest: resolvable 102, snat_masked 602 (dropped), infra 696 (filtered), unresolved 3.
- Key finding: persistent gRPC channels preserve the real caller IP even cross-node,
  so SNAT masking only hits short-lived probe traffic — the recoverable subgraph IS
  the full app graph here.

### C2 — root-cause tracing: **PASS (4/4 trials, discriminator holds)**
Reversible fault: liveness+readiness probes relaxed (timeout 5s, failureThreshold 6),
then CPU clamped to 10m. MEASURED degradation confirmed from cAdvisor (obsd's source):
**throttle ratio 0.960 > 0.25 bar**, pod stayed Ready, 0 restarts.
- productcatalog (3-way fan-in), trials 1–3: root named = `productcatalogservice`,
  impacted = {frontend, checkoutservice, recommendationservice} — **0 symptom-as-root**.
- **Discriminator** — currencyservice (2-caller fan-in), trial 4: root = `currencyservice`,
  impacted = {frontend, checkoutservice}; **recommendation correctly ABSENT** (it does
  not call currency). The walk traces actual callers, not a blanket fan-out.
- **Flow-disabled control:** obsd's existing edges (`runs-on`/`mounts`/`selects`) relate
  NONE of the callers to the hub — boutique has **0 PVCs** (no `mounts` path), each
  Service selects only its own pods (no cross-`selects`), and 2 of 3 callers are on a
  DIFFERENT node from the hub (no `runs-on` relation). So obsd alone sees three isolated
  findings; the flow edge is the only thing that connects them. This is exactly the
  "looks isolated, is a chain reaction" gap, closed.

### C3 — direction: **PASS** — directed-correct on all 16 edges, 0 reply-tuple phantoms.

### C4 — non-disruption: **PASS** — no production package imports `internal/flow`;
`flow` absent from `graph.knownTraversalEdgeTypes` + `params.requiredEdgeBudgets`; the
spike is a separate binary that never calls RunCycle/Digest and builds its own
EdgeStore. Full repo gate green with the package added (gofmt clean, vet ok,
`go test ./obsd/...` all ok incl. clock/detect/observe/replay).

### C5 — charter: **PASS** — flow edge labelled MEASURED ("observed flow", never
"depends-on"); exactly ONE authored relation, surfaced verbatim with author+version;
structural root labelled as position, not cause. Zero causal-claim tokens
(`cause`/`caused`/`due to`/…) in any generated chain (asserted by `cascade_test.go`
and re-grepped across all 4 live chains). JOIN, never FUSE.

## What it does NOT prove (deferred to a production lane)
Not a production collector (no DaemonSet/eBPF; the spike reads conntrack via
`docker exec` for the experiment). Cross-node SNAT caller recovery is unsolved (needs
per-netns conntrack or eBPF socket tracing). Does NOT wire flow into the production
`detect.related()`/Cascades walk while keeping replay byte-identical — a distinct,
larger task. The structural root is a MEASURED+topology+AUTHORED join, NOT a causal
claim, by construction. A green spike JUSTIFIES building the production flow lane; it
does not certify it.

## Artifacts
`obsd/internal/flow/` (conntrack parse+DNAT, resolver, graph, reverse-walk cascade,
chain, golden tests), `obsd/cmd/flowprobe/`, `ontology/graph/overlays/experimental/
flow-relation-v0.yaml` (the one authored relation). Design + adversarial spec produced
by the `vigil-flowedge-experiment-design` workflow (9 agents). Boutique left clean
(12/12 pods 1/1, resources restored).
