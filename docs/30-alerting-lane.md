# 30 — The alerting lane (off-digest email)

Vigil is pull-only by default: an operator reads the console or the API. The alerting
lane adds a **push** channel — it emails the CLASSED facts the surfacing layer already
publishes, so an on-call doesn't have to be watching. It is the same off-digest,
non-gating pattern as every other surfacing lane: **detection never waits on it, and
with it off the deterministic path and replay are byte-identical.**

Owning package: [`obsd/internal/notify`](../obsd/internal/notify) (transport + fatigue +
store) plus the where/when mapping in [`obsd/cmd/obsd/notify.go`](../obsd/cmd/obsd/notify.go).
Gated by `--alerts-enabled` (default off). Gate: `just alerts-gate`.

## Transport — Gmail SMTP, not a self-hosted gateway

The first sketch (a self-hosted WhatsApp gateway, OpenWA) was scrapped: it needed a
Docker sidecar, a headless Chromium, and QR pairing — far too heavy for an alert
channel. The lane uses **Gmail SMTP with an App Password** instead: you send *as* a
Gmail account, free (≈500 recipients/day), no SaaS signup, no sender-domain
verification. It is Go-stdlib `net/smtp` (PlainAuth + STARTTLS on `:587`), CGO-free.

The `Notifier` interface is the swap seam: `SMTPNotifier` (Gmail today) and
`FakeNotifier` (hermetic tests). Outgrow Gmail → change the SMTP host + creds (or write
a new `Notifier`); nothing else moves.

### Config (all env; the App Password is never logged or committed)

| Env | Default | Meaning |
|---|---|---|
| `ALERT_SMTP_USER` | — (required) | the sender Gmail address |
| `ALERT_SMTP_PASSWORD` | — (required) | the 16-char Gmail App Password (2FA must be on) |
| `ALERT_SMTP_HOST` | `smtp.gmail.com:587` | SMTP `host:port` |
| `ALERT_TO` | — (required) | comma-separated recipients |
| `ALERT_COOLDOWN` | `30m` | per-key cooldown (also the flap damper) |
| `ALERT_SHORT_LEAD` | `30m` | forecast crossing sooner than this ⇒ P1, else P2 |
| `ALERT_RATE_PER_WINDOW` | `10` | token-bucket size |
| `ALERT_RATE_WINDOW` | `1h` | token-bucket refill window |
| `ALERT_QUIET_HOURS` | off | `"22-7"` (24h, wraps midnight) — holds P2/P3 |
| `ALERT_QUIET_P1` | `true` | P1 still mails during quiet hours |
| `ALERT_FINDING_STALE_AFTER` | `90s` | a finding last matched longer ago is resolved, not firing |
| `ALERT_CONSOLE_URL` | — | base URL for deep links (e.g. `http://localhost:5173`) |
| `ALERT_INTERVAL` | `30s` | how often the lane evaluates the views |

If any required secret/recipient is absent the lane logs and stays **idle** (it never
crashes the process). With `--alerts-enabled` absent the lane is never constructed.

## Where & when — the trigger matrix

Each interval the lane reads the per-tick atomic views (`warningsView`,
`transitiveChainView`, `crossSvcView`, `eventsView`) and the findings store, maps the
**new** facts to alerts, and dispatches. One fact = one alert (deduped by key).

| When (the fact) | Source | Priority | Class(es) | Dedup key |
|---|---|---|---|---|
| **A cascade chain forms** — degraded workloads stitched over the authored relation | `flow.Chain` (transitive + one-hop cross-service) | **P1** | MEASURED edges + MEASURED symptoms + AUTHORED *why* | `cascade\|<sorted member nodes>` |
| **An OOM/crash fires** — `PHEN_OOM_KILL_{CGROUP,SYSTEM}`, `PHEN_PROBE_FAILURE_RESTART`, `PHEN_INIT_CONTAINER_FAILURE` | findings store (non-stale) + corroborated k8s events | **P1** | MEASURED | `oomcrash\|<ns/name>\|{oom\|crash}` |
| **A forecast crossing with short lead** — projected to cross a bar sooner than `ALERT_SHORT_LEAD` | `WarningsView` (only when `Enabled` — the gate passed) | **P1** | PROJECTED + AUTHORED precursors/at-risk | `forecast\|<cei>\|<metric>` |
| **A forecast crossing with comfortable lead / open band** | `WarningsView` | **P2** | PROJECTED + AUTHORED | `forecast\|<cei>\|<metric>` |

The OOM dedup bucket (`oom` vs `crash`, keyed on `ns/name`) makes a phenomenon finding
and its k8s event for the same workload **collapse to one mail**. The forecast P1/P2
split is the single declared `ALERT_SHORT_LEAD` threshold — not a learned score; an open
band (`LatestBeyondHorizon`) is always P2 (less certain).

**Not yet wired (documented follow-ups):** incident recurrence ≥ N (P2), band-departure
(P3, gate-pending), unexplained-loud (P3), a console *Alerts* page (provider/QR was the
OpenWA design; for email it becomes recipients + routing matrix + history), and
ACK/snooze. The `Store.History` + the `Record` rows already persist the durable history
those surfaces would read.

## Fatigue controls (all operator-DECLARED, never learned)

1. **Edge-trigger via cooldown** — a key mails, then is suppressed for `ALERT_COOLDOWN`;
   if still active afterward it re-fires (an escalation reminder). The cooldown state is
   durable in `alerts.db`, so a restart does **not** re-alert everything (idempotent).
2. **Coalesce** — a cascade is already one `flow.Chain` ⇒ one alert; and every alert
   selected in one tick is rendered into **one digest email**, not N.
3. **Quiet hours** — during the window, P2/P3 are *held* (re-surface on a later tick,
   never lost); P1 breaks through when `ALERT_QUIET_P1=true`.
4. **Rate limit** — an in-memory token bucket (`ALERT_RATE_PER_WINDOW` per
   `ALERT_RATE_WINDOW`); over budget ⇒ hold for the next tick.
5. **Flap suppression** — subsumed by the cooldown: a key flipping faster than the
   cooldown window mails at most once per window. (An explicit flip-counter is a
   follow-up if needed.)

## Charter

Every alert clause carries exactly one provenance class (MEASURED / PROJECTED /
AUTHORED), printed as `[CLASS] …`. The lane is a **dumb transport**: the mapping cites
each clause's class and copies any authored *why* **verbatim**; the `notify` package
re-derives no class and invents no prose. Enforced by `notify_test.go`:

- the **system-generated** scaffolding (subject, headlines, MEASURED/PROJECTED lines,
  the whole body minus authored notes and route URLs) is **causal-token-free** — no
  "cause/caused/because of/due to";
- a **PROJECTED** clause uses the "**projected to cross**" register, never "will";
- every `Line` carries a valid class; the verbatim AUTHORED note (which may legitimately
  read "…propagates to its callers…") is surfaced, not censored.

Off-digest + non-gating is mechanically guarded by `firewall_test.go`: the Go toolchain
is asked for the full transitive deps of each replay-deterministic package (detect,
selection, binding, observe, forecast, qss, graph, clock, replay) and the test fails if
`internal/notify` appears in any of them. A send failure records nothing — the fact
re-evaluates next tick (no silent drop).
