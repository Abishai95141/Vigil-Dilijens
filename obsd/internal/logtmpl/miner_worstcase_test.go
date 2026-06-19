package logtmpl

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This file hardens the Go-native deterministic Drain miner (doc 20 P4). It asserts,
// for arbitrary inputs and adversarial orderings, that:
//   - the output is a DETERMINISTIC, totally-ordered template set (sorted, byte-identical
//     across runs);
//   - the template set is INVARIANT to the order lines arrive in (Mine sorts its input);
//   - a mined template is MEASURED — every literal token is EXTRACTED from the (masked)
//     byte stream, never authored/invented; counts are exact and conserved;
//   - arbitrary bytes never panic (Go Fuzz).

// ----------------------------------------------------------------------------
// Determinism: the OUTPUT is sorted by (Pattern asc, Count desc) — a total order
// independent of the (map-based) tree walk. A miner that returned tree-walk order
// would be non-deterministic; this test pins the contract its doc.go promises.
// ----------------------------------------------------------------------------

func TestMineOutputIsSorted(t *testing.T) {
	// Lines chosen so several distinct templates with several counts coexist, AND
	// so that two templates share a count (to exercise the Count tie-break) and two
	// share nothing (to exercise the Pattern key).
	lines := []string{
		"zeta worker 1 started",
		"zeta worker 2 started",
		"alpha cache miss key 1",
		"alpha cache miss key 2",
		"alpha cache miss key 3",
		"mid request 200 ok",
		"GET /a 200 1ms",
		"GET /b 404 2ms",
	}
	got := Mine(lines, DefaultParams)
	if len(got) < 3 {
		t.Fatalf("expected several templates to exercise ordering, got %d: %+v", len(got), got)
	}
	// Re-derive the required order independently and demand byte-identity.
	want := append([]Template(nil), got...)
	sort.SliceStable(want, func(i, j int) bool {
		if want[i].Pattern != want[j].Pattern {
			return want[i].Pattern < want[j].Pattern
		}
		return want[i].Count > want[j].Count
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Mine output is not sorted by (Pattern asc, Count desc):\n got=%+v\nwant=%+v", got, want)
	}
	// And verify the pairwise invariant directly (would catch a swapped comparator).
	for i := 1; i < len(got); i++ {
		a, b := got[i-1], got[i]
		switch {
		case a.Pattern > b.Pattern:
			t.Errorf("patterns out of ascending order at %d: %q then %q", i, a.Pattern, b.Pattern)
		case a.Pattern == b.Pattern && a.Count < b.Count:
			t.Errorf("equal patterns not in descending count order at %d: %d then %d", i, a.Count, b.Count)
		}
	}
}

// ----------------------------------------------------------------------------
// Input-order invariance under MANY random permutations. The existing gate test
// only reverses the input; a true Drain is order-sensitive at the leaf (the first
// line in a cluster seeds the pattern, later ones widen it with wildcards), so the
// ONLY thing that saves us is the internal sort. Shuffling exercises that.
// ----------------------------------------------------------------------------

func TestMineInvariantUnderShuffle(t *testing.T) {
	base := []string{
		"user 12 logged in from 10.0.0.1",
		"user 340 logged in from 10.0.0.2",
		"user 9 logged in from 10.0.0.99",
		"connection to 10.0.0.5:5432 failed after 3 retries",
		"connection to 10.0.0.6:5432 failed after 17 retries",
		"GET /api/cart 200 12ms",
		"GET /api/checkout 500 1200ms",
		"cache miss for key user:42",
		"cache miss for key user:99",
		"worker 3 started",
		"worker 70 started",
		"disk usage 85 percent on /dev/sda",
		"GetCartAsync called with userId=39c70ffd-778f-4db3-b8fe-cdbe57215e6c",
		"GetCartAsync called with userId=37bc7d59-6842-498e-98c4-560ea6db36c9",
		// Distinct FIRST tokens that share an arity. With MaxChildren tight (below),
		// the limited first-layer child slots are won by whichever prefixes ARRIVE
		// first — so the template SET genuinely depends on insertion order unless the
		// miner sorts its input. This makes the shuffle adversarial, not cosmetic.
		"apple seen once today",
		"banana seen once today",
		"cherry seen once today",
		"date seen once today",
		"fig seen once today",
		"grape seen once today",
	}
	// Tight MaxChildren forces the order-sensitive collapse path; only the input sort
	// makes the outcome order-invariant.
	p := Params{Depth: 4, SimThreshold: 0.5, MaxChildren: 3}
	want := Mine(base, p)
	if len(want) == 0 {
		t.Fatal("fixture produced no templates")
	}
	// 64 independent shuffles, fixed seed (no wall-clock) ⇒ reproducible.
	rng := rand.New(rand.NewSource(0x10C7E))
	for trial := 0; trial < 64; trial++ {
		shuf := append([]string(nil), base...)
		rng.Shuffle(len(shuf), func(i, j int) { shuf[i], shuf[j] = shuf[j], shuf[i] })
		got := Mine(shuf, p)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: shuffled input produced a different template set\n perm=%v\n want=%+v\n got =%+v",
				trial, shuf, want, got)
		}
	}
	assertCountsConserved(t, base, want)
}

