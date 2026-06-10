// Command replay is the harness replay runner (doc 11 §3.1): it re-runs a recorded
// bundle — (readings, graph version, topology snapshot with edge validity, resolved
// bars, parameter set) — through the real observation and detection components to
// produce findings deterministically, for byte-identical regression.
//
// This is a Phase-0a placeholder. The replay substrate is a Phase-0b deliverable
// (doc 11 M1, doc 05 M5); the command exists now so the binary path and CLI surface
// are stable, and it is explicit that it is not yet wired.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/version"
)

func main() {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	bundle := fs.String("bundle", "", "path to a replay bundle (Parquet readings + JSON manifest)")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println(version.String())
		return
	}
	fmt.Fprintln(os.Stderr, "replay: not yet implemented (doc 11 M1 / doc 05 M5 — Phase 0b)")
	if *bundle != "" {
		fmt.Fprintf(os.Stderr, "replay: requested bundle %q will be supported once the replay substrate lands\n", *bundle)
	}
	os.Exit(0)
}
