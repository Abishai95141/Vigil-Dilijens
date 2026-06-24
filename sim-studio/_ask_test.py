"""Ad-hoc rehearsal harness for the Ask agent — run the flagship question and print the
validated advisory + narrative + tool trace. Not part of the shipped app (underscore-prefixed)."""
import json
import sys

import agent


def run(q: str) -> None:
    steps = []
    def on_step(name, args, result):
        steps.append(name)
        print(f"   🔧 {name} {json.dumps(args) if args else ''}", file=sys.stderr)
    res = agent.chat_agentic([{"role": "user", "content": q}], on_step=on_step)
    adv = res.get("advisory")
    print("=" * 70)
    print("QUESTION:", q)
    print("TOOLS CALLED:", ", ".join(steps) or "(none)")
    print("=" * 70)
    if adv:
        print("STRUCTURED ADVISORY (pydantic-validated):")
        print(f"  HEADLINE   : {adv.get('headline')}")
        print(f"  CONFIDENCE : {adv.get('confidence')}")
        print(f"  WHAT       : {adv.get('what')}")
        print(f"  WHEN       : {adv.get('when')}")
        print(f"  WHY        : {adv.get('why')}  [{adv.get('why_provenance')}]")
        print(f"  BLAST      : {adv.get('blast_radius')}")
        print("  HOW        :")
        for i, s in enumerate(adv.get("how", []), 1):
            print(f"     {i}. {s}")
        print(f"  EVIDENCE   : {len(adv.get('evidence', []))} grounded facts")
        for e in adv.get("evidence", [])[:6]:
            print(f"     - [{e.get('provenance')}] {e.get('claim')}  ({e.get('source')})")
        print("  BLIND SPOTS:")
        for b in adv.get("blind_spots", []):
            print(f"     - {b}")
    else:
        print("(no structured advisory — narrative only)")
    print("-" * 70)
    print("NARRATIVE:")
    print(res.get("answer"))


if __name__ == "__main__":
    run(sys.argv[1] if len(sys.argv) > 1 else
        "What is happening in our cluster right now — what, when, why, and how do I fix it?")
