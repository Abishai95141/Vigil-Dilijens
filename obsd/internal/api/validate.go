package api

import (
	"fmt"
	"sort"
	"strings"
)

// The validate_claim referee (v3 T-D, doc 01 enforcement at the surface). It takes a
// claim written by an EXTERNAL generator (an LLM via MCP, or any free prose about the
// system) and reports whether the claim crosses the charter — a generated cause, a
// projection restated as a measurement, or a future certainty. It is a REFEREE, not a
// gate: it NEVER blocks. It returns {Flagged, Reasons, MatchedAuthored,
// LabelledBestEffort=true} and the consumer decides. The cardinal rule is FALSE-BLOCK
// == 0: it must never flag a LEGITIMATE claim — a referee that cries wolf on a true
// statement is worse than none, so every check biases to UNDER-flag.
//
// Two backstops (doc 14 §T-D):
//   1. SUBSTRING + REGISTER — the charter denylist (CharterViolations): banned causal /
//      future-certainty / fusion phrasings. A blunt instrument; it knows no graph. For
//      future certainty it is augmented by a REGISTER-LEVEL matcher (futureCertainty-
//      RegisterMatches): a future modal (will / shall / going to / about to) bound to a
//      crossing/failure predicate ("will crash", "will fill up", "is about to fall
//      over") is the promise register a flat substring list cannot enumerate — it
//      catches the bare phrasings the denylist misses, negation-aware so a negated
//      future ("will not crash", "won't fill up") never flags.
//   2. STRUCTURAL — graph-aware. It extracts (trigger -> downstream) causal pairs from
//      the prose and checks them against the AUTHORED phenomenon relations: a causal
//      claim WITH an authored basis is a (clumsy) surfacing of a real relation, NOT a
//      violation (so the exact causal phrase is rescued from the substring flag); a
//      causal claim with NO authored basis is a generated cause. It also catches a
//      MEASURED-tense crossing asserted about a subject that is only PROJECTED now.
//
// The structural backstop is what gives the referee teeth a denylist cannot have: it
// catches a honeypot phrased WITHOUT any banned substring ("the leak knocked out the
// pod"), and it RESCUES a true authored relation a denylist would wrongly flag ("the
// leak led to the OOM" — an authored MEMORY_LEAK->OOM_KILL relation exists).
//
// Hardened (adversarial review, 27 exploits): causal cues carry DIRECTION (passive
// "B was caused by A" maps A->B, not the reverse); negated assertions ("did not cross",
// "neither is responsible for") never flag; the class-fusion crossing must be bound to
// its projected subject (a crossing about a MEASURED subject elsewhere in the sentence
// is not fusion); subjects/phenomena match on word boundaries; and the authored-relation
// rescue is PER-CUE (it suppresses only the exact authored phrase, never an unrelated
// fabricated clause in the same sentence).

// ClaimVerdict is the referee's advisory output. It never carries a block directive —
// the absence of any "blocked" field is the NEVER-BLOCK guarantee, structural.
type ClaimVerdict struct {
	Flagged            bool           `json:"flagged"`
	Reasons            []ClaimFinding `json:"reasons"`
	MatchedAuthored    bool           `json:"matchedAuthored"` // the claim surfaced >=1 real authored relation
	LabelledBestEffort bool           `json:"labelledBestEffort"`
	Note               string         `json:"note"`
}

// ClaimFinding is one thing the referee noticed: a violation (Flagged-bearing) or an
// advisory (a true authored relation phrased as a generated cause — surface it as
// authored).
type ClaimFinding struct {
	Class    string `json:"class"`    // generated-causation | class-fusion | future-certainty | fusion | advisory
	Detail   string `json:"detail"`   // human-readable, quoting the offending text
	Advisory bool   `json:"advisory"` // true ⇒ not a violation, a phrasing suggestion (does not set Flagged)
}

// ClaimContext is the authored + classed ground the referee checks a claim against.
// Built live from the loaded graph (Phenomena + AuthoredLinks from phenomenon_relation
// edges) and the current surfaces (Projected from warnings, Measured from findings);
// for the gate it is supplied per-case so the referee stays a pure function.
type ClaimContext struct {
	// Phenomena maps a phenomenon id to the ALIAS tails the endpoint-mapping recognises
	// in prose (e.g. PHEN_CRI_ERRORS -> ["cri errors", "container runtime errors"]).
	// Multiple aliases matter live, where a graph label ("OOM kill (cgroup-level)") and
	// the natural phrasing ("oom kill") differ — the referee accepts either.
	Phenomena map[string][]string `json:"phenomena"`
	// AuthoredLinks is the set of directed (src,dst) phenomenon-id pairs the graph
	// authored a relation for — the ONLY legitimate causal/sequence basis.
	AuthoredLinks []AuthoredLink `json:"authoredLinks"`
	// Projected / Measured are human subject keys (entity names / phenomenon tails)
	// currently in each class. A subject only in Projected, asserted in MEASURED
	// tense, is class fusion.
	Projected []string `json:"projected"`
	Measured  []string `json:"measured"`
}

