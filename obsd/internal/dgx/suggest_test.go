package dgx_test

import (
	"context"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

func TestSuggestPhenomenon(t *testing.T) {
	// Valid JSON followed by trailing prose (the json.Decoder reads one value, ignores the rest).
	resp := `{"label":"Node CPU scheduling pressure","description":"tasks repeatedly waiting for CPU time"}

That is my suggestion!`
	ag := dgx.New(dgx.NewStaticProvider("fake", resp), dgx.DefaultParams)
	sg, err := ag.SuggestPhenomenon(context.Background(),
		[]string{"node_pressure_cpu_waiting_seconds_total"}, "Node")
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if sg.Label != "Node CPU scheduling pressure" {
		t.Fatalf("label = %q", sg.Label)
	}
	if sg.Description == "" {
		t.Fatal("description must not be empty")
	}
	if sg.Model != "fake" {
		t.Fatalf("model provenance = %q, want fake", sg.Model)
	}
}

func TestSuggestPhenomenonGuards(t *testing.T) {
	// No metrics → error (nothing to summarise).
	ag := dgx.New(dgx.NewStaticProvider("fake", `{"label":"x"}`), dgx.DefaultParams)
	if _, err := ag.SuggestPhenomenon(context.Background(), nil, "Node"); err == nil {
		t.Error("expected an error with no metrics")
	}
	// Malformed (not JSON) → error, never a silent empty hint.
	bad := dgx.New(dgx.NewStaticProvider("fake", "I cannot name this."), dgx.DefaultParams)
	if _, err := bad.SuggestPhenomenon(context.Background(), []string{"m"}, "Node"); err == nil {
		t.Error("expected an error for non-JSON output")
	}
	// JSON without a label → error.
	noLabel := dgx.New(dgx.NewStaticProvider("fake", `{"description":"d"}`), dgx.DefaultParams)
	if _, err := noLabel.SuggestPhenomenon(context.Background(), []string{"m"}, "Node"); err == nil {
		t.Error("expected an error for a missing label")
	}
}
