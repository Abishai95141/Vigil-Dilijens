// Package clock is the Go client for the forecasting "clock" — the model-agnostic
// service (clockd) that answers exactly one question: when does this number cross
// that number. Everything that makes the answer MEAN something is authored graph
// knowledge, joined only at surfacing, never here.
//
// Owning doc: 09-forecasting-layer.md (this package owns the client side; the
// service is clockd/, in Python).
//
// The clock interface / swap contract (doc 09 §3.1):
//   - Input: one bare float series (scale-normalized inside the clock); optionally
//     one known-future covariate channel.
//   - FORBIDDEN input: labels, names, units, entities, graph structure, reasons —
//     anything semantic. This is enforced at the proto layer (the clock service
//     carries NO string fields) and asserted by conformance_test.go.
//   - Output: point forecast plus a quantile band over the horizon.
//   - Any conformant model is a drop-in; swapping the clock touches ZERO knowledge.
//
// Output is PROJECTED-class with AUTHORED references attached, never fused. The two
// permitted graph->model flows are exhaustive (doc 01 §4): target selection, and
// footprint subtraction. The model's inference channel sees bare floats only, and
// the graph never ingests anything this layer produces.
//
// Non-gating (absolute): detection is identical whether the clock is present,
// degraded, or absent.
package clock
