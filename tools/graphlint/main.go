package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	schema := flag.String("schema", "ontology/schema/kg.schema.json", "path to the KG JSON Schema")
	strict := flag.Bool("strict", false, "treat authoring gaps (e.g. phenomena without a span) as failures")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "graphlint — validate the ontology KG and report the authoring gap (doc 02 M3 / doc 14 A14)")
		fmt.Fprintln(os.Stderr, "usage: graphlint [-schema PATH] [-strict] [PATH ...]   (default PATH: ontology/graph)")
		flag.PrintDefaults()
	}
	flag.Parse()

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

	failed := false
	for _, f := range files {
		res, err := lintFile(sch, f)
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
		if res.refWarnTotal > 0 {
			fmt.Printf("      warn: %d owned_by_agent edge(s) reference an undefined Agent node (curation item, non-detection). e.g. %s\n",
				res.refWarnTotal, res.refWarnings[0])
		}
		fmt.Print(res.gap.String())
		if *strict && (res.gap.hasGaps() || res.refWarnTotal > 0) {
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}
