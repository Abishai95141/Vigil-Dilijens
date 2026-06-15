package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	claimsPath   = "../../../corpus/validate-claim/claims.json"
	verdictsPath = "../../../corpus/validate-claim/verdicts.jsonl"
)

type corpusFile struct {
	Phenomena     map[string]string `json:"phenomena"`
	AuthoredLinks []AuthoredLink    `json:"authoredLinks"`
	Cases         []corpusCase      `json:"cases"`
}

type corpusCase struct {
	ID            string   `json:"id"`
	Category      string   `json:"category"`
	ExpectFlagged bool     `json:"expectFlagged"`
	Claim         string   `json:"claim"`
	Projected     []string `json:"projected"`
	Measured      []string `json:"measured"`
	Note          string   `json:"note"`
}

type verdictRec struct {
	ID                 string      `json:"id"`
	Category           string      `json:"category"`
	ExpectFlagged      bool        `json:"expectFlagged"`
	LabelledBestEffort bool        `json:"labelledBestEffort"`
	Full               verdictSide `json:"full"`
	Structural         verdictSide `json:"structural"`
}

type verdictSide struct {
	Flagged         bool     `json:"flagged"`
	MatchedAuthored bool     `json:"matchedAuthored"`
	Classes         []string `json:"classes"`
}

func loadCorpus(t *testing.T) corpusFile {
	t.Helper()
	b, err := os.ReadFile(claimsPath)
	if err != nil {
		t.Fatal(err)
	}
	var cf corpusFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	return cf
}

// runCase runs one corpus case through the REAL ValidateClaim in both modes and
// records the verdict — the single point of truth both the regen and the drift guard
// use, so the frozen file can never diverge from the code.
func runCase(cf corpusFile, c corpusCase) verdictRec {
	phen := make(map[string][]string, len(cf.Phenomena))
	for id, tail := range cf.Phenomena {
		phen[id] = []string{tail}
	}
	ctx := ClaimContext{
		Phenomena: phen, AuthoredLinks: cf.AuthoredLinks,
		Projected: c.Projected, Measured: c.Measured,
	}
	full := ValidateClaim(c.Claim, ctx, ClaimOpts{})
	struc := ValidateClaim(c.Claim, ctx, ClaimOpts{DisableSubstring: true})
	return verdictRec{
		ID: c.ID, Category: c.Category, ExpectFlagged: c.ExpectFlagged,
		LabelledBestEffort: full.LabelledBestEffort,
		Full:               verdictSide{Flagged: full.Flagged, MatchedAuthored: full.MatchedAuthored, Classes: classesOf(full)},
		Structural:         verdictSide{Flagged: struc.Flagged, MatchedAuthored: struc.MatchedAuthored, Classes: classesOf(struc)},
	}
}

func classesOf(v ClaimVerdict) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range v.Reasons {
		if !seen[r.Class] {
			seen[r.Class] = true
			out = append(out, r.Class)
		}
	}
	return out
}

func TestRegenValidateClaimCorpus(t *testing.T) {
	if os.Getenv("REGEN_VALIDATE_CLAIM_CORPUS") != "1" {
		t.Skip("set REGEN_VALIDATE_CLAIM_CORPUS=1 to regenerate corpus/validate-claim/verdicts.jsonl")
	}
	cf := loadCorpus(t)
	var buf []byte
	for _, c := range cf.Cases {
		b, err := json.Marshal(runCase(cf, c))
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(filepath.Join(verdictsPath), buf, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("regenerated %d verdicts into %s", len(cf.Cases), verdictsPath)
}

// TestValidateClaimCorpusFrozenConsistent is the always-on drift guard: the frozen
// verdicts.jsonl must equal a fresh run of ValidateClaim over claims.json. A change to
// ValidateClaim that forgets to regenerate fails here, in CI, immediately.
func TestValidateClaimCorpusFrozenConsistent(t *testing.T) {
	cf := loadCorpus(t)
	raw, err := os.ReadFile(verdictsPath)
	if err != nil {
		t.Skipf("frozen verdicts not present (run REGEN_VALIDATE_CLAIM_CORPUS=1): %v", err)
	}
	frozen := map[string]verdictRec{}
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		var vr verdictRec
		if err := json.Unmarshal(line, &vr); err != nil {
			t.Fatalf("frozen verdict parse: %v", err)
		}
		frozen[vr.ID] = vr
	}
	if len(frozen) != len(cf.Cases) {
		t.Fatalf("frozen verdicts (%d) != corpus cases (%d) — regenerate", len(frozen), len(cf.Cases))
	}
	for _, c := range cf.Cases {
		want := runCase(cf, c)
		got, ok := frozen[c.ID]
		if !ok {
			t.Errorf("case %s missing from frozen verdicts", c.ID)
			continue
		}
		wb, _ := json.Marshal(want)
		gb, _ := json.Marshal(got)
		if string(wb) != string(gb) {
			t.Errorf("case %s: frozen verdict drifted from ValidateClaim\n want %s\n got  %s", c.ID, wb, gb)
		}
	}
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
