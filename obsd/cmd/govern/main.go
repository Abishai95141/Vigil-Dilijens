// Command govern is the governance CLI (doc 12): the offline, deterministic
// surface for the graph change machinery — classify a change by blast radius,
// verify a proposal through the review workflow, triage the curation intake queue,
// and drive a staged-rollout exercise. It is NOT a runtime component (doc 12 §1);
// it never touches the hot path. Migration diffs against a LIVE cluster (M4) and the
// seeded-bad-release live exercise (M5) are driven by the governance integration
// tests + the obsd `-overlays` override, since they need the binding compiler over a
// real cluster; this CLI covers everything that is a pure function of committed
// artifacts.
//
// Subcommands:
//
//	govern classify --from <release> --to <release>          derive the change class + diff
//	govern verify   --proposal <p.yaml> [--ledger <l.json>]  run the review workflow; exit 1 if blocked
//	govern intake   --unexplained <j> --coverage <j> ...     triage the curation queue
//	govern rollout  --plan <plan.json>                       drive a staged-rollout exercise
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/governance"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// newFlags makes a subcommand FlagSet that reports parse errors to the caller.
func newFlags(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "classify":
		err = cmdClassify(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "intake":
		err = cmdIntake(os.Args[2:])
	case "migrate":
		err = cmdMigrate(os.Args[2:])
	case "rollout":
		err = cmdRollout(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `govern — Vigil graph governance CLI (doc 12)

  govern classify --from <release> --to <release> [--base-dir .]
  govern verify   --proposal <p.yaml> [--base-dir .] [--ledger <ledger.json>]
  govern intake   [--unexplained <candidates.json>] [--coverage <coverage.json>] [--falsification <f.json>]
  govern migrate  --from <bindingsA.json> --to <bindingsB.json>   (obsd --dump-bindings per release)
  govern rollout  --plan <plan.json>

A <release> is a release name (resolved under <base-dir>/ontology/releases/<name>.yaml)
or a path to a manifest. Exit code is non-zero when a workflow blocks or a rollout
rolls back.
`)
}

// resolveRelease loads a graph by release name or manifest path. "" or "none" yields
// (nil, nil) — the first-release baseline.
func resolveRelease(ref, baseDir string) (*graph.Graph, error) {
	if ref == "" || ref == "none" {
		return nil, nil
	}
	path := ref
	if !strings.HasSuffix(ref, ".yaml") && !strings.Contains(ref, "/") {
		path = filepath.Join(baseDir, "ontology", "releases", ref+".yaml")
	}
	g, _, err := graph.LoadReleasePinned(path, baseDir)
	return g, err
}

func cmdClassify(args []string) error {
	fs := newFlags("classify")
	from := fs.String("from", "", "base release name or manifest path ('' = first release)")
	to := fs.String("to", "", "target release name or manifest path")
	baseDir := fs.String("base-dir", ".", "repo root the relative paths resolve against")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}
	fromG, err := resolveRelease(*from, *baseDir)
	if err != nil {
		return fmt.Errorf("load --from: %w", err)
	}
	toG, err := resolveRelease(*to, *baseDir)
	if err != nil {
		return fmt.Errorf("load --to: %w", err)
	}
	class, items := governance.Classify(fromG, toG)
	fmt.Printf("derived change class: %s  (%d element change(s))\n", class, len(items))
	for _, it := range items {
		fmt.Printf("  [%-18s] %s %s — %s\n", it.Class, it.Op, it.Kind, it.Detail)
	}
	return nil
}

func cmdVerify(args []string) error {
	fs := newFlags("verify")
	proposalPath := fs.String("proposal", "", "proposal YAML path")
	baseDir := fs.String("base-dir", ".", "repo root")
	ledgerPath := fs.String("ledger", "", "harness regression ledger JSON (optional; without it gates are advisory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *proposalPath == "" {
		return fmt.Errorf("--proposal is required")
	}
	p, err := governance.LoadProposal(*proposalPath)
	if err != nil {
		return err
	}
	fromG, err := resolveRelease(p.From, *baseDir)
	if err != nil {
		return fmt.Errorf("load proposal 'from' (%s): %w", p.From, err)
	}
	toG, err := resolveRelease(p.Release, *baseDir)
	if err != nil {
		return fmt.Errorf("load proposal 'release' (%s): %w", p.Release, err)
	}
	var ledger *governance.GateLedger
	if *ledgerPath != "" {
		l, err := governance.LoadGateLedger(*ledgerPath)
		if err != nil {
			return err
		}
		ledger = l
	}
	w, err := governance.EvaluateProposal(p, fromG, toG, ledger)
	if err != nil {
		return err
	}
	fmt.Printf("proposal %s: declared %s · derived %s\n", w.Proposal, w.DeclaredClass, w.DerivedClass)
	for _, c := range w.Checks {
		fmt.Printf("  [%-5s] %-22s %s\n", c.Status, c.Name, c.Detail)
	}
	fmt.Printf("VERDICT: %s\n", strings.ToUpper(string(w.Verdict)))
	if w.Blocked() {
		return fmt.Errorf("proposal is BLOCKED — see release-blocking checks above")
	}
	return nil
}

