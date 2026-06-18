package assoc_test

import (
	"os/exec"
	"strings"
	"testing"
)

const assocPkg = "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/assoc"

// deterministicPkgs must produce byte-identical fingerprints/matches on replay; an
// assoc coefficient is MEASURED but data-derived, so routing it into any of them
// (especially forecast/decompose and selection/tierb — the critic's HIGH flag) would
// launder a data-fit number onto the replay-load-bearing path. None may import assoc.
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

// TestNoDeterministicPackageImportsAssoc is the seam-firewall (doc 20 §2.3): the full
// transitive dependency set of each deterministic package must not contain assoc.
func TestNoDeterministicPackageImportsAssoc(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping import-firewall (CI enforces it)")
	}
	const repoRoot = "../../.." // obsd/internal/assoc → repo root
	for _, pkg := range deterministicPkgs {
		cmd := exec.Command("go", "list", "-deps", pkg)
		cmd.Dir = repoRoot
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) == assocPkg {
				t.Errorf("FIREWALL BREACH: deterministic package %s imports %s — a MEASURED "+
					"association must never reach the replay-deterministic path (doc 20 §2.3)", pkg, assocPkg)
			}
		}
	}
}
