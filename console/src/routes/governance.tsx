// Governance (TRUTH) — GET /api/governance + POST /api/governance/decide. The one place
// a DGX candidate becomes authoritative, and only by a NAMED human (doc 12 §3.3: the
// harness can block, it cannot approve). Every proposal — stray→entity mappings, agent
// associations, trace topology, audit hypotheses — is firewalled from detection until a
// human promotes it here. Promotion authors the note (the model's rationale is discarded)
// and emits a committable overlay; reject removes it. Clear evidence, signed decisions.

import { decideGovernance, useGovernance } from "@/api/client";
import type { GovernanceDecisionResult, GovernanceItem, GovernanceSupport } from "@/api/types";
import {
  Button,
  LaneNote,
  Mono,
  ProvChip,
  SectionHead,
  StateBadge,
  cx,
} from "@/components/ui/primitives";
import {
  AuthoredNote,
  CodeBlock,
  DataState,
  Page,
  Stat4,
  Table,
  Tag,
  Td,
  Tr,
} from "@/components/ui/widgets";
import { relTime } from "@/lib/format";
import type { Severity } from "@/lib/tokens";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

// Friendly producer labels — clear visibility into WHO proposed each candidate.
const SOURCE: Record<string, string> = {
  "cei-fallback": "stray-ER",
  "dgx-agent": "AI agent",
  trace: "trace lane",
  audit: "audit lane",
  assoc: "dependency",
};
const statusSev = (s: string): Severity =>
  s === "promoted" ? "ok" : s === "rejected" ? "neutral" : s === "shadow" ? "neutral" : "info";

// A one-line human summary of what the candidate proposes (never a causal claim).
function kindSummary(it: GovernanceItem): string {
  if (it.kind === "edge" && it.relation === "topology") return "observed call topology edge";
  if (it.kind === "edge") return "associated-with edge (never causal)";
  if (it.kind === "causal_hypothesis") return "direction-free co-occurrence hypothesis";
  if (it.kind === "node") return "provisional node (unmapped metric)";
  if (it.kind === "member") return "phenomenon member binding";
  if (it.kind === "bar_source") return "declared-bar pointer";
  return it.kind;
}

// Render a candidate's DETERMINISTIC support as COUNTS only (doc 21 §4) — verbatim integers,
// NEVER a confidence, percentage, or synthesized score (support is MEASURED, not a belief).
function supportLabel(s?: GovernanceSupport): string {
  const ev = s?.evidenceCount ?? 0;
  const de = s?.distinctEntities ?? 0;
  const cs = s?.captureSample ?? 0;
  const rc = s?.recurrence ?? 0;
  const parts = [`${ev} evidence`];
  if (de > 0) parts.push(`${de} ${de === 1 ? "entity" : "entities"}`);
  if (cs > 0) parts.push(`captures ${cs}`);
  if (rc > 0) parts.push(`seen ${rc}×`);
  return parts.join(" · ");
}

// Rank by the lexicographic tuple, STRONGEST first — a pure comparison of counts, no weight.
function cmpSupportDesc(a: GovernanceItem, b: GovernanceItem): number {
  const x = a.support;
  const y = b.support;
  if (!x || !y) return 0;
  if (x.evidenceCount !== y.evidenceCount) return y.evidenceCount - x.evidenceCount;
  if (x.captureSample !== y.captureSample) return y.captureSample - x.captureSample;
  if (x.recurrence !== y.recurrence) return y.recurrence - x.recurrence;
  if (x.distinctEntities !== y.distinctEntities) return y.distinctEntities - x.distinctEntities;
  return x.ageSeconds - y.ageSeconds; // newer (smaller age) ranks higher
}

