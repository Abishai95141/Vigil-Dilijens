package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	schema := flag.String("schema", "ontology/schema/graph.schema.json", "path to the ontology JSON Schema")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "graphlint — validate ontology release YAML against the authoring schema (doc 02 M3)")
		fmt.Fprintln(os.Stderr, "usage: graphlint [-schema PATH] [PATH ...]   (default PATH: ontology/graph)")
		flag.PrintDefaults()
	}
	flag.Parse()

	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{"ontology/graph"}
	}

	results, ok, err := lint(*schema, roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, "graphlint:", err)
		os.Exit(2)
	}
	if len(results) == 0 {
		fmt.Println("graphlint: no ontology documents found under", roots)
		return
	}
	for _, r := range results {
		if r.err == nil {
			fmt.Printf("PASS  %s\n", r.path)
		} else {
			fmt.Printf("FAIL  %s\n      %v\n", r.path, r.err)
		}
	}
	if !ok {
		os.Exit(1)
	}
}
