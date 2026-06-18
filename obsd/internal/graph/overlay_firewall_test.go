package graph

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOverlayPathsSkipsCandidateSubdir proves the firewall MECHANISM directly: the
// production globber returns only the root *.yaml/*.yml files and never descends
// into a subdirectory — so ontology/graph/overlays/candidate/ is invisible to a
// release (doc 20 §1, the hash-firewall). Zero dependency on any KG content.
func TestOverlayPathsSkipsCandidateSubdir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "overlay: a\n")
	writeFile(t, filepath.Join(dir, "b.yml"), "overlay: b\n")
	writeFile(t, filepath.Join(dir, "c.txt"), "ignored: not yaml\n")
	if err := os.MkdirAll(filepath.Join(dir, "candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "candidate", "poison.yaml"), "overlay: poison\n")
	writeFile(t, filepath.Join(dir, "candidate", "x.yml"), "overlay: x\n")

	paths, err := overlayPaths(dir)
	if err != nil {
		t.Fatalf("overlayPaths: %v", err)
	}
	want := []string{filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yml")}
	if len(paths) != len(want) {
		t.Fatalf("overlayPaths returned %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("overlayPaths[%d] = %s, want %s", i, paths[i], want[i])
		}
	}
	for _, p := range paths {
		if filepath.Base(filepath.Dir(p)) == "candidate" {
			t.Errorf("FIREWALL BREACH: overlayPaths returned a path under candidate/: %s", p)
		}
	}
}

// TestReleaseHashIgnoresCandidateSubdir is the load-level determinism-diff (doc 20
// §1c) on the REAL release files: the pinned content hash is byte-identical with and
// without a candidate/ subdirectory present. The poison file would fail applyOverlay
// if it were ever globbed, so a successful, hash-unchanged load proves it is not.
func TestReleaseHashIgnoresCandidateSubdir(t *testing.T) {
	path, r, err := LatestRelease(releaseDir)
	if err != nil || r == nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	_ = path
	base := filepath.Join(repoRoot, r.Base)
	realOverlayDir := filepath.Join(repoRoot, filepath.Dir(r.Base), "overlays")

	// Reproduce the release in a temp dir from the root overlay files only.
	tmpOverlays := filepath.Join(t.TempDir(), "overlays")
	if err := os.MkdirAll(tmpOverlays, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(realOverlayDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // skip candidate/ and any other subdir, exactly as overlayPaths does
		}
		if ext := filepath.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
			copyFile(t, filepath.Join(realOverlayDir, e.Name()), filepath.Join(tmpOverlays, e.Name()))
		}
	}

	g1, err := LoadWithOverlays(base, tmpOverlays)
	if err != nil {
		t.Fatalf("load (no candidate subdir): %v", err)
	}
	if g1.Version != r.GraphVersion {
		t.Fatalf("temp reproduction drifted from the release pin: got %s, want %s "+
			"(the copy must reproduce the real release exactly)", g1.Version, r.GraphVersion)
	}

	// Add a candidate/ subdir holding a file that WOULD break applyOverlay if globbed.
	if err := os.MkdirAll(filepath.Join(tmpOverlays, "candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tmpOverlays, "candidate", "poison.yaml"),
		"overlay: poison\n# deliberately missing 'author' — would fail applyOverlay if ever globbed\n")

	g2, err := LoadWithOverlays(base, tmpOverlays)
	if err != nil {
		t.Fatalf("load (with candidate subdir) failed — the subdir was globbed: %v", err)
	}
	if g2.Version != g1.Version {
		t.Errorf("FIREWALL BREACH: release hash changed when a candidate subdir was present: %s != %s", g2.Version, g1.Version)
	}
	if len(g2.Phenomena) != len(g1.Phenomena) {
		t.Errorf("candidate subdir altered the graph: %d phenomena vs %d", len(g2.Phenomena), len(g1.Phenomena))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}
