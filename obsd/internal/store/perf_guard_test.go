package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
)

// perf_guard_test.go — a machine-INDEPENDENT allocation guard for the durable findings
// write path (UpsertFindings). It is OFF the deterministic detection path (non-gating), but
// it bounds the per-finding write cost so the surfacing back end can't fall behind.
//
// CRITICAL (the adversarial-review lesson): the scale BENCHMARK uses THIN findings (empty
// Members), so per-finding JSON-marshal bloat in the heavy path is invisible — UpsertFindings
// marshals f.Members + f.Unobservable to JSON columns (findings.go). Production findings carry
// a populated evidence trail. So this guard builds PRODUCTION-SHAPED findings (3 members each)
// and pins allocs-per-finding; a regression that bloats the marshal path (an extra copy, a
// per-member allocation) trips it. Allocations are deterministic, so this is CI-stable.
//
// HONEST LIMIT (same as the detect guard): a constant-factor CPU/IO regression with flat
// allocations is not caught here — `just bench` (BenchmarkUpsertFindings) is the manual catch.

// prodFindings builds n production-shaped findings: a populated member evidence trail (the
// marshal-heavy path), not the thin synthetic findings the scale benchmark uses.
func prodFindings(n int) []detect.Finding {
	at := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	out := make([]detect.Finding, 0, n)
	for i := 0; i < n; i++ {
		ns := fmt.Sprintf("ns-%d", i%20)
		out = append(out, detect.Finding{
			EntityCEI:    fmt.Sprintf("i|cl|%s|Pod|pod-%d|uid-%d", ns, i, i),
			Phenomenon:   "PHEN_THROTTLING_CASCADE",
			GraphVersion: "sha256:bench",
			Label:        "CPU throttling cascade",
			Namespace:    ns,
			Name:         fmt.Sprintf("pod-%d", i),
			Kind:         "Container",
			Quality:      detect.QualityDegraded,
			Completeness: 0.66, RequiredTotal: 3, RequiredMet: 2, RequiredUnobserved: 1,
			Members: []detect.MemberEvidence{
				{SignalID: "SIG_cpu_throttle", Metric: "container_cpu_cfs_throttled_periods_total", Role: "required", Temporal: "T0", Observable: true, Met: true, State: "well-above", Note: "throttle ratio derivation", SampleAt: at},
				{SignalID: "SIG_node_psi", Metric: "node_pressure_cpu_waiting_seconds_total", Role: "required", Temporal: "T0+", Observable: true, Met: true, State: "above", Note: "PSI waiting", Neighbour: "i|cl|cl|Node|node-1|nodeuid-1", Via: "runs-on", Hop: 1, EdgeResult: "valid", SampleAt: at},
				{SignalID: "SIG_runq", Metric: "node_schedstat_running_seconds_total", Role: "supporting", Temporal: "T0", Observable: false, Met: false, Note: "run-queue depth (unobserved on this platform)"},
			},
			Unobservable: []string{"SIG_runq — run-queue depth not exported"},
		})
	}
	return out
}

func upsertAllocsPerFinding(t *testing.T, n int) float64 {
	at := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "perf.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	fs := prodFindings(n)
	if err := s.UpsertFindings(at, fs); err != nil { // warm (insert path)
		t.Fatalf("upsert: %v", err)
	}
	allocs := testing.AllocsPerRun(2, func() {
		if err := s.UpsertFindings(at, fs); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	})
	return allocs / float64(n)
}

// Pinned ceiling. Calibrated against production-shaped findings (3 members each) on
// 2026-06-18, Go 1.26: ~32 allocs/finding (plain) / ~38.7 under `go test -race` (the CI run
// mode). Pinned at 60 (~55% headroom over the race baseline); a doubling (~77) trips it.
const upsertAllocCeilingPerFinding = 60.0

// TestUpsertPerfGuard locks the per-finding write allocation cost on the marshal-heavy
// production path (populated member evidence), so a write-path bloat regression goes red.
func TestUpsertPerfGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("perf guard skipped under -short (runs in CI's `go test -race ./...`)")
	}
	apf := upsertAllocsPerFinding(t, 1000)
	t.Logf("upsert allocs/finding (production-shaped, 3 members) = %.1f", apf)
	if apf > upsertAllocCeilingPerFinding {
		t.Errorf("allocs/finding=%.1f exceeds ceiling %.0f — a write-path (marshal) allocation regression "+
			"on the production finding shape. See file header to re-pin deliberately.", apf, upsertAllocCeilingPerFinding)
	}
}
