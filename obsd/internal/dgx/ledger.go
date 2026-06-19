package dgx

import (
	"fmt"
	"strings"
)

// Proposal-ledger memory (doc 21 §2.2c). At cycle start the harness reads the agent's OWN
// firewalled candidate store and renders a deterministically-ordered summary of what it has
// already proposed — promoted (succeeded, do not duplicate), rejected (with the SYSTEM
// reason, do not re-propose unless citing NEW evidence), and pending (already staged,
// awaiting review). This is POSITIVE+NEGATIVE prompt context only — it changes the prompt,
// never a gate; the store's content-id dedup is the hard backstop. The harness (package
// main) builds the Ledger from candidate.Store.List (already ordered created_at,id) so the
// rendering is reproducible.

// LedgerEntry is one prior proposal, flattened for the prompt.
type LedgerEntry struct {
	Kind    string // candidate kind (edge, equiv_group, ...)
	Subject string // the focal subject
	Reason  string // SYSTEM reason (rejected rows: why it was rejected)
	Note    string // human note (promoted rows: the authored why)
}

// Ledger is the agent's prior-proposal history, bucketed by lifecycle status.
type Ledger struct {
	Promoted []LedgerEntry
	Rejected []LedgerEntry
	Pending  []LedgerEntry
}

func (l Ledger) empty() bool {
	return len(l.Promoted) == 0 && len(l.Rejected) == 0 && len(l.Pending) == 0
}

// render writes the PRIOR PROPOSAL HISTORY section, bounded by budget chars (truncation
// stated). Order is exactly the slices' order (the caller sorts+caps deterministically).
func (l Ledger) render(b *strings.Builder, budget int) {
	if l.empty() {
		return
	}
	b.WriteString("\nPRIOR PROPOSAL HISTORY (your own past proposals — do not repeat them):\n")
	writeBucket(b, "PROMOTED (already accepted — do NOT duplicate)", l.Promoted, budget, func(e LedgerEntry) string {
		return fmt.Sprintf("- [%s] %s%s", e.Kind, e.Subject, noteSuffix(e.Note))
	})
	writeBucket(b, "REJECTED (do NOT re-propose unless you cite NEW evidence)", l.Rejected, budget, func(e LedgerEntry) string {
		// Prefer the human's authored rejection note; fall back to the system reason.
		why := strings.TrimSpace(e.Note)
		if why == "" {
			why = strings.TrimSpace(e.Reason)
		}
		return fmt.Sprintf("- [%s] %s%s", e.Kind, e.Subject, reasonSuffix(why))
	})
	writeBucket(b, "PENDING (already staged, awaiting human review — do NOT restage)", l.Pending, budget, func(e LedgerEntry) string {
		return fmt.Sprintf("- [%s] %s", e.Kind, e.Subject)
	})
}

func writeBucket(b *strings.Builder, header string, entries []LedgerEntry, budget int, line func(LedgerEntry) string) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s:\n", header)
	for _, e := range entries {
		s := "  " + line(e) + "\n"
		if b.Len()+len(s) > budget {
			b.WriteString("  (+more history omitted to fit the budget)\n")
			return
		}
		b.WriteString(s)
	}
}

func noteSuffix(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	return " — " + truncate(note, 80)
}

func reasonSuffix(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	return " (rejected: " + truncate(reason, 80) + ")"
}
