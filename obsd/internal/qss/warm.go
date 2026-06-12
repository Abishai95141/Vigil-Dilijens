package qss

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The warm tier (doc 14 §2.3): ring contents flushed into append-only 2 h segment
// files; sealed segments are immutable; crash recovery truncates the unsealed
// segment's torn tail; retention deletes sealed segments older than 7 d; fsync is
// batched (losing ≤ the batch interval of telemetry on crash is the accepted
// policy). The store stores and retrieves — it never interprets (doc 05 §3.1):
// tick frames carry an opaque payload written and read by the replay layer.
//
// The segment log is the READINGS component of the replay bundle (doc 11 §3.1).
// It preserves the exact ARRIVAL order of samples interleaved with evaluation
// ticks, which is what makes byte-identical replay possible: re-appending the log
// in order reconstructs the hot rings exactly as live evaluation saw them.
//
// Format (all little-endian; CRC32-Castagnoli over the frame minus its crc):
//
//	file   := magic "VWS1" frame*
//	def    := 0x01 idx:u32 len:u32 json(StreamDef) crc:u32   (once per stream per segment)
//	sample := 0x02 idx:u32 recv:i64ns at:i64ns value:f64bits crc:u32  (fixed 33 B)
//	tick   := 0x03 recv:i64ns len:u32 payload crc:u32        (opaque; replay layer owns it)
//
// The doc's "plain fixed-width 16-byte record (ts 8 B + value 8 B)" is the sample
// payload; multiplexing many streams into one file adds the stream index, and the
// receive stamp (the audit time, doc 14 A12) makes bundles time-shiftable (doc 11).
// Gorilla/delta compression remains a later milestone, not a prerequisite.
//
// Sizing (dev cluster, ~5.8 k series at 15 s): 33 B × 5 800 × 4/min × 120 min
// ≈ 92 MB per segment, ≈ 7.7 GB per 7 d — acceptable uncompressed; retention is a
// parameter.

