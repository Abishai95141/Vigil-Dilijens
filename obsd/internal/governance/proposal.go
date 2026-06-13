package governance

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"gopkg.in/yaml.v3"
)

// EvidenceRef is one falsifiable basis an assertion rests on (doc 02 §3.6 / doc 12
// §3.3): "review without evidence is rejection by default." Kind names the evidence
// type (corpus-run, replay-bundle, backtest, doc, ticket); Ref locates it.
type EvidenceRef struct {
	Kind string `yaml:"kind"`
	Ref  string `yaml:"ref"`
}

// Review is the human approval record (doc 12 §3.3: approval is human; the harness
// can block but never approve). A proposal is not releasable until a named reviewer
// records an approve decision — the tooling enforces the gates, the human owns the
// call.
type Review struct {
	Reviewer string `yaml:"reviewer"`
	Decision string `yaml:"decision"` // approve | reject | "" (pending)
	At       string `yaml:"at"`
	Notes    string `yaml:"notes"`
}

// Proposal is a change proposal in the review workflow (doc 12 §3.3/§3.7). It
// declares its change class (verified against the actual diff), its author, the
// evidence each assertion rests on, and — once reviewed — the human decision.
type Proposal struct {
	Release          string        `yaml:"release"`           // target release name (e.g. "v0.4.0")
	From             string        `yaml:"from"`              // base release this builds on ("" = first release)
	Class            string        `yaml:"class"`             // DECLARED class; verified against the diff
	Author           string        `yaml:"author"`            // authorship provenance (mandatory)
	Summary          string        `yaml:"summary"`           //
	AffectedElements []string      `yaml:"affected_elements"` // optional; the diff is authoritative
	Evidence         []EvidenceRef `yaml:"evidence"`          // falsifiable bases (mandatory for behavioural+)
	Review           Review        `yaml:"review"`            // human decision record
}

// LoadProposal reads a proposal YAML file.
func LoadProposal(path string) (*Proposal, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read proposal %q: %w", path, err)
	}
	var p Proposal
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse proposal %q: %w", path, err)
	}
	if strings.TrimSpace(p.Release) == "" {
		return nil, fmt.Errorf("proposal %q: missing 'release'", path)
	}
	return &p, nil
}

// CheckStatus is one workflow gate's verdict.
type CheckStatus string

const (
	StatusPass  CheckStatus = "pass"
	StatusBlock CheckStatus = "block" // a release-blocking defect (mechanical)
	StatusWarn  CheckStatus = "warn"  // advisory; does not block
)

// Check is one step of the review workflow with its verdict and reason.
type Check struct {
	Name   string
	Status CheckStatus
	Detail string
}

// Verdict is the aggregate workflow outcome.
type Verdict string

const (
	// VerdictBlocked — at least one mechanical gate is a release-blocking defect.
	VerdictBlocked Verdict = "blocked"
	// VerdictReadyForReview — mechanical gates pass; awaiting human approval.
	VerdictReadyForReview Verdict = "ready-for-review"
	// VerdictApproved — mechanical gates pass AND a named reviewer approved.
	VerdictApproved Verdict = "approved"
)

// WorkflowResult aggregates the review workflow's checks into one verdict.
type WorkflowResult struct {
	Proposal      string
	DeclaredClass ChangeClass
	DerivedClass  ChangeClass
	Changes       []ChangeItem
	Checks        []Check
	Verdict       Verdict
}

// Blocked reports whether the workflow blocks the release.
func (w *WorkflowResult) Blocked() bool { return w.Verdict == VerdictBlocked }

