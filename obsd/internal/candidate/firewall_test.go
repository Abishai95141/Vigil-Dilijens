package candidate_test

import (
	"os/exec"
	"strings"
	"testing"
)

// candidatePkg is the import path the deterministic path must NEVER reach.
const candidatePkg = "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"

// deterministicPkgs are the packages whose output must be byte-identical on replay
// (doc 20 §1 read-firewall). If ANY of them imports internal/candidate — directly or
// transitively — the candidate machinery could perturb a fingerprint/match/digest,
// and the firewall is breached. This is the mechanical, reviewer-independent guard.
var deterministicPkgs = []string{
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock",
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay",
}

// TestNoDeterministicPackageImportsCandidate is the read-firewall: it asks the Go
// toolchain for the FULL transitive dependency set of each deterministic package and
// fails if internal/candidate is anywhere in it. Hermetic (go list reads the module
// locally, no network); skips cleanly if the toolchain is unavailable.
func TestNoDeterministicPackageImportsCandidate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping import-firewall (CI enforces it)")
	}
	const repoRoot = "../../.." // obsd/internal/candidate → repo root
	for _, pkg := range deterministicPkgs {
		deps, err := transitiveDeps(t, repoRoot, pkg)
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		if deps[candidatePkg] {
			t.Errorf("FIREWALL BREACH: deterministic package %s imports %s (transitively) — "+
				"the candidate space must never be reachable from the replay-deterministic path (doc 20 §1)", pkg, candidatePkg)
		}
	}
}

func transitiveDeps(t *testing.T, dir, pkg string) (map[string]bool, error) {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			set[strings.TrimSpace(line)] = true
		}
	}
	return set, nil
}
