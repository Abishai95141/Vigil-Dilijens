// Ask Vigil (assistant) — POST /api/chat. A grounded, register-safe responder. It
// answers from classed facts and cites its sources; a draft carrying a banned
// register is REFUSED first-class (never quietly softened). It never upgrades a
// class or improvises a cause.

import { askChat } from "@/api/client";
import type { ChatResponse } from "@/api/types";
import { Icon } from "@/components/ui/icons";
import { Button, ProvChip, SectionHead, StateBadge } from "@/components/ui/primitives";
import { Page, Tag } from "@/components/ui/widgets";
import { useState } from "react";

type Turn = { q: string; resp?: ChatResponse; err?: string };

const SUGGESTIONS = [
  "what is happening now?",
  "what is forecast to cross soon?",
  "what is not being watched, and why?",
  "what keeps recurring?",
];

export function ChatPage() {
  const [turns, setTurns] = useState<Turn[]>([]);
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);

  async function ask(text: string) {
    if (!text.trim()) return;
    setBusy(true);
    setQ("");
    const idx = turns.length;
    setTurns((t) => [...t, { q: text }]);
    try {
      const resp = await askChat(text);
      setTurns((t) => t.map((x, i) => (i === idx ? { ...x, resp } : x)));
    } catch (e) {
      setTurns((t) => t.map((x, i) => (i === idx ? { ...x, err: String(e) } : x)));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Page max="max-w-3xl">
      <SectionHead
        num="15"
        title="Ask Vigil"
        lede="A grounded assistant. It answers from MEASURED, PROJECTED, and AUTHORED facts, cites its sources, and refuses — visibly — rather than improvise a cause it cannot ground."
      />

      <div className="flex flex-col gap-3">
        {turns.length === 0 && (
          <div className="flex flex-wrap gap-2">
            {SUGGESTIONS.map((s) => (
              <button
                key={s}
                type="button"
                onClick={() => ask(s)}
                className="cursor-pointer rounded-[6px] border border-rule-strong bg-surface-hi px-2.5 py-1 text-[12px] text-ink-mid transition-colors hover:text-ink"
              >
                {s}
              </button>
            ))}
          </div>
        )}

        {turns.map((t, i) => (
          <div key={i} className="flex flex-col gap-2">
            <div className="flex items-start gap-2.5">
              <Icon.ask size={16} className="mt-0.5 shrink-0 text-ink-low" />
              <div className="text-[13px] text-ink">{t.q}</div>
            </div>
            {t.err ? (
              <div className="ml-[26px] text-[12px] text-error">Could not reach chat: {t.err}</div>
            ) : t.resp ? (
              <div className="ml-[26px]">
                {t.resp.refused ? (
                  <div className="v-panel p-3.5">
                    <StateBadge sev="degraded">refused</StateBadge>
                    <div className="mt-2 text-[12.5px] text-ink-mid">{t.resp.refusedReason}</div>
                  </div>
                ) : (
                  <div className="v-panel p-3.5">
                    <div className="whitespace-pre-wrap text-[12.5px] leading-relaxed text-ink-soft">
                      {t.resp.answer}
                    </div>
                    {t.resp.citations?.length ? (
                      <div className="mt-3 flex flex-wrap items-center gap-1.5 border-t border-rule pt-2.5">
                        <ProvChip kind="MEASURED" title="cited classed facts" />
                        <span className="text-[10.5px] text-ink-low">cited:</span>
                        {t.resp.citations.map((c) => (
                          <Tag key={c}>{c}</Tag>
                        ))}
                      </div>
                    ) : null}
                  </div>
                )}
              </div>
            ) : (
              <div className="ml-[26px] text-[12px] text-ink-low">thinking…</div>
            )}
          </div>
        ))}

        <form
          onSubmit={(e) => {
            e.preventDefault();
            ask(q);
          }}
          className="sticky bottom-0 mt-2 flex items-center gap-2 bg-plane pb-1 pt-2"
        >
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Ask about the cluster…"
            className="flex-1 rounded-[8px] border border-rule-strong bg-surface-hi px-3 py-2 text-[13px] text-ink placeholder:text-ink-low focus:border-ink-low focus:outline-none"
          />
          <Button variant="primary" type="submit" disabled={busy || !q.trim()}>
            Ask
          </Button>
        </form>
      </div>
    </Page>
  );
}
