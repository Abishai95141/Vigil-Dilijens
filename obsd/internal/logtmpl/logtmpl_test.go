package logtmpl

import (
	"reflect"
	"strings"
	"testing"
)

func find(ts []Template, sub string) (Template, bool) {
	for _, t := range ts {
		if strings.Contains(t.Pattern, sub) {
			return t, true
		}
	}
	return Template{}, false
}

func TestMineGroupsSimilarLines(t *testing.T) {
	lines := []string{
		"user 12 logged in from 10.0.0.1",
		"user 34 logged in from 10.0.0.2",
		"user 9 logged in from 10.0.0.99",
		"disk usage 85 percent on /dev/sda",
	}
	got := Mine(lines, DefaultParams)
	if len(got) != 2 {
		t.Fatalf("templates = %d, want 2: %+v", len(got), got)
	}
	u, ok := find(got, "user <*> logged in from <*>")
	if !ok || u.Count != 3 {
		t.Errorf("want the user template merged to count 3, got %+v", got)
	}
	if _, ok := find(got, "disk usage <*> percent"); !ok {
		t.Errorf("disk template not found / not masked: %+v", got)
	}
}

func TestMineMasking(t *testing.T) {
	got := Mine([]string{"request took 250ms id 0xdeadbeef uuid 550e8400-e29b-41d4-a716-446655440000"}, DefaultParams)
	if len(got) != 1 {
		t.Fatalf("want 1 template, got %+v", got)
	}
	p := got[0].Pattern
	if !strings.Contains(p, "request took <*> id <*> uuid <*>") {
		t.Errorf("masking wrong: %q", p)
	}
}

// Embedded variable values (userId=<uuid>, main.go:<line>, CIDRs) are masked at the
// line level so otherwise-identical lines merge — the real-data quality fix.
func TestMineMasksEmbeddedValues(t *testing.T) {
	lines := []string{
		"GetCartAsync called with userId=39c70ffd-778f-4db3-b8fe-cdbe57215e6c",
		"GetCartAsync called with userId=37bc7d59-6842-498e-98c4-560ea6db36c9",
		"GetCartAsync called with userId=6db97934-42c0-44ba-9977-061dff7b9250",
	}
	got := Mine(lines, DefaultParams)
	if len(got) != 1 {
		t.Fatalf("embedded userId values should merge to 1 template, got %d: %+v", len(got), got)
	}
	if got[0].Count != 3 || !strings.Contains(got[0].Pattern, "userId=<*>") {
		t.Errorf("want one userId=<*> template count 3, got %+v", got[0])
	}
}

// THE LOGTMPL-GATE (doc 20 P4): the template set is byte-identical across runs AND
// invariant to the order lines arrive in (Mine sorts its input). This is the
// determinism guarantee the review required before any log-derived signal is trusted.
func TestLogtmplGateDeterministic(t *testing.T) {
	lines := []string{
		"GET /api/cart 200 12ms",
		"GET /api/cart 200 9ms",
		"GET /api/checkout 500 1200ms",
		"connection to 10.0.0.5:5432 failed",
		"connection to 10.0.0.6:5432 failed",
		"cache miss for key user:42",
		"cache miss for key user:99",
		"worker 3 started",
		"worker 7 started",
		"GET /api/cart 200 15ms",
	}
	first := Mine(lines, DefaultParams)
	second := Mine(lines, DefaultParams)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("miner not deterministic across runs:\n%+v\n%+v", first, second)
	}
	// reverse the input order — the template set must be identical.
	rev := make([]string, len(lines))
	for i := range lines {
		rev[i] = lines[len(lines)-1-i]
	}
	if got := Mine(rev, DefaultParams); !reflect.DeepEqual(first, got) {
		t.Fatalf("miner not invariant to input order:\n%+v\n%+v", first, got)
	}
	// sanity: it actually compressed (fewer templates than lines).
	total := 0
	for _, tpl := range first {
		total += tpl.Count
	}
	if total != len(lines) {
		t.Errorf("counts must sum to line count: %d != %d", total, len(lines))
	}
	if len(first) >= len(lines) {
		t.Errorf("expected compression: %d templates for %d lines", len(first), len(lines))
	}
}

func TestMineEmpty(t *testing.T) {
	if got := Mine(nil, DefaultParams); len(got) != 0 {
		t.Errorf("nil lines → %d templates, want 0", len(got))
	}
	if got := Mine([]string{"", "   "}, DefaultParams); len(got) != 0 {
		t.Errorf("blank lines → %d templates, want 0", len(got))
	}
}
