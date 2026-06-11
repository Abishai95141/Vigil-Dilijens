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
func NewCapture(dir string, cfg qss.WarmConfig) (*Capture, error) {
	warm, err := qss.OpenWarm(filepath.Join(dir, segmentsDir), cfg)
	if err != nil {
		return nil, err
	}
	return &Capture{dir: dir, warm: warm}, nil
}

// Warm exposes the warm store (the ingest tap target and recovery report).
func (c *Capture) Warm() *qss.WarmStore { return c.warm }

// WriteManifest writes manifest.json (once, at capture start).
func (c *Capture) WriteManifest(m Manifest) error {
	m.BundleVersion = bundleV1
	return writeJSON(filepath.Join(c.dir, manifestName), m)
}

// SetBars records the resolved-bar set in force from now on. It writes a new
// bars-<epoch>.json only when the set actually changed (content-hashed), so the
// periodic re-bind of an unchanged cluster does not bloat the bundle. Returns
// the epoch ticks should reference.
func (c *Capture) SetBars(at time.Time, bindings []binding.Binding) (int, error) {
	raw, err := json.Marshal(bindings)
	if err != nil {
		return 0, fmt.Errorf("replay: marshal bars: %w", err)
	}
	sum := sha256.Sum256(raw)
	h := hex.EncodeToString(sum[:])
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

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	return nil
}
