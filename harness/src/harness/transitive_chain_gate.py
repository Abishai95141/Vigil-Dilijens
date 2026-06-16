"""Transitive root-cause chain gate (doc 15 cap. B / doc 11 §3.5).

Certifies the transitive root-cause chain over a FROZEN corpus, offline: MEASURED-degraded
workloads stitched into an ORDERED chain by walking the observed-flow topology, oriented
ONLY by the AUTHORED relation (never by timing). Each scenario was folded through the REAL
flow.TransitiveChains path — a pure function of (flow topology, degraded set, relation,
window) — graded against a LABEL ORACLE fixed by construction. The Go drift guard proves
the frozen corpus reproduces byte-identically (the off-digest "pure function" determinism).

Floors:
  1. CHAIN-COUNT      — the number of emitted chains equals the oracle's expectChains
                        (independent/disconnected faults yield SEPARATE chains, or none).
  2. ROOT-FIDELITY    — each emitted chain's root is the structural root the oracle fixes.
  3. PATH-FIDELITY    — the emitted ordered path edges equal the authored-reach the oracle
                        fixes (no missing hop, no invented hop).
  4. NO-FALSE-CHAIN==0 — THE CARDINAL RULE: no single chain ever contains BOTH members of
                        an independent pair (two coincident-but-unrelated faults, or a pair
                        separated only by a silent intermediate). Zero false linkings.
  5. CHARTER == 0     — no rendered chain restates a cause (the scaffolding is a JOIN).

A substantive VIOLATION fails the gate. Absent any violation, a corpus missing a required
scenario is INSUFFICIENT — never a pass.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass, field
from pathlib import Path

REQUIRED_SCENARIOS = {
    "linear-chain",
    "independent-faults",
    "silent-intermediate",
    "fan-in",
    "two-disjoint-real-chains",
    "healthy-no-degradation",
}

DENYLIST = [
    "cause",
    "caused",
    "causes",
    "causing",
    "root cause",
    "because of",
    "due to",
    "leads to",
    "results in",
]


@dataclass
class GateReport:
    scenarios: set = field(default_factory=set)
    chains_graded: int = 0
    count_violations: list = field(default_factory=list)
    root_violations: list = field(default_factory=list)
    path_violations: list = field(default_factory=list)
    false_chains: list = field(default_factory=list)
    charter_violations: list = field(default_factory=list)


def _chain_nodes(chain: dict) -> set:
    nodes = {chain.get("most_upstream_degraded_node")}
    for s in chain.get("path") or []:
        nodes.add(s.get("upstream"))
        nodes.add(s.get("downstream"))
    for sy in chain.get("symptoms") or []:
        nodes.add(sy.get("workload"))
    nodes.discard(None)
    return nodes


def _path_pairs(chains: list[dict]) -> set:
    pairs = set()
    for c in chains:
        for s in c.get("path") or []:
            pairs.add((s.get("upstream"), s.get("downstream")))
    return pairs


def _scrub_authored_why(chains: list[dict]) -> list[dict]:
    """Return chains with every AUTHORED verbatim `why` blanked, so the charter scan sees
    only the system-generated scaffolding (the curator's governed note is surfaced verbatim
    and is not subject to the generated-text guard). Mirrors flow.ScaffoldingForCharter."""
    def _blank_why(steps):
        return [{k: ("" if k == "why" else v) for k, v in (s or {}).items()} for s in (steps or [])]

    out = []
    for c in chains:
        c2 = dict(c)
        c2["path"] = _blank_why(c.get("path"))
        c2["chain"] = _blank_why(c.get("chain"))
        out.append(c2)
    return out


def score(bundles: dict[str, tuple[list[dict], dict]]) -> GateReport:
    g = GateReport()
    for name, (chains, label) in bundles.items():
        scenario = label.get("scenario", name)
        g.scenarios.add(scenario)
        g.chains_graded += len(chains)

        expect_chains = int(label.get("expectChains", 0))
        if len(chains) != expect_chains:
            g.count_violations.append(f"{name}: {len(chains)} chains, expect {expect_chains}")

        # root-fidelity: the emitted roots equal the oracle's structural roots
        expect_roots = sorted(label.get("expectRoots") or [])
        got_roots = sorted(c.get("most_upstream_degraded_node") for c in chains)
        if expect_chains > 0 and got_roots != expect_roots:
            g.root_violations.append(f"{name}: roots {got_roots} != oracle {expect_roots}")

        # path-fidelity: the emitted path edges equal the authored-reach
        expect_path = {(p[0], p[1]) for p in (label.get("expectPath") or [])}
        got_path = _path_pairs(chains)
        if got_path != expect_path:
            g.path_violations.append(f"{name}: path {sorted(got_path)} != {sorted(expect_path)}")

        # CARDINAL: no single chain contains both members of an independent pair
        independent = [tuple(p) for p in (label.get("independentPairs") or [])]
        for c in chains:
            nodes = _chain_nodes(c)
            root = c.get("most_upstream_degraded_node")
            for a, b in independent:
                if a in nodes and b in nodes:
                    g.false_chains.append(
                        f"{name}: chain rooted {root!r} FALSELY links independent {a} + {b}"
                    )

        # charter — scan ONLY the system-generated scaffolding. The authored `why` is
        # curated, governance-reviewed text surfaced VERBATIM (the guard catches the system
        # fabricating a causal sentence, never censors the curator), so strip it first.
        blob = json.dumps(_scrub_authored_why(chains)).lower()
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
    if g.false_chains:
        reasons.append("NO-FALSE-CHAIN > 0 (CARDINAL): " + "; ".join(g.false_chains))
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
        "transitive-chain gate — class: measured_transitive_rootcause_over_observed_flow",
        f"  scenarios:        {sorted(g.scenarios)}",
        f"  chains graded:    {g.chains_graded}",
        f"  no-false-chain:   {len(g.false_chains)} (CARDINAL, max 0; faults never falsely merge)",
        f"  chain-count:      {ok(g.count_violations)} (emitted == oracle)",
        f"  root-fidelity:    {ok(g.root_violations)} (root == structural root)",
        f"  path-fidelity:    {ok(g.path_violations)} (path == authored-reach)",
        f"  charter:          {len(g.charter_violations)} violations (max 0)",
    ]
    if v.insufficient:
        lines.append(f"  GATE: INSUFFICIENT — {'; '.join(v.reasons)} (never a pass)")
    elif v.passed:
        lines.append(
            "  GATE: PASSED — the transitive root-cause chain is certified deterministic"
            " (authored-only direction, silent intermediates never bridged, zero false chains"
            " on coincident faults); the lane may be surfaced behind --flow-enabled (doc 11 §3.5)"
        )
    else:
        lines.append(f"  GATE: FAILED — {'; '.join(v.reasons)}")
    return "\n".join(lines)


def load_rows(path: str | Path) -> list[dict]:
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def load_label(path: str | Path) -> dict:
    return json.loads(Path(path).read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description="transitive-chain gate (doc 15 cap. B)")
    ap.add_argument("--chains", required=True, nargs="+", help="per-scenario chains JSONL")
    ap.add_argument("--labels", required=True, nargs="+", help="ground-truth oracle JSON(s)")
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
