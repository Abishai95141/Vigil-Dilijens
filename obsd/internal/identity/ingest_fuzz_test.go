package identity

import (
	"strings"
	"testing"
	"time"
)

// Go Fuzz over the cAdvisor / kube-state-metrics metric-exposition INGEST — the
// boundary where a heterogeneous exporter's labels become (or fail to become) a CEI
// (doc 03 §3.3, doc 14 §3.2). The exposition is adversarial: a misbehaving or
// malicious exporter, a future dialect, or a corrupt scrape can emit any metric name
// with any label keys and values (empty, the reserved separator, control bytes, huge
// strings, identity labels that disagree with each other).
//
// The charter invariant the fuzzer enforces, on EVERY input:
//
//  1. Normalize never panics (a malformed row cannot crash ingest).
//  2. It never MIS-MINTS a CEI: a resolved CEI's join key is well-formed (exactly the
//     field count for its layer, separator-clean), it round-trips through Key(), and
//     its UID was SOURCED (from the time-aware lookup, or a uid label, or derived as
//     <podUID>/<container>) — never invented from thin air.
//  3. A non-resolved outcome (drop/quarantine) carries a stated Reason and NO CEI —
//     honest partial coverage, never a silent guess.
//  4. The result class is exactly Resolved | Dropped | Quarantined (never a fourth).

// fuzzLookup is a fixed, known control-plane view for the fuzzer. Its UIDs are the
// ONLY pod/node/pvc UIDs that may ever legitimately appear in a resolved CEI; any
// other UID in a resolved CEI is a mis-mint (an invented identity).
type fuzzLookup struct{}

const (
	fuzzPodUID  = "uid-fuzz-pod"
	fuzzNodeUID = "uid-fuzz-node"
	fuzzPVCUID  = "uid-fuzz-pvc"
)

func (fuzzLookup) PodUID(ns, name string, _ time.Time) (string, bool) {
	if ns == "ns-known" && name == "pod-known" {
		return fuzzPodUID, true
	}
	return "", false
}

func (fuzzLookup) NodeUID(name string, _ time.Time) (string, bool) {
	if name == "node-known" {
		return fuzzNodeUID, true
	}
	return "", false
}

func (fuzzLookup) PVCUID(ns, name string, _ time.Time) (string, bool) {
	if ns == "ns-known" && name == "pvc-known" {
		return fuzzPVCUID, true
	}
	return "", false
}

// sourcedUID reports whether uid is one the fuzzer's control plane could legitimately
// have produced: a known lookup UID, a uid carried on a KSM row's label (any value, as
// long as it is what we passed in), or a container UID derived as "<sourced>/<name>".
// The fuzz body builds the set of label-carried uids it actually supplied.
func sourcedUID(uid string, labelUIDs map[string]bool) bool {
	if uid == fuzzPodUID || uid == fuzzNodeUID || uid == fuzzPVCUID {
		return true
	}
	if labelUIDs[uid] {
		return true
	}
	// container CEI: "<podUID>/<container>" — the prefix before the LAST '/' must be a
	// sourced pod uid and the suffix a non-empty container name. (Pod uids in this
	// fuzzer never contain '/', so SplitN on the first '/' is exact.)
	if i := strings.Index(uid, "/"); i >= 0 {
		prefix, suffix := uid[:i], uid[i+1:]
		if suffix == "" {
			return false
		}
		if prefix == fuzzPodUID || labelUIDs[prefix] {
			return true
		}
	}
	return false
}

