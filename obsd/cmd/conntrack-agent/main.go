// Command conntrack-agent is the Phase A flow-lane collector exposer (doc 15 §4):
// a tiny HTTP server that serves THIS node's conntrack table so obsd can scrape it
// via the API-server node proxy — the same pattern obsd already uses for cAdvisor
// and node-exporter. It runs as a hostNetwork DaemonSet, so /proc/net/nf_conntrack
// (a per-net-namespace file) is the node's own table.
//
// It does NOT parse, map, or interpret — parsing + DNAT recovery + IP→CEI mapping
// happen in obsd (internal/flow), where the identity informer lives. The agent is
// the thinnest possible privileged surface: read a file, write it to the wire.
// CGO-free, stdlib only.
package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := flag.String("addr", ":9111", "listen address")
	path := flag.String("path", "/proc/net/nf_conntrack", "conntrack table path (node's own under hostNetwork)")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/conntrack", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(*path)
		if err != nil {
			http.Error(w, "conntrack open: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("conntrack-agent: copy: %v", err)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(*path)
		if err != nil {
			http.Error(w, "conntrack unreadable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		f.Close()
		_, _ = io.WriteString(w, "ok\n")
	})

	log.Printf("conntrack-agent: serving %s on %s", *path, *addr)
	srv := &http.Server{Addr: *addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("conntrack-agent: %v", err)
	}
}
