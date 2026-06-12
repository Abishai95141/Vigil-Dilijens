package forecast

import "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"

// SnapshotReader is a frozen copy of exactly the series a cycle's targets
// need. The live store gate only has to be held while Snapshot copies the
// rings (microseconds); the clock's long RPCs then run lock-free against the
// copy — the warm path never stalls ingest (the non-gating mandate applied to
// the scrape loop, not just the tick).
type SnapshotReader struct {
	streams map[string][]string // uid \x1f metric -> ids
	samples map[string][]qss.Sample
	types   map[string]string
}

var _ StreamReader = (*SnapshotReader)(nil)

// Snapshot copies the per-target series out of r. Call under the store
// gate's read side; the result is immutable.
func Snapshot(r StreamReader, targets []Target, n int) *SnapshotReader {
	s := &SnapshotReader{
		streams: map[string][]string{},
		samples: map[string][]qss.Sample{},
		types:   map[string]string{},
	}
	for _, t := range targets {
		key := t.StreamUID + "\x1f" + t.Metric
		if _, done := s.streams[key]; done {
			continue
		}
		ids := r.StreamsFor(t.StreamUID, t.Metric)
		s.streams[key] = append([]string{}, ids...)
		for _, id := range ids {
			if _, done := s.samples[id]; done {
				continue
			}
			s.samples[id] = append([]qss.Sample{}, r.LastN(id, n)...)
			if ty, ok := r.StreamType(id); ok {
				s.types[id] = ty
			}
		}
	}
	return s
}

func (s *SnapshotReader) StreamsFor(uid, metric string) []string {
	return s.streams[uid+"\x1f"+metric]
}

func (s *SnapshotReader) LastN(id string, n int) []qss.Sample {
	out := s.samples[id]
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func (s *SnapshotReader) StreamType(id string) (string, bool) {
	t, ok := s.types[id]
	return t, ok
}
