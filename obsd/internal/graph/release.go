package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Release is an immutable, semantically-versioned graph release (doc 12 §3.1).
// The graph ships as immutable releases; nothing edits a released graph — change
// means a NEW release. The manifest declares the exact content-hash the release
// must equal, so any drift between a released graph and what is actually loaded
// is caught loudly (immutability enforced, not merely asserted). Every bound
// graph, finding, and harness result pins this version (04/07/11).
type Release struct {
	Name         string   `yaml:"release"`       // semantic name, e.g. "v0.1.0"
	GraphVersion string   `yaml:"graph_version"` // the content-hash pin this release MUST equal
	Created      string   `yaml:"created"`       // ISO date
	Author       string   `yaml:"author"`
	Base         string   `yaml:"base"`     // the KG file (relative to repo root)
	Overlays     []string `yaml:"overlays"` // overlay filenames merged into the release
	Summary      string   `yaml:"summary"`
}

// LoadReleaseManifest reads and validates a release manifest.
func LoadReleaseManifest(path string) (*Release, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release manifest %q: %w", path, err)
	}
	var r Release
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse release manifest %q: %w", path, err)
	}
	if r.Name == "" {
		return nil, fmt.Errorf("release manifest %q: missing 'release' name", path)
	}
	if !strings.HasPrefix(r.GraphVersion, "sha256:") {
		return nil, fmt.Errorf("release %q: 'graph_version' must be a sha256: pin, got %q", r.Name, r.GraphVersion)
	}
	if r.Base == "" {
		return nil, fmt.Errorf("release %q: missing 'base' graph path", r.Name)
	}
	return &r, nil
}

// VerifyRelease confirms the loaded graph IS the declared release — its content
// hash equals the pin. A mismatch means a released graph was edited (or the wrong
// base/overlays were loaded): a released graph is immutable, so this is a hard,
// loud failure, never a silent acceptance.
func (g *Graph) VerifyRelease(r *Release) error {
	if g.Version != r.GraphVersion {
		return fmt.Errorf("graph drifted from release %s: manifest pins %s, loaded graph hashes to %s — "+
			"a released graph is IMMUTABLE; cut a new release instead of editing %s",
			r.Name, r.GraphVersion, g.Version, r.Name)
	}
	return nil
}

// LoadRelease loads the graph named by a release manifest and verifies
// immutability. baseDir is the directory the manifest's relative base/overlay
// paths resolve against (the repo root). On success the returned graph carries
// the human release name (g.Release) alongside its content-hash pin.
func LoadRelease(manifestPath, baseDir string) (*Graph, *Release, error) {
	r, err := LoadReleaseManifest(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	overlayDir := ""
	if len(r.Overlays) > 0 {
		// Overlays are named relative to the base graph's directory.
		overlayDir = filepath.Join(baseDir, filepath.Dir(r.Base), "overlays")
	}
	g, err := LoadWithOverlays(filepath.Join(baseDir, r.Base), overlayDir)
	if err != nil {
		return nil, nil, err
	}
	if err := g.VerifyRelease(r); err != nil {
		return nil, nil, err
	}
	g.Release = r.Name
	return g, r, nil
}

// IdentifyRelease returns the release NAME whose pin matches the graph's content
// hash, or "" if the loaded graph corresponds to no committed release (a dev
// build). It never fails the load — identity is informational here; the
// immutability GATE is the release_test.go check and graphlint -release.
func IdentifyRelease(g *Graph, releaseDir string) string {
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		r, err := LoadReleaseManifest(filepath.Join(releaseDir, e.Name()))
		if err == nil && r.GraphVersion == g.Version {
			return r.Name
		}
	}
	return ""
}

// LatestRelease returns the newest release manifest in dir (by semantic name,
// then created date), or ("", nil) if the directory holds no manifests. A
// manifest is any *.yaml that parses as a Release.
func LatestRelease(dir string) (string, *Release, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, fmt.Errorf("read release dir %q: %w", dir, err)
	}
	type cand struct {
		path string
		rel  *Release
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		r, err := LoadReleaseManifest(p)
		if err != nil {
			continue // not a release manifest; skip (graphlint validates them)
		}
		cands = append(cands, cand{p, r})
	}
	if len(cands) == 0 {
		return "", nil, nil
	}
	// Sort by the ISO `created` date (lexically correct, and immune to the
	// vX.10 > vX.2 lexical-name trap) — newest first; name as a stable tiebreak.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].rel.Created != cands[j].rel.Created {
			return cands[i].rel.Created > cands[j].rel.Created
		}
		return cands[i].rel.Name > cands[j].rel.Name
	})
	return cands[0].path, cands[0].rel, nil
}