// Duplicate lines and pure-noise high-cardinality lines must not break invariance
// or conservation. (A subtle order-bug often only shows up with duplicates.)
func TestMineInvariantWithDuplicatesAndNoise(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("processed batch %d in %dms", i, i*2))
		lines = append(lines, "heartbeat ok") // exact duplicates
	}
	for i := 0; i < 10; i++ {
		// every line structurally distinct (different arity) — high cardinality
		lines = append(lines, strings.Repeat("tok ", i+1)+"end")
	}
	want := Mine(lines, DefaultParams)
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 16; trial++ {
		shuf := append([]string(nil), lines...)
		rng.Shuffle(len(shuf), func(i, j int) { shuf[i], shuf[j] = shuf[j], shuf[i] })
		if got := Mine(shuf, DefaultParams); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: not invariant with duplicates/noise\nwant=%+v\ngot =%+v", trial, want, got)
		}
	}
	assertCountsConserved(t, lines, want)
}

// ----------------------------------------------------------------------------
// MEASURED, not AUTHORED. A mined pattern is an arithmetic consequence of the byte
// stream: every NON-wildcard token in a pattern must be a real token of some masked
// input line. The miner may only (a) copy a literal token through, or (b) replace a
// position with the wildcard. It may NEVER synthesize a token that was not present —
// that would be authoring. We verify against the miner's OWN masking (maskTokens) so
// the property is exact.
// ----------------------------------------------------------------------------

func TestMinedTemplatesAreExtractedNotInvented(t *testing.T) {
	lines := []string{
		"payment gateway timeout for order 1001 amount 49.99 USD",
		"payment gateway timeout for order 1002 amount 12.50 USD",
		"payment gateway timeout for order 1003 amount 7.00 USD",
		"redis SET key session:abc ttl 3600",
		"redis SET key session:xyz ttl 7200",
		"GET https://svc.local/health 200 in 5ms",
		"unhandled panic at main.go:42 in handler",
		"unhandled panic at server.go:117 in handler",
	}
	got := Mine(lines, DefaultParams)

	// Build the universe of tokens the miner is ALLOWED to emit: every token of every
	// masked input line, plus the wildcard.
	allowed := map[string]bool{wildcard: true}
	for _, ln := range lines {
		for _, tok := range maskTokens(ln) {
			allowed[tok] = true
		}
	}
	for _, tpl := range got {
		for _, tok := range tpl.Tokens {
			if !allowed[tok] {
				t.Errorf("template token %q in pattern %q was INVENTED (not in any masked input line): authoring, not measuring",
					tok, tpl.Pattern)
			}
		}
		// The serialized Pattern must be exactly the join of its tokens (no extra prose).
		if want := strings.Join(tpl.Tokens, " "); tpl.Pattern != want {
			t.Errorf("Pattern %q != join(Tokens) %q — surfaced text diverged from the measured tokens", tpl.Pattern, want)
		}
	}
}

