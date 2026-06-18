# Security & auth — threat model + the in-binary minimum (Track 4)

> Closes audit-v3 roadmap **#3** ("zero auth/authz/TLS on the served API + MCP —
> disqualifying for enterprise"). This documents the threat model, what obsd now enforces,
> and what is deliberately left to the platform.

## What obsd serves

On one listener (`--health-addr`, default `:9095`):

| Surface | Sensitivity | Auth |
|---|---|---|
| `/api/*` | **High** — full incident / topology / silence-ledger / coverage state (doc 10) | **bearer token (deny-by-default once set)** |
| `/mcp` | **High** — the same state via the MCP harness (read-only) | **bearer token** |
| `/metrics` | Medium — join accuracy, entity counts, edge staleness | open (Prometheus scraping) |
| `/healthz`, `/readyz` | Low — liveness/readiness only | open (kubelet probes) |

obsd is **read-only against the cluster** (the `deploy/rbac` ClusterRole grants only get/
list/watch) and **never auto-remediates**, so the API exposes information, not control —
but that information (what's failing, where, the topology) is itself sensitive.

## What's enforced now (the in-binary minimum)

- **Bearer token on `/api` + `/mcp`**, deny-by-default once configured:
  `obsd --api-token <tok>` or `$VIGIL_API_TOKEN`. A protected request without exactly
  `Authorization: Bearer <tok>` gets **401**; the compare is constant-time.
  (`obsd/internal/api/auth.go`.)
- **Loud warning when unauthenticated.** With no token, the surfaces are open (the dev
  default) and obsd logs a prominent warning naming the exposure at serve time.
- **Tested both ways:** `obsd/internal/api/auth_test.go` (hermetic: 401 none/wrong/no-prefix,
  200 correct, liveness+metrics always open) **and** `obsd/internal/e2e/auth_test.go`
  (live: the shipped binary enforces it over real HTTP).

## What is deliberately the platform's job (not in obsd)

This is an honest boundary, not a gap to hide:

- **TLS / mTLS.** obsd speaks plain HTTP. For transport encryption and per-caller
  identity, terminate TLS at an ingress / service mesh (Istio, Linkerd) in front of obsd,
  or run it behind `kubectl port-forward` for local use. The bearer token protects
  content; the mesh/ingress protects the transport.
- **`/metrics` exposure.** Left open for scraping; restrict it with a NetworkPolicy (or
  move it behind the mesh) rather than a token, since Prometheus scraping is the consumer.
- **Authz / RBAC for callers.** The token is all-or-nothing (one shared secret). Per-user
  authorization, audit logging, and rotation belong to the fronting gateway.

## Deployment guidance (and the cloud tie-in)

- **Never expose obsd's port via a public LoadBalancer** while unauthenticated. On the
  GCP single-node runbook the API stays on loopback + `kubectl port-forward`; if it must be
  reachable, set `--api-token` AND restrict the firewall/NetworkPolicy to known sources.
- For a real deployment: set `--api-token`, keep `/api` + `/mcp` off any public path, scrape
  `/metrics` only from in-cluster Prometheus, and front the whole thing with TLS at the
  ingress.

## Status

Bearer auth + deny-by-default + tests + this threat model close the "zero auth" finding to
a **documented, tested minimum**. mTLS/ingress integration and per-caller authz are a named
roadmap item (platform-layer), not a silent omission.
