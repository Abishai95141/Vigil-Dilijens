package notify

import (
	"context"
	"time"
)

// Priority orders alerts from P1 (most urgent) to P3 (info). It drives the subject
// summary and the quiet-hours breakthrough rule; it is an AUTHORED mapping (which fact
// is how urgent), never a learned score.
type Priority int

const (
	P1 Priority = iota // critical: a cascade chain forms, an OOM/crash fires, a short-lead crossing
	P2                 // warning: a comfortable-lead crossing, an incident recurrence
	P3                 // info: digest-only
)

func (p Priority) String() string {
	switch p {
	case P1:
		return "P1"
	case P2:
		return "P2"
	case P3:
		return "P3"
	default:
		return "P?"
	}
}

// The three provenance classes (docs/01). Every Line carries exactly one.
const (
	ClassMeasured  = "MEASURED"
	ClassProjected = "PROJECTED"
	ClassAuthored  = "AUTHORED"
)

// Line is one labelled clause of an alert body — the join, never the fusion. An
// AUTHORED line is a curator's note quoted VERBATIM (it may legitimately read
// "...propagates to its callers..."); it is exempt from the causal-token guard, which
// only ever scans SYSTEM-generated text. A MEASURED/PROJECTED line is system-generated
// and must stay register-clean.
type Line struct {
	Class string // ClassMeasured | ClassProjected | ClassAuthored
	Text  string
}

// Alert is one CLASSED fact selected for notification. It is transport-neutral: the
// mapping in cmd/obsd builds it from the surface views (flow.Chain, WarningCard,
// FindingRow, EventCard), labelling each clause's class and copying any authored "why"
// verbatim. This package never re-derives a class or invents prose.
type Alert struct {
	Priority    Priority
	Kind        string // "cascade-chain" | "oom-crash" | "forecast-crossing"
	Headline    string // short, fact-only, NO causal verb (system-generated scaffolding)
	Entity      string // CEI/role label, for display
	DedupKey    string // stable identity for edge-trigger + per-key cooldown
	Lines       []Line
	ConsoleLink string // deep link into the console, or ""
	At          time.Time
}

// Message is a rendered email ready to send.
type Message struct {
	Subject string
	Body    string
	To      []string
}

// Notifier sends a rendered message. Implementations: SMTPNotifier (Gmail) and
// FakeNotifier (hermetic tests). Send is synchronous; the caller runs it off the
// deterministic path in its own goroutine, so an SMTP stall never blocks detection.
type Notifier interface {
	Send(ctx context.Context, m Message) error
}

// Config is the operator-DECLARED alert policy (never learned). The caller loads it
// from env and validates To/Cooldown before constructing a Dispatcher.
type Config struct {
	To                  []string       // recipients
	Cooldown            time.Duration  // per dedup key: suppress a re-send within this window (also the flap damper)
	RatePerWindow       int            // token-bucket size: max emails per RateWindow
	RateWindow          time.Duration  // token-bucket refill window
	QuietStartHour      int            // [0,24); == QuietEndHour ⇒ quiet hours disabled
	QuietEndHour        int            // [0,24)
	QuietLocation       *time.Location // nil ⇒ UTC
	QuietP1Breakthrough bool           // P1 still mails during quiet hours
	ConsoleURL          string         // base URL for deep links, or ""
}

// QuietEnabled reports whether a quiet-hours window is configured.
func (c Config) QuietEnabled() bool { return c.QuietStartHour != c.QuietEndHour }