// EvaluateProposal runs the mechanical review workflow (doc 12 §3.3) over a proposal
// against the two loaded graph releases. It performs the gates the TOOLING owns —
// class verification (the diff is authoritative; misclassification blocks),
// evidence-required-by-default, and the regression-gate ledger (M3) — and records
// whether a human reviewer has approved. It never approves on its own: a clean
// mechanical pass yields ReadyForReview until a named reviewer's approve decision is
// present (doc 12 §3.3 "the harness can block; it cannot approve").
//
// ledger is the harness regression ledger (M3); pass nil to evaluate only the class
// + evidence gates (e.g. an early dry-run before regression has run). When supplied,
// its Class and Proposal are ENFORCED: a ledger run for a weaker class, or for a
// different proposal, cannot satisfy this one (doc 12 §3.1 — every result pins the
// version + class it tested).
func EvaluateProposal(p *Proposal, from, to *graph.Graph, ledger *GateLedger) (*WorkflowResult, error) {
	declared, err := ParseChangeClass(p.Class)
	if err != nil {
		return nil, fmt.Errorf("proposal %s: %w", p.Release, err)
	}
	derived, changes := Classify(from, to)

	w := &WorkflowResult{
		Proposal:      p.Release,
		DeclaredClass: declared,
		DerivedClass:  derived,
		Changes:       changes,
	}
	add := func(name string, st CheckStatus, detail string) {
		w.Checks = append(w.Checks, Check{name, st, detail})
	}

	// Gate 1 — authorship provenance (doc 02 §3.6, doc 12 §4: every assertion
	// carries the author's identity).
	if strings.TrimSpace(p.Author) == "" {
		add("authorship", StatusBlock, "no author recorded — every authored assertion carries an identity (doc 02 §3.6)")
	} else {
		add("authorship", StatusPass, "author: "+p.Author)
	}

	// Gate 2 — class verification (doc 12 §3.2: misclassification is a release-
	// blocking defect; the dangerous direction is under-classification).
	switch {
	case derived == ClassNone:
		add("class-verification", StatusWarn,
			"the diff shows no semantic change — proposal declares "+declared.String())
	case declared < derived:
		add("class-verification", StatusBlock, fmt.Sprintf(
			"MISCLASSIFICATION: declared %s but the diff reaches %s — %s (doc 12 §3.2; under-classification under-reviews a wide-reaching change)",
			declared, derived, widestReason(changes, derived)))
	case declared > derived:
		add("class-verification", StatusWarn, fmt.Sprintf(
			"over-declared: declared %s, diff reaches only %s — extra rigor is safe, not blocking", declared, derived))
	default:
		add("class-verification", StatusPass, "declared class "+declared.String()+" matches the derived diff")
	}

	// Gate 3 — evidence required by default for behavioural+ (doc 12 §3.3).
	if derived >= ClassBehaviouralMedium || declared >= ClassBehaviouralMedium {
		if len(p.Evidence) == 0 {
			add("evidence", StatusBlock,
				"no evidence references — a behavioural/normative assertion without evidence is rejection by default (doc 12 §3.3)")
		} else {
			add("evidence", StatusPass, fmt.Sprintf("%d evidence reference(s) recorded", len(p.Evidence)))
		}
	} else {
		add("evidence", StatusPass, "additive-low change: domain review + lints (no falsification evidence mandated)")
	}

	// Gate 4 — regression gates scaled to class (M3). The effective class for
	// gating is the WIDER of declared/derived (never under-gate).
	effective := declared
	if derived > effective {
		effective = derived
	}
	required := RequiredGates(effective)
	if ledger == nil {
		add("regression-gates", StatusWarn, fmt.Sprintf(
			"regression ledger not supplied — %s requires: %s (run `harness governance run-gates`)",
			effective, gateList(required)))
	} else {
		// The ledger must have been run FOR this proposal and for a class at least as
		// strong as the effective class — otherwise it tested the wrong thing.
		if ledger.Proposal != "" && ledger.Proposal != "(none)" && ledger.Proposal != p.Release {
			add("ledger-binding", StatusBlock, fmt.Sprintf(
				"regression ledger was produced for proposal %q, not %q — a ledger does not transfer between proposals", ledger.Proposal, p.Release))
		}
		if ledger.Class != "" {
			ranClass, err := ParseChangeClass(ledger.Class)
			if err != nil {
				add("ledger-binding", StatusBlock, "regression ledger declares an unknown class "+ledger.Class)
			} else if ranClass < effective {
				add("ledger-binding", StatusBlock, fmt.Sprintf(
					"regression ledger ran for %s but this change reaches %s — the stronger-class gates were not run", ranClass, effective))
			}
		}
		for _, c := range verifyGateChecks(required, ledger.Results) {
			w.Checks = append(w.Checks, c)
		}
	}

	// Gate 5 — staged rollout mandatory for normative-high (doc 12 §3.2).
	if effective == ClassNormativeHigh {
		add("staged-rollout", StatusWarn,
			"normative-high: staged rollout is MANDATORY and a rollback plan is required (doc 12 §3.2/§3.5) — exercise via `govern rollout`")
	}

	// Derive the verdict.
	blocked := false
	for _, c := range w.Checks {
		if c.Status == StatusBlock {
			blocked = true
		}
	}
	switch {
	case blocked:
		w.Verdict = VerdictBlocked
	case strings.EqualFold(p.Review.Decision, "approve") && strings.TrimSpace(p.Review.Reviewer) != "":
		w.Verdict = VerdictApproved
	default:
		w.Verdict = VerdictReadyForReview
	}
	return w, nil
}

// widestReason names the widest-reaching change that forces the derived class — so a
// misclassification message points at the specific element, not just the verdict.
func widestReason(changes []ChangeItem, derived ChangeClass) string {
	var hits []string
	for _, c := range changes {
		if c.Class == derived {
			hits = append(hits, c.Detail)
		}
	}
	sort.Strings(hits)
	if len(hits) == 0 {
		return "see diff"
	}
	if len(hits) > 3 {
		hits = append(hits[:3], fmt.Sprintf("(+%d more)", len(hits)-3))
	}
	return strings.Join(hits, "; ")
}

func gateList(gs []Gate) string {
	parts := make([]string, len(gs))
	for i, g := range gs {
		parts[i] = string(g)
	}
	return strings.Join(parts, ", ")
}
