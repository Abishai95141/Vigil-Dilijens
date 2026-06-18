package api

import "time"

// LogTemplateClass labels the /api/log-templates payload: MEASURED mined structure,
// NOT an authored event and NOT a cause (doc 20 P4 §3.1). Regex is the authored first
// layer; this is the measured second layer for the unmapped tail. An LLM name/grouping
// for a template would be a separate PROPOSED candidate, never surfaced here as fact.
const LogTemplateClass = "MEASURED log templates (mined structure; not an authored event, not a cause)"

const logTemplateNote = "Mined log templates (doc 20 P4): a deterministic Drain over the unmapped tail of pod " +
	"logs, turning raw lines into (template, count) pairs. MEASURED — a function of the byte stream, the same class " +
	"as a fingerprint. Off the deterministic digest; never feeds detection or replay. Naming/grouping a template is a " +
	"separate PROPOSED candidate (the agent), never an authored fact here."

const logTemplateOffNote = "The log lane is not enabled (--logs-enabled). Detection and forecasting are unaffected."

// LogTemplateRow is one mined template + its occurrence count.
type LogTemplateRow struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
}

// LogTemplatesView is the /api/log-templates payload.
type LogTemplatesView struct {
	Class       string           `json:"class"`
	Available   bool             `json:"available"`
	GeneratedAt time.Time        `json:"generatedAt"`
	PodsSampled int              `json:"podsSampled"`
	LinesMined  int              `json:"linesMined"`
	Templates   []LogTemplateRow `json:"templates"`
	Note        string           `json:"note"`
}

// NewLogTemplatesView builds the available view from already-mapped rows.
func NewLogTemplatesView(now time.Time, podsSampled, linesMined int, rows []LogTemplateRow) *LogTemplatesView {
	if rows == nil {
		rows = []LogTemplateRow{}
	}
	return &LogTemplatesView{
		Class: LogTemplateClass, Available: true, GeneratedAt: now,
		PodsSampled: podsSampled, LinesMined: linesMined, Templates: rows, Note: logTemplateNote,
	}
}

// UnavailableLogTemplates is the honest OFF state.
func UnavailableLogTemplates(now time.Time) *LogTemplatesView {
	return &LogTemplatesView{
		Class: LogTemplateClass, Available: false, GeneratedAt: now,
		Templates: []LogTemplateRow{}, Note: logTemplateOffNote,
	}
}
