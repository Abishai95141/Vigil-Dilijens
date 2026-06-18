package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
)

// Scale characterization for the durable findings store (audit roadmap #6). The store
// is deliberately single-writer (SetMaxOpenConns(1), CGO-free modernc.org/sqlite), so
// the per-tick persistence cost is the relevant envelope: how long it takes to upsert
// a tick's worth of findings to disk. This is OFF the deterministic detection path
// (non-gating), but it bounds how many findings a tick can durably record without the
// surfacing back end falling behind.
//
//	go test -bench=BenchmarkUpsertFindings -benchmem -run='^$' ./obsd/internal/store/

func benchFindings(n int) []detect.Finding {
	out := make([]detect.Finding, 0, n)
	for i := 0; i < n; i++ {
		ns := fmt.Sprintf("ns-%d", i%20)
		out = append(out, detect.Finding{
			EntityCEI:     fmt.Sprintf("i|cl|%s|Pod|pod-%d|uid-%d", ns, i, i),
			Phenomenon:    "PHEN_MEMORY_LEAK",
			GraphVersion:  "sha256:bench",
			Label:         "Memory leak",
			Namespace:     ns,
			Name:          fmt.Sprintf("pod-%d", i),
			Kind:          "Container",
			Quality:       detect.QualityDegraded,
			Completeness:  0.5,
			RequiredTotal: 2, RequiredMet: 1, RequiredUnobserved: 1,
		})
	}
	return out
}

// BenchmarkUpsertFindings measures one tick's write cost (insert then steady-state
// upsert) across a growing finding count, to a real on-disk SQLite file.
func BenchmarkUpsertFindings(b *testing.B) {
	at := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	for _, n := range []int{10, 100, 1000} {
		fs := benchFindings(n)
		b.Run(fmt.Sprintf("findings=%d", n), func(b *testing.B) {
			s, err := Open(filepath.Join(b.TempDir(), "bench.db"))
			if err != nil {
				b.Fatalf("open store: %v", err)
			}
			defer s.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.UpsertFindings(at, fs); err != nil {
					b.Fatalf("upsert: %v", err)
				}
			}
			b.ReportMetric(float64(n), "findings/tick")
		})
	}
}
