package dgx

import "time"

// ExplorationEvent is a discrete trigger asking the agent to explore NOW (doc 21 §3, Phase 3
// slice 4) — a newly-seen unexplained card, k8s event, or operational stray — instead of
// waiting for the next scheduled sweep. main owns the PRODUCERS (seen-set watchers over the
// SAME read-only atomic snapshots the api/MCP serve), so dgx defines only this contract and
// the dgx⊥api firewall holds.
//
// The event carries a stable Ref the agent treats as a gap to examine; an event-triggered run
// BYPASSES gap_state backoff (a genuinely NEW thing is worth a look even if its gap recently
// backed off — the slice-3 risk mitigation: backoff must never starve a new high-signal gap).
// Newness is a content-keyed seen-set DIFF, never a novelty score — no model decides what is
// "interesting" (charter: no learned trigger).
type ExplorationEvent struct {
	Kind      string    // "unexplained" | "event" | "stray" | "departure"
	Ref       string    // stable gap ref, e.g. "unexplained:<scope|ns|name|kind>", "event:<reason>@<ns>/<name>", "stray:<metric>"
	Detail    string    // a MEASURED, human-readable fact (never a causal claim)
	EmittedAt time.Time // when the watcher saw it (injected; ordering + telemetry)
}
