package meta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Guard 3: no time.Now() in logic — only at the injected clock seam ---------
//
// CLAUDE.md (determinism-first): "inject clocks (no time.Now in logic)". The replay
// guarantee depends on it: a stray wall-clock read in a logic package makes a
// fingerprint non-reproducible. This guard walks every NON-test .go file under
// obsd/internal and asserts that any `time.Now(` appears ONLY inside a clock-provider
// seam — a `func() time.Time { ... }` literal that DEFINES the injectable clock (the
// one place the real wall clock is legitimately read, then injected). A bare
// `x := time.Now()` in logic fails this test until it is replaced by an injected clock.
//
// The two sanctioned seams today:
//   - api/server.go:   var timeNowUTC = func() time.Time { return time.Now().UTC() }
//   - kube/proxy.go:   NewProxyFetcher{ now: func() time.Time { return time.Now().UTC() } }
//
// Both are single-line provider literals; a future seam must keep the `func() time.Time`
// and `time.Now(` on the same line, or be added to a documented exemption here.
func TestNoTimeNowInLogic(t *testing.T) {
	root := repoRoot(t)
	internal := filepath.Join(root, "obsd", "internal")
	err := filepath.WalkDir(internal, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "time.Now(") {
				continue
			}
			// The only sanctioned use: defining the injectable clock seam.
			if strings.Contains(line, "func() time.Time") {
				continue
			}
			t.Errorf("%s:%d reads the wall clock in logic — inject a clock instead "+
				"(CLAUDE.md: no time.Now in logic; the replay guarantee depends on it). "+
				"Only a `func() time.Time { ... }` provider seam may call time.Now.\n  %s",
				rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk obsd/internal: %v", err)
	}
}