const (
	warmMagic = "VWS1"

	FrameDef    byte = 0x01
	FrameSample byte = 0x02
	FrameTick   byte = 0x03
	// FrameRunStart marks a process-run boundary: written once per store open,
	// in the first segment the run touches. Live evaluation restarts with EMPTY
	// rings; replay must reset its reconstructed state at the same point or it
	// would evaluate pre-restart samples live evaluation never saw.
	FrameRunStart byte = 0x04

	// Reader/writer frame-size bounds (enforced symmetrically: the writer
	// refuses what the reader would reject as corrupt).
	maxDefPayload  = 1 << 20
	maxTickPayload = 1 << 24

	sealedExt = ".vseg"
	activeExt = ".active"
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// StreamDef describes one stream inside a segment — everything replay needs to
// rebuild the read-side view (the (UID, metric) join index and the exposition
// type) without the live cluster.
type StreamDef struct {
	ID      string `json:"id"`      // the ring key (CEI key + "|" + metric)
	CEIKey  string `json:"cei"`     // canonical entity identity
	UID     string `json:"uid"`     // join key into bindings (podUID/container for containers)
	Kind    string `json:"kind"`    // Container | Pod | Node
	Metric  string `json:"metric"`  // canonical variable
	Type    string `json:"type"`    // gauge | counter | untyped
	Node    string `json:"node"`    // scrape source
	Cadence string `json:"cadence"` // "scrape"
}

// WarmConfig carries the doc 14 §2.3 store constants (from the params file).
type WarmConfig struct {
	SegmentDuration time.Duration // 2 h
	Retention       time.Duration // 7 d
	FsyncBatch      time.Duration // 1 s; <= 0 disables the background syncer (tests sync explicitly)
}

// WarmStore is the on-disk warm tier writer. Safe for concurrent use (the scrape
// goroutine appends samples while the evaluation goroutine appends ticks).
type WarmStore struct {
	dir string
	cfg WarmConfig

	mu       sync.Mutex
	f        *os.File
	w        *bufio.Writer
	segStart time.Time
	lastRecv time.Time
	idx      map[string]uint32 // streamID -> index in the CURRENT segment
	nextIdx  uint32
	dirty    bool
	closed   bool  // set by Close; later appends are refused, never silently re-opened
	err      error // latched first write error; appends after it are refused loudly

	// runStartPending: the next segment this run opens must begin with a
	// run-start frame (process-restart boundary for replay).
	runStartPending bool

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	// Recovered reports crash recovery performed at open: segments sealed from a
	// leftover .active file and bytes truncated from a torn tail. Stated, never silent.
	Recovered struct {
		Segments       int
		TruncatedBytes int64
	}
}

// OpenWarm opens (creating if needed) the warm tier in dir. Any leftover .active
// segment from a crash is recovered: scanned frame-by-frame, truncated at the
// first torn/corrupt frame, and sealed. The next segment starts fresh.
func OpenWarm(dir string, cfg WarmConfig) (*WarmStore, error) {
	if cfg.SegmentDuration <= 0 {
		return nil, errors.New("warm: segment duration must be positive")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("warm: %w", err)
	}
	w := &WarmStore{
		dir: dir, cfg: cfg, idx: map[string]uint32{},
		runStartPending: true,
		stop:            make(chan struct{}), done: make(chan struct{}),
	}
	if err := w.recoverActive(); err != nil {
		return nil, err
	}
	go w.fsyncLoop()
	return w, nil
}

// recoverActive seals any .active file left by a crash, truncating a torn tail.
func (w *WarmStore) recoverActive() error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return fmt.Errorf("warm: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), activeExt) {
			continue
		}
		// Only files this store wrote (seg-<startMs>.active) are recovered. A
		// foreign .active file is left untouched — recovery must never delete or
		// rename data it cannot prove is its own.
		start, perr := segStartFromName(e.Name())
		if perr != nil {
			continue
		}
		path := filepath.Join(w.dir, e.Name())
		valid, lastRecv, scanErr := scanValid(path)
		if scanErr != nil {
			return fmt.Errorf("warm: recover %s: %w", e.Name(), scanErr)
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("warm: %w", err)
		}
		if valid <= int64(len(warmMagic)) {
			// Nothing usable in it (header only, or torn before the first frame).
			w.Recovered.TruncatedBytes += info.Size()
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("warm: %w", err)
			}
			continue
		}
		if info.Size() > valid {
			w.Recovered.TruncatedBytes += info.Size() - valid
			if err := os.Truncate(path, valid); err != nil {
				return fmt.Errorf("warm: %w", err)
			}
		}
		end := lastRecv
		if end.Before(start) {
			end = start
		}
		if err := sealRename(path, filepath.Join(w.dir, sealedName(start, end))); err != nil {
			return err
		}
		w.Recovered.Segments++
	}
	return nil
}

// Append records one sample (and, on its first appearance in the current
// segment, the stream's definition frame). recv is the receive/audit stamp that
// also drives segment rollover; it must be UTC and non-decreasing in the large
// (small regressions are tolerated and stay in the current segment).
func (w *WarmStore) Append(def StreamDef, recv time.Time, s Sample) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureSegment(recv); err != nil {
		return err
	}
	id, ok := w.idx[def.ID]
	if !ok {
		id = w.nextIdx
		w.nextIdx++
		w.idx[def.ID] = id
		payload, err := json.Marshal(def)
		if err != nil {
			return w.latch(fmt.Errorf("warm: marshal def: %w", err))
		}
		if len(payload) > maxDefPayload {
			// Enforced symmetrically with the reader: never write a frame the
			// reader would reject as corrupt.
			return w.latch(fmt.Errorf("warm: stream def %q exceeds the frame bound (%d bytes)", def.ID, len(payload)))
		}
		buf := make([]byte, 0, 9+len(payload)+4)
		buf = append(buf, FrameDef)
		buf = binary.LittleEndian.AppendUint32(buf, id)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(payload)))
		buf = append(buf, payload...)
		buf = binary.LittleEndian.AppendUint32(buf, crc32.Checksum(buf, castagnoli))
		if _, err := w.w.Write(buf); err != nil {
			return w.latch(fmt.Errorf("warm: write def: %w", err))
		}
	}
	var buf [33]byte
	buf[0] = FrameSample
	binary.LittleEndian.PutUint32(buf[1:5], id)
	binary.LittleEndian.PutUint64(buf[5:13], uint64(recv.UnixNano()))
	binary.LittleEndian.PutUint64(buf[13:21], uint64(s.At.UnixNano()))
	binary.LittleEndian.PutUint64(buf[21:29], math.Float64bits(s.Value))
	binary.LittleEndian.PutUint32(buf[29:33], crc32.Checksum(buf[:29], castagnoli))
	if _, err := w.w.Write(buf[:]); err != nil {
		return w.latch(fmt.Errorf("warm: write sample: %w", err))
	}
	w.dirty = true
	if recv.After(w.lastRecv) {
		w.lastRecv = recv
	}
	return nil
}

