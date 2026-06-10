// Package unexplained is the blind-spot patch, honestly bounded: it guarantees that
// loud activity matching NO curated phenomenon is still surfaced for human
// investigation — with no causal claim attached — and states plainly what even this
// channel cannot see.
//
// Owning doc: 08-unexplained-anomaly-channel.md.
//
// Loudness, defined precisely (doc 08 §3.1) — without learned baselines, an entity
// is loud within a window iff at least one of:
//   - a bar crossing: a thresholded signal is at `above` or `well-above`, or
//   - a rate excursion: a rate-guarded signal exceeds its authored rate guard.
//
// Nothing else qualifies. No distribution distance, no novelty score.
//
// The residual blind spot, stated (doc 08 §3.2): signals carrying neither a resolved
// bar nor a rate guard can never be loud — so a novel failure expressing itself only
// through un-thresholded signals is invisible even here. This is recorded, never
// implied away. The channel covers KNOWN signals exhibiting UNKNOWN patterns.
//
// Output is MEASURED and conspicuously NOT authored: the defining property is the
// ABSENCE of a reason, kept visible. No causal vocabulary; not-yet-explained mark
// mandatory. Recurrences aggregate into candidate-phenomenon reports for humans
// (via governance, doc 12) — the system never writes to the graph.
package unexplained
