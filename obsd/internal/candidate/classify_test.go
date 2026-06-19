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
