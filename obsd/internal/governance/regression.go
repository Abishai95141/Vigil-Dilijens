package governance

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Gate is one harness regression gate (doc 12 §3.2/§3.3, with 11 M7). The set of
// required gates scales with the change class; a required gate that did not RUN, or
// ran and FAILED, blocks the release. The harness produces the ledger; this package
// verifies it. The harness can block; it cannot approve (doc 12 §3.3).
type Gate string

const (
	// GateLints — authoring lints (graphlint: schema + referential integrity +
	// overlay coherence + immutability). Required for every class.
	GateLints Gate = "authoring-lints"
	// GateBindingQA — semantic binding QA on affected families (false-equivalence
	// catcher, doc 04 M3). Required for every class.
	GateBindingQA Gate = "binding-qa"
	// GateFalsification — the falsification/phenomenon suite for the changed
	// phenomenon (doc 11 §3.3). Behavioural+ ("untested status blocks ship-as-
	// validated").
	GateFalsification Gate = "falsification"
	// GateReplayDiff — replay diff on reference bundles (the change must not perturb
	// the deterministic digest in unexpected ways). Behavioural+.
	GateReplayDiff Gate = "replay-diff"
	// GateShippedBacktest — re-run every shipped warning class's backtest gate where
	// bars feed forecasts (doc 09 M3, doc 12 §3.2). Normative-high.
	GateShippedBacktest Gate = "shipped-class-backtest"
)

// RequiredGates returns the regression gates mandatory for a change class
// (doc 12 §3.2). Higher classes are supersets: every gate of a lower class plus its
// own. A skipped required gate cannot release (M3 exit).
func RequiredGates(class ChangeClass) []Gate {
	switch class {
	case ClassNone:
		return []Gate{GateLints}
	case ClassAdditiveLow:
		return []Gate{GateLints, GateBindingQA}
	case ClassBehaviouralMedium:
		return []Gate{GateLints, GateBindingQA, GateFalsification, GateReplayDiff}
	case ClassNormativeHigh:
		return []Gate{GateLints, GateBindingQA, GateFalsification, GateReplayDiff, GateShippedBacktest}
	default:
		// Unknown class: demand the full battery (fail safe, never under-gate).
		return []Gate{GateLints, GateBindingQA, GateFalsification, GateReplayDiff, GateShippedBacktest}
	}
}

// GateResult is one gate's outcome in the harness ledger.
type GateResult struct {
	Gate    string `json:"gate"`    // matches a Gate constant
	Status  string `json:"status"`  // passed | failed | skipped
	Detail  string `json:"detail"`  // what ran, what it found
	RanAt   string `json:"ran_at"`  // ISO timestamp (harness-stamped)
	Command string `json:"command"` // the underlying command executed (auditability)
}

// GateLedger is the harness-produced regression ledger consumed by the workflow.
// It pins the proposal + the effective class it was run for, so a ledger run for a
// weaker class cannot satisfy a stronger one.
type GateLedger struct {
	Proposal string       `json:"proposal"`
	Class    string       `json:"class"`
	Results  []GateResult `json:"results"`
}

// LoadGateLedger reads a harness regression ledger JSON file.
func LoadGateLedger(path string) (*GateLedger, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gate ledger %q: %w", path, err)
	}
	var l GateLedger
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("parse gate ledger %q: %w", path, err)
	}
	return &l, nil
}

// verifyGateChecks turns the required-gate set + the harness ledger into workflow
// checks. The rule (doc 12 §3.3, M3 exit): a required gate must have a result that
// PASSED — a missing result is "a skipped gate cannot release" (block), a failed
// result is the harness exercising its blocking power (block). Extra results beyond
// the required set are fine (over-testing never blocks).
func verifyGateChecks(required []Gate, results []GateResult) []Check {
	byGate := map[string]GateResult{}
	for _, r := range results {
		byGate[r.Gate] = r
	}
	out := make([]Check, 0, len(required))
	for _, g := range required {
		r, ok := byGate[string(g)]
		switch {
		case !ok:
			out = append(out, Check{"gate:" + string(g), StatusBlock,
				"required regression gate did not run — a skipped gate cannot release (doc 12 M3)"})
		case r.Status == "passed":
			out = append(out, Check{"gate:" + string(g), StatusPass, gateDetail(r)})
		case r.Status == "skipped":
			out = append(out, Check{"gate:" + string(g), StatusBlock,
				"required regression gate was SKIPPED — a skipped gate cannot release (doc 12 M3)"})
		case r.Status == "dry-run":
			out = append(out, Check{"gate:" + string(g), StatusBlock,
				"gate result is a DRY-RUN (nothing executed) — a dry-run ledger is a planning aid, never proof a gate ran (doc 12 M3)"})
		default: // failed / anything else
			out = append(out, Check{"gate:" + string(g), StatusBlock,
				"regression gate FAILED — the harness blocks the release: " + gateDetail(r)})
		}
	}
	return out
}

func gateDetail(r GateResult) string {
	if r.Detail != "" {
		return r.Detail
	}
	if r.Command != "" {
		return r.Command
	}
	return r.Status
}

// VerifyGates is the standalone gate verification (used by tests + the CLI's
// `verify --ledger` path): returns a blocking error if any required gate is missing
// or failed. Mirrors verifyGateChecks but as a hard error for scripting.
func VerifyGates(class ChangeClass, ledger *GateLedger) error {
	checks := verifyGateChecks(RequiredGates(class), ledger.Results)
	var blocks []string
	for _, c := range checks {
		if c.Status == StatusBlock {
			blocks = append(blocks, c.Name+": "+c.Detail)
		}
	}
	if len(blocks) > 0 {
		sort.Strings(blocks)
		return fmt.Errorf("regression gates block the %s release:\n  - %s", class, joinLines(blocks))
	}
	return nil
}

func joinLines(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += "\n  - "
		}
		out += x
	}
	return out
}