// Tick records one evaluation-tick frame with an opaque payload (the replay
// layer's record of eval time, bars epoch, and result digest — this store does
// not interpret it, doc 05 §3.1).
func (w *WarmStore) Tick(recv time.Time, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureSegment(recv); err != nil {
		return err
	}
	if len(payload) > maxTickPayload {
		return w.latch(fmt.Errorf("warm: tick payload exceeds the frame bound (%d bytes)", len(payload)))
	}
	buf := make([]byte, 0, 13+len(payload)+4)
	buf = append(buf, FrameTick)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(recv.UnixNano()))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(payload)))
	buf = append(buf, payload...)
	buf = binary.LittleEndian.AppendUint32(buf, crc32.Checksum(buf, castagnoli))
	if _, err := w.w.Write(buf); err != nil {
		return w.latch(fmt.Errorf("warm: write tick: %w", err))
	}
	w.dirty = true
	if recv.After(w.lastRecv) {
		w.lastRecv = recv
	}
	return nil
}

// ensureSegment opens the first segment or rolls into a new one when recv has
// passed the current segment's window. Windows are wall-clock aligned
// (start = recv truncated to the segment duration).
func (w *WarmStore) ensureSegment(recv time.Time) error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		// A straggler append after Close (e.g. an in-flight scrape cycle finishing
		// during shutdown) must not silently re-open a fresh segment — the bundle
		// is sealed; the refusal is surfaced by the tap's error log.
		return errors.New("warm: store closed")
	}
	if w.f != nil && recv.Sub(w.segStart) < w.cfg.SegmentDuration {
		return nil
	}
	if w.f != nil {
		if err := w.sealLocked(); err != nil {
			return err
		}
	}
	start := recv.UTC().Truncate(w.cfg.SegmentDuration)
	path := filepath.Join(w.dir, fmt.Sprintf("seg-%d%s", start.UnixMilli(), activeExt))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return w.latch(fmt.Errorf("warm: open segment: %w", err))
	}
	w.f, w.w, w.segStart = f, bufio.NewWriterSize(f, 1<<16), start
	w.lastRecv = recv.UTC()
	w.idx = map[string]uint32{}
	w.nextIdx = 0
	if _, err := w.w.WriteString(warmMagic); err != nil {
		return w.latch(fmt.Errorf("warm: write header: %w", err))
	}
	if w.runStartPending {
		// The run boundary: replay resets its reconstructed rings here, mirroring
		// the empty rings this process started with.
		buf := make([]byte, 0, 13)
		buf = append(buf, FrameRunStart)
		buf = binary.LittleEndian.AppendUint64(buf, uint64(recv.UTC().UnixNano()))
		buf = binary.LittleEndian.AppendUint32(buf, crc32.Checksum(buf, castagnoli))
		if _, err := w.w.Write(buf); err != nil {
			return w.latch(fmt.Errorf("warm: write run-start: %w", err))
		}
		w.runStartPending = false
	}
	return nil
}

