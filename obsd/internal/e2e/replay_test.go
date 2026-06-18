//go:build integration

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveReplayDeterminism is the portability proof: obsd captures a live window
// against the cluster, then bin/replay re-runs that bundle TWICE and the canonical
// per-tick output is byte-identical — and identical to the digests captured live.
//
// This is the determinism guarantee made operational across machines: a bundle
// captured on a cloud cluster replays byte-for-byte on a laptop. (The hermetic
// replay_test.go already proves the in-memory-vs-disk paths agree; this proves it
// end-to-end from a real scrape.)
func TestLiveReplayDeterminism(t *testing.T) {
	kc := kubeconfigPath(t)
	requireKubectl(t)

	_, replayBin := buildBinaries(t)
	dataDir := t.TempDir()
	obsd := startObsd(t, kc, dataDir) // default lanes; a healthy capture is enough to prove determinism

	// Let several evaluation ticks (15s cadence) accumulate in the bundle.
	bundleDir := filepath.Join(dataDir, "captures")
	eventually(t, 3*time.Minute, 5*time.Second, "the replay bundle to accumulate ticks", func() error {
		return bundleHasSegment(bundleDir)
	})
	time.Sleep(90 * time.Second) // a few more whole scrape/eval cycles

	// Stop obsd gracefully so it SEALS the bundle (SIGINT -> seal -> exit).
	obsd.stop()

	// Replay twice; each run must exit 0 (every tick reproduced its captured digest).
	out1 := filepath.Join(dataDir, "replay-out-1")
	out2 := filepath.Join(dataDir, "replay-out-2")
	runReplay(t, replayBin, bundleDir, out1)
	runReplay(t, replayBin, bundleDir, out2)

	// The two runs must be byte-identical (cross-run determinism), and non-empty
	// (a zero-tick bundle passing replay would be a hollow proof).
	n := compareCanonicalDirs(t, out1, out2)
	if n == 0 {
		t.Fatal("replay produced zero canonical tick files — hollow determinism pass")
	}
	t.Logf("live replay determinism OK: %d ticks reproduced byte-identically across two runs", n)
}

func runReplay(t *testing.T, replayBin, bundleDir, outDir string) {
	t.Helper()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir replay out: %v", err)
	}
	cmd := exec.Command(replayBin, "-bundle", bundleDir, "-out", outDir)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("replay -bundle %s failed (determinism violation or error): %v\n%s", bundleDir, err, out)
	}
	t.Logf("replay output:\n%s", bytes.TrimSpace(out))
}

// bundleHasSegment reports nil once the capture dir holds a manifest and ≥1 segment
// file. Segments are "seg-<ms>.active" while being written and only become
// "seg-...-...vseg" when SEALED (on graceful shutdown or at SegmentDuration), so the
// live capture is detected by the active file — the seal happens later, at stop().
func bundleHasSegment(bundleDir string) error {
	if _, err := os.Stat(filepath.Join(bundleDir, "manifest.json")); err != nil {
		return err
	}
	matches, _ := filepath.Glob(filepath.Join(bundleDir, "segments", "seg-*"))
	if len(matches) == 0 {
		return os.ErrNotExist
	}
	return nil
}

// compareCanonicalDirs asserts the two replay-out directories hold the same file set
// with byte-identical contents, and returns the number of files compared.
func compareCanonicalDirs(t *testing.T, a, b string) int {
	t.Helper()
	files := map[string]bool{}
	for _, dir := range []string{a, b} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read replay out dir %s: %v", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				files[e.Name()] = true
			}
		}
	}
	n := 0
	for name := range files {
		ba, err := os.ReadFile(filepath.Join(a, name))
		if err != nil {
			t.Fatalf("read %s/%s: %v", a, name, err)
		}
		bb, err := os.ReadFile(filepath.Join(b, name))
		if err != nil {
			t.Fatalf("read %s/%s: %v", b, name, err)
		}
		if !bytes.Equal(ba, bb) {
			t.Fatalf("DETERMINISM VIOLATION: canonical tick %q differs between replay runs (%d vs %d bytes)", name, len(ba), len(bb))
		}
		n++
	}
	return n
}
