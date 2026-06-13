package flow

import "strings"

// FlowClass is the pre-resolution classification of a conntrack row.
type FlowClass uint8

const (
	// ClassCandidate: a real pod→pod app flow whose caller IP is recoverable. The
	// resolver decides whether both ends map to known workloads.
	ClassCandidate FlowClass = iota
	// ClassSnatMasked: the caller was SNAT'd to a node bridge gateway (10.244.x.1),
	// so the real initiator is unrecoverable from this node's table. Counted, never
	// guessed — the honest cross-node coverage gap.
	ClassSnatMasked
	// ClassUnreplied: only the original tuple is present (no backend recovered yet).
	ClassUnreplied
	// ClassInfra: a control-plane / DNS / host-networked flow, excluded by policy.
	ClassInfra
)

// FlowObservation is a single conntrack row reduced to a directed call:
// caller dialed servicePort, callee is the real (post-DNAT) backend. Direction is
// the TCP initiator: caller→callee. Cause, when a callee degrades, propagates in
// REVERSE along this edge (see cascade.go) — the edge is a call, not a cause.
type FlowObservation struct {
	Caller      string // original-tuple source IP (the initiator)
	Callee      string // reply-tuple source IP (the post-DNAT backend) — the load-bearing recovery
	ServicePort int    // original-tuple dport (the stable service port the caller dialed)
	ViaVIP      string // original-tuple dst when it differs from Callee (a ClusterIP breadcrumb, never an identity)
	State       string
	Class       FlowClass
}

// infraPorts are control-plane / DNS / host service ports excluded by policy.
// None collide with the boutique app ports (80,3550,5000,5050,7000,7070,8080,
// 9555,50051,6379).
var infraPorts = map[int]bool{
	53: true, 9153: true, 8181: true, // DNS / CoreDNS
	6443: true, 2379: true, 2380: true, 2381: true, // apiserver / etcd
	10250: true, 10256: true, 10257: true, 10259: true, // kubelet / kube-proxy / scheduler / controller
	9100: true, // node-exporter
}

// Recover applies DNAT recovery + classification to one parsed Conn. The single
// rule — caller = original source, callee = REPLY source, servicePort = original
// dport — is correct for both DNAT'd ClusterIP calls (origDst is the VIP, replySrc
// is the backend) and direct pod-to-pod calls (origDst == replySrc), so no row is
// special-cased.
func Recover(c Conn) FlowObservation {
	o := FlowObservation{
		Caller:      c.OrigSrc,
		Callee:      c.ReplySrc,
		ServicePort: c.OrigDport,
		State:       c.State,
	}
	if c.OrigDst != "" && c.OrigDst != c.ReplySrc {
		o.ViaVIP = c.OrigDst // a service VIP was DNAT'd to the backend
	}
	switch {
	case c.Unreplied || c.ReplySrc == "":
		o.Class = ClassUnreplied
	case infraPorts[c.OrigDport]:
		o.Class = ClassInfra
	case isHostOrLoopback(o.Caller) || isHostOrLoopback(o.Callee):
		o.Class = ClassInfra
	case isNodeBridge(o.Caller):
		o.Class = ClassSnatMasked
	default:
		o.Class = ClassCandidate
	}
	return o
}

// isNodeBridge reports whether an IP is a pod-CIDR node gateway (10.244.x.1) —
// the SNAT address a cross-node caller is masked behind.
func isNodeBridge(ip string) bool {
	return strings.HasPrefix(ip, "10.244.") && strings.HasSuffix(ip, ".1")
}

// isHostOrLoopback reports whether an IP is a kind host node (172.19.x) or
// loopback (127.x) — never a resolvable pod.
func isHostOrLoopback(ip string) bool {
	return strings.HasPrefix(ip, "172.19.") || strings.HasPrefix(ip, "127.")
}
