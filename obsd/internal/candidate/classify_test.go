package candidate

import "testing"

func TestClassifyStrayMetric(t *testing.T) {
	cases := []struct {
		metric string
		want   string
	}{
		// KSM object-inventory families → object-metadata (suppressed from review)
		{"kube_replicaset_status_observed_generation", StrayObjectMetadata},
		{"kube_replicaset_status_replicas", StrayObjectMetadata},
		{"kube_endpoint_address_available", StrayObjectMetadata},
		{"kube_configmap_info", StrayObjectMetadata},
		{"kube_secret_info", StrayObjectMetadata},
		{"kube_service_info", StrayObjectMetadata},
		{"kube_job_status_succeeded", StrayObjectMetadata},
		{"kube_namespace_status_phase", StrayObjectMetadata},
		{"kube_lease_owner", StrayObjectMetadata},
		{"kube_storageclass_info", StrayObjectMetadata},
		{"kube_poddisruptionbudget_status_current_healthy", StrayObjectMetadata},
		// Operational signals — real exporters + workload/node/storage kube_* → still asked
		{"kube_persistentvolume_capacity_bytes", StrayOperational},
		{"kube_persistentvolume_status_phase", StrayOperational},
		{"kube_pod_status_phase", StrayOperational},
		{"kube_deployment_status_replicas_unavailable", StrayOperational},
		{"kube_node_status_condition", StrayOperational},
		{"mysqld_global_status_threads_connected", StrayOperational},
		{"redis_connected_clients", StrayOperational},
		{"pg_stat_database_xact_commit", StrayOperational},
		{"http_request_duration_seconds_p99", StrayOperational},
		// Universal metadata-carrier suffixes — pure inventory even on an operational KIND:
		// a label/annotation carrier (gauge=1) or a static creation timestamp → suppressed.
		{"kube_persistentvolume_info", StrayObjectMetadata},
		{"kube_persistentvolume_created", StrayObjectMetadata},
		{"kube_pod_info", StrayObjectMetadata},
		{"kube_pod_created", StrayObjectMetadata},
		{"kube_node_labels", StrayObjectMetadata},
		{"kube_deployment_annotations", StrayObjectMetadata},
		// …but a real runtime sub-metric on the SAME kind stays operational (suffix, not substring)
		{"kube_persistentvolume_capacity_bytes", StrayOperational},
		{"kube_pod_container_status_restarts_total", StrayOperational},
		{"kube_node_status_condition", StrayOperational},
		// Runtime/process introspection — the exporter's OWN process, never the cluster.
		{"go_memstats_alloc_bytes", StrayRuntimeIntrospection},
		{"go_gc_duration_seconds", StrayRuntimeIntrospection},
		{"process_cpu_seconds_total", StrayRuntimeIntrospection},
		{"promhttp_metric_handler_requests_total", StrayRuntimeIntrospection},
		{"scrape_duration_seconds", StrayRuntimeIntrospection},
		// Client-library / workqueue / leader-election plumbing inside a k8s component.
		{"rest_client_requests_total", StrayClientInternal},
		{"workqueue_depth", StrayClientInternal},
		{"grpc_client_handled_total", StrayClientInternal},
		{"leader_election_master_status", StrayClientInternal},
		{"authentication_token_cache_request_total", StrayClientInternal},
		// Control-plane component internals — no workload/node/storage entity to map to.
		{"apiserver_request_total", StrayControlPlaneInternal},
		{"apiserver_watch_cache_events_dispatched_total", StrayControlPlaneInternal},
		{"etcd_requests_total", StrayControlPlaneInternal},
		{"scheduler_pending_pods", StrayControlPlaneInternal},
		{"kubernetes_feature_enabled", StrayControlPlaneInternal},
		{"k3s_certificate_expiration_seconds", StrayControlPlaneInternal},
		{"node_ipam_controller_cidrset_usage_cidrs", StrayControlPlaneInternal},
		// …but genuine operational signals on the SAME providers stay operational (must bind):
		{"node_cpu_seconds_total", StrayOperational},          // node-exporter, not node_collector_
		{"node_memory_MemAvailable_bytes", StrayOperational},  // node-exporter
		{"container_cpu_usage_seconds_total", StrayOperational},
		{"kubelet_volume_stats_used_bytes", StrayOperational}, // the PVC-fill signal
		{"container_pressure_memory_waiting_seconds_total", StrayOperational}, // PSI
		// Defensive: a bare kube_ prefix with no kind, or non-stray noise
		{"kube_", StrayOperational},
		{"some_random_metric", StrayOperational},
	}
	for _, c := range cases {
		if got := ClassifyStrayMetric(c.metric); got != c.want {
			t.Errorf("ClassifyStrayMetric(%q) = %q, want %q", c.metric, got, c.want)
		}
	}
}

