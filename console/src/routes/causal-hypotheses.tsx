// Causal hypotheses (TRUTH) — GET /api/causal-hypotheses + POST /api/causal-hypotheses/author
// (doc 22 C3). Coupled series that STEPPED TOGETHER, surfaced as PROJECTIONS + ASSOCIATIONS:
// the association (Pearson r) and the two onset times are MEASURED; the observed order is
// shown but is NOT a cause. The system refuses to draw the arrow — a NAMED operator authors
// the causal direction from their knowledge of the system, or marks it not-causal. Only that
// human decision becomes AUTHORED. This is the charter-clean form of the competitor's
// correlation-plus-precedence root cause (whose auto-direction invents edges, E3b/E4b).

import { authorCausalDirection, useCausalHypotheses } from "@/api/client";
import type { CausalDirectionResult, CausalHypothesisRow } from "@/api/types";
import {
  Button,
  LaneNote,
  Mono,
  ProvChip,
  SectionHead,
  StateBadge,
} from "@/components/ui/primitives";
import { AuthoredNote, CodeBlock, DataState, Page, Tag } from "@/components/ui/widgets";
import { relTime } from "@/lib/format";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

// Render a "CEI|metric" stream key compactly: "Kind name · metric".
function shortSeries(key?: string): string {
  if (!key) return "—";
  const bar = key.lastIndexOf("|");
  const metric = bar >= 0 ? key.slice(bar + 1).split("|")[0] : key;
  const cei = bar >= 0 ? key.slice(0, bar) : "";
  const p = cei.split("|");
  const name = p.length >= 5 ? `${p[3]} ${p[4]}` : cei;
  return `${name} · ${metric}`;
}

function SeriesOnset({
  label,
  name,
  dir,
  ts,
}: { label: string; name?: string; dir?: string; ts?: string }) {
  return (
    <div className="rounded-[5px] border border-rule bg-surface-hi px-2.5 py-1.5">
      <span className="v-eyebrow text-[9px] text-ink-low">{label}</span>
      <div className="v-mono text-[12px] break-all text-ink">{shortSeries(name)}</div>
      <div className="mt-0.5 text-[11px] text-ink-mid">
        stepped <span className="text-ink">{dir ?? "?"}</span>
        {ts ? <span className="text-ink-low"> · onset {relTime(ts)}</span> : null}
      </div>
    </div>
  );
}

