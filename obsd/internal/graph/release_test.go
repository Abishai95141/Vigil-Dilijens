package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	repoRoot   = "../../.."
	releaseDir = "../../../ontology/releases"
)

// THE IMMUTABILITY GATE (doc 12 §3.1): the LATEST committed release must still
// describe the actual graph. Editing the ontology without cutting a new release
// drifts the content hash and fails this test — a released graph is immutable.
// Older releases are HISTORICAL pins that intentionally no longer match the
// current graph (each names the exact knowledge state it shipped), so only the
// latest is checked against the live graph; every manifest is still validated as
// well-formed. This is the authoritative check (it uses the real
// LoadWithOverlays the runtime uses); graphlint -release mirrors it for the lint.
func TestReleaseImmutability(t *testing.T) {
	manifests := releaseManifests(t)
	if len(manifests) == 0 {
		t.Fatal("no release manifests in ontology/releases — Phase-0b requires a first immutable release (12 M1)")
	}
	// Every manifest must at least be well-formed (parse + sha256 pin shape).
	for _, m := range manifests {
		if _, err := LoadReleaseManifest(m); err != nil {
			t.Errorf("malformed release manifest %s: %v", filepath.Base(m), err)
		}
	}
	// The latest release must IS the current graph.
	path, r, err := LatestRelease(releaseDir)
	if err != nil || r == nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	g, _, err := LoadRelease(path, repoRoot)
	if err != nil {
		t.Fatalf("latest release %s drifted from the graph (edit without a new release?): %v", r.Name, err)
	}
	if g.Release != r.Name || g.Version != r.GraphVersion {
		t.Errorf("latest release mismatch: graph %s/%s vs manifest %s/%s", g.Release, g.Version, r.Name, r.GraphVersion)
	}
}

// The latest release is selectable and is the one a runtime would auto-load.
func TestLatestReleaseLoads(t *testing.T) {
	path, r, err := LatestRelease(releaseDir)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("LatestRelease found no manifest")
	}
	if !strings.HasPrefix(r.Name, "v") || !strings.HasPrefix(r.GraphVersion, "sha256:") {
		t.Errorf("latest release looks malformed: %+v", r)
	}
	g, _, err := LoadRelease(path, repoRoot)
	if err != nil {
		t.Fatalf("latest release must load: %v", err)
	}
	// A real graph behind the pin (sanity: the reference KG has hundreds of nodes).
	if g.NodeCount() < 100 {
		t.Errorf("release %s loaded a suspiciously small graph (%d nodes)", r.Name, g.NodeCount())
	}
}

// VerifyRelease rejects a graph whose hash differs from the manifest pin — the
// immutability violation, surfaced loudly, never silently accepted.
func TestVerifyReleaseRejectsDrift(t *testing.T) {
	g := &Graph{Version: "sha256:actualXYZ"}
	err := g.VerifyRelease(&Release{Name: "v9.9.9", GraphVersion: "sha256:declaredABC"})
	if err == nil || !strings.Contains(err.Error(), "IMMUTABLE") {
		t.Errorf("a drifted graph must be rejected loudly, got %v", err)
	}
	// A matching pin verifies.
	g.Version = "sha256:declaredABC"
	if err := g.VerifyRelease(&Release{Name: "v9.9.9", GraphVersion: "sha256:declaredABC"}); err != nil {
		t.Errorf("a matching graph must verify, got %v", err)
	}
}

func TestLoadReleaseManifestValidates(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("release: v1\ngraph_version: not-a-hash\nbase: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReleaseManifest(bad); err == nil {
		t.Error("a non-sha256 graph_version must be rejected")
	}
}

func releaseManifests(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			out = append(out, filepath.Join(releaseDir, e.Name()))
		}
	}
	return out
}