func TestPartitionStrays(t *testing.T) {
	mk := func(metric string) StrayObservation {
		return StrayObservation{Family: "app", Metric: metric, Node: "n1", Labels: map[string]string{"x": "1"}}
	}
	strays := []StrayObservation{
		mk("go_gc_duration_seconds"),                  // runtime → excluded
		mk("apiserver_request_total"),                 // control-plane → excluded
		mk("workqueue_depth"),                         // client → excluded
		mk("kube_persistentvolume_capacity_bytes"),    // operational → staged
		mk("kube_replicaset_status_observed_generation"), // object-metadata → staged (counted, suppressed from queue)
		mk("mysqld_global_status_threads_connected"),  // operational → staged
	}
	stage, excluded := PartitionStrays(strays)
	if len(stage) != 3 {
		t.Errorf("staged = %d, want 3 (PVC + KSM-metadata + mysqld)", len(stage))
	}
	if len(excluded) != 3 {
		t.Fatalf("excluded = %d, want 3 (runtime + control-plane + client)", len(excluded))
	}
	byClass := map[string]int{}
	for _, e := range excluded {
		byClass[e.Class]++
		if e.Subject == "" || e.Subject == "stray:" {
			t.Errorf("excluded subject not minted: %+v", e)
		}
	}
	for _, want := range []string{StrayRuntimeIntrospection, StrayClientInternal, StrayControlPlaneInternal} {
		if byClass[want] != 1 {
			t.Errorf("excluded class %q count = %d, want 1", want, byClass[want])
		}
	}
	// The excluded subject must equal the subject Resolve would have minted (dedup parity).
	if got := StraySubject(mk("go_gc_duration_seconds")); got != excluded[0].Subject {
		t.Errorf("StraySubject parity: got %q vs excluded %q", got, excluded[0].Subject)
	}
}

func TestStrayMetricFromSubject(t *testing.T) {
	cases := []struct {
		subject    string
		wantOK     bool
		wantMetric string
	}{
		{"stray:kube_replicaset_status_replicas/29db6578895f", true, "kube_replicaset_status_replicas"},
		{"stray:kube_job_status_succeeded/f41e13199b04 ~> i|cluster|ns|Kind|name|uid", true, "kube_job_status_succeeded"},
		{"stray:mysqld_global_status_threads_connected/abc123", true, "mysqld_global_status_threads_connected"},
		{"trace-call:erpnext-nginx->erpnext-gunicorn", false, ""},
		{"", false, ""},
		{"stray:", false, ""},
	}
	for _, c := range cases {
		m, ok := StrayMetricFromSubject(c.subject)
		if ok != c.wantOK || m != c.wantMetric {
			t.Errorf("StrayMetricFromSubject(%q) = (%q,%v), want (%q,%v)", c.subject, m, ok, c.wantMetric, c.wantOK)
		}
	}
}

func TestStrayCandidateActionable(t *testing.T) {
	// object-metadata stray mapping → not actionable (suppressed)
	if StrayCandidateActionable("stray:kube_endpoint_address_available/deadbeef") {
		t.Error("object-metadata stray must be non-actionable")
	}
	if StrayCandidateActionable("stray:kube_configmap_info/abcd ~> i|c|n|ConfigMap|x|u") {
		t.Error("object-metadata stray edge must be non-actionable")
	}
	// operational stray mapping → actionable
	if !StrayCandidateActionable("stray:kube_persistentvolume_capacity_bytes/9b1b337f8ec3 ~> i|c|n|PVC|x|u") {
		t.Error("operational stray must be actionable")
	}
	// non-stray candidates (trace topology, audit hypothesis) → always actionable
	if !StrayCandidateActionable("trace-call:a->b") {
		t.Error("trace candidate must be actionable")
	}
}
