// Package replay is the replay substrate (doc 05 M5, doc 11 §3.1): the capture
// side writes the replay bundle while obsd runs — readings (qss warm segments,
// arrival-ordered, interleaved with evaluation-tick frames), the resolved-bar
// set per binding epoch, and a manifest pinning the graph version and parameter
// set — and the engine side re-runs a bundle through the REAL observation and
// detection components (observe.Materialize, detect.Matcher) to reproduce every
// recorded tick byte-identically.
//
// The determinism contract (doc 05 §3.5, doc 07 §3.7): same readings + same
// resolved bars + same graph version + same parameters ⇒ the same fingerprints
// and findings, always. Each live tick computes a canonical digest of its
// (fingerprints, findings) and records it in the tick frame; replay recomputes
// the digest from disk and compares. A mismatch is a determinism violation and
// fails the run loudly.
//
// Pinned-version everything (doc 11 §3.1): the engine refuses a graph whose
// version differs from the manifest's; the parameter set replays from the
// manifest, never from the local params file.
//
// Honest scope (stated): a bundle carries the entity-local detection inputs.
// The topology log (edge validity intervals, doc 14 §1.3) joins the bundle when
// first-order traversal lands (07 M2); time-shifted evaluation ("as of" any
// instant, for backtesting) lands with the harness suites that consume it
// (doc 11 M3+). Neither gap is hidden: the manifest names what the bundle holds.
//
// Do NOT: fuse provenance classes here (replay reproduces; it never reinterprets),
// read the live cluster from the engine, or let the engine "repair" a divergent
// tick — divergence is surfaced, never patched.
package replay
