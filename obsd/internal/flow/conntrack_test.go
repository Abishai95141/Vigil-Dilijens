package flow

import (
	"os"
	"testing"
)

// TestParseAndRecover is the golden parser+DNAT test against a frozen capture of
// REAL boutique conntrack rows. It pins the load-bearing recovery rule (callee =
// reply-tuple source) and the classification of SNAT-masked / infra noise. It is
// network-free and deterministic.
func TestParseAndRecover(t *testing.T) {
	f, err := os.Open("testdata/nf_conntrack_sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conns, err := ParseConntrack(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 11 {
		t.Fatalf("parsed %d tcp rows, want 11", len(conns))
	}

	type want struct {
		caller, callee string
		port           int
		viaVIP         string
		class          FlowClass
	}
	wants := []want{
		{"10.244.1.6", "10.244.1.3", 3550, "10.96.23.58", ClassCandidate},   // frontend -> catalog (DNAT)
		{"10.244.3.3", "10.244.1.3", 3550, "10.96.23.58", ClassCandidate},   // checkout -> catalog (DNAT)
		{"10.244.3.7", "10.244.1.3", 3550, "10.96.23.58", ClassCandidate},   // recommendation -> catalog (DNAT)
		{"10.244.1.2", "10.244.1.6", 80, "10.96.102.223", ClassCandidate},   // loadgen -> frontend (DNAT)
		{"10.244.1.7", "10.244.1.4", 6379, "10.96.5.184", ClassCandidate},   // cart -> redis (DNAT)
		{"10.244.3.3", "10.244.3.6", 5000, "10.96.98.134", ClassCandidate},  // checkout -> email (port remap 5000->8080)
		{"10.244.3.3", "10.244.1.7", 7070, "10.96.191.118", ClassCandidate}, // checkout -> cart (DNAT)
		{"10.244.3.3", "10.244.1.3", 3550, "", ClassCandidate},              // checkout -> catalog (direct: origDst==replySrc)
		{"10.244.1.1", "10.244.1.3", 3550, "", ClassSnatMasked},             // SNAT-masked caller (node bridge)
		{"10.244.0.4", "172.19.0.3", 443, "10.96.0.1", ClassInfra},          // kube API (callee = host IP)
		{"127.0.0.1", "127.0.0.1", 2381, "", ClassInfra},                    // etcd loopback
	}
	if len(wants) != len(conns) {
		t.Fatalf("test wants %d, parsed %d", len(wants), len(conns))
	}
	for i, c := range conns {
		o := Recover(c)
		w := wants[i]
		if o.Caller != w.caller || o.Callee != w.callee || o.ServicePort != w.port || o.ViaVIP != w.viaVIP || o.Class != w.class {
			t.Errorf("row %d: got caller=%s callee=%s port=%d via=%q class=%d; want caller=%s callee=%s port=%d via=%q class=%d",
				i, o.Caller, o.Callee, o.ServicePort, o.ViaVIP, o.Class, w.caller, w.callee, w.port, w.viaVIP, w.class)
		}
	}

	// Candidate / SNAT-masked / infra tally — the coverage accounting must be exact.
	var cand, snat, infra int
	for _, c := range conns {
		switch Recover(c).Class {
		case ClassCandidate:
			cand++
		case ClassSnatMasked:
			snat++
		case ClassInfra:
			infra++
		}
	}
	if cand != 8 || snat != 1 || infra != 2 {
		t.Errorf("class tally got candidate=%d snat=%d infra=%d; want 8/1/2", cand, snat, infra)
	}
}
