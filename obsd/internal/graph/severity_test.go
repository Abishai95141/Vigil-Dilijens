package graph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsValidSeverityAndRank(t *testing.T) {
	for _, s := range []string{"", "critical", "high", "medium", "low"} {
		if !IsValidSeverity(s) {
			t.Errorf("%q should be a valid severity", s)
		}
	}
	for _, s := range []string{"sev1", "CRITICAL", "urgent", "none"} {
		if IsValidSeverity(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
	// Strict ordering by harm; undeclared ("") sorts last.
	if !(SeverityRank("critical") > SeverityRank("high") &&
		SeverityRank("high") > SeverityRank("medium") &&
		SeverityRank("medium") > SeverityRank("low") &&
		SeverityRank("low") > SeverityRank("")) {
		t.Fatalf("severity rank order wrong: c=%d h=%d m=%d l=%d u=%d",
			SeverityRank("critical"), SeverityRank("high"), SeverityRank("medium"),
			SeverityRank("low"), SeverityRank(""))
	}
}

func TestOverlaySeverityLoadsAndValidates(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "sev.yaml")
	if err := os.WriteFile(good, []byte(`overlay: test-sev
version: 1
author: tester
phenomena:
  - id: PHEN_TEST_SEVERITY
    label: Test severity phenomenon
    severity: high
    signals:
      - ["test_sev_metric", "required", "T0", "n"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadWithExtraOverlays(kgPath, overlayDir, good)
	if err != nil {
		t.Fatalf("load with valid severity overlay: %v", err)
	}
	p := g.Phenomena["PHEN_TEST_SEVERITY"]
	if p == nil {
		t.Fatal("PHEN_TEST_SEVERITY not loaded")
	}
	if p.Severity != "high" {
		t.Fatalf("severity = %q, want high", p.Severity)
	}

	// An unknown severity is an authoring defect — the overlay loader rejects it loudly.
	bad := filepath.Join(dir, "badsev.yaml")
	if err := os.WriteFile(bad, []byte(`overlay: test-badsev
version: 1
author: tester
phenomena:
  - id: PHEN_TEST_BADSEVERITY
    label: Bad severity phenomenon
    severity: urgent
    signals:
      - ["test_sev_metric", "required", "T0", "n"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithExtraOverlays(kgPath, overlayDir, bad); err == nil {
		t.Fatal("expected an error for an invalid overlay severity 'urgent'")
	}
}
