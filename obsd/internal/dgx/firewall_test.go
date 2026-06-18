package dgx_test

import (
	"os/exec"
	"strings"
	"testing"
)

const dgxPkg = "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"

// The dgx agent is OFF the deterministic path: it reads classed facts and proposes
// candidates, but a model's stochastic output must never reach the replay-deterministic
// engine. No deterministic package may import internal/dgx.
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

func TestNoDeterministicPackageImportsDGX(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping import-firewall (CI enforces it)")
	}
	const repoRoot = "../../.."
	for _, pkg := range deterministicPkgs {
		cmd := exec.Command("go", "list", "-deps", pkg)
		cmd.Dir = repoRoot
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) == dgxPkg {
				t.Errorf("FIREWALL BREACH: deterministic package %s imports %s — a model's "+
					"output must never reach the replay-deterministic path (doc 20 P3)", pkg, dgxPkg)
			}
		}
	}
}
