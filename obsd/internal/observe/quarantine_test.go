package observe

import (
	"context"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// OFF by default: quarantined series are counted (as always) but NOT buffered.
func TestQuarantineCaptureOffByDefault(t *testing.T) {
	in, _ := newTestIngestor(t)
	f := fixtureFetcher{payloads: map[string]string{"worker-1/metrics/cadvisor": goldenCAdvisor}, at: scrapeAt}
	sum := in.ScrapeCAdvisor(context.Background(), f, []string{"worker-1"})
	if sum.SeriesQuarantine["unknown-pod"] != 1 {
		t.Fatalf("expected the golden unknown-pod quarantine, got %v", sum.SeriesQuarantine)
	}
	if got := in.DrainQuarantined(); got != nil {
		t.Errorf("capture off: drained %d strays, want nil", len(got))
	}
}

// ON: the quarantined stray is buffered with its labels + reason for the ER; counting
// is unchanged; drain is destructive.
func TestQuarantineCaptureOn(t *testing.T) {
	in, _ := newTestIngestor(t)
	in.EnableQuarantineCapture()
	f := fixtureFetcher{payloads: map[string]string{"worker-1/metrics/cadvisor": goldenCAdvisor}, at: scrapeAt}
	sum := in.ScrapeCAdvisor(context.Background(), f, []string{"worker-1"})
	if sum.SeriesQuarantine["unknown-pod"] != 1 {
		t.Fatalf("counting changed under capture: %v", sum.SeriesQuarantine)
	}
	strays := in.DrainQuarantined()
	if len(strays) != 1 {
		t.Fatalf("drained %d strays, want 1", len(strays))
	}
	s := strays[0]
	if s.Reason != "unknown-pod" || s.Family != identity.FamilyCAdvisor {
		t.Errorf("stray = %+v, want unknown-pod / cadvisor", s)
	}
	if len(s.Labels) == 0 {
		t.Error("stray should carry its labels for the ER to intersect")
	}
	if again := in.DrainQuarantined(); again != nil {
		t.Errorf("drain is destructive: second drain = %v, want nil", again)
	}
}
