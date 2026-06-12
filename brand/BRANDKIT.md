# Vigil — Brand Kit & Design System

> The honest instrument. Vigil reports what is happening *now*, estimates what
> crosses a bar *soon*, and never invents *why*. The brand exists to make that
> discipline visible: an operator should read a finding's epistemic strength at a
> glance — before a single word.

**Single source of truth:** [`brand/tokens.css`](tokens.css) (CSS custom
properties) and [`web/src/provenance.ts`](../web/src/provenance.ts) (the class
labels + registers). Every surface imports these; nothing redefines a color, a
space value, or a font inline. Owning docs:
[01 — Epistemic Separation](../docs/01-epistemic-separation-charter.md),
[10 — Surfacing](../docs/10-surfacing-and-operator-experience.md).

---

## 1. Brand essence

| | |
|---|---|
| **Name** | Vigil — *vigilance*: the calm, constant watcher. |
| **Promise** | "Reports what is. Estimates what's soon. Never invents why." |
| **Personality** | Exact · calm · honest about its limits · never alarmist. |
| **Anti-personality** | Hype, urgency theatre, false certainty, "AI magic." |
| **One line** | Kubernetes-native AI observability with a conscience. |

Vigil is an **instrument**, not an oracle. The design language borrows from
precision tools (a calibrated gauge, an editorial citation, a forecast band) and
from the best data-dense observability surfaces (Grafana's quiet rigour,
Honeycomb's simplicity) — never from dashboards that shout.

---

## 2. The organizing principle — provenance-led design

Every statement-bearing datum carries exactly **one** provenance class, assigned
at birth (doc 01). The UI's first job is to keep them visually unmistakable and
**never fuse them** (doc 10 §3.1). So the three classes get the first, most
distinct identities; chrome, severity, and status are built around them and may
never impersonate them.

| Class | Means | Mood (register) | Identity | Treatment |
|---|---|---|---|---|
| **MEASURED** | A fact from the store, or its arithmetic consequence | Indicative — *"is"* | Teal, solid | Solid left-rule, filled value pills, `mono` numbers |
| **PROJECTED** | A forecast against a bar, with a mandatory band | Modal — *"might"* | Violet, translucent | **Band fill, never a line**; dashed centre; "~" qualifiers |
| **AUTHORED** | A curated graph note, surfaced verbatim + attributed | Attributed — *"per the graph"* | Muted gold, editorial | Quiet gold rule, italic note, author + version chip |

**Three non-negotiable rules** (each is a release-blocking review check):

1. **Never upgrade a class.** A co-occurrence is not a cause; a projection is
   never restated as measured.
2. **The band never collapses.** PROJECTED series render as a translucent band;
   there is no solid-stroke token for a forecast value.
3. **Color is never the only signal.** Each class also carries a text label, an
   icon, and a shape/treatment — legible in greyscale and for color-vision
   deficiency.

Composition is **adjacency, never blending**: a warning card *contains* a
projection and *cites* an authored edge; it never paraphrases them into one fused
causal sentence (doc 10 §4).

---

## 3. Logo

The mark is a **watcher's aperture** — the surfacing layer, the one place the
classes meet — holding three tiers of certainty that converge on a single focal
reading at the base: **join, never fuse**.

- Top **AUTHORED** — a dotted gold rule (attributed).
- Middle **PROJECTED** — a translucent violet band (modal).
- Bottom **MEASURED** — a solid teal bar (indicative), with a sightline + dot:
  the "now" the system reports.

Files: [`logo/vigil-mark.svg`](logo/vigil-mark.svg) (64×64 icon, favicons,
avatars) · [`logo/vigil-lockup.svg`](logo/vigil-lockup.svg) (mark + wordmark).

**Usage**
- Clear space ≥ the aperture's corner radius (16px at 64px scale) on all sides.
- Minimum size: mark 24px; lockup 120px wide.
- Wordmark is **Plus Jakarta Sans 700**, tracking −0.01em.
- **Don't:** recolor the three tiers, swap the class order, stretch, add a drop
  shadow, or place the dark mark on a busy photo (use the aperture container).

---

## 4. Color

Full values in [`tokens.css`](tokens.css). Dark-first (observability is
dark-first); a light mode keeps every hue and meaning identical and only swaps
neutrals.

### Provenance (reserved — never used for chrome)
| Token | Dark | Use |
|---|---|---|
| `--prov-measured-on-dark` | `#2dd4bf` | Measured rules, value pills, live marks |
| `--prov-projected-on-dark` | `#a78bfa` | Projection band + dashed centre |
| `--prov-authored-on-dark` | `#d8b765` | Authored rules, note chips |

Each ships `*-tint` (12% fill), `*-border` (~36%), `*-text`, and PROJECTED ships
`--prov-projected-band` (18% — the band fill).

### Severity ladder (doc 05 §3.4 — *inside* measured content only)
`--rung-below` quiet slate · `--rung-at-threshold` amber · `--rung-above`
orange · `--rung-well-above` red · `--rung-unknown` hatched. **"below" is
deliberately quiet** — healthy is unremarkable, never celebrated. These appear
only as small ladder pills/ticks attached to a measured variable, never as a
standalone class mark.

### Chrome, status, neutrals
`--brand-azure` `#4c82f7` is the only interactive accent (links, focus, primary
action) and **never encodes a class**. Operational status
(`--status-ok/warn/error/info`) is orthogonal to provenance. The neutral ink ramp
runs `--bg #0a0d13` → `--surface-1/2/3` → `--border*` → `--text-strong #eaedf4` /
`--text` / `--text-muted` / `--text-faint`.

**Contrast:** body text on its surface ≥ 4.5:1; large text and non-text marks ≥
3:1 (WCAG AA). All provenance-on-dark values clear AA on `--surface-1/2`.

---

## 5. Typography

The mandated pairing, with a data face added for numbers:

| Role | Family | Weights | Notes |
|---|---|---|---|
| **Titles & headings** | **Plus Jakarta Sans** | 600 / 700 | Warmth + authority; tracking −0.01em on display/H1 |
| **Subheadings & supporting/body** | **Poppins** | 400 / 500 / 600 | Friendly, legible; the default reading voice |
| **Data only** | **JetBrains Mono** | 400 / 500 | Values, units, CEI keys, digests, ladders — **never prose** |

Both brand faces are geometric sans; to keep hierarchy unmistakable, separate
them by **weight, size, color, and tracking**, not family alone — headings are
Plus Jakarta Sans semibold/bold in `--text-strong`; supporting text is Poppins
regular in `--text`/`--text-muted`.

**Scale** (size / line-height, 8pt-aligned): display 40/48 · h1 32/40 · h2 24/32
· h3 20/28 · h4 16/24 · body-lg 16/24 · body 14/20 · caption 12/16 · overline
11/16 (600, tracking +0.04em, uppercase) · mono 13/20. Tokens: `--text-*` /
`--lh-*`.

**Self-host** via `@fontsource` (committed lockfile, no runtime CDN — matches the
network-free build contract). Fallback `system-ui` everywhere.

---

## 6. Spacing, layout & radii

**8pt grid** with a 4pt half-step (Apple/Google convention; the research
consensus). Token ramp `--space-3xs … --space-5xl` = 2,4,8,12,16,24,32,40,48,64,80.

**Internal ≤ external:** padding inside an element is ≤ the margin around it, so
groups read as groups. Presets encode it: `--pad-card` 24 · `--pad-cell` 12 ·
`--pad-control` 12 16 · `--gutter` 24 · `--page-pad` 32.

**Radii:** `--radius-xs` 4 (pills) · `sm` 8 (controls) · `md` 12 (cards) · `lg`
16 (panels) · `full`. **Elevation** is restrained: `--shadow-1/2/3`, hairline
borders preferred over heavy shadows. Layout: 12-column fluid, max content width
~1200px for reports, gutter `--gutter`.

---

## 7. Core components

Specs live with the tokens; the canon:

- **Provenance card** — the keystone. A 3px left-rule in the class color, a class
  chip (icon + label) top-right, evidence body, and a drill-down affordance. One
  card = one class; multi-class findings *stack labelled sections*, never merge.
- **Severity rung** — a small 4-segment ladder; the active rung filled in its
  `--rung-*` color, others hairline. Always paired with the mono value and the
  bar it crossed (with bar provenance: config vs default-flagged).
- **Coverage cell** — the honest-map unit: a state swatch (`--cov-*`) + label +
  reason on hover. Every (entity, variable) pair has a visible cell; gaps are
  cells, not blanks.
- **Projection band** — translucent `--prov-projected-band` fill between low/high
  bounds, dashed centre line; the bar drawn as a solid reference. Renders even at
  mobile width; the band never degrades to a point.
- **Suspect edge / stale mark** — dashed stroke (`--suspect-dash`) — topology
  edges past their validity budget, and stale fingerprints, are visibly distinct
  (doc 03/05), never silently solid.
- **Buttons / inputs / tags / tables / nav** — `--radius-sm`, `--pad-control`,
  focus `--ring-focus`; tables use mono for numeric columns, right-aligned.

**Motion:** 120–260ms, `--ease-out`; calm fades and slides only — no pulsing,
flashing, or attention-grabbing motion (doc 10 §6, alert fatigue is the enemy).

---

## 8. Voice & microcopy (register at render — doc 01 §5)

- **MEASURED** — indicative, with the value & provenance: *"Working set 119.1 MiB,
  above its 121.6 MiB limit (config)."*
- **PROJECTED** — always modal, always a band: *"Projected to cross in ~13 min
  (band 9–22)."* Never an unqualified future tense.
- **AUTHORED** — attributed, verbatim: *"Known OOM precursor — per the graph
  (vigil-engineering, v1)."* Never paraphrased into a causal claim.
- **The honesty clause** — when nothing explains something, say so plainly: *"No
  curated pattern explains this; flagged unexplained."* Never improvise a bridge.

Numbers, units, IDs, and digests are mono. Avoid exclamation marks; avoid
"critical/urgent" unless a declared bar says so.

---

## 9. Accessibility checklist (per surface)

- [ ] Every provenance mark has label + icon + treatment (not color alone).
- [ ] Text ≥ 4.5:1, non-text marks ≥ 3:1 on their surface.
- [ ] Focus visible (`--ring-focus`) on every interactive element.
- [ ] PROJECTED renders a band at the narrowest supported width.
- [ ] Degraded/suspect/stale marks are never elided for visual cleanliness.
- [ ] A card without a complete derivation path is a release-blocking defect.

---

*This kit is versioned with the product. Changing a provenance hue, the class
order, or the band rule is a charter-level change — review with doc 01.*
