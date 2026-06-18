package trace_test

import (
	"os/exec"
	"strings"
	"testing"
)

const tracePkg = "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/trace"

// The trace lane is OFF the deterministic fingerprint digest (spans are sampled +
// census-incomplete). No deterministic package may import internal/trace — obsd must be
// byte-identical with --traces-enabled off (the non-gating guarantee).
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
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained", // marshaled into the hashed TickResult — assert directly, not only via replay's transitive edge
}

func TestNoDeterministicPackageImportsTrace(t *testing.T) {
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
			if strings.TrimSpace(line) == tracePkg {
				t.Errorf("FIREWALL BREACH: deterministic package %s imports %s (doc 20 P4: the trace lane is off-digest)", pkg, tracePkg)
			}
		}
	}
}
