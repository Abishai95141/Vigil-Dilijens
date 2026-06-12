package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// Manifest pins everything a replay needs to be exact (doc 11 §3.1): the graph
// release, the parameter set, and what the bundle does/doesn't contain.
type Manifest struct {
	BundleVersion int       `json:"bundle_version"` // format version of the bundle layout
	CreatedAt     time.Time `json:"created_at"`
	ClusterID     string    `json:"cluster_id"`
	GraphVersion  string    `json:"graph_version"` // full sha256 pin; replay refuses a mismatch
	ParamsVersion string    `json:"params_version"`
	Profile       string    `json:"profile"`

	// The evaluation constants in force during capture (durations in ns). Replay
	// uses THESE, never the local params file — pinned-version everything.
	FPParams       observe.FPParams `json:"fp_params"`
	ScrapeInterval time.Duration    `json:"scrape_interval"`

	// HotRingCapacity pins the digest-bearing ring constant; the engine refuses
	// a mismatch. ObsdVersion is informational provenance (which build captured).
	HotRingCapacity int    `json:"hot_ring_capacity,omitempty"`
	ObsdVersion     string `json:"obsd_version,omitempty"`

	// Contents declares what this bundle carries — honest partial coverage.
	Contents []string `json:"contents"`
	// Absent names replay inputs NOT captured yet (stated, never implied).
	Absent []string `json:"absent"`
}

// BarsFile is one binding epoch's resolved-bar set (doc 11's "resolved bars").
type BarsFile struct {
	Epoch     int               `json:"epoch"`
	WrittenAt time.Time         `json:"written_at"`
	Bindings  []binding.Binding `json:"bindings"`
}

const (
	manifestName = "manifest.json"
	segmentsDir  = "segments"
	bundleV1     = 1
)

// Capture is the bundle writer: it owns the bundle directory, the warm store
// inside it, and the bars-epoch bookkeeping. Methods are safe for concurrent
// use; sample appends come from the scrape goroutine and ticks from the
// evaluation goroutine.
type Capture struct {
	dir  string
	warm *qss.WarmStore

	mu       sync.Mutex
	epoch    int
	barsHash string
}

// NewCapture opens (creating if needed) a bundle at dir with the given store
// constants. The warm store lives under dir/segments.
//
// Re-opening an existing bundle (a restart into the same --store-dir) CONTINUES
// it: the bars-epoch counter resumes past the highest existing epoch and its
// hash is re-seeded from the newest bars file, so prior epochs are never
// overwritten and an unchanged bar set mints no new epoch. Stale .tmp leftovers
// from a crashed atomic write are removed (they were never part of the bundle).
func NewCapture(dir string, cfg qss.WarmConfig) (*Capture, error) {
	warm, err := qss.OpenWarm(filepath.Join(dir, segmentsDir), cfg)
	if err != nil {
		return nil, err
	}
	c := &Capture{dir: dir, warm: warm}
	if tmps, err := filepath.Glob(filepath.Join(dir, "*.json.tmp")); err == nil {
		for _, t := range tmps {
			_ = os.Remove(t)
		}
	}
	epochs, err := filepath.Glob(filepath.Join(dir, "bars-*.json"))
	if err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	for _, p := range epochs {
		var n int
		if _, err := fmt.Sscanf(filepath.Base(p), "bars-%d.json", &n); err == nil && n > c.epoch {
			c.epoch = n
		}
	}
	if c.epoch > 0 {
		raw, err := os.ReadFile(filepath.Join(dir, barsName(c.epoch)))
		if err != nil {
			return nil, fmt.Errorf("replay: continue bundle: %w", err)
		}
		var bf BarsFile
		if err := json.Unmarshal(raw, &bf); err != nil {
			return nil, fmt.Errorf("replay: continue bundle: bars-%d: %w", c.epoch, err)
		}
		h, err := barsHash(bf.Bindings)
		if err != nil {
			return nil, err
		}
		c.barsHash = h
	}
	return c, nil
}

