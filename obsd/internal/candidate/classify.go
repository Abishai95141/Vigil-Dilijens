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

	// The three NON-OPERATIONAL classes below describe the INSTRUMENTATION or the CONTROL
	// PLANE itself, never a workload/node/storage runtime signal. A stray in one of these
	// classes can never bind to such an entity (there is no entity to map it to), so it is
	// classified, COUNTED, and NOT staged as a mapping candidate (PartitionStrays) — the
	// operator is never asked to decide on an un-mappable row. This matters on a full k3s/
	// kubeadm control plane (AWS), which exposes apiserver/etcd/scheduler/kubelet /metrics
	// endpoints that flood the quarantine with thousands of component-internal series a
	// workload-observability engine has no entity for. Deterministic + authored (a maintained
	// prefix list, the same discipline as metadataObjectKinds); no threshold, no learning.

	// StrayRuntimeIntrospection — the exporter's OWN Go/process/HTTP-handler runtime
	// (go_*, process_*, promhttp_*, scrape_* meta, and other language runtimes). Emitted by
	// EVERY exporter regardless of what it monitors; it describes the exporter process, never
	// the cluster. Universally non-operational on any target.
	StrayRuntimeIntrospection = "runtime-introspection"
	// StrayClientInternal — client-go / gRPC-client / workqueue / leader-election / token
	// plumbing emitted INSIDE a k8s component (or any client-go-based controller). A real
	// operator's OWN such series carries pod identity and BINDS — it never strays; so a
	// client_* series on the stray path is, by definition, un-bindable component plumbing.
	StrayClientInternal = "client-library-internal"
	// StrayControlPlaneInternal — the kube CONTROL-PLANE components' own telemetry
	// (apiserver request/storage/watch internals, etcd client, scheduler, kube-proxy, the
	// per-controller counters, k3s/lasso server internals, feature-gate enablement). Real
	// control-plane HEALTH (apiserver overload, etcd slowness) is a SEPARATE modality Vigil
	// does not model yet (no control-plane ENTITY, no authored phenomenon); until it is built,
	// these have no entity to map to and only flood the queue. Counted + surfaced, and
	// RECLAIMABLE: drop a prefix from controlPlaneInternalPrefixes when the modality lands.
	StrayControlPlaneInternal = "control-plane-internal"
)

// runtimeIntrospectionPrefixes — process/runtime instrumentation emitted by every exporter
// about its OWN process, never about the cluster. Universally non-operational on any target.
var runtimeIntrospectionPrefixes = []string{
	"go_", "process_", "promhttp_", "scrape_",
	"python_", "jvm_", "nodejs_", "rails_", "ruby_",
}

// clientLibraryInternalPrefixes — client-go / gRPC-client / workqueue / leader-election /
// token-cache plumbing. Safe to classify aggressively because the partition runs AFTER the
// binding attempt: a workload's own such series binds (pod identity) and never reaches here;
// only un-bindable component plumbing does.
var clientLibraryInternalPrefixes = []string{
	"rest_client_", "grpc_client_", "workqueue_", "reflector_", "leader_election_",
	"apiserver_client_", "client_cert_", "get_token_", "authentication_token_",
	"cardinality_enforcement_", "disabled_metric", "hidden_metric", "registered_metrics_",
}

