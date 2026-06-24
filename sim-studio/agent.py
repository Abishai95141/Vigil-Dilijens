"""agent.py — the MCP-grounded "Ask the agent" brain.

This is the studio's stand-in for *any* MCP-connected agent (Claude Desktop, the console,
a script). It does exactly what such an agent does:

  1. picks the MCP tools relevant to the question,
  2. calls them over obsd's ``/mcp`` relay to gather GROUNDED, classed facts,
  3. asks DeepSeek (the same provider obsd's dgx lane uses) to synthesise an answer that is
     grounded ONLY in those facts, with provenance preserved and no invented causes.

The charter stance is enforced in the system prompt: the model SYNTHESISES from classed facts;
it never fabricates a cause and it labels provenance (MEASURED / PROJECTED / AUTHORED /
ASSOCIATION / ADVISORY). If the facts do not support an answer, it says so. Credentials come
from the environment (DGX_API_KEY / DGX_BASE_URL / DGX_MODEL) — never hard-coded, never logged.
"""
from __future__ import annotations

import json
import os
import urllib.request

import vigil

DGX_BASE_URL = os.environ.get("DGX_BASE_URL", "https://api.deepseek.com")
DGX_MODEL = os.environ.get("DGX_MODEL", "deepseek-chat")
DGX_API_KEY = os.environ.get("DGX_API_KEY", "")


# --------------------------------------------------------------------------- #
# Question → MCP-tool routing. Each showcase question is answered from a SPECIFIC
# set of classed surfaces, so the operator can see exactly which grounded facts
# fed the answer. "freeform" gathers the broad incident-context set.
# --------------------------------------------------------------------------- #
QUESTION_TOOLS: dict[str, list[str]] = {
    "How are PVC I/O patterns linked to pod restarts?": [
        "get_dependency", "get_causal_hypotheses", "get_events", "get_incidents", "get_findings",
    ],
    "Are different services influencing each other's resource consumption?": [
        "get_dependency", "get_causal_hypotheses", "get_onsets", "get_cross_service",
    ],
    "Which workloads need optimization?": [
        "get_rightsizing_advice",
    ],
    "Which pod is causing unexpected CPU spikes?": [
        "get_onsets", "get_findings", "get_unexplained", "get_dependency",
    ],
    "What's the cause of a failure in our cluster?": [
        "get_root_cause_chain", "get_insights", "get_cross_service", "get_findings", "get_timeline",
    ],
    "Is there any early warning, and how could I resolve it?": [
        "get_warnings", "get_insights", "get_departures",
    ],
}

# the broad set for a free-form question
FREEFORM_TOOLS = [
    "get_findings", "get_insights", "get_root_cause_chain", "get_cross_service",
    "get_warnings", "get_onsets", "get_dependency", "get_rightsizing_advice",
    "get_incidents", "get_events",
]

SYSTEM_PROMPT = (
    "You are Vigil's reasoning relay. You answer an operator's question about a live Kubernetes "
    "cluster using ONLY the grounded, classed facts provided to you from Vigil's MCP tools. "
    "Each fact carries a provenance class — MEASURED (observed), PROJECTED (forecast), AUTHORED "
    "(a human-curated causal relation), ASSOCIATION (an undirected correlation — a lead, NOT a "
    "cause), or ADVISORY (a right-sizing recommendation). Rules you must follow:\n"
    "  1. Ground every claim in the supplied facts. If the facts do not support an answer, say so "
    "plainly — never invent a cause, a metric, or an entity that is not present.\n"
    "  2. Preserve provenance. Call an association an association (a candidate link, not proof of "
    "causation); call a forecast a forecast; call a recommendation advisory.\n"
    "  3. Be concrete: name the specific workloads/pods, phenomena, metrics, edges, and (when "
    "present) lead times and remediation hints from the facts.\n"
    "  4. Be concise and operator-useful. Lead with the answer, then the evidence, then (if asked "
    "'how do I resolve it') the remediation grounded in the insight/forecast hints.\n"
    "  5. If a relevant surface is empty (no incident currently), say what that means: the cluster "
    "is currently clean on that dimension."
)


def _compact(obj, limit: int = 14000) -> str:
    """JSON-encode a tool result, trimmed so the prompt stays bounded."""
    try:
        s = json.dumps(obj, default=str)
    except Exception:
        s = str(obj)
    return s if len(s) <= limit else s[:limit] + " …(truncated)"


def gather(tools: list[str]) -> dict[str, object]:
    """Call each MCP tool and collect its parsed result, keyed by tool name."""
    out: dict[str, object] = {}
    for t in tools:
        out[t] = vigil.mcp_call(t)
    return out


def _facts_block(grounded: dict[str, object]) -> str:
    parts = []
    for tool, res in grounded.items():
        parts.append(f"### {tool}\n{_compact(res)}")
    return "\n\n".join(parts)


def deepseek(system: str, user: str, timeout: int = 60) -> str:
    """One-shot OpenAI-compatible chat completion against DeepSeek (the dgx provider)."""
    if not DGX_API_KEY:
        raise RuntimeError(
            "DGX_API_KEY is not set — export it (same key obsd uses) to enable the agent pane."
        )
    body = json.dumps(
        {
            "model": DGX_MODEL,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
            "temperature": 0.2,
            "stream": False,
        }
    ).encode()
    req = urllib.request.Request(
        DGX_BASE_URL.rstrip("/") + "/chat/completions",
        data=body,
        headers={"content-type": "application/json", "authorization": f"Bearer {DGX_API_KEY}"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as r:
        d = json.loads(r.read().decode())
    return d["choices"][0]["message"]["content"].strip()


def answer(question: str) -> dict:
    """Answer a question end-to-end: route → gather grounded facts → synthesise.

    Returns {question, tools, grounded, answer, error} so the UI can show BOTH the
    grounded facts (the provenance) and the synthesised answer.
    """
    tools = QUESTION_TOOLS.get(question, FREEFORM_TOOLS)
    grounded = gather(tools)
    user = (
        f"Operator question: {question}\n\n"
        f"Grounded facts from Vigil's MCP tools (each block is one tool's classed output):\n\n"
        f"{_facts_block(grounded)}\n\n"
        f"Answer the question using only these facts, preserving provenance."
    )
    res = {"question": question, "tools": tools, "grounded": grounded, "answer": "", "error": ""}
    try:
        res["answer"] = deepseek(SYSTEM_PROMPT, user)
    except Exception as e:
        res["error"] = f"{type(e).__name__}: {e}"
    return res
