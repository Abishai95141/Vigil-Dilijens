package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// release.go gives graphlint the release-engineering surface (doc 12 M1): print
// the content-hash pin for a new release, and verify a committed release manifest
// still matches the graph (immutability). graphlint cannot import obsd/internal
// (Go's internal rule), so the hash is recomputed here with the SAME algorithm as
// graph.LoadWithOverlays: sha256(base ‖ 0x00 ‖ overlay₁ ‖ 0x00 ‖ overlay₂ …) over
// overlays in sorted-path order. The runtime's release_test.go asserts the two
// agree, so a drift between this and the loader is caught in CI.

type releaseManifest struct {
	Name         string   `yaml:"release"`
	GraphVersion string   `yaml:"graph_version"`
	Created      string   `yaml:"created"`
	Base         string   `yaml:"base"`
	Overlays     []string `yaml:"overlays"`
}

// latestManifestPath returns the newest release manifest in dir by ISO `created`
// date (lexically correct, immune to the vX.10/vX.2 name trap), or "" if none.
// Mirrors graph.LatestRelease so the lint gate and the runtime agree on "latest".
func latestManifestPath(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	best, bestKey := "", ""
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r releaseManifest
		if yaml.Unmarshal(raw, &r) != nil || r.Name == "" {
			continue
		}
		key := r.Created + "\x00" + r.Name
		if key > bestKey {
			best, bestKey = p, key
		}
	}
	return best, nil
}

// computeGraphVersion recomputes the content-hash pin for (base + overlays).
func computeGraphVersion(kgPath, overlayDir string) (string, error) {
	raw, err := os.ReadFile(kgPath)
	if err != nil {
		return "", fmt.Errorf("read base graph %q: %w", kgPath, err)
	}
	var paths []string
	if overlayDir != "" {
		entries, err := os.ReadDir(overlayDir)
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("read overlay dir %q: %w", overlayDir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if ext := filepath.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
				paths = append(paths, filepath.Join(overlayDir, e.Name()))
			}
		}
		sort.Strings(paths)
	}
	if len(paths) == 0 {
		sum := sha256.Sum256(raw)
		return "sha256:" + hex.EncodeToString(sum[:]), nil
	}
	h := sha256.New()
	h.Write(raw)
	for _, p := range paths {
		oraw, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("read overlay %q: %w", p, err)
		}
		h.Write([]byte{0})
		h.Write(oraw)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// verifyRelease loads a release manifest and checks the live graph still hashes
// to the declared pin. Returns nil on match; a loud error on drift.
func verifyRelease(manifestPath, kgPath, overlayDir string) (*releaseManifest, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read release manifest %q: %w", manifestPath, err)
	}
	var r releaseManifest
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse release manifest %q: %w", manifestPath, err)
	}
	got, err := computeGraphVersion(kgPath, overlayDir)
	if err != nil {
		return &r, err
	}
	if got != r.GraphVersion {
		return &r, fmt.Errorf("release %s DRIFTED: manifest pins %s, graph now hashes to %s — a released graph is immutable; cut a new release",
			r.Name, r.GraphVersion, got)
	}
	return &r, nil
}
