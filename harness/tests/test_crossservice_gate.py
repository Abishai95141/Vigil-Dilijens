"""Cross-service cascade gate scoring + verdict regressions (doc 15 D / 11 §3.5)."""

from __future__ import annotations

from pathlib import Path

from harness.crossservice_gate import (
    MIN_FIRED_TICKS,
    GateReport,
    gate,
    load_events,
    load_label,
    score_bundle,
)

CORPUS = Path(__file__).resolve().parents[2] / "corpus" / "crossservice"


def tick(fired: bool, root: str = "", impacted: list | None = None,
         degraded: list | None = None, charter: bool = True) -> dict:
    return {
        "evalNow": "2026-06-14T00:00:00Z", "barsEpoch": 1, "findings": 1,
        "degraded": degraded or ([root] if root else []),
        "fired": fired, "root": root, "impacted": impacted or [],
        "charterClean": charter,
    }


def positive_bundle(name: str, root: str, callers: list[str], n_fired: int,
                    n_quiet: int = 2) -> tuple[list[dict], dict]:
    events = [tick(False) for _ in range(n_quiet)]
    events += [tick(True, root, callers, [root]) for _ in range(n_fired)]
    label = {"bundle": name, "scenario": "positive", "expect_fire": True,
             "root": root, "callers": callers}
    return events, label


def negative_bundle(name: str, n_ticks: int) -> tuple[list[dict], dict]:
    events = [tick(False) for _ in range(n_ticks)]
    label = {"bundle": name, "scenario": "negative", "expect_fire": False}
    return events, label


# Scored-bundle shorthands keep the call sites short and readable.
CHAOS_ROOT = "chaos/leaky-callee"
CHAOS_CALLERS = ["chaos/flow-caller"]
OB_ROOT = "ob/productcatalogservice"
OB_CALLERS = ["ob/frontend", "ob/checkoutservice", "ob/recommendationservice"]


def pos(name, root, callers, n_fired, n_quiet=2):
    return score_bundle(*positive_bundle(name, root, callers, n_fired, n_quiet))


def neg(name, n):
    return score_bundle(*negative_bundle(name, n))


def passing_corpus() -> GateReport:
    """Two distinct firing topologies (sufficient diversity) + a quiet negative,
    all clean — the shape a real PASS has."""
    return GateReport(bundles=[
        pos("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4),
        pos("boutique-fanin", OB_ROOT, OB_CALLERS, n_fired=4),
        neg("healthy", 8),
    ])


def test_clean_corpus_passes():
    g = passing_corpus()
    v = gate(g)
    assert g.fired_ticks >= MIN_FIRED_TICKS
    assert len(g.distinct_roots) == 2
    assert g.root_accuracy == 1.0
    assert g.caller_recall == 1.0
    assert g.caller_precision == 1.0
    assert g.false_cascades == 0
    assert g.charter_violations == 0
    assert v.passed and not v.insufficient


def test_wrong_root_fails():
    ev, lbl = positive_bundle("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4)
    ev[-1]["root"] = "chaos/flow-caller"  # one tick names a caller as the root
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("boutique-fanin", OB_ROOT, OB_CALLERS, n_fired=4),
        neg("healthy", 8),
    ])
    v = gate(g)
    assert not v.passed and not v.insufficient
    assert any("root accuracy" in r for r in v.reasons)


def test_false_cascade_fails():
    """A chain firing in a negative scenario is the trust-killing failure."""
    ev, lbl = negative_bundle("healthy", n_ticks=8)
    ev[3] = tick(True, OB_ROOT, ["ob/frontend"], [OB_ROOT])
    g = GateReport(bundles=[
        pos("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4),
        pos("boutique-fanin", OB_ROOT, ["ob/frontend"], n_fired=4),
        score_bundle(ev, lbl),
    ])
    v = gate(g)
    assert g.false_cascades == 1
    assert not v.passed and not v.insufficient
    assert any("false cascade" in r for r in v.reasons)


def test_charter_violation_fails():
    ev, lbl = positive_bundle("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4)
    ev[-1]["charterClean"] = False  # a fused causal token leaked into the chain
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("boutique-fanin", OB_ROOT, ["ob/frontend"], n_fired=4),
        neg("healthy", 8),
    ])
    v = gate(g)
    assert g.charter_violations == 1
    assert not v.passed and not v.insufficient
    assert any("charter" in r for r in v.reasons)


