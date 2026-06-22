package binding

import "testing"

// TestClassifyFrontier pins the coverage-frontier classifier (doc 33 §1) to the stable
// reason templates obtain.go emits. Emission (deploy/scrape/probe) dominates completeness,
// because authoring a member cannot rescue a signal nothing produces.
func TestClassifyFrontier(t *testing.T) {
	cases := []struct {
		name      string
		obs       string
		reasons   []string
		wantClass string
		wantInClo string // substring expected in the Closer (empty = don't check)
	}{
		{"full is covered", "full", nil, "covered", ""},
		{
			"no emitting tool -> emission/deploy", "none",
			[]string{"no emitting tool deployed: etcd"},
			"emission", "deploy etcd",
		},
		{
			"capability not met -> emission/deploy", "partial",
			[]string{"capability CAP_CILIUM not met (cilium not deployed)"},
			"emission", "deploy cilium",
		},
		{
			"capability not met, non-tool parenthetical -> cleaned", "partial",
			[]string{"capability CAP_LOG_AGGREGATOR not met (no log pipeline deployed)"},
			"emission", "deploy log pipeline",
		},
		{
			"metrics endpoint not scraped -> emission/scrape", "none",
			[]string{"CoreDNS /metrics endpoint not scraped by obsd (no resolver-metrics lane)"},
			"emission", "add scrape lane for CoreDNS",
		},
		{
			"not API-derivable -> emission/probe", "partial",
			[]string{"capability CAP_CONFIG_PSI not API-derivable (needs node probe)"},
			"emission", "node-probe to assert CAP_CONFIG_PSI",
		},
		{
			"inline patterns pending -> completeness", "none",
			[]string{"no structured required members to gate on (inline patterns pending resolution)"},
			"completeness", "author structured required members",
		},
		{
			"meta-member -> completeness", "partial",
			[]string{"member node_disk_io is not a gateable signal (meta-member)"},
			"completeness", "",
		},
		{
			"emission dominates when mixed", "partial",
			[]string{
				"no emitting tool deployed: etcd",
				"member foo is not a gateable signal (meta-member)",
			},
			"emission", "deploy etcd",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc := PhenomenonCoverage{Observability: tc.obs, MissingReasons: tc.reasons}
			classifyFrontier(&pc)
			if pc.GapClass != tc.wantClass {
				t.Fatalf("GapClass = %q, want %q (closer=%q)", pc.GapClass, tc.wantClass, pc.Closer)
			}
			if tc.wantInClo != "" && !contains(pc.Closer, tc.wantInClo) {
				t.Fatalf("Closer = %q, want substring %q", pc.Closer, tc.wantInClo)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
