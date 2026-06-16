package identity

import (
	"testing"
	"time"
)

// FamilyApp (doc 15 cap. A): an app's own /metrics series carry no k8s identity, so
// the entity is the SCRAPE TARGET pod, resolved time-aware — never the series labels.

// The cardinal identity guarantee: even with MISLEADING namespace/pod labels on the
// series, the resolved CEI is the SCRAPE TARGET pod, never the labels (no mis-join).
func TestAppIdentityFromTargetNotLabels(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyApp,
		Metric: "app_queue_depth",
		// Hostile labels that, if trusted, would mis-attribute the series.
		Labels:        map[string]string{"namespace": "evil", "pod": "imposter", "queue": "inbound"},
		SourcePodNS:   "shop",
		SourcePodName: "currencyservice-x",
		At:            at(time.Minute),
	})
	if r.Outcome != OutcomeResolved {
		t.Fatalf("outcome = %s (reason %q), want resolved", r.Outcome, r.Reason)
	}
	if r.CEI.Kind != "Pod" || r.CEI.UID != "uid-pod-1" {
		t.Errorf("app series bound to kind %q uid %q, want the TARGET Pod CEI uid-pod-1 (labels must be ignored for identity)", r.CEI.Kind, r.CEI.UID)
	}
	if r.Family != FamilyApp || r.MapVersion == "" {
		t.Errorf("family/mapversion = %q/%q, want app/<set>", r.Family, r.MapVersion)
	}
}

// No scrape target ⇒ quarantine, never a guess.
func TestAppMissingTargetQuarantines(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyApp, Metric: "app_request_total",
		Labels: map[string]string{"code": "200"}, At: at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonMissingSource {
		t.Errorf("outcome/reason = %s/%q, want quarantined/%q", r.Outcome, r.Reason, ReasonMissingSource)
	}
}

// A target the control plane has not seen ⇒ quarantine, never a guessed pod.
func TestAppUnknownTargetQuarantines(t *testing.T) {
	r := newNorm().Normalize(Series{
		Family: FamilyApp, Metric: "app_request_total",
		SourcePodNS: "shop", SourcePodName: "does-not-exist", At: at(time.Minute),
	})
	if r.Outcome != OutcomeQuarantined || r.Reason != ReasonUnknownPod {
		t.Errorf("outcome/reason = %s/%q, want quarantined/%q", r.Outcome, r.Reason, ReasonUnknownPod)
	}
}
