package meta

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot walks up from this test file to the directory containing go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test file")
		}
		dir = parent
	}
}

// --- Guard 1: every gate-passed flag maps to a real gate ---------------------
//
// A `…GatePassed` constant WITHHOLDS an operator-visible feature until its gate certifies
// it (doc 11 / doc 15). gateFlagRegistry names the gate recipe behind each. The test
// AST-discovers every such top-level const/var across obsd/ and asserts (a) each is
// registered, and (b) the named recipe exists in the justfile. A v5 dev adding
// `phaseFGatePassed` to gate a new lane gets a failing test until they name its gate.

var gateFlagRegistry = map[string]string{
	"phaseECrossServiceGatePassed":        "xsvc-projected-gate",
	"phaseDProjectedTransitiveGatePassed": "projected-transitive-gate",
	"mcpAdvisoryGatePassed":               "mcp-gate",
	"phaseCDepartureGatePassed":           "departure-gate",
}

// discoverGateFlags AST-walks every non-test .go file under obsd/ and returns the names of
// top-level const/var declarations whose identifier ends with "GatePassed". Top-level only,
// so function parameters (projectedGatePassed) and struct fields (the prometheus desc) are
// correctly excluded.
func discoverGateFlags(t *testing.T, root string) map[string]string {
	t.Helper()
	found := map[string]string{} // flag -> file
	obsd := filepath.Join(root, "obsd")
	err := filepath.WalkDir(obsd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, decl := range f.Decls { // top-level declarations only
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if strings.HasSuffix(name.Name, "GatePassed") {
						found[name.Name] = strings.TrimPrefix(path, root+string(os.PathSeparator))
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk obsd: %v", err)
	}
	return found
}

func TestGatePassedFlagsAreRegistered(t *testing.T) {
	root := repoRoot(t)
	flags := discoverGateFlags(t, root)
	if len(flags) == 0 {
		t.Fatal("discovered zero …GatePassed flags — the AST walk is broken")
	}
	justfile, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatalf("read justfile: %v", err)
	}
	jf := string(justfile)

	for flag, file := range flags {
		recipe, ok := gateFlagRegistry[flag]
		if !ok {
			t.Errorf("gate flag %q (in %s) is NOT in gateFlagRegistry — a feature is gated on a "+
				"flag with no named gate. Register it with the just recipe that certifies it.", flag, file)
			continue
		}
		// the gate recipe must exist in the justfile (e.g. a "departure-gate:" target).
		if !strings.Contains(jf, recipe+":") {
			t.Errorf("gate flag %q names recipe %q but the justfile has no %q target.", flag, recipe, recipe+":")
		}
	}
	for flag := range gateFlagRegistry {
		if _, ok := flags[flag]; !ok {
			t.Errorf("gateFlagRegistry lists %q but no such flag exists in obsd/ — remove the stale entry.", flag)
		}
	}
}

// --- Guard 2: every package has tests (or a documented exemption) ------------
//
// `go test ./...` never complains about a package with zero tests. This guard does: every
// directory under obsd/ holding non-test .go files must carry a _test.go, OR be on
// untestedAllowlist with an explicit reason. A NEW package cannot ship untested silently —
// the dev must either test it or consciously exempt it (and justify why).

var untestedAllowlist = map[string]string{
	// cmd/obsd was here, but v5 added cmd/obsd tests (dgx tool wiring) — the gate now
	// requires the stale exemption to be removed (it is exercised hermetically + by e2e).
	"cmd/replay":             "thin CLI over internal/replay (tested); the replay engine + bundles are golden-tested there.",
	"cmd/govern":             "thin CLI over internal/governance (tested with 42 funcs).",
	"cmd/conntrack-agent":    "privileged DaemonSet binary; no hermetic surface — certified by the flow Phase-A live gate.",
	"cmd/flowprobe":          "conntrack probe binary; live-only, no hermetic surface — certified by the flow live path.",
	"internal/version":       "build-stamp ldflags only; no logic to test.",
	"internal/replay/export": "Parquet export bridge; exercised by the harness Parquet round-trip (test_replay_determinism.py) and the replay export path.",
}

func TestEveryPackageHasTestsOrIsExempt(t *testing.T) {
	root := repoRoot(t)
	obsd := filepath.Join(root, "obsd")

	hasSrc := map[string]bool{}
	hasTest := map[string]bool{}
	err := filepath.WalkDir(obsd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(obsd, filepath.Dir(path))
		if strings.HasSuffix(path, "_test.go") {
			hasTest[rel] = true
		} else {
			hasSrc[rel] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk obsd: %v", err)
	}

	for pkg := range hasSrc {
		if hasTest[pkg] {
			if _, exempt := untestedAllowlist[pkg]; exempt {
				t.Errorf("package %q now HAS tests but is still on untestedAllowlist — remove the stale exemption.", pkg)
			}
			continue
		}
		if _, exempt := untestedAllowlist[pkg]; !exempt {
			t.Errorf("package obsd/%s has source files but ZERO tests and is not on untestedAllowlist. "+
				"Add tests, or add it to untestedAllowlist with a documented reason (meta_test.go).", pkg)
		}
	}
	// keep the allowlist honest: no entry for a package that no longer exists.
	for pkg := range untestedAllowlist {
		if !hasSrc[pkg] {
			t.Errorf("untestedAllowlist lists %q but no such package exists under obsd/ — remove the stale entry.", pkg)
		}
	}
}
