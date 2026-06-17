// Referee (TRUTH) — POST /api/validate-claim. Paste a sentence; see the charter
// verdict. It flags fabricated causation, class-fusion, and future-certainty, and
// it RESCUES a phrasing that merely restates an AUTHORED relation. It is advisory
// and NEVER blocks — the absence of any "blocked" field IS the guarantee.

import { validateClaim } from "@/api/client";
import type { ClaimVerdict } from "@/api/types";
import { Button, ProvChip, SectionHead, StateBadge } from "@/components/ui/primitives";
import { AuthoredNote, Page, Tag } from "@/components/ui/widgets";
import type { Severity } from "@/lib/tokens";
import { useState } from "react";

const classSev = (c: string): Severity =>
  c === "advisory" ? "info" : c === "future-certainty" ? "degraded" : "firing";

const EXAMPLES = [
  "the memory leak caused the OOM kill",
  "currencyservice will crash in 10 minutes",
  "the leak and the restart are the same incident",
  "checkout latency rose while the cart cache was cold",
];

export function RefereePage() {
  const [claim, setClaim] = useState("");
  const [busy, setBusy] = useState(false);
  const [verdict, setVerdict] = useState<ClaimVerdict | null>(null);
  const [err, setErr] = useState<string | null>(null);

  async function run(text: string) {
    setBusy(true);
    setErr(null);
    setVerdict(null);
    try {
      setVerdict(await validateClaim(text));
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Page max="max-w-4xl">
      <SectionHead
        num="12"
        title="Referee"
        lede="Run a sentence through the charter. It flags generated causation, class-fusion, and future-certainty — and rescues a faithful restatement of an authored relation. Advisory, never blocking."
      />

      <div className="v-panel p-4">
        <textarea
          value={claim}
          onChange={(e) => setClaim(e.target.value)}
          placeholder="Paste a claim — e.g. a sentence an AI is about to say about this cluster…"
          rows={3}
          className="w-full resize-y rounded-[8px] border border-rule-strong bg-plane px-3 py-2.5 text-[13px] text-ink placeholder:text-ink-low focus:border-ink-low focus:outline-none"
        />
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button variant="primary" disabled={busy || !claim.trim()} onClick={() => run(claim)}>
            {busy ? "Checking…" : "Check claim"}
          </Button>
          <span className="text-[11px] text-ink-low">or try:</span>
          {EXAMPLES.map((ex) => (
            <button
              key={ex}
              type="button"
              onClick={() => {
                setClaim(ex);
                run(ex);
              }}
              className="cursor-pointer rounded-[5px] border border-rule-strong bg-surface-hi px-2 py-0.5 text-[11px] text-ink-mid transition-colors hover:text-ink"
            >
              {ex}
            </button>
          ))}
        </div>
      </div>

      {err && <div className="mt-4 text-[12px] text-error">Could not reach the referee: {err}</div>}

      {verdict && (
        <div className="mt-4 flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <StateBadge sev={verdict.flagged ? "degraded" : "ok"}>
              {verdict.flagged ? "flagged" : "clean"}
            </StateBadge>
            {verdict.matchedAuthored && <Tag sev="info">authored relation matched</Tag>}
            <Tag>best-effort · never blocks</Tag>
          </div>

          {verdict.reasons.length > 0 ? (
            <div className="flex flex-col gap-2">
              {verdict.reasons.map((r, i) => (
                <div key={i} className="v-panel p-3.5">
                  <div className="mb-1.5 flex items-center gap-2">
                    <ProvChip kind={r.advisory ? "AUTHORED" : "MEASURED"} />
                    <Tag sev={classSev(r.class)}>{r.class}</Tag>
                    {r.advisory && (
                      <span className="text-[11px] text-ink-low">phrasing suggestion</span>
                    )}
                  </div>
                  <div className="text-[12.5px] leading-relaxed text-ink-mid">{r.detail}</div>
                </div>
              ))}
            </div>
          ) : (
            <div className="v-panel-inset p-3.5 text-[12.5px] text-ink-mid">
              Nothing flagged — but absence of a flag is not a guarantee of correctness.
            </div>
          )}

          <AuthoredNote>{verdict.note}</AuthoredNote>
        </div>
      )}
    </Page>
  );
}
