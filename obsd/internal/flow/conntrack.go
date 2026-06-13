package flow

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Conn is one parsed conntrack row: the ORIGINAL tuple (what the initiator dialed)
// and the REPLY tuple (the post-DNAT backend that answered). The reply tuple is
// the load-bearing part — kube-proxy DNATs a ClusterIP to a real pod at SYN, so
// the reply source is the real callee even though the original destination is a
// service VIP.
type Conn struct {
	Proto string
	State string // ESTABLISHED, TIME_WAIT, SYN_SENT, ... ("" if absent)

	OrigSrc, OrigDst     string
	OrigSport, OrigDport int

	ReplySrc, ReplyDst     string
	ReplySport, ReplyDport int

	Assured   bool
	Unreplied bool
}

// ParseConntrack reads the /proc/net/nf_conntrack format (as produced by
// `cat /proc/net/nf_conntrack`). It keeps tcp rows only. The first src/dst/sport/
// dport quadruple on a line is the original tuple; the second is the reply tuple.
// Robust to interleaved [ASSURED]/[UNREPLIED]/mark=/zone=/use= tokens and to
// UNREPLIED rows that carry only the original tuple.
func ParseConntrack(r io.Reader) ([]Conn, error) {
	var out []Conn
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if c, ok := parseLine(line); ok {
			out = append(out, c)
		}
	}
	return out, sc.Err()
}

func parseLine(line string) (Conn, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Conn{}, false
	}
	var c Conn
	// Proto: the l4 protocol name is a bare token (e.g. "tcp") among the leading
	// non key=value fields. Find it; bail unless tcp.
	seenSrc, seenDst, seenSport, seenDport := false, false, false, false
	for _, f := range fields {
		switch {
		case f == "[ASSURED]":
			c.Assured = true
		case f == "[UNREPLIED]":
			c.Unreplied = true
		case strings.Contains(f, "="):
			k, v, _ := strings.Cut(f, "=")
			switch k {
			case "src":
				if !seenSrc {
					c.OrigSrc, seenSrc = v, true
				} else {
					c.ReplySrc = v
				}
			case "dst":
				if !seenDst {
					c.OrigDst, seenDst = v, true
				} else {
					c.ReplyDst = v
				}
			case "sport":
				if !seenSport {
					c.OrigSport, seenSport = atoi(v), true
				} else {
					c.ReplySport = atoi(v)
				}
			case "dport":
				if !seenDport {
					c.OrigDport, seenDport = atoi(v), true
				} else {
					c.ReplyDport = atoi(v)
				}
			}
		default:
			// Bare tokens: l3 name ("ipv4"), numbers, l4 name ("tcp"/"udp"), and the
			// TCP state (uppercase). Record proto and state.
			if f == "tcp" || f == "udp" || f == "icmp" {
				c.Proto = f
			} else if isUpperState(f) && c.State == "" {
				c.State = f
			}
		}
	}
	if c.Proto != "tcp" {
		return Conn{}, false
	}
	if !seenSrc || !seenDst {
		return Conn{}, false
	}
	if c.ReplySrc == "" {
		c.Unreplied = true // only the original tuple was present
	}
	return c, true
}

// isUpperState reports whether a bare token looks like a conntrack TCP state
// (ESTABLISHED, TIME_WAIT, SYN_SENT, FIN_WAIT, CLOSE_WAIT, LAST_ACK, ...).
func isUpperState(f string) bool {
	if len(f) < 5 {
		return false
	}
	for _, r := range f {
		if !(r >= 'A' && r <= 'Z' || r == '_') {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