// AuthoredLink is one authored directed relation between two phenomena.
type AuthoredLink struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
	Why string `json:"why"`
}

// ClaimOpts tunes the referee. DisableSubstring turns OFF the charter denylist
// backstop, leaving ONLY the structural (graph-aware) backstop — the mutation mode the
// gate uses to prove the structural backstop independently catches relation-absent
// fabrications (it is not merely riding the denylist).
type ClaimOpts struct {
	DisableSubstring bool
}

const validateNote = "This is a best-effort LABELLER, not a gate (doc 01): it is ADVISORY ONLY — it NEVER blocks and has no veto. A flag is a labelling instruction, not a rejection. It flags generated causation, class fusion, and future certainty (a projection stated as a certainty). A causal claim with an AUTHORED relation behind it is not flagged — surface it AS authored, verbatim. Detection is best-effort and incomplete: the absence of a flag is NOT a guarantee of correctness."

// cue is a causal connective with its DIRECTION. reverse=true means the cue is
// passive/effect-first ("B was caused by A" ⇒ trigger=A is AFTER the cue, downstream=B
// is BEFORE it); reverse=false is active ("A caused B" ⇒ trigger before, downstream after).
type cue struct {
	text    string
	reverse bool
}

// causalCueList is the structural extractor's vocabulary — a SUPERSET of the charter
// denylist, so the structural backstop catches phrasings the denylist misses. Sorted
// longest-first so a longer cue ("triggered by") is matched before a shorter one it
// contains ("triggered"); the endpoint guard (a known phenomenon on BOTH sides) keeps
// even everyday words from false-flagging ordinary prose.
var causalCueList = buildCueList()

func buildCueList() []cue {
	forward := []string{
		"led to", "leads to", "leading to", "resulted in", "resulting in", "results in",
		"caused the", "caused a", "causes the", "causing", "caused", "causes",
		"brought about", "brought down", "gave rise to", "precipitated", "cascaded into",
		"responsible for", "to blame for", "knocked out", "took down", "drove the",
		"is the reason for", "is why", "set off", "kicked off", "sparked", "induced",
		"provoked", "is behind the", "spawned", "unleashed", "triggered",
	}
	reverse := []string{
		"caused by", "because of", "due to", "resulted from", "triggered by", "owing to",
		"stems from", "stemmed from", "blamed on", "result of", "arose from", "arising from",
		"brought on by",
	}
	out := make([]cue, 0, len(forward)+len(reverse))
	for _, t := range forward {
		out = append(out, cue{t, false})
	}
	for _, t := range reverse {
		out = append(out, cue{t, true})
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].text) > len(out[j].text) })
	return out
}

// measuredTenseCrossing are phrasings that assert a crossing has ALREADY happened
// (MEASURED tense). Bound to a PROJECTED-only subject ⇒ class fusion. Deliberately
// excludes projection tense ("projected to cross", "forecast to cross") so a legitimate
// banded projection never trips this. Matched only within a short window AFTER the
// projected subject, so a crossing about a MEASURED subject elsewhere is not fusion.
var measuredTenseCrossing = []string{
	"has crossed", "have crossed", "already crossed", "crossed its", "breached its",
	"exceeded its", "has reached", "has hit", "has surpassed", "surpassed its",
	"over its", "above its", "past its", "passed its", "tripped its", "topped its",
	"blew past", "went over", "is exceeding", "exceeding its", "sitting above",
}

// subjectWindow bounds how far AFTER a projected subject a crossing cue may sit to count
// as a claim about THAT subject (one clause's worth).
const subjectWindow = 55

// negationTokens precede an assertion to negate it; a negated assertion is not a claim.
var negationTokens = []string{"not ", "n't", "never ", "neither ", "no longer", "isn't", "aren't", "hasn't", "haven't"}