// FuzzNormalizeIngest fuzzes the ingest boundary. The corpus seeds the known-good
// rows and the known traps; the fuzzer mutates metric names and label values around
// them. We reconstruct a Series from fuzzed scalars rather than fuzzing a map directly
// (Go fuzz supports only scalar args), driving the family, metric, and the label
// values most likely to matter for identity.
func FuzzNormalizeIngest(f *testing.F) {
	// Seed corpus: each tuple is
	// (familySel, metric, namespace, pod, container, uid, node, image, name, pvc, sourceNode, sourcePodNS, sourcePodName)
	seeds := []struct {
		fam                                             uint8
		metric                                          string
		ns, pod, container, uid, node, image, name, pvc string
		sourceNode, sourcePodNS, sourcePodName          string
	}{
		// cAdvisor: named container on a known pod (resolves to a container CEI).
		{0, "container_cpu_usage_seconds_total", "ns-known", "pod-known", "app", "", "", "img:v1", "", "", "worker", "", ""},
		// cAdvisor: pod aggregate (container="" image="" name="").
		{0, "container_memory_working_set_bytes", "ns-known", "pod-known", "", "", "", "", "", "", "worker", "", ""},
		// cAdvisor: pause sandbox (container="POD").
		{0, "container_cpu_usage_seconds_total", "ns-known", "pod-known", "POD", "", "", "", "", "", "worker", "", ""},
		// cAdvisor: machine row (node identity from scrape source).
		{0, "machine_cpu_cores", "", "", "", "", "", "", "", "", "node-known", "", ""},
		// cAdvisor: unknown pod (quarantine).
		{0, "container_cpu_usage_seconds_total", "ns-x", "ghost", "app", "", "", "", "", "", "worker", "", ""},
		// KSM: kube_pod_info with a uid label (direct mint).
		{1, "kube_pod_info", "ns-known", "pod-known", "", "uid-label-1", "node-known", "", "", "", "", "", ""},
		// KSM: container resource limits (container-scoped).
		{1, "kube_pod_container_resource_limits", "ns-known", "pod-known", "server", "uid-label-2", "", "", "", "", "", "", ""},
		// KSM: node row.
		{1, "kube_node_status_capacity", "", "", "", "", "node-known", "", "", "", "", "", ""},
		// KSM: deployment role row.
		{1, "kube_deployment_status_replicas", "ns-known", "", "", "", "", "", "web", "", "", "", ""},
		// KSM: PVC row.
		{1, "kube_persistentvolumeclaim_status_phase", "ns-known", "", "", "", "", "", "", "pvc-known", "", "", ""},
		// App: scrape-target identity.
		{3, "http_requests_total", "", "", "", "", "", "", "", "", "", "ns-known", "pod-known"},
		// Adversarial: separator injection into identity labels (must never mint).
		{0, "container_cpu_usage_seconds_total", "ns|evil", "pod|evil", "c|evil", "", "", "", "", "", "node|evil", "", ""},
		{1, "kube_pod_info", "ns-known", "pod-known", "", "uid|with|seps", "", "", "", "", "", "", ""},
		// Adversarial: unknown family selector.
		{9, "whatever_total", "ns-known", "pod-known", "app", "", "", "", "", "", "", "", ""},
		// Adversarial: empties everywhere.
		{0, "", "", "", "", "", "", "", "", "", "", "", ""},
	}
	for _, s := range seeds {
		f.Add(s.fam, s.metric, s.ns, s.pod, s.container, s.uid, s.node, s.image, s.name, s.pvc, s.sourceNode, s.sourcePodNS, s.sourcePodName)
	}

	n := NewNormalizer(cluster, fuzzLookup{})
	// A fixed, injected ingest instant — never time.Now (determinism + charter).
	fuzzAt := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	f.Fuzz(func(t *testing.T,
		famSel uint8, metric, ns, pod, container, uid, node, image, name, pvc, sourceNode, sourcePodNS, sourcePodName string,
	) {
		fam := []Family{FamilyCAdvisor, FamilyKSM, FamilyNodeExporter, FamilyApp}[int(famSel)%4]
		// Deliberately also exercise the unknown-family path on some inputs.
		if famSel >= 200 {
			fam = Family("exporter-from-the-future")
		}

		labels := map[string]string{}
		// Only populate a label key when its value is non-empty OR the dialect treats
		// "" as meaningful (container="" is the pod-aggregate signal), mirroring how a
		// real exposition omits absent labels. We set the identity-bearing keys for
		// every family so the fuzzer reaches the minting paths.
		set := func(k, v string) {
			if v != "" {
				labels[k] = v
			}
		}
		set("namespace", ns)
		set("pod", pod)
		labels["container"] = container // keep "" so the aggregate vs sandbox branch is reachable
		set("uid", uid)
		set("node", node)
		set("image", image)
		set("name", name)
		set("deployment", name)
		set("statefulset", name)
		set("daemonset", name)
		set("persistentvolumeclaim", pvc)
		set("resource", "memory")

		s := Series{
			Family:        fam,
			Metric:        metric,
			Labels:        labels,
			SourceNode:    sourceNode,
			SourcePodNS:   sourcePodNS,
			SourcePodName: sourcePodName,
			At:            fuzzAt,
			// No EventTime: receive-time join, the simplest path; the churn property
			// test covers EventTime-keyed late joins exhaustively.
		}

		// (1) Must not panic. (The call itself is the assertion; a panic fails the test.)
		r := n.Normalize(s)

		// The set of UIDs the input legitimately supplied via labels (KSM uid label).
		labelUIDs := map[string]bool{}
		if uid != "" && !strings.Contains(uid, sep) {
			labelUIDs[uid] = true
		}

		switch r.Outcome {
		case OutcomeResolved:
			cei := r.CEI
			key := cei.Key()

			// (2a) The join key must be separator-clean per field and round-trip.
			if cei.Layer == LayerInstance {
				parts := strings.Split(key, sep)
				if len(parts) != 6 || parts[0] != "i" {
					t.Fatalf("malformed instance CEI key %q from input %+v", key, s)
				}
				// Every identity coordinate is present (minting rejects empties).
				if cei.Kind == "" || cei.Name == "" || cei.UID == "" || cei.Cluster == "" {
					t.Fatalf("resolved instance CEI missing a coordinate: %+v (input %+v)", cei, s)
				}
				// (2b) The UID must have been SOURCED, never invented.
				if !sourcedUID(cei.UID, labelUIDs) {
					t.Fatalf("MIS-MINT: resolved CEI carries an unsourced UID %q (key %q) from input %+v",
						cei.UID, key, s)
				}
			} else if cei.Layer == LayerRole {
				parts := strings.Split(key, sep)
				if len(parts) != 5 || parts[0] != "r" {
					t.Fatalf("malformed role CEI key %q from input %+v", key, s)
				}
				if cei.RoleKey == "" || cei.Kind == "" || cei.Cluster == "" {
					t.Fatalf("resolved role CEI missing a coordinate: %+v (input %+v)", cei, s)
				}
			} else {
				t.Fatalf("resolved CEI has an invalid layer %d: %+v", cei.Layer, cei)
			}

			// (2c) Round-trip: Same() is reflexive on the resolved key and Key() is stable.
			if !cei.Same(cei) || cei.Key() != key {
				t.Fatalf("resolved CEI does not round-trip: %q", key)
			}
			// A resolved result must always name its family + map version (provenance).
			if r.Family == "" || r.MapVersion == "" {
				t.Fatalf("resolved result missing provenance: family=%q ver=%q", r.Family, r.MapVersion)
			}

		case OutcomeDropped, OutcomeQuarantined:
			// (3) Non-resolved: a stated reason, and absolutely no CEI leaks through.
			if r.Reason == ReasonNone {
				t.Fatalf("%s result without a stated reason (input %+v)", r.Outcome, s)
			}
			if r.CEI.Layer != 0 || r.CEI.Key() != "?"+sep+sep+sep {
				// A zero CEI's Key() is the unknown-layer sentinel; any real key here
				// would mean a guessed identity surfaced on a non-resolved row.
				if r.CEI.UID != "" || r.CEI.Name != "" || r.CEI.RoleKey != "" {
					t.Fatalf("%s result leaked a CEI: %+v (input %+v)", r.Outcome, r.CEI, s)
				}
			}

		default:
			t.Fatalf("unknown outcome %d (input %+v)", r.Outcome, s)
		}
	})
}