// Warm exposes the warm store (the ingest tap target and recovery report).
func (c *Capture) Warm() *qss.WarmStore { return c.warm }

// WriteManifest writes manifest.json at capture start. A continued bundle (the
// file already exists) must be PIN-COMPATIBLE: same graph version and same
// parameter set — a bundle pins exactly one of each (doc 11 §3.1). On a
// mismatch the capture refuses: continuing would silently mix two regimes whose
// recorded ticks cannot all replay against one pin.
func (c *Capture) WriteManifest(m Manifest) error {
	m.BundleVersion = bundleV1
	path := filepath.Join(c.dir, manifestName)
	if raw, err := os.ReadFile(path); err == nil {
		var prev Manifest
		if err := json.Unmarshal(raw, &prev); err != nil {
			return fmt.Errorf("replay: existing manifest unreadable: %w", err)
		}
		if prev.GraphVersion != m.GraphVersion {
			return fmt.Errorf("replay: bundle already pinned to graph %s; refusing to continue it under %s (use a fresh --store-dir)",
				prev.GraphVersion, m.GraphVersion)
		}
		if prev.FPParams != m.FPParams || prev.ScrapeInterval != m.ScrapeInterval ||
			(prev.HotRingCapacity != 0 && prev.HotRingCapacity != m.HotRingCapacity) {
			return fmt.Errorf("replay: bundle already pinned to a different parameter set; refusing to continue it (use a fresh --store-dir)")
		}
		return nil // compatible: keep the original manifest (and its created_at)
	}
	return writeJSON(path, m)
}

// barsHash content-hashes a resolved-bar set with every ResolvedAt zeroed: the
// re-resolution STAMP changes on every periodic recompile, but an unchanged set
// of bars must not mint a new epoch (live evidence: 3 epochs in 3.5 minutes of
// identical bars before this guard).
func barsHash(bindings []binding.Binding) (string, error) {
	cp := make([]binding.Binding, len(bindings))
	copy(cp, bindings)
	for i := range cp {
		if cp[i].Bar != nil {
			bar := *cp[i].Bar
			bar.ResolvedAt = time.Time{}
			cp[i].Bar = &bar
		}
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		return "", fmt.Errorf("replay: marshal bars: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SetBars records the resolved-bar set in force from now on. It writes a new
// bars-<epoch>.json only when the set actually changed (content-hashed with
// re-resolution stamps excluded), so the periodic re-bind of an unchanged
// cluster does not bloat the bundle. Returns the epoch ticks should reference.
func (c *Capture) SetBars(at time.Time, bindings []binding.Binding) (int, error) {
	h, err := barsHash(bindings)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == c.barsHash {
		return c.epoch, nil
	}
	next := c.epoch + 1
	bf := BarsFile{Epoch: next, WrittenAt: at, Bindings: bindings}
	if err := writeJSON(filepath.Join(c.dir, barsName(next)), bf); err != nil {
		return c.epoch, err
	}
	c.epoch, c.barsHash = next, h
	return next, nil
}

// Epoch returns the current bars epoch (0 = no bars recorded yet).
func (c *Capture) Epoch() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

// Tick records one evaluation tick's record into the segment log.
func (c *Capture) Tick(rec TickRecord) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("replay: marshal tick: %w", err)
	}
	return c.warm.Tick(rec.EvalNow, payload)
}

// Close seals the active segment; the bundle is then replayable.
func (c *Capture) Close() error { return c.warm.Close() }

func barsName(epoch int) string { return fmt.Sprintf("bars-%d.json", epoch) }

// writeJSON writes atomically (tmp + rename) and fsyncs before the rename, so a
// bars/manifest file a durable tick frame references cannot be lost to a crash
// while the frame survives (the segment log fsyncs on its own batch).
func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("replay: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("replay: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	return nil
}
