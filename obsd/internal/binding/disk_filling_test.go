package binding

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// The ephemeral-storage config path (v0.9.0, PHEN_DISK_FILLING): the bar is the
// container's OWN declared ephemeral-storage limit (borrowed normativity); undeclared
// resolves to declared=false so the pair is unbounded/out-of-scope, never a guessed default.
func TestReadEphemeralStorageLimit(t *testing.T) {
	declared := ContainerConfig{Name: "filler", EphemeralStorageLimitBytes: 64 << 20}
	v, unit, ok := readContainerPath(graph.PathContainerLimitsEphemeralStorage, declared)
	if !ok || unit != "bytes" || v != float64(64<<20) {
		t.Errorf("declared limit: got (%v,%q,%v), want (%d,bytes,true)", v, unit, ok, 64<<20)
	}
	// Undeclared (0) → not resolvable: declared=false, no fabricated value.
	if _, _, ok := readContainerPath(graph.PathContainerLimitsEphemeralStorage, ContainerConfig{Name: "x"}); ok {
		t.Error("undeclared ephemeral-storage limit must resolve declared=false")
	}
}

// Eligibility gating: a container with no ephemeral-storage limit is OUT-OF-SCOPE for the
// disk-filling rule (fill-toward-limit has no bar), exactly as the CPU/memory rules gate.
func TestEphemeralStorageEligibilityGating(t *testing.T) {
	in, _ := eligibilityMet(graph.PathContainerLimitsEphemeralStorage, ContainerConfig{Name: "filler", EphemeralStorageLimitBytes: 64 << 20})
	if !in {
		t.Error("a declared ephemeral-storage limit must be IN scope")
	}
	out, reason := eligibilityMet(graph.PathContainerLimitsEphemeralStorage, ContainerConfig{Name: "x"})
	if out || reason == "" {
		t.Errorf("undeclared limit must be OUT-of-scope with a stated reason, got in=%v reason=%q", out, reason)
	}
}