// futureModals are the auxiliaries that put a clause in the future-CERTAINTY register —
// a promise about what WILL happen. The modal ALONE is never enough to flag (the system
// makes plenty of benign promises about its OWN behaviour: "detection will produce
// identical results"); the register is the modal BOUND to a crossing/failure predicate
// (futurePredicates) within a short window. Note "won't" is deliberately absent: it is a
// NEGATED future ("won't crash") and carries no certainty to flag, and it does not
// contain "will" so it never matches by accident.
var futureModals = []string{"will", "shall", "going to", "about to", "gonna"}

// futurePredicates are the crossing / failure / depletion verbs that, after a future
// modal, assert a bar will be crossed or a thing will fail — the promise a forecast must
// never make. Curated (whole-word matched, so "fill" never fires inside "backfill"/
// "fulfill" and "fail" never inside "failover") rather than a flat substring list, so
// "asset-api will crash soon" and "the disk will fill up" — neither on the charter
// denylist — are caught by REGISTER, not by enumerated phrase.
var futurePredicates = []string{
	// threshold / capacity crossings
	"cross", "crosses", "crossing", "reach", "reaches", "breach", "breaches",
	"exceed", "exceeds", "surpass", "surpasses", "overflow", "overflows",
	"fill", "fills", "saturate", "saturates", "exhaust", "exhausts",
	"deplete", "depletes", "fill up", "run out", "run dry", "go over", "max out", "blow past",
	"hit the limit", "hit the cap", "hit the ceiling",
	// failure / outage
	"crash", "crashes", "crashing", "fail", "fails", "failing", "die", "dies",
	"flatline", "flatlines", "restart", "restarts", "restarting", "reboot", "reboots",
	"freeze", "freezes", "hang", "hangs", "oom", "be killed", "get killed",
	"be evicted", "get evicted", "be oom-killed", "get oom-killed",
	"go down", "fall over", "stop responding", "lock up", "run out of",
}

// futureWindow bounds how far AFTER a future modal a crossing/failure predicate may sit
// to count as one future-certainty register claim (one clause's worth).
const futureWindow = 40

// futureCertaintyRegisterMatches returns deduped, sorted human-readable details for every
// future-certainty REGISTER match in the (lowercased) claim: a future modal followed,
// within futureWindow chars and not negated, by a crossing/failure predicate. It is the
// register-level companion to the charter denylist — it catches "will crash"/"will fill
// up"/"is about to fall over", which the denylist (a fixed phrase list) cannot. Biased to
// UNDER-flag for the FALSE-BLOCK==0 floor: a negation before the modal OR between the
// modal and the predicate suppresses the match.
func futureCertaintyRegisterMatches(low string) []string {
	matched := map[string]bool{}
	var out []string
	for _, modal := range futureModals {
		for _, mstart := range allIndexWord(low, modal) {
			if negatedBefore(low, mstart) {
				continue
			}
			end := mstart + len(modal)
			stop := end + futureWindow
			if stop > len(low) {
				stop = len(low)
			}
			win := low[end:stop]
			for _, pred := range futurePredicates {
				ci := firstIndexWord(win, pred)
				if ci < 0 || negatedIn(win[:ci]) {
					continue
				}
				detail := fmt.Sprintf("future certainty (register): the modal %q + %q states a future crossing/failure as a certainty — a projection is modal (a band), never a promise; soften to a band", modal, pred)
				if !matched[detail] {
					matched[detail] = true
					out = append(out, detail)
				}
				break // one predicate per modal occurrence is enough to flag the clause
			}
		}
	}
	sort.Strings(out)
	return out
}