// sealLocked flushes, syncs, closes, and renames the active segment to its
// immutable sealed name, then applies retention. Caller holds mu.
func (w *WarmStore) sealLocked() error {
	if w.f == nil {
		return nil
	}
	if err := w.w.Flush(); err != nil {
		return w.latch(fmt.Errorf("warm: flush: %w", err))
	}
	if err := w.f.Sync(); err != nil {
		return w.latch(fmt.Errorf("warm: sync: %w", err))
	}
	name := w.f.Name()
	if err := w.f.Close(); err != nil {
		return w.latch(fmt.Errorf("warm: close: %w", err))
	}
	end := w.lastRecv
	if end.Before(w.segStart) {
		end = w.segStart
	}
	if err := sealRename(name, filepath.Join(w.dir, sealedName(w.segStart, end))); err != nil {
		return w.latch(err)
	}
	w.f, w.w = nil, nil
	w.dirty = false
	w.applyRetention(end)
	return nil
}

// sealRename moves a segment to its immutable sealed name, REFUSING to replace
// an existing sealed segment ("sealed segments are immutable" is the warm
// tier's whole guarantee — a same-(start,end) collision after a restart into
// the same window must fail loudly, never silently destroy recorded readings).
// link(2) fails with EEXIST instead of replacing, atomically, on macOS/Linux.
func sealRename(old, dst string) error {
	if err := os.Link(old, dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("warm: sealed segment %s already exists; refusing to clobber it (unsealed data left at %s)",
				filepath.Base(dst), filepath.Base(old))
		}
		return fmt.Errorf("warm: seal: %w", err)
	}
	if err := os.Remove(old); err != nil {
		return fmt.Errorf("warm: seal: %w", err)
	}
	return nil
}

// applyRetention deletes sealed segments whose END is older than the retention
// window, anchored on the just-sealed segment's end (no wall clock in logic).
func (w *WarmStore) applyRetention(now time.Time) {
	if w.cfg.Retention <= 0 {
		return
	}
	segs, err := ListSegments(w.dir)
	if err != nil {
		return // retention is best-effort; the next seal retries
	}
	cutoff := now.Add(-w.cfg.Retention)
	for _, s := range segs {
		if s.Sealed && s.End.Before(cutoff) {
			_ = os.Remove(s.Path)
		}
	}
}

// Sync flushes buffered frames and fsyncs the active segment (the batched
// durability point, doc 14 §2.3).
func (w *WarmStore) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil || !w.dirty {
		return w.err
	}
	if err := w.w.Flush(); err != nil {
		return w.latch(fmt.Errorf("warm: flush: %w", err))
	}
	if err := w.f.Sync(); err != nil {
		return w.latch(fmt.Errorf("warm: sync: %w", err))
	}
	w.dirty = false
	return nil
}

// Close seals the active segment and stops the background syncer. Appends after
// Close are refused (never a silent re-open). Idempotent: a second Close is a
// no-op, never a panic.
func (w *WarmStore) Close() error {
	w.closeOnce.Do(func() {
		close(w.stop)
	})
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.err
	}
	w.closed = true
	return w.sealLocked()
}

func (w *WarmStore) fsyncLoop() {
	defer close(w.done)
	if w.cfg.FsyncBatch <= 0 {
		<-w.stop
		return
	}
	t := time.NewTicker(w.cfg.FsyncBatch)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			_ = w.Sync()
		}
	}
}

// latch records the first write error; every later operation returns it. A warm
// tier that silently keeps "succeeding" after a disk failure would be a silent
// gap in the replay record — refusing loudly is the honest behaviour.
func (w *WarmStore) latch(err error) error {
	if w.err == nil {
		w.err = err
	}
	return w.err
}

// Err returns the latched write error, if any.
func (w *WarmStore) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// ---- Reading ----------------------------------------------------------------

// SegmentFile names one on-disk segment.
type SegmentFile struct {
	Path   string
	Start  time.Time
	End    time.Time // zero for an unsealed (.active) segment
	Sealed bool
}