def test_phantom_caller_fails_precision():
    ev, lbl = positive_bundle("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4)
    ev[-1]["impacted"] = ["chaos/flow-caller", "chaos/innocent-bystander"]  # a non-caller
    g = GateReport(bundles=[
        score_bundle(ev, lbl),
        pos("boutique-fanin", OB_ROOT, ["ob/frontend"], n_fired=4),
        neg("healthy", 8),
    ])
    v = gate(g)
    assert not v.passed and not v.insufficient
    assert any("precision" in r for r in v.reasons)


def test_missed_caller_fails_recall():
    ev, lbl = positive_bundle("boutique-fanin", OB_ROOT, OB_CALLERS, n_fired=4)
    ev[-1]["impacted"] = ["ob/frontend"]  # two real callers went unnamed this tick
    g = GateReport(bundles=[
        pos("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4),
        score_bundle(ev, lbl),
        neg("healthy", 8),
    ])
    v = gate(g)
    assert not v.passed and not v.insufficient
    assert any("recall" in r for r in v.reasons)


def test_one_topology_is_insufficient():
    """A single firing topology cannot carry the gate, however clean."""
    g = GateReport(bundles=[
        pos("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=8),
        neg("healthy", 8),
    ])
    v = gate(g)
    assert v.insufficient and not v.passed
    assert any("scenario diversity" in r for r in v.reasons)


def test_no_negative_evidence_is_insufficient():
    """Two positives, no quiet cluster ever observed: the no-false-cascade claim
    is unsubstantiated → INSUFFICIENT, never a pass."""
    g = GateReport(bundles=[
        pos("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4, n_quiet=0),
        pos("boutique-fanin", OB_ROOT, ["ob/frontend"], n_fired=4, n_quiet=0),
    ])
    v = gate(g)
    assert v.insufficient and not v.passed
    assert any("negative evidence" in r for r in v.reasons)


def test_thin_fired_corpus_is_insufficient():
    g = GateReport(bundles=[
        pos("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=1),
        pos("boutique-fanin", OB_ROOT, ["ob/frontend"], n_fired=1),
        neg("healthy", 8),
    ])
    v = gate(g)
    assert v.insufficient and not v.passed
    assert any("positive evidence" in r for r in v.reasons)


def test_live_captured_corpus_passes():
    """Regression on the FROZEN, live-captured corpus (corpus/crossservice/) — the
    exact replay output that passed the gate on the kind cluster: chaos-pair
    (root=leaky-callee ← flow-caller) + chaos-fanin (root=leaky-hub ← 3 callers) +
    a healthy negative. If the scorer or the corpus ever regresses, this fails."""
    scenarios = [("events-pair.jsonl", "label-pair.json"),
                 ("events-fanin.jsonl", "label-fanin.json"),
                 ("events-healthy.jsonl", "label-healthy.json")]
    bundles = [score_bundle(load_events(CORPUS / ev), load_label(CORPUS / lbl))
               for ev, lbl in scenarios]
    g = GateReport(bundles=bundles)
    v = gate(g)
    assert g.fired_ticks == 20
    assert len(g.distinct_roots) == 2
    assert g.root_accuracy == 1.0
    assert g.caller_recall == 1.0
    assert g.caller_precision == 1.0
    assert g.false_cascades == 0
    assert g.charter_violations == 0
    assert g.negative_ticks == 16
    assert v.passed and not v.insufficient


def test_positive_bundle_no_fire_is_insufficient():
    """A positive scenario that never fired produced no gradeable evidence."""
    dud = ([tick(False) for _ in range(6)],
           {"bundle": "dud", "scenario": "positive", "expect_fire": True,
            "root": "ob/x", "callers": ["ob/y"]})
    g = GateReport(bundles=[
        pos("chaos-pair", CHAOS_ROOT, CHAOS_CALLERS, n_fired=4),
        pos("boutique-fanin", OB_ROOT, ["ob/frontend"], n_fired=4),
        score_bundle(*dud),
        neg("healthy", 8),
    ])
    v = gate(g)
    assert v.insufficient and not v.passed
    assert any("0 fired ticks" in r for r in v.reasons)
