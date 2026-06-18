package logtmpl_test

import (
	"os/exec"
	"strings"
	"testing"
)

const logtmplPkg = "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/logtmpl"

// The log lane is OFF the deterministic fingerprint digest (logs are sampled/unbounded,
// like events). No deterministic package may import internal/logtmpl.
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

func TestNoDeterministicPackageImportsLogtmpl(t *testing.T) {
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
			if strings.TrimSpace(line) == logtmplPkg {
				t.Errorf("FIREWALL BREACH: deterministic package %s imports %s (doc 20 P4: the log lane is off-digest)", pkg, logtmplPkg)
			}
		}
	}
}
