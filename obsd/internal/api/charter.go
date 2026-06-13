package api

import (
	"fmt"
	"strings"
)

// Charter register linter (doc 01 §5, doc 11 M6). The surfacing layer is the ONE
// place the three provenance classes are joined — adjacently, each labelled, never
// fused. This linter sweeps a serialized surface payload for register violations the
// join must never commit:
//
//   - GENERATED CAUSAL CLAIMS — a co-occurrence or cascade is never "because" /
//     "caused by" / "root cause". Causation is only ever an AUTHORED graph note,
//     surfaced verbatim and terse; the product never generates a causal sentence.
//   - FUTURE CERTAINTY — a PROJECTED crossing is "projected to cross", never "will
//     cross" / "will reach". The band never collapses to a promise.
//   - CLASS FUSION — a PROJECTED datum restated as MEASURED ("has crossed" on a
//     forecast), or a MEASURED state dressed as a forecast.
//
// The real authored notes in the KG are terse and clean ("Eventual outcome",
// "Slope > 0"), so a populated real payload passes; the battery's honeypots prove
// the linter has teeth.

// bannedCausal are generated-causation markers. Authored notes may describe an
// authored relation ("Eventual outcome"), but the product never emits a generated
// causal SENTENCE — these phrases are how that would read. NOTE: a substring denylist
// is inherently incomplete (it is a backstop, not a proof of clean register); the
// PRIMARY guarantee is structural — the responder/surfacers generate no free causal
// prose at all, only classed facts + verbatim authored notes. This list catches the
// obvious phrasings if dirty data ever reaches a surface.
var bannedCausal = []string{
	"because", "caused by", "caused the", "causes the", "causing", "due to",
	"owing to", "on account of", "leads to", "led to", "results in", "result of",
	"as a result", "root cause", "root-cause", "the cause", "stems from",
	"stem from", "arises from", "arising from", "comes from", "triggered by",
	"brought about by", "explained by", "explains the", "responsible for",
	"to blame", "the reason is", "reason for the", "blamed on", "attributable to",
}

// bannedFutureCertainty are promise-register markers — banned EVERYWHERE (a forecast
// is modal; a measurement is past/present, never a future promise).
var bannedFutureCertainty = []string{
	"will cross", "will reach", "will breach", "will hit", "will exceed",
	"will happen", "will fail", "shall cross", "shall reach", "is going to",
	"are going to", "set to breach", "set to cross", "bound to", "guaranteed to",
	"definitely will", "certain to", "imminent crossing", "inevitably",
}

// bannedFusion are class-confusion markers: a forecast spoken as a fact.
var bannedFusion = []string{
	"has been forecast to have crossed", "projected and confirmed",
	"measured forecast", "forecast measurement",
}

// CharterViolation is one register breach found in a surface payload.
type CharterViolation struct {
	Surface string // which surface payload
	Phrase  string // the banned phrase
	Class   string // causal | future-certainty | fusion
}

func (v CharterViolation) String() string {
	return fmt.Sprintf("[%s] %q (%s) in %s payload", v.Class, v.Phrase, v.Class, v.Surface)
}

// CharterViolations scans a serialized surface payload (lower-cased internally) for
// register breaches. Returns the violations found (empty = clean). Surface labels
// the payload for the message.
func CharterViolations(surface string, payload []byte) []CharterViolation {
	low := strings.ToLower(string(payload))
	var out []CharterViolation
	scan := func(phrases []string, class string) {
		for _, p := range phrases {
			if strings.Contains(low, p) {
				out = append(out, CharterViolation{Surface: surface, Phrase: p, Class: class})
			}
		}
	}
	scan(bannedCausal, "causal")
	scan(bannedFutureCertainty, "future-certainty")
	scan(bannedFusion, "fusion")
	return out
}
