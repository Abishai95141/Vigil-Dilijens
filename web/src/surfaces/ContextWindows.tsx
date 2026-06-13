import { useState } from "react";
import {
  type ContextWindow,
  type ContextWindowKind,
  type ContextWindowsView,
  isZeroTime,
} from "./types";
import { useApi } from "./useApi";

// The Context-windows surface (doc 10 M6). Operator-authored annotations of known
// events — deploys, config changes, incidents, maintenance. They are NOT findings
// and wear no provenance class (the surface says so). A deploy or config change is
// a clean process/container boundary, so it is SPLICE-ELIGIBLE: the Phase-3
// forecast decomposition (doc 09 §3.4) cuts the forecast context at that boundary
// and projects only the clean remainder. An incident is a region to AVOID
// forecasting across, not a splice — shown, but not splice-eligible.

const KINDS: { id: ContextWindowKind; label: string; splice: boolean }[] = [
  { id: "deploy", label: "Deploy", splice: true },
  { id: "config-change", label: "Config change", splice: true },
  { id: "incident", label: "Incident", splice: false },
  { id: "maintenance", label: "Maintenance", splice: false },
];

function nowLocalInput(): string {
  // datetime-local wants "YYYY-MM-DDTHH:mm" in local time.
  const d = new Date();
  const off = d.getTimezoneOffset();
  return new Date(d.getTime() - off * 60_000).toISOString().slice(0, 16);
}

export function ContextWindows() {
  const [reloadKey, setReloadKey] = useState(0);
  const { data, error, loaded } = useApi<ContextWindowsView>(
    `/api/context-windows?r=${reloadKey}`,
  );

  const [label, setLabel] = useState("");
  const [kind, setKind] = useState<ContextWindowKind>("deploy");
  const [startAt, setStartAt] = useState(nowLocalInput());
  const [endAt, setEndAt] = useState("");
  const [annotation, setAnnotation] = useState("");
  const [author, setAuthor] = useState("");
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setSubmitErr(null);
    try {
      const body = {
        label,
        kind,
        startAt: new Date(startAt).toISOString(),
        endAt: endAt ? new Date(endAt).toISOString() : undefined,
        annotation,
        author,
      };
      const res = await fetch("/api/context-windows", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(await res.text());
      setLabel("");
      setAnnotation("");
      setReloadKey((k) => k + 1); // force the list to refetch
    } catch (err) {
      setSubmitErr(err instanceof Error ? err.message : "submit failed");
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
        <h2 style={{ fontFamily: "var(--font-heading)" }}>Context windows</h2>
        <p className="v-muted" style={{ fontSize: "var(--text-small)" }}>
          Operator annotations of known events — no provenance class. Deploy and
          config-change boundaries are <strong>splice-eligible</strong>: the
          forecasting clock cuts its context there and projects only the clean
          remainder (doc 09 §3.4). An incident is a region to avoid forecasting
          across, not a splice.
        </p>
      </header>

      <form className="v-card" onSubmit={submit}>
        <div className="v-overline">Annotate a window</div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "var(--space-sm)",
            marginTop: "var(--space-sm)",
          }}
        >
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="v-faint">Label</span>
            <input
              required
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. cartservice v2.3 rollout"
            />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="v-faint">Kind</span>
            <select
              value={kind}
              onChange={(e) => setKind(e.target.value as ContextWindowKind)}
            >
              {KINDS.map((k) => (
                <option key={k.id} value={k.id}>
                  {k.label}
                  {k.splice ? " — splice-eligible" : ""}
                </option>
              ))}
            </select>
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="v-faint">Start</span>
            <input
              type="datetime-local"
              required
              value={startAt}
              onChange={(e) => setStartAt(e.target.value)}
            />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="v-faint">End (optional — blank = marker)</span>
            <input
              type="datetime-local"
              value={endAt}
              onChange={(e) => setEndAt(e.target.value)}
            />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="v-faint">Author</span>
            <input
              required
              value={author}
              onChange={(e) => setAuthor(e.target.value)}
              placeholder="who is annotating (it is an authored note)"
            />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="v-faint">Annotation</span>
            <input
              value={annotation}
              onChange={(e) => setAnnotation(e.target.value)}
              placeholder="optional detail"
            />
          </label>
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-sm)",
            marginTop: "var(--space-sm)",
          }}
        >
          <button type="submit" className="v-tab" disabled={busy}>
            {busy ? "Saving…" : "Add window"}
          </button>
          {submitErr && (
            <span style={{ color: "var(--status-error)" }}>{submitErr}</span>
          )}
        </div>
      </form>

      <section>
        <h3 className="v-overline" style={{ marginBottom: "var(--space-sm)" }}>
          {loaded && data
            ? `${data.windows.length} window(s) · ${data.spliceCount} splice-eligible`
            : "Windows"}
        </h3>
        {!loaded && <p className="v-muted">Loading…</p>}
        {error && !data && (
          <p className="v-muted">Could not reach the obsd API ({error}).</p>
        )}
        {loaded && data && data.windows.length === 0 && (
          <div className="v-card">
            <p className="v-muted">
              No context windows yet. Annotate a deploy or config change above
              to give the forecasting clock a clean splice boundary.
            </p>
          </div>
        )}
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "var(--space-sm)",
          }}
        >
          {data?.windows.map((w) => (
            <WindowRow key={w.id} w={w} />
          ))}
        </div>
      </section>
    </div>
  );
}

function WindowRow({ w }: { w: ContextWindow }) {
  const fmt = (t: string) =>
    isZeroTime(t) ? "—" : new Date(t).toLocaleString();
  return (
    <article className="v-card">
      <header
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: "var(--space-sm)",
          flexWrap: "wrap",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-sm)",
          }}
        >
          <strong style={{ fontFamily: "var(--font-heading)" }}>
            {w.label}
          </strong>
          <span className="v-badge">{w.kind}</span>
          {w.spliceEligible ? (
            <span
              className="v-badge"
              style={{
                background: "var(--prov-projected-tint)",
                borderColor: "var(--prov-projected-border)",
                color: "var(--prov-projected-text)",
              }}
              title="Forecast context is spliced at this boundary (doc 09 §3.4)"
            >
              ◈ splice-eligible
            </span>
          ) : (
            <span className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
              not a splice (forecasting avoids this span)
            </span>
          )}
        </div>
        <span
          className="v-mono v-muted"
          style={{ fontSize: "var(--text-small)" }}
        >
          {fmt(w.startAt)}
          {isZeroTime(w.endAt) ? "" : ` → ${fmt(w.endAt)}`}
        </span>
      </header>
      {w.annotation && (
        <p style={{ marginTop: "var(--space-xs)" }}>{w.annotation}</p>
      )}
      <p className="v-faint" style={{ fontSize: "var(--text-tiny)" }}>
        authored by {w.author} · recorded {fmt(w.createdAt)} · {w.id}
      </p>
    </article>
  );
}