// controlPlaneInternalPrefixes — the control-plane components' own internals (apiserver,
// etcd, scheduler, kube-proxy, the named controllers, k3s/lasso, feature gates, the storage/
// volume/IPAM controller counters). Same post-binding safety as above: a workload that
// legitimately emits one of these binds and never reaches the stray path.
var controlPlaneInternalPrefixes = []string{
	"apiserver_", "etcd_", "watch_", "scheduler_", "kubeproxy_",
	"kubernetes_feature_", "kubernetes_healthcheck", "kubernetes_build",
	"aggregator_", "authorization_", "authentication_", "field_validation_",
	"serviceaccount_", "node_collector_", "running_managed_", "plugin_manager_",
	"pod_security_", "endpoint_slice_", "pv_collector_", "garbagecollector_",
	"ephemeral_volume_", "attachdetach_controller_", "root_ca_cert_",
	"storage_count_", "storage_operation_", "k3s_", "lasso_",
	"node_ipam_controller_", "service_controller_", "taint_eviction_controller_",
	"replicaset_controller_", "job_controller_", "cronjob_controller_",
	"retroactive_storageclass_", "reconstruct_volume_", "force_cleaned_",
	"session_server_", "horizontal_pod_autoscaler_controller_",
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

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

// metadataMetricSuffixes are kube-state-metrics sub-metric suffixes that are PURE object
// inventory regardless of the object KIND: a label/annotation carrier (always gauge=1, the
// data is in the labels) or a static creation timestamp. They never describe a runtime signal,
// so even on an otherwise-operational kind (pod, node, persistentvolume) the *_info / *_created
// / *_labels / *_annotations series are inventory, not something an operator binds for detection.
// This is the AUTHORED, universal complement to the kind-level metadataObjectKinds list above —
// it suppresses e.g. kube_persistentvolume_info and kube_pod_created without special-casing any
// cluster's objects. Maintained list; not a model output; no threshold, no learning.
var metadataMetricSuffixes = []string{"_info", "_created", "_labels", "_annotations"}

// ClassifyStrayMetric labels a quarantined stray metric name by operational actionability.
// A kube_<kind>_… stray is object-metadata (suppressed from the review queue) when EITHER the
// <kind> is a pure-inventory API object (metadataObjectKinds) OR the sub-metric is a universal
// inventory carrier (metadataMetricSuffixes — *_info/_created/_labels/_annotations). Everything
// else → operational (worth a human decision). Pure function of the metric name; deterministic;
// no threshold, no learning.
func ClassifyStrayMetric(metric string) string {
	const ksm = "kube_"
	if strings.HasPrefix(metric, ksm) {
		for _, suf := range metadataMetricSuffixes {
			if strings.HasSuffix(metric, suf) {
				return StrayObjectMetadata
			}
		}
		rest := metric[len(ksm):]
		kind := rest
		if i := strings.IndexByte(rest, '_'); i >= 0 {
			kind = rest[:i]
		}
		if metadataObjectKinds[kind] {
			return StrayObjectMetadata
		}
		// A workload/node/storage kube_* signal (kube_pod_*, kube_persistentvolume_*, …) is
		// operational. kube_-prefixed names never match the non-operational prefix sets below
		// (those are bare component families), so return early to keep the two taxonomies clean.
		return StrayOperational
	}
	// Non-operational instrumentation / control-plane families (checked specific→general).
	switch {
	case hasAnyPrefix(metric, runtimeIntrospectionPrefixes):
		return StrayRuntimeIntrospection
	case hasAnyPrefix(metric, clientLibraryInternalPrefixes):
		return StrayClientInternal
	case hasAnyPrefix(metric, controlPlaneInternalPrefixes):
		return StrayControlPlaneInternal
	}
	return StrayOperational
}

// IsNonOperational reports whether a class is one of the three non-operational classes —
// instrumentation/control-plane series that can never bind to a workload/node/storage entity.
func IsNonOperational(class string) bool {
	switch class {
	case StrayRuntimeIntrospection, StrayClientInternal, StrayControlPlaneInternal:
		return true
	}
	return false
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
// (Non-operational classes are not even staged — see PartitionStrays — so they never reach
// here; the explicit == operational check is the belt-and-braces complement to that.)
func StrayCandidateActionable(subject string) bool {
	metric, ok := StrayMetricFromSubject(subject)
	if !ok {
		return true
	}
	return ClassifyStrayMetric(metric) == StrayOperational
}

// ExcludedStray is one stray series classified NON-OPERATIONAL and therefore not staged as a
// mapping candidate (not saved). Subject mirrors the subject Resolve would have minted (so the
// caller can dedup excluded series exactly as the store dedups candidates); Class is one of the
// three non-operational classes, surfaced so coverage states WHAT was excluded and WHY.
type ExcludedStray struct {
	Subject string
	Metric  string
	Class   string
}

// PartitionStrays splits drained strays into those worth STAGING as mapping candidates
// (operational signals + k8s object-metadata — both still bind or stay honestly counted in the
// candidate store) and those CLASSIFIED non-operational (Go/process runtime, client-library
// internals, control-plane component internals). The latter can never bind to a workload/node/
// storage entity, so they are returned for honest counting and NOT saved — the operator is
// never asked to map an un-mappable row. Pure + deterministic; safe to classify aggressively
// because it runs AFTER the binding attempt (only un-bindable strays ever reach it).
func PartitionStrays(strays []StrayObservation) (stage []StrayObservation, excluded []ExcludedStray) {
	for _, s := range strays {
		if class := ClassifyStrayMetric(s.Metric); IsNonOperational(class) {
			excluded = append(excluded, ExcludedStray{Subject: StraySubject(s), Metric: s.Metric, Class: class})
			continue
		}
		stage = append(stage, s)
	}
	return stage, excluded
}
