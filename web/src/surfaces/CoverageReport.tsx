import { useEffect, useState } from "react";
import { CoverageCell, type CoverageState } from "../components/Brand";
import type {
  CoverageView,
  Observability,
  PhenomenonRow,
} from "./coverage-types";

// The Coverage Report (doc 10 M1) — the honest map of what Vigil can and cannot
// watch, shown before any finding. Built entirely on the design system: a
// MEASURED-class surface (it is a fact about the system's own coverage), gaps
// surfaced first, nothing blank. Plain fetch + polling for M1; the typed Connect
// client replaces it as /proto lands.

const POLL_MS = 15_000;

const OBS_TO_COVERAGE: Record<Observability, CoverageState> = {
  full: "full",
  partial: "partial",
  none: "none",
};

export function CoverageReport() {
  const [view, setView] = useState<CoverageView | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const res = await fetch("/api/coverage");
        if (!res.ok) throw new Error(`coverage: ${res.status}`);
        const data = (await res.json()) as CoverageView;
        if (alive) {
          setView(data);
          setError(null);
        }
      } catch (e) {
        if (alive) setError(e instanceof Error ? e.message : "request failed");
      }
    };
    load();
    const id = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  if (error && !view) {
    return (
      <div className="v-card" style={{ borderColor: "var(--status-error)" }}>
        <h2>Coverage unavailable</h2>
        <p className="v-muted" style={{ marginTop: "var(--space-xs)" }}>
          Could not reach the obsd API ({error}). Start <code>obsd</code> with{" "}
          <code>--api</code> and a cluster target.
        </p>
      </div>
    );
  }
  if (!view) return <Skeleton />;
  if (!view.available) return <Unavailable view={view} />;

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-xl)",
      }}
    >
      <Header view={view} />
      <SummaryGrid view={view} />
      <PhenomenaSection rows={view.phenomena} />
      <RulesSection view={view} />
      <SelectionSection view={view} />
      <CaveatsSection view={view} />
    </div>
  );
}

function Header({ view }: { view: CoverageView }) {
  return (
    <section>
      <span className="v-overline">Coverage report · doc 10 M1</span>
      <h1 style={{ marginTop: "var(--space-2xs)" }}>
        What Vigil can watch here
      </h1>
      <p
        className="v-muted"
        style={{ marginTop: "var(--space-xs)", maxWidth: 680 }}
      >
        The honest map, shown before any finding: every phenomenon, rule, and
        entity with a visible state. Gaps are stated, never hidden.
      </p>
      <div
        className="mono"
        style={{
          marginTop: "var(--space-sm)",
          color: "var(--text-faint)",
          fontSize: "var(--text-mono)",
        }}
      >
        cluster {view.clusterId.slice(0, 8)}… · graph{" "}
        {view.graphRelease ? `${view.graphRelease} · ` : "unreleased · "}
        {view.graphVersion.slice(7, 19)}… ·{" "}
        {new Date(view.generatedAt).toLocaleTimeString()}
      </div>
    </section>
  );
}

function Metric({
  label,
  value,
  hint,
}: { label: string; value: string; hint?: string }) {
  return (
    <div
      style={{
        background: "var(--surface-2)",
        borderRadius: "var(--radius-md)",
        padding: "var(--space-md)",
      }}
    >
      <div className="v-overline">{label}</div>
      <div
        className="mono"
        style={{
          fontSize: "var(--text-h2)",
          color: "var(--text-strong)",
          marginTop: "var(--space-2xs)",
        }}
      >
        {value}
      </div>
      {hint ? (
        <div
          className="v-faint"
          style={{ fontSize: "var(--text-caption)", marginTop: "2px" }}
        >
          {hint}
        </div>
      ) : null}
    </div>
  );
}

