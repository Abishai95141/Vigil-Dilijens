package binding

import "testing"

// TestAssertedCapability pins the docs/33 P5 emission win: a capability obsd cannot derive
// from the k8s API (defaults to Indeterminate "needs node probe") becomes Obtainable when the
// operator asserts it — and an UN-asserted capability stays Indeterminate (no silent change).
func TestAssertedCapability(t *testing.T) {
	// Un-asserted: CAP_CONFIG_PSI is not kernel/tool-derivable → Indeterminate.
	if st, _ := capabilityMet("CAP_CONFIG_PSI", PlatformFacts{}); st != Indeterminate {
		t.Fatalf("un-asserted CAP_CONFIG_PSI = %q, want Indeterminate", st)
	}
	// Asserted: becomes Obtainable.
	facts := PlatformFacts{AssertedCapabilities: map[string]bool{"CAP_CONFIG_PSI": true}}
	if st, reason := capabilityMet("CAP_CONFIG_PSI", facts); st != Obtainable {
		t.Fatalf("asserted CAP_CONFIG_PSI = %q (%s), want Obtainable", st, reason)
	}
	// Assertion is scoped: a DIFFERENT capability is unaffected.
	if st, _ := capabilityMet("CAP_CGROUP_V2", facts); st != Indeterminate {
		t.Fatalf("non-asserted CAP_CGROUP_V2 = %q, want Indeterminate (assertion must not leak)", st)
	}
	// Assertion never overrides a HARD negative: a kernel cap genuinely not met stays out-of-scope
	// only if it's a kernel cap — assertion short-circuits before kernel check, which is the point
	// (operator-verified). We only assert what we've verified; the flag help states this.
}
