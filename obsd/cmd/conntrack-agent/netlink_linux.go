//go:build linux

package main

import (
	"fmt"
	"io"

	"github.com/ti-mo/conntrack"
)

// netlinkAvailable reports whether the conntrack netlink (CTNETLINK) socket can be
// opened — the liveness signal used by /healthz when the proc file is absent.
func netlinkAvailable() error {
	c, err := conntrack.Dial(nil)
	if err != nil {
		return err
	}
	return c.Close()
}

// netlinkConntrack dumps the kernel conntrack table over netlink and writes each TCP
// flow to w in the /proc/net/nf_conntrack text format, so obsd's parser
// (internal/flow.ParseConntrack) consumes it unchanged. The reply tuple is emitted
// verbatim — it carries the post-DNAT backend that obsd uses to recover the real callee.
func netlinkConntrack(w io.Writer) error {
	c, err := conntrack.Dial(nil)
	if err != nil {
		return err
	}
	defer c.Close()

	flows, err := c.Dump(nil)
	if err != nil {
		return err
	}

	for i := range flows {
		f := &flows[i]
		o, rep := f.TupleOrig, f.TupleReply
		if o.Proto.Protocol != 6 { // tcp only — obsd keeps tcp rows
			continue
		}
		l3, l3n := "ipv4", "2"
		if o.IP.IsIPv6() {
			l3, l3n = "ipv6", "10"
		}
		// header: l3 + l3num + l4 + l4num [+ state]
		if _, err := fmt.Fprintf(w, "%s %s tcp 6", l3, l3n); err != nil {
			return err
		}
		if f.ProtoInfo.TCP != nil {
			if name := tcpStateName(f.ProtoInfo.TCP.State); name != "" {
				if _, err := fmt.Fprintf(w, " %s", name); err != nil {
					return err
				}
			}
		}
		// original tuple, then reply tuple (the order obsd's parser expects)
		if _, err := fmt.Fprintf(w, " src=%s dst=%s sport=%d dport=%d",
			o.IP.SourceAddress, o.IP.DestinationAddress, o.Proto.SourcePort, o.Proto.DestinationPort); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, " src=%s dst=%s sport=%d dport=%d",
			rep.IP.SourceAddress, rep.IP.DestinationAddress, rep.Proto.SourcePort, rep.Proto.DestinationPort); err != nil {
			return err
		}
		if f.Status.Assured() {
			if _, err := io.WriteString(w, " [ASSURED]"); err != nil {
				return err
			}
		}
		if !f.Status.SeenReply() {
			if _, err := io.WriteString(w, " [UNREPLIED]"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

// tcpStateName maps the netfilter conntrack TCP state enum (nf_conntrack_tcp.h) to the
// uppercase name the proc format prints. Returns "" for NONE/unknown (state token omitted).
func tcpStateName(s uint8) string {
	switch s {
	case 1:
		return "SYN_SENT"
	case 2:
		return "SYN_RECV"
	case 3:
		return "ESTABLISHED"
	case 4:
		return "FIN_WAIT"
	case 5:
		return "CLOSE_WAIT"
	case 6:
		return "LAST_ACK"
	case 7:
		return "TIME_WAIT"
	case 8:
		return "CLOSE"
	case 9:
		return "SYN_SENT2"
	default:
		return ""
	}
}