// What promoting THIS candidate authors and where it lands — so the operator knows exactly
// what they are committing, and (just as important) what it does NOT do. Promotion changes
// the GRAPH (identity + topology); it never adds a metric bar, so it never makes a metric
// "watched" on its own — that is a separate detection-rule authoring act.
function promoteDestination(it: GovernanceItem): {
  becomes: string;
  lands: string;
  caveat: string;
} {
  if (it.kind === "edge" && it.relation === "topology")
    return {
      becomes: "an AUTHORED dependency edge (caller → callee)",
      lands: "the cluster graph topology — read by cascade & root-cause-chain reasoning",
      caveat: "Adds a dependency link, not a metric bar — it does not make any metric watched.",
    };
  if (it.kind === "edge")
    return {
      becomes: "an AUTHORED identity rule attributing this metric to the entity",
      lands: "the bound graph — the metric stops being a stray and is grouped under that entity",
      caveat:
        "Resolves WHICH entity owns the metric. It is still NOT watched against a bar — that needs a separate detection rule + a declared bar.",
    };
  if (it.kind === "causal_hypothesis")
    return {
      becomes: "a direction-free CO-OCCURRENCE record (change ↔ incident)",
      lands: "the investigation surface — shown to a human, never asserted as a cause",
      caveat: "Asserts no causal direction and never drives detection.",
    };
  if (it.kind === "node")
    return {
      becomes: "a provisional node for the unmapped metric (no entity link yet)",
      lands: "the candidate graph only — it is below the ≥2-coordinate identification floor",
      caveat: "Low value alone: it maps to nothing. Prefer promoting a mapping EDGE.",
    };
  return { becomes: it.kind, lands: "the bound graph", caveat: "" };
}

// entityLabel renders a CEI key ("i|cluster|ns|Kind|name|uid" or "r|cluster|ns|Kind|RoleKey")
// as a human name "Kind name (ns)" so the operator sees the concrete target, not a key.
function entityLabel(key: string): string {
  const p = key.replace(/^entity:/, "").split("|");
  if (p.length >= 5 && p[0] === "i") return `${p[3]} ${p[4]} · ${p[2]}`;
  if (p.length >= 5 && p[0] === "r") return `${p[3]} ${(p[4] ?? "").split("/").pop()} · ${p[2]}`;
  return key.slice(0, 48);
}

// concreteTarget is the SPECIFIC mapping/dependency this candidate makes, in plain names —
// the "what equivalent group / which dependency" the operator must see before promoting.
function concreteTarget(it: GovernanceItem): { label: string; value: string } | null {
  if (it.kind === "edge" && it.relation === "topology") {
    const m = it.subject.match(/trace-call:(.+?)->(.+)/);
    if (m) return { label: "new dependency", value: `${m[1]}  →  ${m[2]}` };
  }
  if (it.kind === "edge") {
    const i = it.subject.indexOf(" ~> ");
    if (i > 0) {
      const metric = it.subject
        .slice(0, i)
        .replace(/^stray:/, "")
        .split("/")[0];
      return {
        label: "grouped under",
        value: `${metric}  →  ${entityLabel(it.subject.slice(i + 4))}`,
      };
    }
  }
  if (it.kind === "causal_hypothesis") {
    const m = it.subject.match(/change:(.+?) ~ incident:(.+)/);
    if (m)
      return { label: "co-occurrence", value: `change ${m[1]}  ↔  incident ${m[2].slice(0, 14)}…` };
  }
  if (it.kind === "node") {
    const metric = it.subject.replace(/^stray:/, "").split("/")[0];
    return { label: "metric (unmapped)", value: `${metric} — no entity match met the floor yet` };
  }
  return null;
}

