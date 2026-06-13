import { useState } from "react";
import type { ChatResponse } from "./types";

// The Chat surface (doc 10 M7). A register-guarded responder: it answers ONLY
// from the structured snapshot (matched phenomena + their MEASURED state, the
// PROJECTED bands, coverage, and AUTHORED graph relations surfaced verbatim). It
// never improvises a cause and never speaks in unqualified future tense; a draft
// that would cross the charter is REFUSED, not sent — and the refusal is shown
// here as a first-class outcome, not hidden. Richer NL understanding and
// multi-turn context are the remaining M7 work; the guard is what makes it safe
// to ship at all and is fully present.

interface Turn {
  question: string;
  response?: ChatResponse;
  error?: string;
}

const SUGGESTIONS = [
  "what is matching now",
  "what is projected to cross",
  "what is the coverage",
  "why did the throttling cascade happen",
];

export function Chat() {
  const [turns, setTurns] = useState<Turn[]>([]);
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);

  const ask = async (question: string) => {
    const text = question.trim();
    if (!text || busy) return;
    setBusy(true);
    setQ("");
    const idx = turns.length;
    setTurns((t) => [...t, { question: text }]);
    try {
      const res = await fetch("/api/chat", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ question: text }),
      });
      if (!res.ok) throw new Error(`${res.status}`);
      const response = (await res.json()) as ChatResponse;
      setTurns((t) =>
        t.map((turn, i) => (i === idx ? { ...turn, response } : turn)),
      );
    } catch (err) {
      const error = err instanceof Error ? err.message : "request failed";
      setTurns((t) =>
        t.map((turn, i) => (i === idx ? { ...turn, error } : turn)),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-lg)",
      }}
    >
      <header>
        <h2 style={{ fontFamily: "var(--font-heading)" }}>Ask</h2>
        <p className="v-muted" style={{ fontSize: "var(--text-small)" }}>
          Answers come only from the structured findings, projections, coverage,
          and authored graph relations — each kept in its own register. The
          responder never generates a cause and never states an unqualified
          future; a draft that would cross the charter is refused, shown below
          as such.
        </p>
      </header>

      {turns.length === 0 && (
        <div
          style={{
            display: "flex",
            gap: "var(--space-xs)",
            flexWrap: "wrap",
          }}
        >
          {SUGGESTIONS.map((s) => (
            <button
              key={s}
              type="button"
              className="v-tab"
              onClick={() => ask(s)}
            >
              {s}
            </button>
          ))}
        </div>
      )}

      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-sm)",
        }}
      >
        {turns.map((t, i) => (
          <TurnView key={`${i}-${t.question}`} turn={t} />
        ))}
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          ask(q);
        }}
        style={{ display: "flex", gap: "var(--space-sm)" }}
      >
        <input
          style={{ flex: 1 }}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Ask about what is matching, projected, or the coverage…"
        />
        <button type="submit" className="v-tab" disabled={busy || !q.trim()}>
          {busy ? "…" : "Ask"}
        </button>
      </form>
    </div>
  );
}

function TurnView({ turn }: { turn: Turn }) {
  const r = turn.response;
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-xs)",
      }}
    >
      <div
        className="v-mono"
        style={{
          alignSelf: "flex-end",
          maxWidth: "80%",
          padding: "var(--space-xs) var(--space-sm)",
          borderRadius: "var(--radius-md, 8px)",
          background: "var(--surface-2)",
          fontSize: "var(--text-small)",
        }}
      >
        {turn.question}
      </div>
      {turn.error && (
        <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
          <p className="v-muted">Request failed ({turn.error}).</p>
        </div>
      )}
      {r?.refused && (
        <article
          className="v-card"
          style={{ borderColor: "var(--status-error)" }}
        >
          <div className="v-overline">Refused by the charter guard</div>
          <p style={{ marginTop: "var(--space-xs)" }}>{r.refusedReason}</p>
        </article>
      )}
      {r && !r.refused && (
        <article className="v-card">
          <p>{r.answer}</p>
          {r.citations && r.citations.length > 0 && (
            <p
              className="v-authored-note"
              style={{ marginTop: "var(--space-xs)" }}
            >
              references: {r.citations.join(", ")}
            </p>
          )}
        </article>
      )}
    </div>
  );
}
