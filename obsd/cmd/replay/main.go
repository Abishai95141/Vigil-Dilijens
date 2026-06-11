// Command replay is the harness replay runner (doc 11 §3.1, doc 05 M5): it
// re-runs a recorded bundle — readings (qss segments), resolved bars per epoch,
// the pinned graph release, and the captured parameter set — through the REAL
// observation and detection components, and verifies that every recorded
// evaluation tick reproduces byte-identically (digest equality).
//
// Exit status: 0 = every tick matched; 1 = any mismatch or error. A mismatch is
// a determinism violation and is never downgraded to a warning.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay/export"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	var (
		bundle      = fs.String("bundle", "", "path to a replay bundle (manifest.json + bars-*.json + segments/)")
		ontology    = fs.String("ontology", "ontology/graph/k8s_signal_kg.json", "ontology KG release (must match the bundle's pinned graph version)")
		overlays    = fs.String("overlays", "ontology/graph/overlays", "authored overlay dir merged into the ontology")
		outDir      = fs.String("out", "", "write each replayed tick's canonical JSON here (for diffing)")
		parquetOut  = fs.String("export-parquet", "", "export the bundle's readings to this Parquet file (the Python-harness bridge)")
		quiet       = fs.Bool("q", false, "summary only (no per-tick lines)")
		showVersion = fs.Bool("version", false, "print version and exit")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintln(out, version.String())
		return nil
	}
	if *bundle == "" {
		return fmt.Errorf("-bundle is required")
	}

	g, err := graph.LoadWithOverlays(*ontology, *overlays)
	if err != nil {
		return fmt.Errorf("load ontology: %w", err)
	}

	rep, err := replay.Run(replay.Options{BundleDir: *bundle, Graph: g, OutDir: *outDir})
	if err != nil {
		return err
	}

	m := rep.Manifest
	fmt.Fprintf(out, "replay bundle %s\n", *bundle)
	fmt.Fprintf(out, "  pinned: graph %s · params v%s (%s) · cluster %s · captured %s\n",
		short(m.GraphVersion), m.ParamsVersion, m.Profile, m.ClusterID, m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(out, "  contents: %v\n  absent (stated): %v\n", m.Contents, m.Absent)
	fmt.Fprintf(out, "  segments=%d streams=%d samples=%d ticks=%d\n", rep.Segments, rep.Streams, rep.Samples, len(rep.Ticks))
	if rep.UnsealedTail {
		fmt.Fprintln(out, "  note: an unsealed .active segment was ignored (capture not closed cleanly)")
	}
	if !*quiet {
		for _, tk := range rep.Ticks {
			mark := "ok"
			if !tk.Match {
				mark = "MISMATCH"
			}
			fmt.Fprintf(out, "  tick %s  bars-epoch=%d  fingerprints=%d findings=%d  digest %s…  %s\n",
				tk.EvalNow.UTC().Format("15:04:05.000Z"), tk.BarsEpoch, tk.Fingerprints, tk.Findings,
				tk.ReplayedDigest[:16], mark)
		}
	}

	if *parquetOut != "" {
		rows, err := export.Parquet(*bundle, *parquetOut)
		if err != nil {
			return fmt.Errorf("parquet export: %w", err)
		}
		fmt.Fprintf(out, "  exported %d readings -> %s\n", rows, *parquetOut)
	}

	if rep.Mismatches > 0 {
		return fmt.Errorf("DETERMINISM VIOLATION: %d of %d ticks did not reproduce byte-identically", rep.Mismatches, len(rep.Ticks))
	}
	fmt.Fprintf(out, "  verdict: all %d ticks replayed byte-identically (doc 05 §3.5 holds)\n", len(rep.Ticks))
	return nil
}

func short(v string) string {
	if len(v) > 19 {
		return v[:19] + "…"
	}
	return v
}
