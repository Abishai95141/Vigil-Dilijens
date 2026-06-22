// Command conntrack-agent is the Phase A flow-lane collector exposer (doc 15 §4):
// a tiny HTTP server that serves THIS node's conntrack table so obsd can scrape it
// via the API-server node proxy — the same pattern obsd already uses for cAdvisor
// and node-exporter. It runs as a hostNetwork DaemonSet, so the conntrack table it
// reads is the node's own.
//
// Two sources, same wire format. The PRIMARY source is /proc/net/nf_conntrack (the
// classic path; works on kernels built with CONFIG_NF_CONNTRACK_PROCFS=y, e.g. kind/
// LinuxKit). When that proc file is absent — modern kernels (e.g. AWS Ubuntu 6.x) ship
// `# CONFIG_NF_CONNTRACK_PROCFS is not set` — the agent FALLS BACK to the conntrack
// netlink interface (CTNETLINK) and FORMATS each TCP flow into the exact
// /proc/net/nf_conntrack text layout. So obsd's parser (internal/flow.ParseConntrack)
// is unchanged and byte-compatible across both substrates.
//
// It does NOT map or interpret — DNAT recovery + IP→CEI mapping happen in obsd
// (internal/flow), where the identity informer lives. The agent stays the thinnest
// possible privileged surface. CGO-free; the netlink path is Linux-only (build-tagged),
// with a stub on other platforms so the package still compiles in CI on macOS.
package main

import (
	"bytes"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := flag.String("addr", ":9111", "listen address")
	path := flag.String("path", "/proc/net/nf_conntrack", "conntrack proc path (primary source; node's own under hostNetwork)")
	flag.Parse()

	mux := http.NewServeMux()

	// /conntrack: stream the proc file if present; else render the netlink table in
	// the same /proc/net/nf_conntrack text format. The netlink render is buffered so a
	// mid-render error can still surface as a clean 503 (no half-written body).
	mux.HandleFunc("/conntrack", func(w http.ResponseWriter, r *http.Request) {
		if f, err := os.Open(*path); err == nil {
			defer f.Close()
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if _, err := io.Copy(w, f); err != nil {
				log.Printf("conntrack-agent: proc copy: %v", err)
			}
			return
		}
		var buf bytes.Buffer
		if err := netlinkConntrack(&buf); err != nil {
			http.Error(w, "conntrack netlink: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	})

	// /healthz: readable proc file OR a reachable netlink socket = healthy.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if f, err := os.Open(*path); err == nil {
			f.Close()
			_, _ = io.WriteString(w, "ok\n")
			return
		}
		if err := netlinkAvailable(); err != nil {
			http.Error(w, "conntrack unreadable (proc absent, netlink: "+err.Error()+")", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})

	source := "proc:" + *path
	if _, err := os.Stat(*path); err != nil {
		source = "netlink (proc absent: " + err.Error() + ")"
	}
	log.Printf("conntrack-agent: serving %s on %s [source=%s]", *path, *addr, source)
	srv := &http.Server{Addr: *addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("conntrack-agent: %v", err)
	}
}
