package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

var bsAt = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

// a silence ledger fixture exercising every reason class: two share an unobtainable
// reason (must dedup), one is structurally N/A (not a blind spot), one is unbounded
// (a normativity gap, not a blind spot).
func bsSilenceFixture() *SilenceLedgerView {
	return &SilenceLedgerView{
		Available: true, GraphVersion: "gv", GraphRelease: "v0.8.0", GeneratedAt: bsAt,
		Silent: []SilenceLedgerRow{
			{ReasonClass: SilenceNoStreamKey, Reason: "bar resolves but no observation stream is ingested for this pair yet", Metric: "kubelet_volume_stats_used_bytes"},
			{ReasonClass: SilenceOutOfScope, Reason: "signal unobtainable on this cluster: no emitting tool deployed: kube-state-metrics", Metric: "kube_pod_container_status_restarts_total"},
			{ReasonClass: SilenceOutOfScope, Reason: "signal unobtainable on this cluster: no emitting tool deployed: kube-state-metrics", Metric: "kube_node_status_disk_pressure"},
			{ReasonClass: SilenceOutOfScope, Reason: "out-of-scope: no CPU limit declared, CFS throttling cannot occur", Metric: "container_cpu_cfs_throttled_periods_total"},
			{ReasonClass: SilenceUnbounded, Reason: "unbounded: no declared limit", Metric: "container_memory_working_set_bytes"},
		},
	}
}

func TestBuildBlindspotRegistry_Static(t *testing.T) {
	v := BuildBlindspotRegistry(bsSilenceFixture())
	if len(v.Static) != len(staticBlindspots) {
		t.Fatalf("static count = %d, want %d", len(v.Static), len(staticBlindspots))
	}
	byID := map[string]BlindspotEntry{}
	for _, e := range v.Static {
		if e.Provenance != "static" {
			t.Errorf("static entry %s provenance = %q", e.ID, e.Provenance)
		}
		if e.Category == "" || e.Reason == "" {
			t.Errorf("static entry %s missing category/reason", e.ID)
		}
		byID[e.ID] = e
	}
	// the Jobs-mis-blame fix must be present, as an epistemic floor
	if e, ok := byID["INTRA_CONTAINER_ATTRIBUTION"]; !ok || e.Category != BlindEpistemicFloor {
		t.Error("INTRA_CONTAINER_ATTRIBUTION missing or not an epistemic floor")
	}
	// the audit's app-layer blind spots must be present
	for _, id := range []string{"APPLICATION_LOGS", "APP_DB_INTERNAL_METRICS", "L7_PROTOCOL_SEMANTICS", "MISSING_EVENT_REASONS", "DNS_FAILURE"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("expected static blindspot %q missing", id)
		}
	}
}

func TestBuildBlindspotRegistry_DynamicReconciliation(t *testing.T) {
	v := BuildBlindspotRegistry(bsSilenceFixture())
	if !v.Available {
		t.Fatal("available should be true when the silence ledger is available")
	}
	// Only the SENSOR/INGEST gaps become dynamic blind spots: the no-stream-key one,
	// and the unobtainable-KSM reason (the two KSM rows dedup into ONE entry).
	if len(v.Dynamic) != 2 {
		t.Fatalf("dynamic count = %d, want 2 (no-stream-key + deduped KSM); got %+v", len(v.Dynamic), v.Dynamic)
	}
	var ksm *BlindspotEntry
	for i := range v.Dynamic {
		e := &v.Dynamic[i]
		if e.Provenance != "dynamic" {
			t.Errorf("dynamic entry %s provenance = %q", e.ID, e.Provenance)
		}
		// the structurally-N/A throttle metric and the unbounded working-set must NOT appear
		if e.SampleMetric == "container_cpu_cfs_throttled_periods_total" || e.SampleMetric == "container_memory_working_set_bytes" {
			t.Errorf("a non-sensor-gap silence row leaked into dynamic blind spots: %s", e.SampleMetric)
		}
		if e.Reason == "signal unobtainable on this cluster: no emitting tool deployed: kube-state-metrics" {
			ksm = e
		}
	}
	if ksm == nil {
		t.Fatal("the KSM unobtainable reason did not produce a dynamic entry")
	}
	if ksm.AffectedPairs != 2 {
		t.Errorf("KSM dynamic entry affectedPairs = %d, want 2 (deduped)", ksm.AffectedPairs)
	}
}

func TestBuildBlindspotRegistry_NilLedger(t *testing.T) {
	v := BuildBlindspotRegistry(nil)
	if v.Available {
		t.Error("available should be false when binding has not compiled")
	}
	if len(v.Static) != len(staticBlindspots) {
		t.Error("the static floors must stand even before binding compiles")
	}
	if len(v.Dynamic) != 0 {
		t.Error("no dynamic entries without a silence ledger")
	}
}

func TestBuildBlindspotRegistry_Deterministic(t *testing.T) {
	a := BuildBlindspotRegistry(bsSilenceFixture())
	b := BuildBlindspotRegistry(bsSilenceFixture())
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same silence ledger must produce a byte-identical registry")
	}
}

// The registry is a surfaced payload; it must pass the charter register scan — no
// banned causal/future-certainty/fusion phrasing in any reason string.
func TestBuildBlindspotRegistry_CharterClean(t *testing.T) {
	v := BuildBlindspotRegistry(bsSilenceFixture())
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if vios := CharterViolations("blindspots", b); len(vios) != 0 {
		t.Fatalf("blindspot registry has charter violations: %+v", vios)
	}
}
