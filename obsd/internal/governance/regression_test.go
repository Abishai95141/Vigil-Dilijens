package governance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequiredGatesScaling(t *testing.T) {
	cases := []struct {
		class ChangeClass
		want  int
	}{
		{ClassAdditiveLow, 2},
		{ClassBehaviouralMedium, 4},
		{ClassNormativeHigh, 5},
	}
	for _, tc := range cases {
		if got := len(RequiredGates(tc.class)); got != tc.want {
			t.Errorf("RequiredGates(%s) = %d gates, want %d", tc.class, got, tc.want)
		}
	}
	// Higher classes must be supersets of lower ones.
	low := gateSet(RequiredGates(ClassAdditiveLow))
	high := gateSet(RequiredGates(ClassNormativeHigh))
	for g := range low {
		if !high[g] {
			t.Errorf("normative-high missing additive-low gate %s (not a superset)", g)
		}
	}
}

func TestVerifyGatesAllPass(t *testing.T) {
	ledger := &GateLedger{Class: "normative-high", Results: allGatesPass(ClassNormativeHigh)}
	if err := VerifyGates(ClassNormativeHigh, ledger); err != nil {
		t.Fatalf("expected pass, got: %v", err)
	}
}

func TestVerifyGatesMissingBlocks(t *testing.T) {
	// Only lints ran; behavioural needs four gates.
	ledger := &GateLedger{Results: []GateResult{{Gate: string(GateLints), Status: "passed"}}}
	if err := VerifyGates(ClassBehaviouralMedium, ledger); err == nil {
		t.Fatal("expected block on missing gates")
	}
}

func TestVerifyGatesFailedBlocks(t *testing.T) {
	rs := allGatesPass(ClassBehaviouralMedium)
	rs[2].Status = "failed"
	ledger := &GateLedger{Results: rs}
	if err := VerifyGates(ClassBehaviouralMedium, ledger); err == nil {
		t.Fatal("expected block on failed gate")
	}
}

func TestVerifyGatesSkippedBlocks(t *testing.T) {
	rs := allGatesPass(ClassBehaviouralMedium)
	rs[1].Status = "skipped"
	ledger := &GateLedger{Results: rs}
	if err := VerifyGates(ClassBehaviouralMedium, ledger); err == nil {
		t.Fatal("expected block on skipped gate (a skipped gate cannot release)")
	}
}

func TestLoadGateLedgerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	content := `{"proposal":"v0.4.0","class":"behavioural-medium","results":[
		{"gate":"authoring-lints","status":"passed","detail":"graphlint -strict ok","command":"graphlint -strict"}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := LoadGateLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Proposal != "v0.4.0" || len(l.Results) != 1 || l.Results[0].Status != "passed" {
		t.Fatalf("unexpected ledger: %+v", l)
	}
}

func gateSet(gs []Gate) map[Gate]bool {
	m := map[Gate]bool{}
	for _, g := range gs {
		m[g] = true
	}
	return m
}
