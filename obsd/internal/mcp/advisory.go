package mcp

import (
	"fmt"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
)

// AdvisoryResult is the ADVISORY 4th class — generated, operator-facing prose that
// is NEVER MEASURED/PROJECTED/AUTHORED and is NEVER written back into the graph,
// findings, or bars. It is the only generative affordance the MCP surface offers,
// and it is doubly fenced:
//
//   - REFUSED: a draft carrying a banned causal or future-certainty register (the
//     same denylist that guards Vigil's own surfaces) is rejected outright. The
//     refusal is the safe behaviour — an LLM that tries to assert a cause is stopped
//     at the boundary.
//   - WITHHELD: even a register-clean draft is withheld until the advisory backtest
//     gate passes (doc 11 §3.5 — no new class is operator-visible before its gate).
//
// The denylist is honestly a backstop, not a proof (see api.CharterViolations); the
// primary guarantee is that ADVISORY is a separate, labelled class that never
// upgrades to MEASURED and never mutates state.
type AdvisoryResult struct {
	Class          string `json:"class"` // always "ADVISORY"
	Text           string `json:"text,omitempty"`
	Refused        bool   `json:"refused"`
	RefusedReason  string `json:"refusedReason,omitempty"`
	Withheld       bool   `json:"withheld"`
	WithheldReason string `json:"withheldReason,omitempty"`
}

// emitAdvisory runs the charter guard, then the gate fence. It writes nothing —
// the result is returned to the caller and discarded by Vigil.
func (s *Server) emitAdvisory(text string) AdvisoryResult {
	res := AdvisoryResult{Class: "ADVISORY"}

	// Charter guard FIRST (refusal precedes any gate consideration): a banned
	// register is rejected whether or not the gate has passed.
	if vs := api.CharterViolations("advisory", []byte(text)); len(vs) > 0 {
		res.Refused = true
		res.RefusedReason = fmt.Sprintf(
			"advisory rejected by the charter guard: banned %s register (%q). ADVISORY may cite classed facts but never assert a cause or a future certainty.",
			vs[0].Class, vs[0].Phrase)
		return res
	}

	if !s.advisoryGatePassed {
		res.Withheld = true
		res.WithheldReason = "ADVISORY content is withheld until its charter/absence backtest gate passes (doc 11 §3.5); the draft is register-clean but the class is not yet operator-visible."
		return res
	}

	res.Text = text
	return res
}

// ValidateAdvisory exposes the guard for the gate's honeypot battery (the teeth
// test): it returns the same result emitAdvisory would, for a given gate posture,
// without needing a live Server.
func ValidateAdvisory(text string, advisoryGatePassed bool) AdvisoryResult {
	return (&Server{advisoryGatePassed: advisoryGatePassed}).emitAdvisory(text)
}