// Counts are exact: every non-empty (after masking) input line is counted exactly
// once across the template set — the "measured arithmetic consequence" property.
func TestMineCountsAreExactlyConserved(t *testing.T) {
	lines := []string{
		"a b c",                                // counted
		"a b d",                                // counted
		"   ",                                  // blank → not counted
		"",                                     // empty → not counted
		"x y z w v u t",                        // counted
		"550e8400-e29b-41d4-a716-446655440000", // masks to "<*>" → one token → counted
	}
	got := Mine(lines, DefaultParams)

	// Independently count how many lines survive masking (non-empty token slice).
	surviving := 0
	for _, ln := range lines {
		if len(maskTokens(ln)) > 0 {
			surviving++
		}
	}
	assertCountsConserved(t, lines, got)
	total := 0
	for _, tpl := range got {
		if tpl.Count <= 0 {
			t.Errorf("non-positive count %d for %q — a template must cover ≥1 line", tpl.Count, tpl.Pattern)
		}
		total += tpl.Count
	}
	if total != surviving {
		t.Fatalf("counts sum to %d, want %d surviving lines (counts must be exact)", total, surviving)
	}
}

// assertCountsConserved checks Σ counts == number of lines that survive masking.
func assertCountsConserved(t *testing.T, lines []string, got []Template) {
	t.Helper()
	surviving := 0
	for _, ln := range lines {
		if len(maskTokens(ln)) > 0 {
			surviving++
		}
	}
	total := 0
	for _, tpl := range got {
		total += tpl.Count
	}
	if total != surviving {
		t.Fatalf("count conservation broken: Σcount=%d, surviving lines=%d", total, surviving)
	}
}

// ----------------------------------------------------------------------------
// Declared (never data-fit) params: the miner must honor Depth / SimThreshold as
// DECLARED knobs, not fit them to the data. We don't assert the absolute template
// count (that is implementation detail) but we assert MONOTONE, principled effects
// so a "params silently ignored" regression is caught:
//   - SimThreshold = 1.0 (require exact match) yields STRICTLY MORE templates than
//     a permissive 0.0 threshold on lines that differ in one position.
// ----------------------------------------------------------------------------

func TestParamsAreHonoredNotIgnored(t *testing.T) {
	// Many lines sharing a prefix but differing in the trailing token.
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, fmt.Sprintf("login attempt for account acct%c failed", 'A'+rune(i)))
	}
	// acctA..acctL are distinct literal tokens (letters, not masked as numbers), so
	// at threshold 1.0 every variant is its own template; at 0.0 they all collapse.
	strict := Mine(lines, Params{Depth: 4, SimThreshold: 1.0, MaxChildren: 100})
	loose := Mine(lines, Params{Depth: 4, SimThreshold: 0.0, MaxChildren: 100})
	if len(loose) >= len(strict) {
		t.Fatalf("SimThreshold appears ignored: loose=%d templates, strict=%d (loose must merge more)",
			len(loose), len(strict))
	}
	if len(loose) != 1 {
		t.Errorf("threshold 0.0 should merge same-arity prefix-sharing lines into 1, got %d: %+v", len(loose), loose)
	}
	// Both partitionings must still conserve counts (params change grouping, not arithmetic).
	assertCountsConserved(t, lines, strict)
	assertCountsConserved(t, lines, loose)
}

// ----------------------------------------------------------------------------
// MaxChildren collapse path: a node wider than MaxChildren collapses extra children
// to a wildcard. With MaxChildren=1 and many distinct first tokens, the miner must
// not explode and must still conserve counts (exercises the collapse branch).
// ----------------------------------------------------------------------------

