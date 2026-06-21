package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Dispatcher applies the fatigue controls to a batch of candidate alerts, coalesces the
// survivors into ONE digest message, sends it (off-digest, non-gating), and records each
// sent key for cooldown. It holds the in-memory rate-limit token bucket; the per-key
// cooldown state is durable in the Store (idempotent across restart).
type Dispatcher struct {
	cfg      Config
	store    *Store
	notifier Notifier

	tokens     float64
	lastRefill time.Time
}

// NewDispatcher builds a dispatcher with a full token bucket as of `now`.
func NewDispatcher(cfg Config, store *Store, notifier Notifier, now time.Time) *Dispatcher {
	return &Dispatcher{
		cfg: cfg, store: store, notifier: notifier,
		tokens: float64(cfg.RatePerWindow), lastRefill: now,
	}
}

// Result reports what a Dispatch did (for logging — never surfaced as a fact).
type Result struct {
	Selected   int      // alerts chosen to send this tick
	Suppressed int      // candidates dropped by cooldown/quiet/rate this tick
	Sent       bool     // an email was actually sent
	Keys       []string // dedup keys sent (deterministic order)
}

// Dispatch evaluates candidate alerts AS OF now: it drops any in cooldown, any held by
// quiet hours, and any over the rate budget; coalesces the survivors into one digest;
// sends it; and records the sent keys. A send error means NOTHING is recorded (the facts
// re-evaluate next tick) — non-gating, the caller logs and continues.
func (d *Dispatcher) Dispatch(ctx context.Context, now time.Time, alerts []Alert) (Result, error) {
	// Deterministic order: priority, then dedup key. Coalescing + rendering are then
	// stable for the same input (replay-friendly even though this lane is off-digest).
	ordered := append([]Alert(nil), alerts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Priority != ordered[j].Priority {
			return ordered[i].Priority < ordered[j].Priority
		}
		return ordered[i].DedupKey < ordered[j].DedupKey
	})

	d.refill(now)
	quiet := d.inQuiet(now)

	var selected []Alert
	var res Result
	for _, a := range ordered {
		// Quiet hours: hold P2/P3 (and P1 unless it breaks through). Held, not lost —
		// a still-active fact re-surfaces on a later tick once quiet ends.
		if quiet && !(a.Priority == P1 && d.cfg.QuietP1Breakthrough) {
			res.Suppressed++
			continue
		}
		// Per-key cooldown (also the flap damper: a key flipping faster than the window
		// mails at most once per window).
		if last, ok, err := d.store.LastSent(a.DedupKey); err == nil && ok && now.Sub(last) < d.cfg.Cooldown {
			res.Suppressed++
			continue
		}
		// Rate limit (token bucket). Over budget ⇒ hold for the next tick.
		if d.tokens < 1 {
			res.Suppressed++
			continue
		}
		d.tokens--
		selected = append(selected, a)
	}
	res.Selected = len(selected)
	if len(selected) == 0 {
		return res, nil
	}

	msg := render(d.cfg, selected)
	if err := d.notifier.Send(ctx, msg); err != nil {
		// Non-gating: refund the tokens, record nothing — the facts retry next tick.
		d.tokens += float64(len(selected))
		return res, fmt.Errorf("notify: send: %w", err)
	}
	for _, a := range selected {
		if err := d.store.RecordSent(now, a); err != nil {
			// The email went out; a ledger write failure is logged by the caller. We do
			// not unsend — worst case the key re-alerts next window (visible, not silent).
			res.Keys = append(res.Keys, a.DedupKey)
			continue
		}
		res.Keys = append(res.Keys, a.DedupKey)
	}
	res.Sent = true
	return res, nil
}

// refill tops up the token bucket by the elapsed fraction of the rate window.
func (d *Dispatcher) refill(now time.Time) {
	if d.cfg.RatePerWindow <= 0 || d.cfg.RateWindow <= 0 {
		d.tokens = 1 // rate limiting disabled: always at least one token
		return
	}
	elapsed := now.Sub(d.lastRefill)
	if elapsed <= 0 {
		return
	}
	d.lastRefill = now
	d.tokens += elapsed.Seconds() / d.cfg.RateWindow.Seconds() * float64(d.cfg.RatePerWindow)
	if d.tokens > float64(d.cfg.RatePerWindow) {
		d.tokens = float64(d.cfg.RatePerWindow)
	}
}

// inQuiet reports whether now falls in the configured quiet-hours window (handles a
// window that wraps past midnight, e.g. 22→7).
func (d *Dispatcher) inQuiet(now time.Time) bool {
	if !d.cfg.QuietEnabled() {
		return false
	}
	loc := d.cfg.QuietLocation
	if loc == nil {
		loc = time.UTC
	}
	h := now.In(loc).Hour()
	s, e := d.cfg.QuietStartHour, d.cfg.QuietEndHour
	if s < e {
		return h >= s && h < e
	}
	// wraps midnight
	return h >= s || h < e
}

// render coalesces the selected alerts into ONE digest email. It is deterministic for a
// given selection. The SYSTEM-generated text (subject, headlines, section labels) is
// register-clean; each Line is printed with its provenance class, and AUTHORED lines are
// the verbatim curator notes — the only place a "why" appears.
func render(cfg Config, selected []Alert) Message {
	counts := map[Priority]int{}
	for _, a := range selected {
		counts[a.Priority]++
	}
	var parts []string
	for _, p := range []Priority{P1, P2, P3} {
		if counts[p] > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", p, counts[p]))
		}
	}
	noun := "alert"
	if len(selected) != 1 {
		noun = "alerts"
	}
	subject := fmt.Sprintf("[Vigil] %d %s — %s", len(selected), noun, strings.Join(parts, " "))

	var b strings.Builder
	b.WriteString("Vigil surfaced ")
	b.WriteString(fmt.Sprintf("%d classed %s.\n", len(selected), noun))
	b.WriteString("Each fact carries its class (MEASURED / PROJECTED / AUTHORED); Vigil joins them and never fabricates a reason or a direction.\n")
	for _, a := range selected {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("● [%s] %s\n", a.Priority, a.Headline))
		if a.Entity != "" {
			b.WriteString("    entity: " + a.Entity + "\n")
		}
		for _, ln := range a.Lines {
			b.WriteString(fmt.Sprintf("    [%s] %s\n", ln.Class, ln.Text))
		}
		if a.ConsoleLink != "" {
			b.WriteString("    → " + a.ConsoleLink + "\n")
		}
	}
	return Message{Subject: subject, Body: b.String(), To: append([]string(nil), cfg.To...)}
}
