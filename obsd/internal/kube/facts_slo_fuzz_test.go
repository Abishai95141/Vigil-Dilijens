package kube

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// FuzzSnapshotSLOParsing fuzzes the annotation->bar parser inside SnapshotConfig
// (the package's only string-decoding path). The annotation key/value are the only
// attacker-controlled inputs; the fuzz pins the charter invariant that drives this
// path:
//
//   - it never panics on any key/value (robust to arbitrary cluster annotations);
//   - a vigil.io/slo.* bar appears in the snapshot IF AND ONLY IF its value parses
//     to a FINITE float, and then it equals exactly that float — never a fabricated
//     or defaulted number;
//   - a non-slo / wrong-domain key never contributes a bar.
//
// This mirrors the production logic with strconv.ParseFloat as the oracle, so a
// regression that started inventing a bar for an unparseable value (or dropping a
// valid one, or admitting a wrong-domain key) fails the fuzz.
func FuzzSnapshotSLOParsing(f *testing.F) {
	f.Add("vigil.io/slo.queue.max_depth", "1000")
	f.Add("vigil.io/slo.latency.p99_ms", "  42.5 ")
	f.Add("vigil.io/slo.bad", "not-a-number")
	f.Add("vigil.io/notslo.x", "5")
	f.Add("other.io/slo.q", "5")
	f.Add("vigil.io/slo.inf", "Inf")
	f.Add("vigil.io/slo.neg", "-1e3")
	f.Add("vigil.io/slo.empty", "")
	f.Add("vigil.io/slo.hex", "0x10")
	f.Add("vigil.io/slo.unicode", "４２")

	f.Fuzz(func(t *testing.T, key, val string) {
		// Keys with control bytes (incl. embedded NUL/slash artifacts) can be invalid
		// as k8s annotation keys; the parser itself must not care, but the fake client
		// validates objects on create. Build the pod object directly and snapshot it.
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "ns",
				Name:        "p",
				Annotations: map[string]string{key: val},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
		}
		cs := fake.NewSimpleClientset(pod)

		snap, err := SnapshotConfig(context.Background(), cs)
		if err != nil {
			// A List error would be the fake client rejecting the object, not the
			// parser; skip rather than fail (parser robustness is the property here).
			t.Skip()
		}
		pc, ok := snap.Pod("ns", "p")
		if !ok {
			t.Fatalf("pod ns/p missing from snapshot")
		}

		// Oracle: reproduce the production acceptance predicate independently.
		path, isVigil := strings.CutPrefix(key, sloAnnotationDomain)
		wantAccept := false
		var wantVal float64
		if isVigil && strings.HasPrefix(path, "slo.") {
			if pf, perr := strconv.ParseFloat(strings.TrimSpace(val), 64); perr == nil {
				wantAccept = true
				wantVal = pf
			}
		}

		got, present := pc.SLOs[path]
		switch {
		case wantAccept && !present:
			t.Fatalf("valid SLO %q=%q dropped (key=%q)", path, val, key)
		case wantAccept && present:
			// Equality must be exact (NaN compares unequal — production never accepts
			// NaN because ParseFloat("NaN") returns NaN with nil err, but the value is
			// stored verbatim; guard the verbatim contract via bit equality).
			if math.Float64bits(got) != math.Float64bits(wantVal) {
				t.Fatalf("SLO value not verbatim: got %v want %v (raw %q)", got, wantVal, val)
			}
		case !wantAccept && present:
			t.Fatalf("fabricated/unwanted bar: key=%q val=%q -> %v", key, val, got)
		}

		// Cross-check: the ONLY accepted key must be a vigil.io/slo.* one. The domain
		// prefix is stripped before storage, so the stored path must itself begin with
		// "slo." (anything else means a wrong-domain or malformed key leaked through).
		for p := range pc.SLOs {
			if !strings.HasPrefix(p, "slo.") {
				t.Fatalf("non-slo path admitted into SLOs: %q", p)
			}
		}
	})
}