// ValidateClaim is the pure referee. Deterministic in (claim, ctx, opts).
func ValidateClaim(claim string, ctx ClaimContext, opts ClaimOpts) ClaimVerdict {
	v := ClaimVerdict{LabelledBestEffort: true, Reasons: []ClaimFinding{}, Note: validateNote}
	low := strings.ToLower(claim)

	links := make(map[string]string, len(ctx.AuthoredLinks))
	for _, l := range ctx.AuthoredLinks {
		links[l.Src+"\x00"+l.Dst] = l.Why
	}

	// 1. Structural causal extraction (graph-aware). rescuedCues records the exact
	// causal phrases that back an AUTHORED relation, so the substring pass can rescue
	// THOSE phrases without rescuing an unrelated fabricated clause.
	rescuedCues := map[string]bool{}
	for _, p := range extractCausalPairs(low, ctx) {
		if why, ok := links[p.a+"\x00"+p.b]; ok {
			v.MatchedAuthored = true
			rescuedCues[p.cue] = true
			v.Reasons = append(v.Reasons, ClaimFinding{
				Class: "advisory", Advisory: true,
				Detail: fmt.Sprintf("the phrasing %q restates the AUTHORED relation %s→%s (%q); surface it as authored, verbatim, not as a generated cause", p.cue, p.a, p.b, why),
			})
			continue
		}
		v.Flagged = true
		v.Reasons = append(v.Reasons, ClaimFinding{
			Class:  "generated-causation",
			Detail: fmt.Sprintf("asserts %s→%s via %q, which the graph does NOT author as a relation (a generated cause)", p.a, p.b, p.cue),
		})
	}

	// 2. Class fusion (structural): a MEASURED-tense crossing BOUND to a subject that is
	// only PROJECTED now. Guarded by projected-only (a true MEASURED statement about a
	// measured subject is never flagged), subject-proximity (the crossing must be about
	// THIS subject), and negation (a "did not cross" is not a crossing).
	measured := toSet(ctx.Measured)
	for _, s := range ctx.Projected {
		ls := strings.ToLower(strings.TrimSpace(s))
		if ls == "" || measured[ls] {
			continue
		}
		if cueText, ok := crossingNearSubject(low, ls); ok {
			v.Flagged = true
			v.Reasons = append(v.Reasons, ClaimFinding{
				Class:  "class-fusion",
				Detail: fmt.Sprintf("states %q as MEASURED (%q) but it is only PROJECTED now — a projection restated as a measurement", s, cueText),
			})
		}
	}

	// 3. Substring backstop (charter denylist), unless disabled (mutation mode). A
	// causal phrase is suppressed iff it is NEGATED or it exactly backs an authored
	// relation (per-cue rescue) — never globally, so a fabricated clause beside an
	// authored one is still flagged.
	if !opts.DisableSubstring {
		for _, cvio := range CharterViolations("claim", []byte(claim)) {
			if cvio.Class == "causal" {
				if substringNegated(low, cvio.Phrase) || overlapsRescued(cvio.Phrase, rescuedCues) {
					continue
				}
			}
			v.Flagged = true
			v.Reasons = append(v.Reasons, ClaimFinding{
				Class:  cvio.Class,
				Detail: fmt.Sprintf("banned %s register (denylist): %q", cvio.Class, cvio.Phrase),
			})
		}
		// 3b. Future-certainty REGISTER backstop (companion to the denylist above). A
		// future modal bound to a crossing/failure predicate is the promise register a
		// fixed phrase list cannot enumerate — this is the same register family as the
		// denylist (not graph-aware), so like the denylist it is OFF in mutation mode.
		for _, detail := range futureCertaintyRegisterMatches(low) {
			v.Flagged = true
			v.Reasons = append(v.Reasons, ClaimFinding{Class: "future-certainty", Detail: detail})
		}
	}

	sort.SliceStable(v.Reasons, func(i, j int) bool {
		if v.Reasons[i].Class != v.Reasons[j].Class {
			return v.Reasons[i].Class < v.Reasons[j].Class
		}
		return v.Reasons[i].Detail < v.Reasons[j].Detail
	})
	return v
}

type causalPair struct {
	a, b, cue string
}

type mention struct {
	id  string
	pos int // start offset of the tail in the lowercased claim
	end int
}

// extractCausalPairs finds (trigger,downstream) phenomenon pairs joined by a directional
// causal cue. ALL occurrences of every phenomenon tail are indexed (so a phenomenon
// mentioned twice, or a downstream after a later cue, is found); a longer cue consumes
// its span so a shorter cue inside it is not double-matched; a negated cue is skipped;
// and a reverse (passive) cue swaps trigger/downstream. The endpoint guard — BOTH sides
// map to a known phenomenon — keeps the broad vocabulary from false-flagging prose.
func extractCausalPairs(low string, ctx ClaimContext) []causalPair {
	var ms []mention
	for id, aliases := range ctx.Phenomena {
		for _, alias := range aliases {
			tail := strings.ToLower(strings.TrimSpace(alias))
			if tail == "" {
				continue
			}
			for _, p := range allIndexWord(low, tail) {
				ms = append(ms, mention{id, p, p + len(tail)})
			}
		}
	}
	if len(ms) < 2 {
		return nil
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].pos < ms[j].pos })

	consumed := make([]bool, len(low))
	seen := map[string]bool{}
	var out []causalPair
	for _, c := range causalCueList {
		from := 0
		for {
			idx := strings.Index(low[from:], c.text)
			if idx < 0 {
				break
			}
			pos := from + idx
			from = pos + len(c.text)
			if rangeConsumed(consumed, pos, pos+len(c.text)) {
				continue
			}
			markRange(consumed, pos, pos+len(c.text))
			if negatedBefore(low, pos) {
				continue // a negated causal assertion is not a causal claim
			}
			before := lastMentionBefore(ms, pos)
			after := firstMentionAfter(ms, pos+len(c.text))
			var a, b string
			if c.reverse {
				a, b = after, before
			} else {
				a, b = before, after
			}
			if a == "" || b == "" || a == b {
				continue
			}
			key := a + "\x00" + b + "\x00" + c.text
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, causalPair{a, b, c.text})
		}
	}
	return out
}

