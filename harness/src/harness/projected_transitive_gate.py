"""Multi-hop projected cascade gate (doc 15 cap. D / doc 11 §3.5).

Certifies the multi-hop PROJECTED cascade over a FROZEN corpus: ONE forecast root rippling
to its transitive callers over observed-flow edges, each downstream node inheriting the
root's band WIDENED per hop. Each scenario was folded through the REAL
flow.ProjectedTransitiveChains path (a pure function), graded against a LABEL ORACLE.

The forecast is PROJECTED by class, so this gate certifies the JOIN, not the tick-digest
(its substrate, the clock, is non-deterministic by class) — but the producer IS a pure
function, so the Go drift guard proves the frozen corpus reproduces byte-identically.

Floors:
  1. BAND-MONOTONICITY == 0 (CARDINAL / absolute-zero) — along EVERY chain, no inherited
     band ever NARROWS downstream: earliest_child ≤ earliest_parent AND latest_child ≥
     latest_parent, checked hop-by-hop from the root band outward. A downstream node tighter
     than its parent is PROJECTED-dressed-as-stronger — band narrowing at any hop = fail.
  2. PROJECTED-CLASS — every band is class PROJECTED and the root symptom is PROJECTED;
     never a MEASURED class on a forecast-derived node (no class laundering).
  3. ONE-ROOT-PER-CHAIN — each chain has exactly one forecast root (hops_from_root==0 lives
     only on the chain's root band); downstream nodes carry inherited bands (hops ≥ 1).
  4. CHAIN-COUNT / ROOT-FIDELITY / PATH-FIDELITY — emitted count/roots/path == the oracle.
  5. CHARTER == 0 — no causal token in the SYSTEM-GENERATED scaffolding. For a PROJECTED
     cascade the denylist ALSO bans propagation verbs (propagates / cascades to / flows to):
     the system must never assert the ripple as fact. The authored `why` is excluded (verbatim).

A substantive VIOLATION fails the gate; a corpus missing a required scenario is INSUFFICIENT.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

REQUIRED_SCENARIOS = {
    "linear-2hop",
    "fan-out",
    "two-roots",
    "open-horizon",
    "no-caller",
    "no-forecast",
    "midnight-wrap",     # the band straddles 00:00Z UTC — RFC3339 edges, no wrap-drop
    "tight-root",        # a zero-width forecast root never collapses the band to a line
    "maxhops-bounded",   # the bounded config the operator runs (hop-ceiling gap)
}

# The base causal denylist PLUS the projection verbs (doc 15 §4.D point 5): a PROJECTED
# cascade's generated scaffolding must never assert the ripple as fact.
DENYLIST = [
    "cause", "caused", "causes", "causing", "root cause", "because of", "due to",
    "propagates", "cascades to", "flows to",
]


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    chains_graded: int = 0
    band_violations: list = field(default_factory=list)
    width_violations: list = field(default_factory=list)
    class_violations: list = field(default_factory=list)
    root_count_violations: list = field(default_factory=list)
    count_violations: list = field(default_factory=list)
    root_violations: list = field(default_factory=list)
    path_violations: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)


def _band_collapsed(b: dict) -> bool:
    """A non-open band that has collapsed to a line (latest <= earliest) — doc 01 forbids a
    band that collapses. The edges are RFC3339 UTC, so the string compare is chronological."""
    if not b or b.get("open"):
        return False
    e, lt = b.get("earliest", ""), b.get("latest", "")
    return bool(e) and bool(lt) and lt <= e


def _scrub_why(chains: list[dict]) -> list[dict]:
    def blank(steps):
        return [{k: ("" if k == "why" else v) for k, v in (s or {}).items()} for s in (steps or [])]

    out = []
    for c in chains:
        c2 = dict(c)
        c2["path"] = blank(c.get("path"))
        c2["chain"] = blank(c.get("chain"))
        out.append(c2)
    return out


def _band_contains(parent: dict, child: dict) -> bool:
    """child's window must contain parent's: earliest_child <= earliest_parent and
    latest_child >= latest_parent. An open (beyond-horizon) far edge contains any finite."""
    if child.get("earliest", "") > parent.get("earliest", ""):
        return False
    if parent.get("open"):
        return bool(child.get("open"))
    if child.get("open"):
        return True
    return child.get("latest", "") >= parent.get("latest", "")


def score(bundles: dict[str, tuple[list[dict], dict]]) -> GateReport:
    g = GateReport()
    for name, (chains, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)
        g.chains_graded += len(chains)

        # chain-count
        expect_chains = int(label.get("expectChains", 0))
        if len(chains) != expect_chains:
            g.count_violations.append(f"{name}: {len(chains)} chains, expect {expect_chains}")

        # root-fidelity
        expect_roots = sorted(label.get("expectRoots") or [])
        got_roots = sorted(c.get("most_upstream_degraded_node") for c in chains)
        if expect_chains > 0 and got_roots != expect_roots:
            g.root_violations.append(f"{name}: roots {got_roots} != {expect_roots}")

        # path-fidelity
        expect_path = {(p[0], p[1]) for p in (label.get("expectPath") or [])}
        got_path = set()
        for c in chains:
            for s in c.get("path") or []:
                got_path.add((s.get("upstream"), s.get("downstream")))
        if got_path != expect_path:
            g.path_violations.append(f"{name}: path {sorted(got_path)} != {sorted(expect_path)}")

        for c in chains:
            root_label = c.get("most_upstream_degraded_node")
            root_band = c.get("root_band")
            # one-root-per-chain: exactly one hops_from_root==0 band (the root's), and no
            # path step claims to be the root.
            if root_band is None or root_band.get("hops_from_root") != 0:
                g.root_count_violations.append(f"{name}: {root_label!r} missing a hop-0 root band")
            for s in c.get("path") or []:
                hops = (s.get("band") or {}).get("hops_from_root", 0)
                if hops < 1:
                    g.root_count_violations.append(f"{name}: step hops_from_root {hops} <1")

            # projected-class: every band PROJECTED; the root symptom PROJECTED
            if root_band and root_band.get("class") != "PROJECTED":
                g.class_violations.append(f"{name}: root band class != PROJECTED")
            for s in c.get("path") or []:
                if (s.get("band") or {}).get("class") != "PROJECTED":
                    g.class_violations.append(f"{name}: a step band class != PROJECTED")
            for sy in c.get("symptoms") or []:
                if sy.get("class") not in ("PROJECTED", None):
                    g.class_violations.append(f"{name}: a symptom class != PROJECTED")

            # CARDINAL: band monotonicity hop-by-hop from the root band outward
            band_of = {root_label: root_band}
            for s in c.get("path") or []:
                band_of[s.get("downstream")] = s.get("band")
            for s in c.get("path") or []:
                parent = band_of.get(s.get("upstream"))
                child = s.get("band")
                if parent and child and not _band_contains(parent, child):
                    g.band_violations.append(
                        f"{name}: band NARROWS {s.get('upstream')}->{s.get('downstream')}"
                        f" (parent [{parent.get('earliest')},{parent.get('latest') or 'open'}]"
                        f" child [{child.get('earliest')},{child.get('latest') or 'open'}])"
                    )

            # WIDTH: a band must NEVER collapse to a line (doc 01) — a self-containing point
            # would slip the monotonicity floor, so check width explicitly on every band.
            for b in [root_band] + [s.get("band") for s in c.get("path") or []]:
                if _band_collapsed(b):
                    g.width_violations.append(
                        f"{name}: band COLLAPSED to a line ({b.get('earliest')}=={b.get('latest')})"
                    )

        # charter (scaffolding only — authored `why` excluded)
        blob = json.dumps(_scrub_why(chains)).lower()
        for p in DENYLIST:
            if p in blob:
                g.charter_violations.append(f"{name}: scaffolding carried banned phrase {p!r}")

    return g


@dataclass
class GateVerdict:
    passed: bool
    insufficient: bool
    reasons: list


def gate(g: GateReport) -> GateVerdict:
    reasons: list[str] = []
    if g.band_violations:
        reasons.append("BAND-MONOTONICITY > 0 (ABSOLUTE-ZERO): " + "; ".join(g.band_violations))
    if g.width_violations:
        reasons.append("BAND-WIDTH > 0 (a band never collapses): " + "; ".join(g.width_violations))
    if g.class_violations:
        reasons.append("PROJECTED-CLASS broken: " + "; ".join(g.class_violations))
    if g.root_count_violations:
        reasons.append("ONE-ROOT-PER-CHAIN broken: " + "; ".join(g.root_count_violations))
    if g.count_violations:
        reasons.append("CHAIN-COUNT broken: " + "; ".join(g.count_violations))
    if g.root_violations:
        reasons.append("ROOT-FIDELITY broken: " + "; ".join(g.root_violations))
    if g.path_violations:
        reasons.append("PATH-FIDELITY broken: " + "; ".join(g.path_violations))
    if g.charter_violations:
        reasons.append("charter: " + "; ".join(g.charter_violations))
    if reasons:
        return GateVerdict(passed=False, insufficient=False, reasons=reasons)

    missing = REQUIRED_SCENARIOS - g.scenarios
    if missing:
        reason = f"missing required scenario(s): {sorted(missing)}"
        return GateVerdict(passed=False, insufficient=True, reasons=[reason])
    return GateVerdict(passed=True, insufficient=False, reasons=[])


def render(g: GateReport, v: GateVerdict) -> str:
    ok = lambda bad: "OK" if not bad else "BROKEN"  # noqa: E731
    lines = [
        "projected-transitive gate — class: projected_multihop_cascade (forecast the ripple)",
        f"  scenarios:        {sorted(g.scenarios)}",
        f"  chains graded:    {g.chains_graded}",
        f"  band-monotonicity:{len(g.band_violations)} (ABSOLUTE-ZERO; never narrows downstream)",
        f"  band-width:       {len(g.width_violations)} (a band never collapses to a line; doc 01)",
        f"  projected-class:  {ok(g.class_violations)} (every band PROJECTED; no laundering)",
        f"  one-root-per-chain:{ok(g.root_count_violations)} (one root; downstream inherits)",
        f"  chain/root/path:  {ok(g.count_violations + g.root_violations + g.path_violations)}",
        f"  charter:          {len(g.charter_violations)} (max 0; +ban propagation verbs)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the multi-hop projected cascade is certified: one forecast root, an"
            " inherited band that WIDENS every hop and never collapses, JOIN-verified off-digest"
            " (doc 11 §3.5). The lane stays gate-pending for the OPERATOR until a real 2-hop"
            " lead+confirm is observed on the cluster (a new PROJECTED class)."
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_rows(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="projected-transitive gate (doc 15 cap. D)")
    ap.add_argument("--chains", required=True, nargs="+")
    ap.add_argument("--labels", required=True, nargs="+")
    args = ap.parse_args()
    if len(args.chains) != len(args.labels):
        ap.error("--chains and --labels must pair up")
    bundles: dict[str, tuple[list[dict], dict]] = {}
    for ch, lbl in zip(args.chains, args.labels, strict=True):
        label = load_label(lbl)
        bundles[label.get("bundle", ch)] = (load_rows(ch), label)
    g = score(bundles)
    v = gate(g)
    print(render(g, v))
    return 0 if v.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
