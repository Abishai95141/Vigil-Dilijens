package candidate

import "strings"

// Stray actionability classes (doc 20 §2.4 follow-up). NOT a provenance class and NOT a
// status — an ACTIONABILITY hint that decides whether a quarantined stray is worth a human
// mapping decision. Deterministic + authored (no learning, no threshold).
const (
	// StrayOperational — a real signal an operator might want bound to an entity (a genuine
	// exporter series, a workload/node/storage kube_* signal): worth enqueuing for review.
	StrayOperational = "operational"
	// StrayObjectMetadata — a kube-state-metrics kube_<kind>_* family describing pure
	// Kubernetes API OBJECT INVENTORY/STATE (a ReplicaSet's generation, an Endpoints'
	// addresses, a ConfigMap/Secret's info): non-actionable as an operational signal. Still
	// recorded + counted (honest partial coverage), but NOT enqueued in the human review
	// queue — these would otherwise flood governance with hundreds of un-mappable metadata.
	StrayObjectMetadata = "object-metadata"
)

// metadataObjectKinds is the AUTHORED set of KSM object kinds whose kube_<kind>_* metrics
// are pure API-object inventory rather than a workload/node/storage runtime signal. This is
// a maintained list (the same discipline as the identity normalize map) — not a model output.
//
// Deliberately EXCLUDED (left "operational", still asked): pod, node, deployment, statefulset,
// daemonset, replicaset->no (inventory), persistentvolume(claim) — workload/node runtime + real
// storage capacity/usage an operator legitimately maps. Any non-kube_ exporter stray
// (mysqld_*, redis_*, pg_*, app metrics, histogram-derived quantiles) is always operational.
var metadataObjectKinds = map[string]bool{
	"replicaset": true, "replicationcontroller": true,
	"endpoint": true, "endpoints": true, "endpointslice": true,
	"configmap": true, "secret": true, "service": true,
	"ingress": true, "ingressclass": true,
	"job": true, "cronjob": true,
	"namespace": true, "lease": true, "storageclass": true,
	"serviceaccount": true,
	"role":           true, "rolebinding": true, "clusterrole": true, "clusterrolebinding": true,
	"networkpolicy": true, "resourcequota": true, "limitrange": true,
	"poddisruptionbudget": true, "horizontalpodautoscaler": true,
	"mutatingwebhookconfiguration": true, "validatingwebhookconfiguration": true,
	"certificatesigningrequest": true, "volumeattachment": true,
}

// ClassifyStrayMetric labels a quarantined stray metric name by operational actionability.
// kube_<kind>_… whose <kind> is a pure-inventory API object → object-metadata (suppress from
// the review queue). Everything else → operational (worth a human decision). Pure function of
// the metric name; deterministic; no threshold, no learning.
func ClassifyStrayMetric(metric string) string {
	const ksm = "kube_"
	if strings.HasPrefix(metric, ksm) {
		rest := metric[len(ksm):]
		kind := rest
		if i := strings.IndexByte(rest, '_'); i >= 0 {
			kind = rest[:i]
		}
		if metadataObjectKinds[kind] {
			return StrayObjectMetadata
		}
	}
	return StrayOperational
}

// StrayMetricFromSubject extracts the bare metric name from a stray candidate subject
// ("stray:<metric>/<key>" for a node, "stray:<metric>/<key> ~> <entity>" for an edge),
// returning ("", false) when the subject is not a stray mapping (trace/audit candidates are
// not stray-based and are always actionable). Mirrors the subject shape minted in Resolve.
func StrayMetricFromSubject(subject string) (string, bool) {
	const p = "stray:"
	if !strings.HasPrefix(subject, p) {
		return "", false
	}
	rest := subject[len(p):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}

// StrayCandidateActionable reports whether a candidate (by its subject) is worth a human
// review decision: any non-stray candidate (trace topology, audit hypothesis) is actionable;
// a stray mapping is actionable only when its metric classifies operational. Used to keep the
// governance queue to the necessary decisions while object-metadata stays counted but unqueued.
func StrayCandidateActionable(subject string) bool {
	metric, ok := StrayMetricFromSubject(subject)
	if !ok {
		return true
	}
	return ClassifyStrayMetric(metric) != StrayObjectMetadata
}