function SummaryGrid({ view }: { view: CoverageView }) {
  const s = view.summary;
  return (
    <section
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))",
        gap: "var(--space-sm)",
      }}
    >
      <Metric
        label="Entities"
        value={String(s.entities)}
        hint={`${s.tierA} watched (Tier A)`}
      />
      <Metric
        label="Resolvability"
        value={`${Math.round(s.resolvability * 100)}%`}
        hint={`${s.configBound}/${s.configEligible} config-bound · ${s.defaultBars} default`}
      />
      <Metric
        label="Phenomena"
        value={`${s.phenomenaFull}/${s.phenomenaPartial}/${s.phenomenaNone}`}
        hint="full / partial / none"
      />
      <Metric
        label="Binding QA"
        value={`${s.qaVerified}`}
        hint={`${s.qaSuspect} suspect · ${s.qaFailed} failed`}
      />
    </section>
  );
}

function PhenomenaSection({ rows }: { rows: PhenomenonRow[] }) {
  if (!rows.length) return null;
  return (
    <section>
      <h3>Phenomena — what we can detect here</h3>
      <p
        className="v-muted"
        style={{
          marginTop: "var(--space-2xs)",
          marginBottom: "var(--space-md)",
        }}
      >
        Gaps first. Partial means a required member is unobservable on this
        cluster — a match would be honestly degraded.
      </p>
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-xs)",
        }}
      >
        {rows.map((p) => (
          <article
            key={p.id}
            className="v-card"
            style={{ padding: "var(--space-sm) var(--space-md)" }}
          >
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "var(--space-sm)",
              }}
            >
              <div>
                <div style={{ color: "var(--text-strong)", fontWeight: 500 }}>
                  {p.label || p.id}
                </div>
                <div
                  className="mono v-faint"
                  style={{ fontSize: "var(--text-caption)" }}
                >
                  {p.id} · {p.requiredObservable}/{p.requiredTotal} required
                  observable
                </div>
              </div>
              <CoverageCell
                state={OBS_TO_COVERAGE[p.observability]}
                label={p.observability}
              />
            </div>
            {p.missingReasons.length ? (
              <ul
                className="v-muted"
                style={{
                  margin: "var(--space-xs) 0 0",
                  paddingLeft: "var(--space-md)",
                  fontSize: "var(--text-caption)",
                }}
              >
                {p.missingReasons.map((r) => (
                  <li key={r}>{r}</li>
                ))}
              </ul>
            ) : null}
          </article>
        ))}
      </div>
    </section>
  );
}