func cmdIntake(args []string) error {
	fs := newFlags("intake")
	unexpPath := fs.String("unexplained", "", "unexplained candidate reports JSON ([]CandidateReport)")
	covPath := fs.String("coverage", "", "binding coverage report JSON (CoverageReport)")
	falsPath := fs.String("falsification", "", "falsification discrepancies JSON ([]FalsificationDiscrepancy)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var feeds [][]governance.IntakeItem
	if *falsPath != "" {
		var ds []governance.FalsificationDiscrepancy
		if err := readJSON(*falsPath, &ds); err != nil {
			return err
		}
		feeds = append(feeds, governance.FromFalsification(ds))
	}
	if *unexpPath != "" {
		var cands []unexplained.CandidateReport
		if err := readJSON(*unexpPath, &cands); err != nil {
			return err
		}
		feeds = append(feeds, governance.FromUnexplainedCandidates(cands))
	}
	if *covPath != "" {
		var rep binding.CoverageReport
		if err := readJSON(*covPath, &rep); err != nil {
			return err
		}
		feeds = append(feeds, governance.FromCoverageGaps(&rep))
	}
	items := governance.Triage(feeds...)
	st := governance.Stats(items)
	fmt.Printf("curation intake queue: %d item(s) — %d falsification · %d unexplained · %d coverage-gap\n",
		st.Total, st.BySource[governance.SourceFalsification], st.BySource[governance.SourceUnexplained], st.BySource[governance.SourceCoverageGap])
	for _, it := range items {
		fmt.Printf("  [%-21s] (recur %d) %s\n      → %s\n", it.Source, it.Recurrence, it.Summary, it.ProposedAction)
	}
	return nil
}

func cmdMigrate(args []string) error {
	fs := newFlags("migrate")
	fromPath := fs.String("from", "", "binding.Result JSON dumped under the FROM release (obsd --dump-bindings)")
	toPath := fs.String("to", "", "binding.Result JSON dumped under the TO release")
	customer := fs.String("customer", "cluster", "customer/cluster label for the diff")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromPath == "" || *toPath == "" {
		return fmt.Errorf("--from and --to binding dumps are required")
	}
	var from, to binding.Result
	if err := readJSON(*fromPath, &from); err != nil {
		return err
	}
	if err := readJSON(*toPath, &to); err != nil {
		return err
	}
	d := governance.BindingDiff(*customer, &from, &to)
	fmt.Print(d.Human())
	return nil
}

// rolloutPlan is the JSON shape for a staged-rollout exercise.
type rolloutPlan struct {
	Release string `json:"release"`
	Prior   string `json:"prior"`
	Stages  []struct {
		Stage   string                    `json:"stage"`
		Signals []governance.HealthSignal `json:"signals"`
	} `json:"stages"`
}

func cmdRollout(args []string) error {
	fs := newFlags("rollout")
	planPath := fs.String("plan", "", "rollout plan JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" {
		return fmt.Errorf("--plan is required")
	}
	var plan rolloutPlan
	if err := readJSON(*planPath, &plan); err != nil {
		return err
	}
	roll := governance.NewRollout(plan.Release, plan.Prior)
	for _, st := range plan.Stages {
		rep := governance.EvaluateStage(governance.Stage(st.Stage), st.Signals)
		for _, s := range rep.Signals {
			fmt.Printf("  [%s] %s\n", st.Stage, s.Explain())
		}
		if !roll.Step(rep) {
			break
		}
	}
	fmt.Print(roll.Summary())
	if roll.RolledBack {
		return fmt.Errorf("rollout ROLLED BACK to %s", roll.PriorRelease)
	}
	return nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	return nil
}
