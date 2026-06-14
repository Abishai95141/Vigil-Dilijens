package mcp

import (
	"encoding/json"
	"testing"
)

// honeypots span the three banned registers the charter guard must catch. The
// refusal-recall over this set must be 1.0 (the teeth test, mirroring
// api.charter_battery_test.go TestCharterLinterHasTeeth): a vacuous guard fails.
var advisoryHoneypots = []string{
	"the memory leak is caused by the new deploy",
	"this happened because of the noisy neighbour",
	"the node fell over due to the leak",
	"working set will cross the limit in ten minutes",
	"the pod is going to OOM shortly",
	"a crossing is imminent and inevitably fatal",
	"this is a measured forecast of the outage",
	"projected and confirmed: the bar was breached",
	"the root cause is the sidecar",
}

// cleanDrafts are register-clean advisories that must NOT be refused (they cite
// classed facts without asserting a cause or a certainty).
var cleanDrafts = []string{
	"Three pairs are silent: one PVC has no stream key, one workload is unbounded.",
	"working_set is projected to cross between 12:05 and 12:11 (a band, open-ended).",
	"Coverage: 1 phenomenon fully observable, 2 partial. See the silence ledger for the rest.",
}

func TestAdvisoryGuardHasTeeth(t *testing.T) {
	missed := 0
	for _, hp := range advisoryHoneypots {
		// Gate posture is irrelevant: refusal precedes the gate.
		res := ValidateAdvisory(hp, true)
		if !res.Refused {
			t.Errorf("honeypot NOT refused: %q", hp)
			missed++
		}
		if res.Text != "" {
			t.Errorf("refused honeypot must not carry text: %q", hp)
		}
	}
	recall := float64(len(advisoryHoneypots)-missed) / float64(len(advisoryHoneypots))
	if recall != 1.0 {
		t.Fatalf("advisory refusal-recall = %.3f, want 1.000 (teeth)", recall)
	}
}

func TestCleanAdvisoryWithheldWhenGateOff(t *testing.T) {
	for _, d := range cleanDrafts {
		res := ValidateAdvisory(d, false)
		if res.Refused {
			t.Errorf("clean draft wrongly refused: %q (%s)", d, res.RefusedReason)
		}
		if !res.Withheld {
			t.Errorf("clean draft must be WITHHELD while the gate is unpassed: %q", d)
		}
		if res.Text != "" {
			t.Errorf("withheld advisory must not surface text: %q", d)
		}
	}
}

func TestCleanAdvisoryReleasedWhenGatePassed(t *testing.T) {
	d := cleanDrafts[0]
	res := ValidateAdvisory(d, true)
	if res.Refused || res.Withheld {
		t.Fatalf("clean draft must pass once the gate is passed: %+v", res)
	}
	if res.Text != d {
		t.Errorf("released advisory text = %q, want %q", res.Text, d)
	}
	if res.Class != "ADVISORY" {
		t.Errorf("class = %q, want ADVISORY", res.Class)
	}
}

// TestEmitAdvisoryViaTool drives the guard through the MCP tools/call path.
func TestEmitAdvisoryViaTool(t *testing.T) {
	s := New(testSources(), false, "vigil-test", "v3")
	// A banned draft is refused through the tool.
	resp := call(t, s, "tools/call", `{"name":"emit_advisory","arguments":{"text":"the leak is caused by the deploy"}}`)
	text, _ := toolText(t, resp)
	var res AdvisoryResult
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Refused {
		t.Errorf("emit_advisory must refuse a causal draft, got %+v", res)
	}
	if res.Class != "ADVISORY" {
		t.Errorf("class = %q, want ADVISORY", res.Class)
	}

	// A clean draft is withheld (gate off).
	resp = call(t, s, "tools/call", `{"name":"emit_advisory","arguments":{"text":"two pairs are silent per the ledger"}}`)
	text, _ = toolText(t, resp)
	_ = json.Unmarshal([]byte(text), &res)
	if res.Refused || !res.Withheld {
		t.Errorf("clean draft via tool must be withheld with the gate off, got %+v", res)
	}
}
