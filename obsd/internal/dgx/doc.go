// Package dgx is the Dynamic Graph eXtension agent harness (doc 20 P3 / §2.1): the
// first place a model enters the loop. An LLM PROPOSES typed candidate graph
// extensions from read-only MEASURED context; the harness AUTHORS nothing and writes
// nothing the model said directly into the graph. It is off the deterministic path
// (no deterministic package imports it — firewall_test.go) and never on the clock
// wire (it is a separate text path; the no-strings rule governs the clock contract).
//
// THE DISCIPLINE (doc 20 §2.1, the propose→verify→promote spine):
//
//   - NO WRITE TOOL. The provider returns strict JSON text. The harness parses it
//     into typed proposals and stages the survivors into the candidate store. The
//     model never touches the store.
//   - GROUNDING GATE. Every evidence reference a proposal cites MUST exist in the
//     context the harness provided. A proposal citing anything else is rejected —
//     this is the anti-hallucination floor: the agent can only build on facts it was
//     shown, never ones it invented.
//   - EVIDENCE FLOOR. A proposal must cite ≥ a DECLARED minimum number of facts
//     (Params.MinEvidence, never data-fit).
//   - STRUCTURAL GUARD. A causal `edge` is rejected (candidate.Validate): causation
//     must use the direction-free KindCausalHypothesis. The model may emit no causal
//     direction and no "reason"; its rationale is stored as PROPOSED payload, never a
//     surfaced authored reason.
//   - HONEST VERIFY. For a count-series binding, verification is a real backtest gate
//     (elsewhere); for a causal relation there is NO machine verifier — promotion is
//     justified solely by a named human at the governance gate. This harness is a
//     QUALITY PRE-FILTER for that human queue, never a validator of a claim.
//
// THE PROVIDER SEAM (extensibility). Provider is an interface: NewGroqProvider is the
// default backend; NewOpenAICompatibleProvider points the same client at any
// OpenAI-compatible endpoint (a local model server, etc.) by base-URL swap; an
// entirely different backend is a new Provider impl. StaticProvider makes the whole
// harness testable hermetically (no network, no key). Groq is one configuration, not
// a dependency.
//
// DETERMINISM. The agent is OFF-digest; its proposals are explicitly NOT part of any
// replay guarantee (the LLM is stochastic, and the candidate store is never read by
// the deterministic path). Store writes take an injected clock.
package dgx