// lastMentionBefore returns the id of the phenomenon whose mention starts latest among
// those starting before pos.
func lastMentionBefore(ms []mention, pos int) string {
	id := ""
	for _, m := range ms {
		if m.pos < pos {
			id = m.id
		}
	}
	return id
}

// firstMentionAfter returns the id of the phenomenon whose mention starts earliest among
// those starting at/after pos.
func firstMentionAfter(ms []mention, pos int) string {
	for _, m := range ms {
		if m.pos >= pos {
			return m.id
		}
	}
	return ""
}

// crossingNearSubject reports a MEASURED-tense crossing within subjectWindow chars AFTER
// a (word-boundary) mention of subj, not negated between the subject and the cue.
func crossingNearSubject(low, subj string) (string, bool) {
	for _, p := range allIndexWord(low, subj) {
		end := p + len(subj)
		stop := end + subjectWindow
		if stop > len(low) {
			stop = len(low)
		}
		win := low[end:stop]
		for _, c := range measuredTenseCrossing {
			ci := strings.Index(win, c)
			if ci < 0 {
				continue
			}
			if negatedIn(win[:ci]) {
				continue
			}
			return c, true
		}
	}
	return "", false
}

// allIndexWord returns every start offset where sub occurs in s on WORD boundaries (so a
// subject "frontend" does not match inside "frontend-cache").
func allIndexWord(s, sub string) []int {
	if sub == "" {
		return nil
	}
	var out []int
	from := 0
	for {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			break
		}
		p := from + i
		from = p + 1
		if wordBoundary(s, p, p+len(sub)) {
			out = append(out, p)
		}
	}
	return out
}

// firstIndexWord returns the first start offset where sub occurs in s on WORD boundaries
// (both ends), or -1. Whole-word so a predicate "fill" never matches inside "backfill"
// and "fail" never inside "failover".
func firstIndexWord(s, sub string) int {
	if sub == "" {
		return -1
	}
	from := 0
	for {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return -1
		}
		p := from + i
		if wordBoundary(s, p, p+len(sub)) {
			return p
		}
		from = p + 1
	}
}

func wordBoundary(s string, a, b int) bool {
	left := a == 0 || !isAlnum(s[a-1])
	right := b >= len(s) || !isAlnum(s[b])
	return left && right
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// negatedBefore reports whether a negation token appears in the ~22 chars before pos.
func negatedBefore(low string, pos int) bool {
	start := pos - 22
	if start < 0 {
		start = 0
	}
	return negatedIn(low[start:pos])
}

func negatedIn(s string) bool {
	for _, n := range negationTokens {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// substringNegated reports whether the (first occurrence of the) phrase is negated.
func substringNegated(low, phrase string) bool {
	i := strings.Index(low, phrase)
	if i < 0 {
		return false
	}
	return negatedBefore(low, i)
}

// overlapsRescued reports whether a substring causal phrase corresponds to one of the
// exact causal cues that backed an authored relation (phrase contains a rescued cue or a
// rescued cue contains the phrase).
func overlapsRescued(phrase string, rescued map[string]bool) bool {
	for c := range rescued {
		if strings.Contains(phrase, c) || strings.Contains(c, phrase) {
			return true
		}
	}
	return false
}

func rangeConsumed(consumed []bool, a, b int) bool {
	for i := a; i < b && i < len(consumed); i++ {
		if consumed[i] {
			return true
		}
	}
	return false
}

func markRange(consumed []bool, a, b int) {
	for i := a; i < b && i < len(consumed); i++ {
		consumed[i] = true
	}
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[strings.ToLower(strings.TrimSpace(x))] = true
	}
	return m
}
