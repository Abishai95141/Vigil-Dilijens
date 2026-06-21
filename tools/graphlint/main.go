package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	schema := flag.String("schema", "ontology/schema/kg.schema.json", "path to the KG JSON Schema")
	overlays := flag.String("overlays", "ontology/graph/overlays", "directory of authored overlays (spans, threshold rules); empty disables")
	strict := flag.Bool("strict", false, "treat authoring gaps (e.g. phenomena without a span) as failures")
	base := flag.String("base", "ontology/graph/k8s_signal_kg.json", "base KG file (for -print-version / -release)")
	printVersion := flag.Bool("print-version", false, "print the content-hash pin for base+overlays and exit (for cutting a release, doc 12 M1)")
	release := flag.String("release", "", "verify a release manifest still matches the graph (immutability, doc 12 M1); non-zero exit on drift")
	releaseLatest := flag.String("release-latest", "", "verify the NEWEST release manifest in this dir (by created date) still matches the graph; no-op if the dir is empty")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "graphlint — validate the ontology KG + authored overlays, report the authoring gap, and verify graph releases (doc 02 M3 / 12 M1 / doc 14 A14)")
		fmt.Fprintln(os.Stderr, "usage: graphlint [-schema PATH] [-overlays DIR] [-strict] [-print-version] [-release MANIFEST] [PATH ...]   (default PATH: ontology/graph)")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Release-engineering modes run standalone and exit.
	if *printVersion {
		v, err := computeGraphVersion(*base, *overlays)
		if err != nil {
			fmt.Fprintln(os.Stderr, "graphlint:", err)
			os.Exit(2)
		}
		fmt.Println(v)
		return
	}
	manifest := *release
	if *releaseLatest != "" {
		p, err := latestManifestPath(*releaseLatest)
		if err != nil {
			fmt.Fprintln(os.Stderr, "graphlint:", err)
			os.Exit(2)
		}
		if p == "" {
			fmt.Printf("no release manifests in %s — nothing to verify\n", *releaseLatest)
			return
		}
		manifest = p
	}
	if manifest != "" {
		r, err := verifyRelease(manifest, *base, *overlays)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL  release %s\n      %v\n", manifest, err)
			os.Exit(1)
		}
		fmt.Printf("PASS  release %s — graph matches %s\n", r.Name, r.GraphVersion)
		return
	}

	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{"ontology/graph"}
	}

	sch, err := compileSchema(*schema)
	if err != nil {
		fmt.Fprintln(os.Stderr, "graphlint:", err)
		os.Exit(2)
	}
	files, err := collect(roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, "graphlint:", err)
		os.Exit(2)
	}
	if len(files) == 0 {
		fmt.Println("graphlint: no graph files found under", roots)
		return
	}
	ovls, err := loadOverlays(*overlays)
	if err != nil {
		fmt.Fprintln(os.Stderr, "graphlint:", err)
		os.Exit(2)
	}

	failed := false
	for _, f := range files {
		res, err := lintFile(sch, f, ovls)
		if err != nil {
			fmt.Printf("ERROR %s\n      %v\n", f, err)
			failed = true
			continue
		}
		status := "PASS"
		if !res.ok() {
			status = "FAIL"
			failed = true
		}
		fmt.Printf("%s  %s\n", status, f)
		for _, e := range res.schemaErrors {
			fmt.Printf("      schema: %s\n", e)
		}
		for _, e := range res.refErrors {
			fmt.Printf("      ref: %s\n", e)
		}
		for _, e := range res.overlayErrors {
			fmt.Printf("      overlay: %s\n", e)
		}
		for _, e := range res.structErrors {
			fmt.Printf("      structuring: %s\n", e)
		}
		if res.refWarnTotal > 0 {
			fmt.Printf("      warn: %d owned_by_agent edge(s) reference an undefined Agent node (curation item, non-detection). e.g. %s\n",
				res.refWarnTotal, res.refWarnings[0])
		}
		for _, o := range res.overlays {
			fmt.Printf("      overlay applied: %s (%s v%d, author: %s) — %d spans, %d rules\n",
				o.Overlay, o.path, o.Version, o.Author, len(o.Spans), len(o.Rules))
		}
		fmt.Print(res.gap.String())
		fmt.Print(res.structuring.String())
		if *strict && (res.gap.hasGaps() || res.refWarnTotal > 0) {
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}
