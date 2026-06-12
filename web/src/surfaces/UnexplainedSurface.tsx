import { ProvChip } from "../components/Brand";
import {
  type CandidateReport,
  type UnexplainedCard,
  type UnexplainedView,
  entityLabel,
  shortMetric,
} from "./types";
import { useApi } from "./useApi";

// The Unexplained surface (doc 10 §3.1 / doc 08) — loud-but-unmatched activity,
// surfaced for human investigation with NO causal claim. MEASURED only,
// conspicuously WITHOUT a reason — the absence of an explanation is the point,
// kept visible. The channel discloses its own residual blind spot.

export function UnexplainedSurface() {
  const { data, error, loaded } = useApi<UnexplainedView>("/api/unexplained");
  if (!loaded) return <p className="v-muted">Loading unexplained channel…</p>;
  if (error && !data)
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Unexplained channel unavailable</h2>
        <p className="v-muted">Could not reach the obsd API ({error}).</p>
      </div>
    );
  if (!data) return null;
  const v = data;
  const open = v.openCards.filter(
    (c) => c.status === "new" || c.status === "aging",
  );
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-lg)",
      }}
    >
      <section>
        <h3 className="v-overline" style={{ marginBottom: "var(--space-sm)" }}>
          Anomalous — investigate ({open.length})
        </h3>
        {open.length === 0 ? (
          <div className="v-card">
            <p className="v-muted">
              No loud-but-unmatched activity right now. Everything currently
              loud is covered by a curated pattern (authored knowledge, not
              inference).
            </p>
          </div>
        ) : (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "var(--space-sm)",
            }}
          >
            {open.map((c) => (
              <Card
                key={`${c.scope}-${c.loudStates.map((s) => s.metric).join(",")}`}
                c={c}
              />
            ))}
          </div>
        )}
      </section>

      {v.candidates.length > 0 && <Candidates list={v.candidates} />}

      <article
        className="v-card"
        style={{
          borderLeft:
            "3px solid var(--prov-authored-on-dark, var(--prov-authored))",
        }}
      >
        <div className="v-overline">Residual blind spot (stated)</div>
        <p
          className="v-muted"
          style={{
            marginTop: "var(--space-xs)",
            fontSize: "var(--text-small)",
          }}
        >
          {v.blindSpot}
        </p>
      </article>
    </div>
  );
}

function Card({ c }: { c: UnexplainedCard }) {
  return (
    <article className="v-prov-card" data-prov="MEASURED">
      <header
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: "var(--space-sm)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-sm)",
            flexWrap: "wrap",
          }}
        >
          <span aria-hidden>◇</span>
          <strong style={{ fontFamily: "var(--font-heading)" }}>
            {entityLabel(c.scope)}
          </strong>
          <span className="v-badge" data-span="">
            {c.status}
          </span>
          <span className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
            seen {c.occurrences} window(s)
          </span>
        </div>
        <ProvChip cls="MEASURED" />
      </header>
      <div style={{ marginTop: "var(--space-xs)" }}>
        {c.loudStates.map((s) => (
          <div key={s.metric} className="v-evi">
            <span className="v-faint">{s.kind}</span>
            <span className="v-mono">
              {shortMetric(s.metric)} = {s.state}
              {s.flagged ? " (default bar)" : ""}
            </span>
            <span />
          </div>
        ))}
      </div>
      <p
        className="v-muted"
        style={{ marginTop: "var(--space-xs)", fontSize: "var(--text-small)" }}
      >
        {c.matchCheck}
      </p>
      {c.supersededBy && (
        <p className="v-mono" style={{ fontSize: "var(--text-tiny)" }}>
          superseded by match: {c.supersededBy}
        </p>
      )}
      <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        MEASURED · NOT a reason — this surface describes loudness, it never
        explains.
      </p>
    </article>
  );
}

function Candidates({ list }: { list: CandidateReport[] }) {
  return (
    <section>
      <h3 className="v-overline" style={{ marginBottom: "var(--space-sm)" }}>
        Curation feedback — candidate phenomena ({list.length})
      </h3>
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-sm)",
        }}
      >
        {list.map((c) => (
          <article
            key={`${c.entityKind}-${c.metrics.join(",")}`}
            className="v-card"
          >
            <div
              style={{
                display: "flex",
                gap: "var(--space-sm)",
                flexWrap: "wrap",
                alignItems: "center",
              }}
            >
              <span className="v-badge" data-span="">
                {c.entityKind}
              </span>
              {c.metrics.map((m) => (
                <span
                  key={m}
                  className="v-mono"
                  style={{ fontSize: "var(--text-small)" }}
                >
                  {shortMetric(m)}
                </span>
              ))}
              <span
                className="v-faint"
                style={{ fontSize: "var(--text-tiny)" }}
              >
                {c.windows} windows · {c.entities.length} entities
              </span>
            </div>
            <p
              className="v-muted"
              style={{
                marginTop: "var(--space-xs)",
                fontSize: "var(--text-small)",
              }}
            >
              {c.rationale}
            </p>
          </article>
        ))}
      </div>
      <p
        className="v-faint"
        style={{ fontSize: "var(--text-tiny)", marginTop: "var(--space-xs)" }}
      >
        The system proposes; humans author (via governance, doc 12). Nothing
        here writes the graph.
      </p>
    </section>
  );
}
