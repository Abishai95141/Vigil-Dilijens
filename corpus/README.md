# corpus — labeled failure data for the harness

The raw material the falsification suites (doc 11) consume. Everything here is
versioned next to the labels it produces, so an induced incident always arrives **with
its label and timestamps** — the corpus annotation comes free (doc 14 §3.3).

```
chaos/    Chaos Mesh CRDs (StressChaos · PodChaos · NetworkChaos · IOChaos) — infra faults
flags/    OTel Demo flagd toggle scripts (recommendationServiceCacheFailure → working-set→OOM, etc.)
bundles/  replay bundle manifests (Parquet readings + JSON manifest, doc 11 §3.1)
labels/   incident annotations: what happened, when, on which entities
```

Sources (doc 11 §3.1): reference clusters, chaos runs, reproduced incidents, and —
with consent and isolation — anonymized customer captures (anonymization standards
land before any customer capture, doc 14 A16).

Phase-0/1 corpora come only from our own clusters. The marquee fixture is the
**working-set → OOM** precursor trajectory (`recommendationServiceCacheFailure`),
with a known start time — exactly what the first forecast backtest gate needs (doc 09 M3).
