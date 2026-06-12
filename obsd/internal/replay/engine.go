package replay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
)

// Options configures one replay run.
type Options struct {
	BundleDir string
	Graph     *graph.Graph // must match the manifest's pinned version
	OutDir    string       // when set, the canonical JSON of every tick is written here
}

// TickOutcome is one replayed tick's verdict.
type TickOutcome struct {
	EvalNow        time.Time
	BarsEpoch      int
	RecordedDigest string
	ReplayedDigest string
	Match          bool
	Fingerprints   int
	Findings       int
}

// Report is a replay run's honest accounting.
type Report struct {
	Manifest     Manifest
	Segments     int
	Runs         int  // process-run boundaries replayed (restarts into the same bundle)
	UnsealedTail bool // an .active segment existed and was ignored (capture not closed)
	Samples      int64
	Streams      int
	Ticks        []TickOutcome
	Mismatches   int
}

// Run replays a bundle through the real observation and detection components,
// verifying every recorded tick's digest. It never mutates the bundle.
func Run(opts Options) (*Report, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(opts.BundleDir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("replay: read manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("replay: parse manifest: %w", err)
	}
	if m.BundleVersion != bundleV1 {
		return nil, fmt.Errorf("replay: bundle version %d not supported (want %d)", m.BundleVersion, bundleV1)
	}
	if opts.Graph == nil {
		return nil, fmt.Errorf("replay: no graph provided")
	}
	if opts.Graph.Version != m.GraphVersion {
		// Pinned-version everything (doc 11 §3.1): replaying against a different
		// graph would change findings and silently invalidate the comparison.
		return nil, fmt.Errorf("replay: graph version mismatch: bundle pinned %s, loaded %s",
			m.GraphVersion, opts.Graph.Version)
	}
	// Manifest plausibility: a zeroed/garbage parameter set would replay with
	// silently different semantics (zero watermark = everything stale) and the
	// failure would masquerade as a determinism violation. Refuse instead.
	if m.FPParams.ScrapeInterval <= 0 || m.FPParams.RateWindow <= 0 || m.FPParams.Watermark <= 0 ||
		m.FPParams.Band < 0 || m.FPParams.Band >= 1 || m.FPParams.WellAboveFactor <= 0 {
		return nil, fmt.Errorf("replay: manifest parameter set implausible (%+v) — refusing to replay with different semantics", m.FPParams)
	}
	// The hot-ring capacity is a digest-bearing constant (it bounds what a window
	// evaluation can see). A bundle captured under a different capacity cannot
	// replay byte-identically; refuse rather than mis-verify. Zero = an older
	// bundle that predates the pin (accepted; the current capacity applied then too).
	if m.HotRingCapacity != 0 && m.HotRingCapacity != qss.HotCapacity() {
		return nil, fmt.Errorf("replay: bundle captured with hot-ring capacity %d, this binary has %d — cannot replay byte-identically",
			m.HotRingCapacity, qss.HotCapacity())
	}

	rules := make(map[string]*graph.ThresholdRule, len(opts.Graph.Rules))
	for _, r := range opts.Graph.Rules {
		rules[r.ID] = r
	}
	matcher := detect.NewMatcher(opts.Graph)
	reader := newBundleReader()
	rep := &Report{Manifest: m}
	bars := map[int]*barsEpoch{} // epoch -> resolved bars + Tier-A set (lazy, cached)

	segs, err := qss.ListSegments(filepath.Join(opts.BundleDir, segmentsDir))
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("replay: bundle has no segments")
	}
	if opts.OutDir != "" {
		if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
			return nil, fmt.Errorf("replay: %w", err)
		}
	}

	for _, seg := range segs {
		if !seg.Sealed {
			rep.UnsealedTail = true // stated in the report, never silently included
			continue
		}
		rep.Segments++
		idxDef := map[uint32]qss.StreamDef{} // per-segment stream dictionary
		err := qss.ReplayFrames(seg.Path, func(f qss.Frame) error {
			switch f.Kind {
			case qss.FrameRunStart:
				// A process-run boundary: live evaluation restarted with empty
				// rings and a fresh stream registry; the reconstruction must too,
				// or replay would evaluate pre-restart samples live never saw.
				reader.reset()
				rep.Runs++
			case qss.FrameDef:
				idxDef[f.Idx] = f.Def
				reader.register(f.Def)
			case qss.FrameSample:
				def, ok := idxDef[f.Idx]
				if !ok {
					return fmt.Errorf("sample references undefined stream index %d (corrupt segment)", f.Idx)
				}
				reader.hot.Append(def.ID, qss.Sample{At: f.At, Value: f.Value})
				rep.Samples++
			case qss.FrameTick:
				var rec TickRecord
				if err := json.Unmarshal(f.Payload, &rec); err != nil {
					return fmt.Errorf("tick frame: %w", err)
				}
				outcome, err := evalTick(rec, bars, opts, rules, matcher, reader, m)
				if err != nil {
					return err
				}
				rep.Ticks = append(rep.Ticks, outcome)
				if !outcome.Match {
					rep.Mismatches++
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	rep.Streams = len(reader.meta)
	return rep, nil
}

// barsEpoch is one cached resolved-bar set plus the Tier-A selection derived
// from it (selection is a pure function of bars + graph, doc 06).
type barsEpoch struct {
	res      *binding.Result
	selected map[string][]string
}

// evalTick re-runs one recorded evaluation tick: materialize fingerprints from
// the reconstructed rings against the recorded bars epoch, match phenomena over
// the selected (Tier-A) entities, and compare digests.
func evalTick(rec TickRecord, bars map[int]*barsEpoch, opts Options,
	rules map[string]*graph.ThresholdRule, matcher *detect.Matcher, reader *bundleReader, m Manifest) (TickOutcome, error) {

	var fps []observe.Fingerprint
	var findings []detect.Finding
	if rec.BarsEpoch > 0 {
		ep, ok := bars[rec.BarsEpoch]
		if !ok {
			var bf BarsFile
			raw, err := os.ReadFile(filepath.Join(opts.BundleDir, barsName(rec.BarsEpoch)))
			if err != nil {
				return TickOutcome{}, fmt.Errorf("replay: tick references bars epoch %d: %w", rec.BarsEpoch, err)
			}
			if err := json.Unmarshal(raw, &bf); err != nil {
				return TickOutcome{}, fmt.Errorf("replay: bars epoch %d: %w", rec.BarsEpoch, err)
			}
			res := &binding.Result{Bindings: bf.Bindings}
			// The same selection live detection consumes (doc 06): the Tier-A set
			// is a pure function of (bindings, graph), both pinned in the bundle —
			// the funnel replays exactly. (For any phenomenon the matcher can fire,
			// the entity is by construction a participant, so the filter can only
			// drop fingerprints that would have produced no findings —
			// pre-selection bundles replay identically.)
			ep = &barsEpoch{res: res, selected: selection.TierASet(res, opts.Graph)}
			bars[rec.BarsEpoch] = ep
		}
		fps = observe.Materialize(ep.res, rules, reader, m.FPParams, rec.EvalNow)
		for _, fp := range fps {
			if len(ep.selected[fp.CEIKey]) == 0 {
				continue
			}
			findings = append(findings, matcher.MatchFingerprint(fp)...)
		}
	}
	digest, canonical, err := Digest(rec.EvalNow, fps, findings)
	if err != nil {
		return TickOutcome{}, err
	}
	out := TickOutcome{
		EvalNow: rec.EvalNow, BarsEpoch: rec.BarsEpoch,
		RecordedDigest: rec.Digest, ReplayedDigest: digest,
		Match:        digest == rec.Digest,
		Fingerprints: len(fps), Findings: len(findings),
	}
	if opts.OutDir != "" {
		name := fmt.Sprintf("tick-%s.json", rec.EvalNow.UTC().Format("20060102T150405.000000000Z"))
		if err := os.WriteFile(filepath.Join(opts.OutDir, name), append(canonical, '\n'), 0o644); err != nil {
			return TickOutcome{}, fmt.Errorf("replay: %w", err)
		}
	}
	return out, nil
}

// bundleReader is the engine's observe.StreamReader: the same read-side
// semantics as the live Ingestor (sorted (UID, metric) joins, hot-ring reads),
// reconstructed purely from the bundle.
type bundleReader struct {
	hot         *qss.HotStore
	meta        map[string]qss.StreamDef
	byUIDMetric map[string][]string // uid+"\x00"+metric -> sorted streamIDs
}

var _ observe.StreamReader = (*bundleReader)(nil)

func newBundleReader() *bundleReader {
	return &bundleReader{hot: qss.NewHotStore(), meta: map[string]qss.StreamDef{}, byUIDMetric: map[string][]string{}}
}

// reset clears all reconstructed state — a process-run boundary: the live side
// restarted with empty rings and a fresh stream registry.
func (r *bundleReader) reset() {
	r.hot = qss.NewHotStore()
	r.meta = map[string]qss.StreamDef{}
	r.byUIDMetric = map[string][]string{}
}

func (r *bundleReader) register(def qss.StreamDef) {
	if _, seen := r.meta[def.ID]; seen {
		return // defs re-emitted per segment; first registration wins
	}
	r.meta[def.ID] = def
	k := def.UID + "\x00" + def.Metric
	ids := append(r.byUIDMetric[k], def.ID)
	sort.Strings(ids) // mirror Ingestor.StreamsByUIDMetric's sorted contract
	r.byUIDMetric[k] = ids
}

func (r *bundleReader) StreamsFor(uid, metric string) []string {
	return r.byUIDMetric[uid+"\x00"+metric]
}

func (r *bundleReader) Latest(streamID string) (qss.Sample, bool) { return r.hot.Latest(streamID) }

func (r *bundleReader) LastN(streamID string, n int) []qss.Sample { return r.hot.LastN(streamID, n) }

func (r *bundleReader) StreamType(streamID string) (string, bool) {
	m, ok := r.meta[streamID]
	return m.Type, ok
}
