package api

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Operator chat (doc 10 M7, begun in Phase 2 per doc 13: "chat begun ... adversarial
// register fixtures"). The hard part of chat is NOT answering — it is answering
// WITHOUT crossing the charter (doc 01): never upgrade a class, never improvise a
// cause, never speak a forecast as a certainty. So this scaffold is built guard-FIRST:
//
//   - It answers ONLY from the structured, already-classed surface data (the
//     ChatSnapshot) — a deterministic retrieval/template responder, no generation.
//   - Every drafted answer passes GuardResponse before it is returned. A draft that
//     carries a banned register (causal claim, future-certainty, class fusion) is
//     REFUSED, with the reason stated — the refusal IS the safe behaviour.
//
// What is deferred to Phase 3 (10 M7 complete): richer NL understanding, multi-turn
// context, and a web chat surface. The register guard — the part that makes chat
// safe to ship at all — is what "begun" must get right, and is fully here + tested.

// ChatMatch is one matched phenomenon, as the chat may cite it (MEASURED state +
// the AUTHORED note kept LABELLED, never fused).
type ChatMatch struct {
	Phenomenon   string
	Label        string
	Entity       string
	Quality      string   // full | degraded (MEASURED)
	AuthoredNote string   // the graph's note, surfaced verbatim + labelled as authored
	Precursors   []string // authored precursor phenomena (references, not causes)
}

// ChatWarning is one PROJECTED early-warning, as the chat may cite it (always banded).
type ChatWarning struct {
	Entity     string
	Metric     string
	EarliestAt time.Time
	LatestAt   time.Time
	OpenEnded  bool // LatestBeyondHorizon — the crossing may not happen
	Confidence string
}

// ChatSnapshot is the read-only, already-classed data the responder answers from.
// It carries no free text the system generated — only structured states + authored
// notes surfaced verbatim.
type ChatSnapshot struct {
	GraphRelease  string
	Matches       []ChatMatch
	Warnings      []ChatWarning
	UnexplainedN  int
	CoverageTierA int
	CoverageNote  string
}

// ChatResponse is the answer (or refusal).
type ChatResponse struct {
	Answer        string   `json:"answer"`
	Citations     []string `json:"citations"`
	Refused       bool     `json:"refused"`
	RefusedReason string   `json:"refusedReason,omitempty"`
}

// AnswerChat composes a register-safe answer to a question from the structured
// snapshot. It recognises a small set of operator intents; anything else gets an
// honest "I can only answer from the structured findings" rather than an improvised
// reply. Every answer is guard-checked before return.
func AnswerChat(question string, snap *ChatSnapshot) ChatResponse {
	q := strings.ToLower(strings.TrimSpace(question))
	var draft string
	var cites []string

	switch {
	case wantsCause(q):
		// The charter's sharpest line: never improvise a cause. We surface the
		// AUTHORED relation if one exists, LABELLED as authored — never a generated
		// "because". If none is authored, we say so.
		draft, cites = answerCause(q, snap)
	case wantsForecast(q):
		// A forecast is modal + banded, never "will". Answer from PROJECTED warnings.
		draft, cites = answerForecast(snap)
	case wantsStatus(q):
		draft, cites = answerStatus(snap)
	case wantsCoverage(q):
		draft = fmt.Sprintf("%d entities are in active monitoring (Tier-A). %s", snap.CoverageTierA, snap.CoverageNote)
	default:
		draft = "I answer only from the structured findings, projections, coverage, and authored graph relations. " +
			"Try: what is matching now, what is projected to cross, what is the coverage, or what does the graph relate to a phenomenon."
	}

	// Guard: a drafted answer that carries a banned register is refused, not sent.
	if vs := CharterViolations("chat", []byte(draft)); len(vs) > 0 {
		return ChatResponse{
			Refused: true,
			RefusedReason: fmt.Sprintf(
				"draft answer rejected by the charter guard: it carried a banned %s register (%q). The system surfaces classed facts and authored relations, never a generated cause or certainty.",
				vs[0].Class, vs[0].Phrase),
		}
	}
	return ChatResponse{Answer: draft, Citations: cites}
}

func answerCause(q string, snap *ChatSnapshot) (string, []string) {
	// Look for a phenomenon the operator named whose AUTHORED relations we can cite.
	for _, m := range snap.Matches {
		if mentions(q, m.Phenomenon) || mentions(q, m.Label) {
			if len(m.Precursors) > 0 {
				return fmt.Sprintf(
					"I do not assert causes. Per the graph (authored), %s has authored precursor relation(s): %s. "+
						"That is a curated relationship, surfaced as-is — not a measured or inferred cause.",
					m.Label, strings.Join(m.Precursors, ", ")), []string{m.Phenomenon}
			}
			note := m.AuthoredNote
			if note == "" {
				note = "(no authored note on this match)"
			}
			return fmt.Sprintf(
				"I do not assert causes. The match on %s is MEASURED (%s); the graph's authored note for it is: %q. "+
					"No causal claim is made beyond the authored relation.", m.Label, m.Quality, note), []string{m.Phenomenon}
		}
	}
	return "I do not assert causes. I can show authored graph relations (precursors, blast radius) for a matched phenomenon, " +
		"but the system never generates a cause. Name a matched phenomenon to see its authored relations.", nil
}

func answerForecast(snap *ChatSnapshot) (string, []string) {
	if len(snap.Warnings) == 0 {
		return "Nothing is projected to cross a bar within the horizon right now (or the forecasting lane is off pending its gate).", nil
	}
	var b strings.Builder
	var cites []string
	b.WriteString("Projected to cross (PROJECTED — a band, never a certainty):\n")
	for _, w := range snap.Warnings {
		edge := w.LatestAt.UTC().Format("15:04:05Z")
		if w.OpenEnded {
			edge = "open (may not cross within the horizon)"
		}
		fmt.Fprintf(&b, "  - %s %s: projected to cross between %s and %s; confidence %s.\n",
			w.Entity, w.Metric, w.EarliestAt.UTC().Format("15:04:05Z"), edge, w.Confidence)
		cites = append(cites, w.Entity+"/"+w.Metric)
	}
	return strings.TrimRight(b.String(), "\n"), cites
}

func answerStatus(snap *ChatSnapshot) (string, []string) {
	if len(snap.Matches) == 0 {
		return fmt.Sprintf("No phenomena are matching right now (MEASURED). %d signal(s) are loud-but-unmatched (unexplained channel).", snap.UnexplainedN), nil
	}
	matches := append([]ChatMatch(nil), snap.Matches...)
	sort.Slice(matches, func(i, j int) bool { return matches[i].Entity < matches[j].Entity })
	var b strings.Builder
	var cites []string
	fmt.Fprintf(&b, "%d phenomenon match(es) now (MEASURED):\n", len(matches))
	for _, m := range matches {
		fmt.Fprintf(&b, "  - %s on %s (%s).\n", m.Label, m.Entity, m.Quality)
		cites = append(cites, m.Phenomenon)
	}
	if snap.UnexplainedN > 0 {
		fmt.Fprintf(&b, "Plus %d loud-but-unmatched signal(s) in the unexplained channel.", snap.UnexplainedN)
	}
	return strings.TrimRight(b.String(), "\n"), cites
}

// --- intent recognition (deliberately conservative) ------------------------

func wantsCause(q string) bool {
	return containsAny(q, "why", "cause", "caused", "reason", "root cause", "blame", "responsible")
}
func wantsForecast(q string) bool {
	return containsAny(q, "when will", "will it", "forecast", "projected", "going to", "cross", "soon", "predict")
}
func wantsStatus(q string) bool {
	return containsAny(q, "what is wrong", "what's wrong", "matching", "happening", "status", "what is broken", "current")
}
func wantsCoverage(q string) bool {
	return containsAny(q, "coverage", "monitored", "tier-a", "tier a", "watching", "how many entities")
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func mentions(q, term string) bool {
	t := strings.ToLower(strings.TrimSpace(term))
	if t == "" {
		return false
	}
	// match on the distinctive tail of a phenomenon id (PHEN_MEMORY_LEAK -> "memory leak")
	t = strings.TrimPrefix(t, "phen_")
	t = strings.ReplaceAll(t, "_", " ")
	return strings.Contains(q, t)
}