export function GovernancePage() {
  const q = useGovernance();
  const qc = useQueryClient();

  // The reviewer signs every decision; persisted so it survives a reload.
  const [reviewer, setReviewer] = useState(() => localStorage.getItem("vigil.reviewer") ?? "");
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);
  // The most recent promotion's committable overlay — kept in a PERSISTENT panel so it
  // survives the queue refetch that moves the card to the decision trail.
  const [promotion, setPromotion] = useState<{
    subject: string;
    res: GovernanceDecisionResult;
  } | null>(null);
  const [err, setErr] = useState<string | null>(null);

  function setReviewerPersist(v: string) {
    setReviewer(v);
    localStorage.setItem("vigil.reviewer", v);
  }

  async function decide(it: GovernanceItem, decision: "promote" | "reject") {
    if (!reviewer.trim()) return;
    setBusy(it.id);
    setErr(null);
    try {
      const res = await decideGovernance(it.id, decision, reviewer.trim(), notes[it.id] ?? "");
      if (!res.ok) {
        setErr(res.message);
        return;
      }
      if (decision === "promote") setPromotion({ subject: it.subject, res });
      await qc.invalidateQueries({ queryKey: ["governance"] });
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <Page max="max-w-5xl">
      <SectionHead
        num="14"
        title="Governance"
        lede="Every dynamic-graph proposal is a candidate, firewalled from detection until a named human promotes it. You author the note (the model's rationale is discarded); a promotion emits a committable overlay. The system can block — it never approves."
      />

      <DataState q={q}>
        {(d) =>
          !d.available ? (
            <LaneNote kind="off" title="The governance lane is not enabled" note={d.note} />
          ) : (
            <div className="flex flex-col gap-6">
              {/* Reviewer identity — every decision is signed. */}
              <div className="v-panel flex flex-wrap items-center gap-3 p-3.5">
                <span className="v-eyebrow text-[10px]">reviewing as</span>
                <input
                  value={reviewer}
                  onChange={(e) => setReviewerPersist(e.target.value)}
                  placeholder="your name or handle — required to decide"
                  className="min-w-[18rem] flex-1 rounded-[6px] border border-rule-strong bg-plane px-3 py-1.5 text-[13px] text-ink placeholder:text-ink-low focus:border-ink-low focus:outline-none"
                />
                {reviewer.trim() ? (
                  <StateBadge sev="ok">signed</StateBadge>
                ) : (
                  <StateBadge sev="degraded">name required</StateBadge>
                )}
              </div>

              <Stat4
                items={[
                  {
                    label: "Pending review",
                    value: d.pending.length,
                    sev: d.pending.length ? "info" : "neutral",
                  },
                  { label: "Promoted", value: d.counts.promoted ?? 0, sev: "ok" },
                  { label: "Rejected", value: d.counts.rejected ?? 0 },
                  {
                    label: "Total candidates",
                    value: Object.values(d.counts).reduce((a, b) => a + b, 0),
                  },
                ]}
              />

              <LaneNote kind="info" title="The discipline" note={d.gateNote} />

              {/* What promotion does — and the load-bearing thing it does NOT do. Operators
                  routinely assume "clear the queue ⇒ everything is watched"; it is not true. */}
              <div className="v-panel p-4">
                <div className="v-eyebrow mb-2 text-[10px]">what a promotion does</div>
                <ol className="ml-4 list-decimal space-y-1 text-[12px] leading-relaxed text-ink-soft">
                  <li>
                    You sign it, and Vigil emits a committable{" "}
                    <span className="text-ink">AUTHORED overlay</span> — a starting artifact, not a
                    live change.
                  </li>
                  <li>
                    You commit that overlay through the release-governance gate; the next graph
                    release carries it. Until then the candidate stays firewalled from detection.
                  </li>
                </ol>
                <div className="mt-3 rounded-[6px] border border-warning/40 bg-surface-hi px-3 py-2 text-[12px] leading-relaxed text-ink-soft">
                  <span className="font-semibold text-warning">
                    Clearing this queue does NOT make every metric watched.
                  </span>{" "}
                  Promotion changes the graph's <span className="text-ink">identity</span> (which
                  entity a metric belongs to) and <span className="text-ink">topology</span>{" "}
                  (dependency edges). A metric is <span className="text-ink">watched</span> only
                  when it also has an authored detection rule and a bar — a separate act. So
                  promoting a stray mapping resolves WHO owns the metric; it never, by itself,
                  starts watching it against a threshold.
                </div>
              </div>

              {/* Non-actionable k8s object-metadata strays are CLASSIFIED and counted, but kept
                  out of the review queue so it stays the necessary decisions, not hundreds of
                  un-mappable KSM inventory rows. Honest: still in counts + /api/provisional-coverage. */}
              {d.suppressedMetadata > 0 && (
                <LaneNote
                  kind="empty"
                  title={`${d.suppressedMetadata} k8s object-metadata series classified non-actionable — not enqueued`}
                  note={
                    d.suppressedNote ??
                    "Pure KSM object inventory (ReplicaSet generation, Endpoints addresses, ConfigMap/Secret info): non-actionable as an operational signal. Counted, never hidden — just not a human decision."
                  }
                />
              )}

              {err && <div className="text-[12px] text-error">Decision failed: {err}</div>}

              {/* The committable overlay of the latest promotion — persists past the refetch. */}
              {promotion?.res.overlayYaml && (
                <div className="v-panel p-4">
                  <div className="mb-2 flex flex-wrap items-center gap-2">
                    <ProvChip kind="AUTHORED" />
                    <span className="text-[12.5px] text-ink-soft">
                      Promoted overlay — ready to commit
                    </span>
                    <button
                      type="button"
                      onClick={() => setPromotion(null)}
                      className="ml-auto cursor-pointer text-[11px] text-ink-low transition-colors hover:text-ink"
                    >
                      dismiss
                    </button>
                  </div>
                  <div className="mb-2 break-all">
                    <Mono>{promotion.subject}</Mono>
                  </div>
                  <p className="mb-2.5 text-[11.5px] leading-relaxed text-ink-mid">
                    {promotion.res.message} The firewall between candidates and detection stays
                    intact until you commit it.
                  </p>
                  <CodeBlock code={promotion.res.overlayYaml} lang="yaml" />
                </div>
              )}

              {/* PENDING QUEUE — one review card per candidate. */}
              <div>
                <div className="mb-2.5 flex items-baseline gap-2">
                  <h3 className="text-[14px] font-semibold tracking-tight text-ink">
                    Review queue
                  </h3>
                  <span className="v-mono text-[11px] text-ink-low">
                    {d.pending.length} pending
                  </span>
                </div>
                {d.pending.length === 0 ? (
                  <LaneNote
                    kind="empty"
                    title="Queue clear"
                    note="No candidates are awaiting a decision."
                  />
                ) : (
                  <div className="flex flex-col gap-3">
                    {/* Ranked STRONGEST-support first — a lexicographic compare of MEASURED
                        counts, never a learned/weighted score (doc 21 §4). */}
                    {[...d.pending].sort(cmpSupportDesc).map((it) => {
                      return (
                        <div key={it.id} className="v-panel p-4">
                          {/* header: producer · kind · relation · support */}
                          <div className="mb-2 flex flex-wrap items-center gap-2">
                            <Tag title="the producer that proposed this">
                              {SOURCE[it.source] ?? it.source}
                            </Tag>
                            <Tag sev="info">candidate</Tag>
                            {it.relation && <Tag>{it.relation}</Tag>}
                            <span className="text-[11px] text-ink-low">{kindSummary(it)}</span>
                            <span
                              className="v-mono rounded-[4px] border border-rule bg-surface-hi px-1.5 py-0.5 text-[10px] text-ink-soft"
                              title="deterministic MEASURED support — a count of agreed facts, never a confidence score"
                            >
                              support · {supportLabel(it.support)}
                            </span>
                            <span className="ml-auto v-mono text-[10.5px] text-ink-low">
                              {relTime(it.createdAt)}
                            </span>
                          </div>

                          {/* the proposed subject */}
                          <div className="mb-2.5 break-all text-[13px] text-ink">
                            <Mono>{it.subject}</Mono>
                          </div>

                          {/* the model's rationale — PROPOSED context, discarded at promotion */}
                          {it.rationale && (
                            <div className="mb-2.5 flex items-start gap-2">
                              <ProvChip
                                kind="PROJECTED"
                                title="proposed by the model — discarded at promotion"
                              />
                              <span className="text-[12px] leading-relaxed text-ink-mid">
                                {it.rationale}
                                <span className="ml-1.5 text-[10.5px] text-ink-low">
                                  (model rationale — discarded; you author the note)
                                </span>
                              </span>
                            </div>
                          )}

                          {/* the evidence it rests on — clear visibility */}
                          {it.evidence.length > 0 && (
                            <div className="mb-3">
                              <div className="v-eyebrow mb-1 text-[9.5px]">
                                evidence ({it.evidence.length})
                              </div>
                              <div className="flex flex-col gap-1">
                                {it.evidence.map((ev, i) => (
                                  <div
                                    key={i}
                                    className="flex items-start gap-2 rounded-[5px] border border-rule bg-surface-hi px-2.5 py-1.5"
                                  >
                                    <span className="v-mono text-[10px] text-ink-low">
                                      {ev.kind}
                                    </span>
                                    <span className="v-mono text-[11px] break-all text-ink-soft">
                                      {ev.ref}
                                    </span>
                                    {ev.detail && (
                                      <span className="text-[11px] text-ink-mid">
                                        — {ev.detail}
                                      </span>
                                    )}
                                  </div>
                                ))}
                              </div>
                            </div>
                          )}

                          {/* WHERE this lands on promote — explicit so the operator commits with eyes open */}
                          {(() => {
                            const dest = promoteDestination(it);
                            const target = concreteTarget(it);
                            return (
                              <div className="mb-2.5 rounded-[6px] border border-rule bg-surface-hi px-3 py-2">
                                <div className="v-eyebrow mb-1 text-[9.5px] text-ink-low">
                                  on promote →
                                </div>
                                {/* the CONCRETE mapping/dependency in plain names — the thing the operator
                                    is actually committing (which group / which dependency edge). */}
                                {target && (
                                  <div className="mb-2 rounded-[5px] border border-rule-strong bg-plane px-2.5 py-1.5">
                                    <span className="v-eyebrow text-[9px] text-ink-low">
                                      {target.label}
                                    </span>
                                    <div className="v-mono text-[12px] break-all text-ink">
                                      {target.value}
                                    </div>
                                  </div>
                                )}
                                <div className="text-[12px] leading-relaxed text-ink-soft">
                                  becomes <span className="text-ink">{dest.becomes}</span>
                                  <br />
                                  lands in <span className="text-ink">{dest.lands}</span>
                                </div>
                                {dest.caveat && (
                                  <div className="mt-1.5 text-[11px] leading-relaxed text-warning">
                                    {dest.caveat}
                                  </div>
                                )}
                              </div>
                            );
                          })()}

                          {/* the human authors the note, then signs the decision */}
                          <textarea
                            value={notes[it.id] ?? ""}
                            onChange={(e) => setNotes((n) => ({ ...n, [it.id]: e.target.value }))}
                            placeholder="Author the note — why this mapping is correct (becomes the AUTHORED prose on the overlay)…"
                            rows={2}
                            className="w-full resize-y rounded-[7px] border border-rule-strong bg-plane px-3 py-2 text-[12.5px] text-ink placeholder:text-ink-low focus:border-ink-low focus:outline-none"
                          />
                          <div className="mt-2.5 flex flex-wrap items-center gap-2">
                            <Button
                              variant="primary"
                              disabled={!reviewer.trim() || busy === it.id}
                              onClick={() => decide(it, "promote")}
                            >
                              {busy === it.id ? "Deciding…" : "Promote"}
                            </Button>
                            <Button
                              disabled={!reviewer.trim() || busy === it.id}
                              onClick={() => decide(it, "reject")}
                            >
                              Reject
                            </Button>
                            {!reviewer.trim() && (
                              <span className="text-[11px] text-warning">
                                set your reviewer name above to decide
                              </span>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* DECIDED — the audit trail */}
              {d.decided.length > 0 && (
                <div>
                  <div className="mb-2.5 flex items-baseline gap-2">
                    <h3 className="text-[14px] font-semibold tracking-tight text-ink">
                      Decision trail
                    </h3>
                    <span className="v-mono text-[11px] text-ink-low">
                      {d.decided.length} decided
                    </span>
                  </div>
                  <Table cols={["Proposal", "Decision", "By", "When", "Note"]}>
                    {d.decided.map((it) => (
                      <Tr key={it.id}>
                        <Td mono>
                          <span className="break-all">{it.subject}</span>
                        </Td>
                        <Td>
                          <StateBadge sev={statusSev(it.status)}>{it.status}</StateBadge>
                        </Td>
                        <Td>{it.decidedBy || "—"}</Td>
                        <Td mono>{it.decidedAt ? relTime(it.decidedAt) : "—"}</Td>
                        <Td className={cx(!it.note && "text-ink-low")}>{it.note || "—"}</Td>
                      </Tr>
                    ))}
                  </Table>
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
