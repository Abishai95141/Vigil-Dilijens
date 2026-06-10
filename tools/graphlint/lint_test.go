package main

import "testing"

const schemaPath = "../../ontology/schema/graph.schema.json"

func TestValidDocPasses(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	if err := validateFile(sch, "testdata/valid.yaml"); err != nil {
		t.Errorf("expected valid.yaml to pass, got: %v", err)
	}
}

func TestInvalidDocFails(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	if err := validateFile(sch, "testdata/invalid.yaml"); err == nil {
		t.Error("expected invalid.yaml to fail validation, got nil")
	}
}

// The committed example release must always validate against the committed schema —
// a regression guard tying the two together.
func TestExampleOntologyValidates(t *testing.T) {
	results, ok, err := lint(schemaPath, []string{"../../ontology/graph"})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least the example ontology document")
	}
	if !ok {
		for _, r := range results {
			if r.err != nil {
				t.Errorf("FAIL %s: %v", r.path, r.err)
			}
		}
	}
}