function RulesSection({ view }: { view: CoverageView }) {
  if (!view.rules.length) return null;
  return (
    <section>
      <h3>Threshold rules — what bound</h3>
      <div
        className="v-card"
        style={{ marginTop: "var(--space-sm)", padding: 0, overflow: "hidden" }}
      >
        <table
          style={{
            width: "100%",
            borderCollapse: "collapse",
            fontSize: "var(--text-body)",
          }}
        >
          <thead>
            <tr style={{ textAlign: "left", color: "var(--text-muted)" }}>
              {[
                "Rule",
                "Inst.",
                "Config",
                "Default",
                "Unbounded",
                "Out-of-scope",
              ].map((h, i) => (
                <th
                  key={h}
                  style={{
                    padding: "var(--space-sm) var(--space-md)",
                    fontWeight: 500,
                    textAlign: i === 0 ? "left" : "right",
                    borderBottom: "var(--border-hairline)",
                  }}
                >
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="mono">
            {view.rules.map((r) => (
              <tr key={r.ruleId}>
                <td
                  style={{
                    padding: "var(--space-xs) var(--space-md)",
                    color: "var(--text)",
                    borderBottom: "var(--border-hairline)",
                  }}
                >
                  {r.ruleId.replace(/^THR_/, "")}
                </td>
                <Num n={r.instantiated} />
                <Num
                  n={r.configBound}
                  tone={r.configBound > 0 ? "ok" : undefined}
                />
                <Num
                  n={r.defaultBound}
                  tone={r.defaultBound > 0 ? "warn" : undefined}
                />
                <Num
                  n={r.unbounded}
                  tone={r.unbounded > 0 ? "muted" : undefined}
                />
                <Num
                  n={r.outOfScope}
                  tone={r.outOfScope > 0 ? "muted" : undefined}
                />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Num({ n, tone }: { n: number; tone?: "ok" | "warn" | "muted" }) {
  const color =
    tone === "ok"
      ? "var(--prov-measured-text)"
      : tone === "warn"
        ? "var(--cov-suspect)"
        : tone === "muted"
          ? "var(--text-faint)"
          : "var(--text)";
  return (
    <td
      style={{
        padding: "var(--space-xs) var(--space-md)",
        textAlign: "right",
        color,
        borderBottom: "var(--border-hairline)",
      }}
    >
      {n}
    </td>
  );
}

function SelectionSection({ view }: { view: CoverageView }) {
  const none = Object.entries(view.selection.noneByReason).sort((a, b) =>
    a[0].localeCompare(b[0]),
  );
  if (!none.length && view.selection.tierA === 0) return null;
  return (
    <section>
      <h3>Attention — why these, not those</h3>
      <p
        className="v-muted"
        style={{
          marginTop: "var(--space-2xs)",
          marginBottom: "var(--space-sm)",
        }}
      >
        <strong style={{ color: "var(--prov-measured-text)" }}>
          {view.selection.tierA}
        </strong>{" "}
        entities are watched (Tier A). The rest are listed with a reason — never
        silently dropped.
      </p>
      <div
        style={{ display: "flex", flexWrap: "wrap", gap: "var(--space-xs)" }}
      >
        {none.map(([reason, n]) => (
          <span
            key={reason}
            className="mono"
            style={{
              fontSize: "var(--text-caption)",
              background: "var(--surface-2)",
              border: "var(--border-hairline)",
              borderRadius: "var(--radius-full)",
              padding: "var(--space-2xs) var(--space-sm)",
              color: "var(--text-muted)",
            }}
          >
            {n} {reason}
          </span>
        ))}
      </div>
    </section>
  );
}

function CaveatsSection({ view }: { view: CoverageView }) {
  if (!view.caveats.length && !view.unbounded.length) return null;
  return (
    <section>
      <h3>Honest gaps</h3>
      <div
        className="v-prov-card"
        data-prov="AUTHORED"
        style={{ marginTop: "var(--space-sm)" }}
      >
        {view.caveats.length ? (
          <ul style={{ margin: 0, paddingLeft: "var(--space-md)" }}>
            {view.caveats.map((c) => (
              <li key={c} style={{ marginBottom: "var(--space-2xs)" }}>
                {c}
              </li>
            ))}
          </ul>
        ) : null}
        {view.unbounded.length ? (
          <details
            style={{ marginTop: view.caveats.length ? "var(--space-sm)" : 0 }}
          >
            <summary style={{ cursor: "pointer", color: "var(--text-muted)" }}>
              {view.unbounded.length} unbounded (entity, variable) pairs —
              Tier-B-ineligible, listed
            </summary>
            <ul
              className="mono v-faint"
              style={{
                margin: "var(--space-xs) 0 0",
                paddingLeft: "var(--space-md)",
                fontSize: "var(--text-caption)",
              }}
            >
              {view.unbounded.slice(0, 50).map((u) => (
                <li key={u}>{u}</li>
              ))}
            </ul>
          </details>
        ) : null}
      </div>
    </section>
  );
}

function Unavailable({ view }: { view: CoverageView }) {
  return (
    <div className="v-card">
      <h2>Coverage not ready</h2>
      <p className="v-muted" style={{ marginTop: "var(--space-xs)" }}>
        {view.caveats[0] ?? "Binding has not compiled yet."}
      </p>
    </div>
  );
}

function Skeleton() {
  return (
    <div
      className="v-muted"
      style={{ padding: "var(--space-3xl) 0", textAlign: "center" }}
    >
      Loading coverage…
    </div>
  );
}