// ListSegments enumerates the segments in dir, sorted by start time (sealed and
// active alike — the caller decides whether an unsealed tail is usable).
func ListSegments(dir string) ([]SegmentFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("warm: %w", err)
	}
	var out []SegmentFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, sealedExt):
			base := strings.TrimSuffix(strings.TrimPrefix(name, "seg-"), sealedExt)
			parts := strings.SplitN(base, "-", 2)
			if len(parts) != 2 {
				continue
			}
			startMs, err1 := strconv.ParseInt(parts[0], 10, 64)
			endMs, err2 := strconv.ParseInt(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				continue
			}
			out = append(out, SegmentFile{
				Path:   filepath.Join(dir, name),
				Start:  time.UnixMilli(startMs).UTC(),
				End:    time.UnixMilli(endMs).UTC(),
				Sealed: true,
			})
		case strings.HasSuffix(name, activeExt):
			start, err := segStartFromName(name)
			if err != nil {
				continue
			}
			out = append(out, SegmentFile{Path: filepath.Join(dir, name), Start: start})
		}
	}
	// Total order: two sealed segments CAN share a start (a crash-recovered seal
	// plus a post-restart seal in the same 2h window); arrival order between them
	// is end-then-path, never the non-stable sort's whim.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Start.Equal(b.Start) {
			return a.Start.Before(b.Start)
		}
		if !a.End.Equal(b.End) {
			return a.End.Before(b.End)
		}
		return a.Path < b.Path
	})
	return out, nil
}

// Frame is one decoded warm-segment frame.
type Frame struct {
	Kind    byte
	Def     StreamDef // FrameDef
	Idx     uint32    // FrameDef, FrameSample
	Recv    time.Time // FrameSample, FrameTick (UTC)
	At      time.Time // FrameSample (UTC)
	Value   float64   // FrameSample
	Payload []byte    // FrameTick (opaque)
}

