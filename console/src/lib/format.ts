// Small, dependency-free formatters. All times from obsd are RFC3339 UTC strings.

export function relTime(iso: string | undefined | null): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 5) return "now";
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function clock(iso: string | undefined | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function bytes(n: number | undefined | null): string {
  if (n == null) return "—";
  const u = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}

export function pct(frac: number | undefined | null, digits = 0): string {
  if (frac == null) return "—";
  return `${(frac * 100).toFixed(digits)}%`;
}

export function dur(seconds: number | undefined | null): string {
  if (seconds == null) return "—";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// "online-boutique/currencyservice-8fdf6cf9d-9f2v2" → short workload-ish label.
export function shortName(name: string | undefined): string {
  if (!name) return "—";
  // strip the trailing ReplicaSet + pod hash (…-<rshash>-<podhash>)
  return name.replace(/-[a-z0-9]{8,10}-[a-z0-9]{5}$/i, "").replace(/-[a-z0-9]{8,10}$/i, "");
}

export function num(n: number | undefined | null): string {
  return n == null ? "—" : n.toLocaleString();
}