func TestMaxChildrenCollapseStaysDeterministic(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("svc%c handled request ok", 'a'+rune(i%26))+fmt.Sprintf("%d", i))
	}
	p := Params{Depth: 4, SimThreshold: 0.5, MaxChildren: 1}
	first := Mine(lines, p)
	// Determinism across runs AND under shuffle, even on the collapse path.
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 8; trial++ {
		shuf := append([]string(nil), lines...)
		rng.Shuffle(len(shuf), func(i, j int) { shuf[i], shuf[j] = shuf[j], shuf[i] })
		if got := Mine(shuf, p); !reflect.DeepEqual(got, first) {
			t.Fatalf("trial %d: collapse path not order-invariant\nwant=%+v\ngot =%+v", trial, first, got)
		}
	}
	assertCountsConserved(t, lines, first)
}

// ----------------------------------------------------------------------------
// FUZZ: arbitrary bytes (split into lines) must never panic, and the two core
// invariants must hold for ANY input — determinism across runs and count
// conservation. Run as a normal test over the seed corpus by `go test`; run as a
// real fuzz with `go test -run x -fuzz FuzzMine`.
// ----------------------------------------------------------------------------

func FuzzMine(f *testing.F) {
	seeds := []string{
		"",
		"\n\n\n",
		"a b c\n a b d\n a b e",
		"user 1 from 10.0.0.1\nuser 2 from 10.0.0.2",
		"550e8400-e29b-41d4-a716-446655440000 0xDEADBEEF 1.2.3.4:8080 250ms",
		strings.Repeat("tok ", 500),
		"  leading   and   collapsed   spaces  ",
		"\t\ttabs\tand\tnewlines\r\nmixed",
		"εmoji 漢字 ☃ \x00\x01 binary-ish",
		"<*> literal wildcard already present <*>",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, blob string) {
		lines := strings.Split(blob, "\n")

		// 1) Never panics (the harness fails the test if Mine panics).
		got := Mine(lines, DefaultParams)

		// 2) Deterministic across two runs on identical input.
		again := Mine(lines, DefaultParams)
		if !reflect.DeepEqual(got, again) {
			t.Fatalf("non-deterministic on fuzz input %q:\n%+v\n%+v", blob, got, again)
		}

		// 3) Invariant to input order (the determinism guarantee) — reverse the lines.
		rev := make([]string, len(lines))
		for i := range lines {
			rev[i] = lines[len(lines)-1-i]
		}
		if revGot := Mine(rev, DefaultParams); !reflect.DeepEqual(revGot, got) {
			t.Fatalf("order-dependent on fuzz input %q:\n%+v\n%+v", blob, got, revGot)
		}

		// 4) Counts are exact and every emitted token is extracted (MEASURED), never
		//    invented; Pattern equals join(Tokens).
		allowed := map[string]bool{wildcard: true}
		surviving := 0
		for _, ln := range lines {
			toks := maskTokens(ln)
			if len(toks) > 0 {
				surviving++
			}
			for _, tok := range toks {
				allowed[tok] = true
			}
		}
		total := 0
		for _, tpl := range got {
			if tpl.Count <= 0 {
				t.Fatalf("non-positive count for %q on input %q", tpl.Pattern, blob)
			}
			total += tpl.Count
			if join := strings.Join(tpl.Tokens, " "); tpl.Pattern != join {
				t.Fatalf("Pattern %q != join(Tokens) %q on input %q", tpl.Pattern, join, blob)
			}
			for _, tok := range tpl.Tokens {
				if !allowed[tok] {
					t.Fatalf("invented token %q in %q on input %q", tok, tpl.Pattern, blob)
				}
			}
		}
		if total != surviving {
			t.Fatalf("count conservation broken on %q: Σ=%d surviving=%d", blob, total, surviving)
		}

		// 5) Output is sorted by (Pattern asc, Count desc) for ALL inputs.
		for i := 1; i < len(got); i++ {
			a, b := got[i-1], got[i]
			if a.Pattern > b.Pattern || (a.Pattern == b.Pattern && a.Count < b.Count) {
				t.Fatalf("unsorted output on %q at %d: (%q,%d) then (%q,%d)",
					blob, i, a.Pattern, a.Count, b.Pattern, b.Count)
			}
		}
	})
}
