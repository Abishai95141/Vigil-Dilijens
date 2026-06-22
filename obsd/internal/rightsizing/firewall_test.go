package rightsizing_test

import (
	"os/exec"
	"strings"
	"testing"
)

// rightsizingPkg is the import path the deterministic path must NEVER reach.
const rightsizingPkg = "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/rightsizing"

// deterministicPkgs are the packages whose output must be byte-identical on replay. The
// right-sizing advisory lane is off-digest and non-gating (docs/31 §6.3); if ANY of these
// imports it — directly or transitively — an advisory percentile/recommendation could perturb
// a fingerprint/match/digest, and the firewall is breached. Same list the notify/candidate/
// audit firewalls guard. docs/31 §7 Step-5 GATE requires this test to stay green.
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

// TestNoDeterministicPackageImportsRightSizing asks the Go toolchain for the FULL transitive
// dependency set of each deterministic package and fails if internal/rightsizing is anywhere
// in it. Hermetic (go list reads the module locally, no network); skips cleanly if the
// toolchain is unavailable.
func TestNoDeterministicPackageImportsRightSizing(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping import-firewall (CI enforces it)")
	}
	const repoRoot = "../../.." // obsd/internal/rightsizing → repo root
	for _, pkg := range deterministicPkgs {
		deps, err := transitiveDeps(t, repoRoot, pkg)
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		if deps[rightsizingPkg] {
			t.Errorf("FIREWALL BREACH: deterministic package %s imports %s (transitively) — "+
				"the right-sizing advisory lane must never be reachable from the replay-deterministic path", pkg, rightsizingPkg)
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