export function CausalHypothesesPage() {
  const q = useCausalHypotheses();
  const qc = useQueryClient();
  const [reviewer, setReviewer] = useState(() => localStorage.getItem("vigil.reviewer") ?? "");
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [results, setResults] = useState<Record<string, CausalDirectionResult>>({});
  const [err, setErr] = useState<string | null>(null);

  function setReviewerPersist(v: string) {
    setReviewer(v);
    localStorage.setItem("vigil.reviewer", v);
  }

  async function author(h: CausalHypothesisRow, direction: "a-to-b" | "b-to-a" | "not-causal") {
    if (!reviewer.trim()) return;
    setBusy(h.id);
    setErr(null);
    try {
      const res = await authorCausalDirection(h.id, direction, reviewer.trim(), notes[h.id] ?? "");
      if (!res.ok) {
        setErr(res.message ?? "authoring failed");
        return;
      }
      setResults((r) => ({ ...r, [h.id]: res }));
      await qc.invalidateQueries({ queryKey: ["causal-hypotheses"] });
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <Page max="max-w-5xl">
      <SectionHead
        num="22"
        title="Causal hypotheses"
        lede="Coupled series that stepped together. The association and the two onset times are MEASURED; the observed order is shown but is never a cause. Vigil surfaces the lead and refuses to draw the arrow — you author the causal direction from your knowledge of the system, or mark it not-causal. Only your decision becomes AUTHORED."
      />

      <DataState q={q}>
        {(d) =>
          !d.enabled ? (
            <LaneNote kind="off" title="The causal-hypothesis lane is not enabled" note={d.note} />
          ) : (
            <div className="flex flex-col gap-6">
              {/* Operator identity — every authored direction is signed. */}
              <div className="v-panel flex flex-wrap items-center gap-3 p-3.5">
                <span className="v-eyebrow text-[10px]">authoring as</span>
                <input
                  value={reviewer}
                  onChange={(e) => setReviewerPersist(e.target.value)}
                  placeholder="your name or handle — required to author a direction"
                  className="min-w-[18rem] flex-1 rounded-[6px] border border-rule-strong bg-plane px-3 py-1.5 text-[13px] text-ink placeholder:text-ink-low focus:border-ink-low focus:outline-none"
                />
                {reviewer.trim() ? (
                  <StateBadge sev="ok">signed</StateBadge>
                ) : (
                  <StateBadge sev="degraded">name required</StateBadge>
                )}
              </div>

              {err && <div className="text-[12px] text-error">Authoring failed: {err}</div>}

              {(d.hypotheses ?? []).length === 0 ? (
                <LaneNote kind="empty" title="No co-onset hypotheses" note={d.note} />
              ) : (
                <div className="flex flex-col gap-4">
                  {(d.hypotheses ?? []).map((h) => {
                    const res = results[h.id];
                    return (
                      <div key={h.id} className="v-panel p-4">
                        {/* header: producer · relation · coefficient · when */}
                        <div className="mb-2.5 flex flex-wrap items-center gap-2">
                          <Tag title="the producer that proposed this">{h.source}</Tag>
                          <Tag sev="info">direction-free</Tag>
                          {h.relation && <Tag>{h.relation}</Tag>}
                          <ProvChip
                            kind="PROJECTED"
                            title="a projection + association, never a cause"
                          />
                          {typeof h.coefficient === "number" && (
                            <span
                              className="v-mono rounded-[4px] border border-rule bg-surface-hi px-1.5 py-0.5 text-[10px] text-ink-soft"
                              title="MEASURED association (Pearson r), undirected"
                            >
                              r = {h.coefficient.toFixed(3)}
                            </span>
                          )}
                          <span className="ml-auto v-mono text-[10.5px] text-ink-low">
                            {relTime(h.createdAt)}
                          </span>
                        </div>

                        {/* the two coupled onsets — projections + associations, side by side */}
                        <div className="mb-2.5 grid gap-2 sm:grid-cols-2">
                          <SeriesOnset
                            label="series A"
                            name={h.a}
                            dir={h.aDirection}
                            ts={undefined}
                          />
                          <SeriesOnset
                            label="series B"
                            name={h.b}
                            dir={h.bDirection}
                            ts={undefined}
                          />
                        </div>

                        {/* observed order — a MEASURED fact, explicitly NOT a cause */}
                        {h.observedFirst && (
                          <div className="mb-2.5 rounded-[6px] border border-rule bg-surface-hi px-3 py-2 text-[11.5px] leading-relaxed text-ink-soft">
                            <span className="v-eyebrow text-[9px] text-ink-low">
                              observed order (MEASURED)
                            </span>
                            <div className="mt-0.5">
                              <Mono>{shortSeries(h.observedFirst)}</Mono> stepped first
                              {typeof h.deltaSeconds === "number" ? ` (by ${h.deltaSeconds}s)` : ""}{" "}
                              —
                              <span className="text-warning"> this is the order, not a cause.</span>{" "}
                              Use your knowledge of the system to author the direction.
                            </div>
                          </div>
                        )}

                        {/* doc 33 P4 — the inference agent's SUGGESTED direction (PROJECTED). Admitted
                            only because both witnesses (onset order + lead-lag) agreed; still just a
                            hint — you author or reject it below. The system never draws this arrow. */}
                        {h.suggestedDirection && (
                          <div className="mb-2.5 rounded-[6px] border border-info/40 bg-surface-hi px-3 py-2 text-[11.5px] leading-relaxed">
                            <span className="v-eyebrow text-[9px] text-info">
                              agent-suggested direction (PROJECTED · dual-witness agreed)
                            </span>
                            <div className="mt-0.5 text-ink-soft">
                              <Mono>
                                {h.suggestedDirection === "not-causal"
                                  ? "not causal (co-occurs, but not a cause)"
                                  : h.suggestedDirection === "a-to-b"
                                    ? `${shortSeries(h.a ?? "A")} → ${shortSeries(h.b ?? "B")}`
                                    : `${shortSeries(h.b ?? "B")} → ${shortSeries(h.a ?? "A")}`}
                              </Mono>
                              {h.suggestedRationale ? ` — ${h.suggestedRationale}` : ""}{" "}
                              <span className="text-ink-low">
                                (a suggestion from {h.suggestedBy || "the agent"}; you author or
                                reject it)
                              </span>
                            </div>
                          </div>
                        )}

                        {/* evidence — the MEASURED facts it rests on */}
                        {(h.evidence ?? []).length > 0 && (
                          <div className="mb-3">
                            <div className="v-eyebrow mb-1 text-[9.5px]">
                              evidence ({(h.evidence ?? []).length})
                            </div>
                            <div className="flex flex-col gap-1">
                              {(h.evidence ?? []).map((ev, i) => (
                                <div
                                  key={i}
                                  className="flex flex-wrap items-start gap-2 rounded-[5px] border border-rule bg-surface-hi px-2.5 py-1.5"
                                >
                                  <span className="v-mono text-[10px] text-ink-low">{ev.kind}</span>
                                  <span className="v-mono text-[11px] break-all text-ink-soft">
                                    {ev.ref}
                                  </span>
                                  {ev.detail && (
                                    <span className="text-[11px] text-ink-mid">— {ev.detail}</span>
                                  )}
                                </div>
                              ))}
                            </div>
                          </div>
                        )}

                        {/* author the note + the DIRECTION (the human draws the arrow) */}
                        <textarea
                          value={notes[h.id] ?? ""}
                          onChange={(e) => setNotes((n) => ({ ...n, [h.id]: e.target.value }))}
                          placeholder="Author the note — why this direction holds (or why it is not causal). Becomes the AUTHORED prose."
                          rows={2}
                          className="w-full resize-y rounded-[7px] border border-rule-strong bg-plane px-3 py-2 text-[12.5px] text-ink placeholder:text-ink-low focus:border-ink-low focus:outline-none"
                        />
                        <div className="mt-2.5 flex flex-wrap items-center gap-2">
                          <Button
                            variant="primary"
                            disabled={!reviewer.trim() || busy === h.id}
                            onClick={() => author(h, "a-to-b")}
                            title={`author: ${shortSeries(h.a)} → ${shortSeries(h.b)}`}
                          >
                            A → B
                          </Button>
                          <Button
                            variant="primary"
                            disabled={!reviewer.trim() || busy === h.id}
                            onClick={() => author(h, "b-to-a")}
                            title={`author: ${shortSeries(h.b)} → ${shortSeries(h.a)}`}
                          >
                            B → A
                          </Button>
                          <Button
                            disabled={!reviewer.trim() || busy === h.id}
                            onClick={() => author(h, "not-causal")}
                          >
                            Not causal
                          </Button>
                          {!reviewer.trim() && (
                            <span className="text-[11px] text-warning">
                              set your name above to author
                            </span>
                          )}
                        </div>

                        {/* the result — the committable AUTHORED overlay, or the rejection */}
                        {res && (
                          <div className="mt-3 rounded-[6px] border border-rule-strong bg-plane px-3 py-2.5">
                            <div className="mb-1.5 flex flex-wrap items-center gap-2">
                              <ProvChip kind="AUTHORED" />
                              <StateBadge sev={res.status === "promoted" ? "ok" : "neutral"}>
                                {res.status ?? "done"}
                              </StateBadge>
                              {res.message && (
                                <span className="text-[11.5px] text-ink-soft">{res.message}</span>
                              )}
                            </div>
                            {res.overlayYaml && (
                              <>
                                <div className="v-eyebrow mb-1 text-[9.5px] text-ink-low">
                                  operator-authored overlay — ready to commit
                                </div>
                                <CodeBlock code={res.overlayYaml} lang="yaml" />
                              </>
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}

              <AuthoredNote source={d.class}>{d.note}</AuthoredNote>
            </div>
          )
        }
      </DataState>
    </Page>
  );
}
