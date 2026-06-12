import { useEffect, useState } from "react";

// useApi polls a GET endpoint on an interval and tracks the latest value, error,
// and whether the first load has completed. The aligned-to-tick default (15s)
// matches the obsd evaluation cadence; the surfaces are live snapshots, so
// polling is the right model until the SSE/Connect stream lands (techstack §10).
export function useApi<T>(path: string, pollMs = 15_000) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let alive = true;
    // Monotonic sequence so a slow poll landing AFTER a newer one can never
    // overwrite fresher data with stale (out-of-order fetch completion).
    let seq = 0;
    const load = async () => {
      const mySeq = ++seq;
      try {
        const res = await fetch(path);
        if (!res.ok) throw new Error(`${path}: ${res.status}`);
        const json = (await res.json()) as T;
        if (alive && mySeq === seq) {
          setData(json);
          setError(null);
          setLoaded(true);
        }
      } catch (e) {
        if (alive && mySeq === seq) {
          setError(e instanceof Error ? e.message : "request failed");
          setLoaded(true);
        }
      }
    };
    load();
    const id = setInterval(load, pollMs);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [path, pollMs]);

  return { data, error, loaded };
}