// ReplayFrames streams a SEALED segment's frames in order. Sealed segments are
// immutable, so any corruption here is real and surfaced as an error — only the
// open-time recovery of an unsealed segment may truncate.
func ReplayFrames(path string, fn func(Frame) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("warm: %w", err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	magic := make([]byte, len(warmMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != warmMagic {
		return fmt.Errorf("warm: %s: bad segment header", filepath.Base(path))
	}
	for {
		fr, _, err := readFrame(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("warm: %s: %w", filepath.Base(path), err)
		}
		if err := fn(fr); err != nil {
			return err
		}
	}
}

// scanValid reads frames from a (possibly torn) segment, returning the byte
// offset of the end of the last fully-valid frame and the newest recv stamp seen.
func scanValid(path string) (validBytes int64, lastRecv time.Time, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	magic := make([]byte, len(warmMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != warmMagic {
		return 0, time.Time{}, nil // unusable from the start
	}
	valid := int64(len(warmMagic))
	for {
		fr, n, err := readFrame(r)
		if err != nil {
			return valid, lastRecv, nil // torn/corrupt tail: stop at the last good frame
		}
		valid += n
		if fr.Recv.After(lastRecv) {
			lastRecv = fr.Recv
		}
	}
}

// readFrame decodes one frame, verifying its CRC. Returns io.EOF cleanly at end
// of stream; any other error means a torn or corrupt frame — a transient I/O
// error must NOT read as a clean end (a silently shortened sealed segment would
// replay confidently over missing data).
func readFrame(r *bufio.Reader) (Frame, int64, error) {
	t, err := r.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Frame{}, 0, io.EOF
		}
		return Frame{}, 0, fmt.Errorf("read frame type: %w", err)
	}
	switch t {
	case FrameRunStart:
		rest := make([]byte, 12)
		if _, err := io.ReadFull(r, rest); err != nil {
			return Frame{}, 0, fmt.Errorf("torn run-start frame: %w", err)
		}
		whole := append([]byte{t}, rest[:8]...)
		if crc32.Checksum(whole, castagnoli) != binary.LittleEndian.Uint32(rest[8:12]) {
			return Frame{}, 0, errors.New("run-start frame: crc mismatch")
		}
		return Frame{
			Kind: FrameRunStart,
			Recv: time.Unix(0, int64(binary.LittleEndian.Uint64(rest[0:8]))).UTC(),
		}, 13, nil
	case FrameDef:
		head := make([]byte, 8)
		if _, err := io.ReadFull(r, head); err != nil {
			return Frame{}, 0, fmt.Errorf("torn def frame: %w", err)
		}
		n := binary.LittleEndian.Uint32(head[4:8])
		if n > maxDefPayload {
			return Frame{}, 0, errors.New("def frame: implausible length (corrupt)")
		}
		rest := make([]byte, int(n)+4)
		if _, err := io.ReadFull(r, rest); err != nil {
			return Frame{}, 0, fmt.Errorf("torn def frame: %w", err)
		}
		whole := append(append([]byte{t}, head...), rest[:n]...)
		want := binary.LittleEndian.Uint32(rest[n:])
		if crc32.Checksum(whole, castagnoli) != want {
			return Frame{}, 0, errors.New("def frame: crc mismatch")
		}
		var def StreamDef
		if err := json.Unmarshal(rest[:n], &def); err != nil {
			return Frame{}, 0, fmt.Errorf("def frame: %w", err)
		}
		return Frame{Kind: FrameDef, Def: def, Idx: binary.LittleEndian.Uint32(head[0:4])},
			int64(1 + 8 + int(n) + 4), nil
	case FrameSample:
		rest := make([]byte, 32)
		if _, err := io.ReadFull(r, rest); err != nil {
			return Frame{}, 0, fmt.Errorf("torn sample frame: %w", err)
		}
		whole := append([]byte{t}, rest[:28]...)
		want := binary.LittleEndian.Uint32(rest[28:32])
		if crc32.Checksum(whole, castagnoli) != want {
			return Frame{}, 0, errors.New("sample frame: crc mismatch")
		}
		return Frame{
			Kind:  FrameSample,
			Idx:   binary.LittleEndian.Uint32(rest[0:4]),
			Recv:  time.Unix(0, int64(binary.LittleEndian.Uint64(rest[4:12]))).UTC(),
			At:    time.Unix(0, int64(binary.LittleEndian.Uint64(rest[12:20]))).UTC(),
			Value: math.Float64frombits(binary.LittleEndian.Uint64(rest[20:28])),
		}, 33, nil
	case FrameTick:
		head := make([]byte, 12)
		if _, err := io.ReadFull(r, head); err != nil {
			return Frame{}, 0, fmt.Errorf("torn tick frame: %w", err)
		}
		n := binary.LittleEndian.Uint32(head[8:12])
		if n > maxTickPayload {
			return Frame{}, 0, errors.New("tick frame: implausible length (corrupt)")
		}
		rest := make([]byte, int(n)+4)
		if _, err := io.ReadFull(r, rest); err != nil {
			return Frame{}, 0, fmt.Errorf("torn tick frame: %w", err)
		}
		whole := append(append([]byte{t}, head...), rest[:n]...)
		want := binary.LittleEndian.Uint32(rest[n:])
		if crc32.Checksum(whole, castagnoli) != want {
			return Frame{}, 0, errors.New("tick frame: crc mismatch")
		}
		payload := make([]byte, n)
		copy(payload, rest[:n])
		return Frame{
			Kind:    FrameTick,
			Recv:    time.Unix(0, int64(binary.LittleEndian.Uint64(head[0:8]))).UTC(),
			Payload: payload,
		}, int64(1 + 12 + int(n) + 4), nil
	default:
		return Frame{}, 0, fmt.Errorf("unknown frame type 0x%02x (corrupt)", t)
	}
}

func sealedName(start, end time.Time) string {
	return fmt.Sprintf("seg-%d-%d%s", start.UnixMilli(), end.UnixMilli(), sealedExt)
}

func segStartFromName(name string) (time.Time, error) {
	base := strings.TrimSuffix(strings.TrimPrefix(name, "seg-"), activeExt)
	ms, err := strconv.ParseInt(base, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("unparseable segment name %q", name)
	}
	return time.UnixMilli(ms).UTC(), nil
}
