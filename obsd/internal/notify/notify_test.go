package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC) // noon UTC, hour 12 (outside the 22→7 quiet window)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func baseCfg() Config {
	return Config{
		To:            []string{"ops@example.com"},
		Cooldown:      30 * time.Minute,
		RatePerWindow: 10,
		RateWindow:    time.Hour,
	}
}

func cascadeAlert() Alert {
	return Alert{
		Priority: P1, Kind: "cascade-chain",
		Headline: "Degradation cascade: cartservice → frontend",
		Entity:   "Deployment/cartservice", DedupKey: "cascade|cartservice|frontend",
		Lines: []Line{
			{ClassMeasured, "observed flow: cartservice → frontend (valid)"},
			{ClassAuthored, "a degraded callee propagates impact to its callers"}, // verbatim curator note — uses "propagates"
		},
		ConsoleLink: "http://localhost:5173/root-cause", At: t0,
	}
}

func oomAlert() Alert {
	return Alert{
		Priority: P1, Kind: "oom-crash",
		Headline: "OOM kill (cgroup-level) on currencyservice",
		Entity:   "Deployment/currencyservice", DedupKey: "oom|currencyservice|PHEN_OOM_KILL_CGROUP",
		Lines:       []Line{{ClassMeasured, "PHEN_OOM_KILL_CGROUP on traffic/currencyservice (Deployment), quality full"}},
		ConsoleLink: "http://localhost:5173/incidents", At: t0,
	}
}

func forecastAlert() Alert {
	return Alert{
		Priority: P2, Kind: "forecast-crossing",
		Headline: "Forecast crossing (PROJECTED): working_set on currencyservice",
		Entity:   "Deployment/currencyservice", DedupKey: "fc|currencyservice|working_set",
		Lines: []Line{
			{ClassProjected, "working_set projected to cross 512 MiB at 2026-06-21T12:40:00Z (band 12:35–12:48, confidence moderate)"},
			{ClassMeasured, "graph-declared precursor phenomena for this metric (cited below):"},
			{ClassAuthored, "PHEN_MEMORY_PRESSURE"}, // verbatim authored id, its own line — join never fuse
		},
		ConsoleLink: "http://localhost:5173/forecast", At: t0,
	}
}

func TestStoreCooldownLedger(t *testing.T) {
	s := newStore(t)
	a := cascadeAlert()
	if _, ok, err := s.LastSent(a.DedupKey); err != nil || ok {
		t.Fatalf("unsent key: ok=%v err=%v (want false,nil)", ok, err)
	}
	if err := s.RecordSent(t0, a); err != nil {
		t.Fatal(err)
	}
	last, ok, err := s.LastSent(a.DedupKey)
	if err != nil || !ok || !last.Equal(t0) {
		t.Fatalf("after send: last=%v ok=%v err=%v (want %v,true,nil)", last, ok, err, t0)
	}
	// re-send advances last_sent + increments count
	if err := s.RecordSent(t0.Add(time.Hour), a); err != nil {
		t.Fatal(err)
	}
	hist, err := s.History(10)
	if err != nil || len(hist) != 1 {
		t.Fatalf("history: %v len=%d (want 1 row)", err, len(hist))
	}
	if hist[0].SendCount != 2 {
		t.Errorf("send_count = %d, want 2", hist[0].SendCount)
	}
}

func TestDispatchCoalescesIntoOneEmail(t *testing.T) {
	fake := &FakeNotifier{}
	d := NewDispatcher(baseCfg(), newStore(t), fake, t0)
	res, err := d.Dispatch(context.Background(), t0, []Alert{cascadeAlert(), oomAlert(), forecastAlert()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Selected != 3 || !res.Sent {
		t.Fatalf("res = %+v, want 3 selected + sent", res)
	}
	if fake.Count() != 1 {
		t.Fatalf("sent %d emails, want 1 coalesced digest", fake.Count())
	}
	m := fake.Messages()[0]
	if !strings.Contains(m.Subject, "3 alerts") || !strings.Contains(m.Subject, "P1×2") || !strings.Contains(m.Subject, "P2×1") {
		t.Errorf("subject %q missing coalesced counts", m.Subject)
	}
}

func TestDispatchCooldownEdgeTrigger(t *testing.T) {
	fake := &FakeNotifier{}
	st := newStore(t)
	d := NewDispatcher(baseCfg(), st, fake, t0)
	a := cascadeAlert()

	// first tick: sent
	if res, _ := d.Dispatch(context.Background(), t0, []Alert{a}); res.Selected != 1 {
		t.Fatalf("tick1 selected %d, want 1", res.Selected)
	}
	// 10 min later, still active: suppressed by the 30m cooldown (edge-trigger, no spam)
	if res, _ := d.Dispatch(context.Background(), t0.Add(10*time.Minute), []Alert{a}); res.Selected != 0 || res.Suppressed != 1 {
		t.Fatalf("tick2 = %+v, want 0 selected / 1 suppressed", res)
	}
	// past the cooldown: re-fires (escalation reminder)
	if res, _ := d.Dispatch(context.Background(), t0.Add(31*time.Minute), []Alert{a}); res.Selected != 1 {
		t.Fatalf("tick3 selected %d, want 1 (cooldown elapsed)", res.Selected)
	}
	if fake.Count() != 2 {
		t.Errorf("sent %d emails over 3 ticks, want 2", fake.Count())
	}
}

func TestDispatchQuietHoursHoldsP2NotP1(t *testing.T) {
	cfg := baseCfg()
	cfg.QuietStartHour, cfg.QuietEndHour = 22, 7 // wraps midnight
	cfg.QuietP1Breakthrough = true
	fake := &FakeNotifier{}
	d := NewDispatcher(cfg, newStore(t), fake, t0)

	night := time.Date(2026, 6, 21, 23, 0, 0, 0, time.UTC) // hour 23 ∈ quiet
	res, err := d.Dispatch(context.Background(), night, []Alert{oomAlert(), forecastAlert()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Selected != 1 { // only the P1 OOM breaks through; the P2 forecast is held
		t.Fatalf("quiet-hours selected %d, want 1 (P1 only)", res.Selected)
	}
	m := fake.Messages()[0]
	if strings.Contains(m.Body, "forecast-crossing") || strings.Contains(m.Subject, "P2") {
		t.Errorf("P2 leaked through quiet hours: subject=%q", m.Subject)
	}
}

func TestDispatchRateLimit(t *testing.T) {
	cfg := baseCfg()
	cfg.RatePerWindow, cfg.RateWindow = 2, time.Hour // only 2 emails/hour
	cfg.Cooldown = 0                                 // isolate the rate limiter
	fake := &FakeNotifier{}
	d := NewDispatcher(cfg, newStore(t), fake, t0)

	// 5 distinct keys in one tick → only 2 fit the budget; the rest are held
	alerts := make([]Alert, 5)
	for i := range alerts {
		a := oomAlert()
		a.DedupKey = "oom|svc" + string(rune('a'+i))
		alerts[i] = a
	}
	res, err := d.Dispatch(context.Background(), t0, alerts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Selected != 2 || res.Suppressed != 3 {
		t.Fatalf("rate limit res = %+v, want 2 selected / 3 suppressed", res)
	}
}

func TestDispatchSendFailureIsNonGating(t *testing.T) {
	st := newStore(t)
	fake := &FakeNotifier{Err: errors.New("smtp down")}
	d := NewDispatcher(baseCfg(), st, fake, t0)
	a := cascadeAlert()

	_, err := d.Dispatch(context.Background(), t0, []Alert{a})
	if err == nil {
		t.Fatal("expected the send error to surface (non-gating: caller logs it)")
	}
	// nothing recorded ⇒ the fact retries next tick (no silent drop)
	if _, ok, _ := st.LastSent(a.DedupKey); ok {
		t.Error("a failed send must NOT record the key (it would suppress the retry)")
	}
	// recover: same fact next tick sends
	fake.Err = nil
	if res, _ := d.Dispatch(context.Background(), t0.Add(time.Minute), []Alert{a}); res.Selected != 1 {
		t.Errorf("after recovery selected %d, want 1", res.Selected)
	}
}

func TestRenderDeterministic(t *testing.T) {
	sel := []Alert{cascadeAlert(), oomAlert(), forecastAlert()}
	a := render(baseCfg(), sel)
	b := render(baseCfg(), sel)
	if a.Subject != b.Subject || a.Body != b.Body {
		t.Error("render must be deterministic for the same selection")
	}
}

// TestCharterScaffoldingCleanAndClassed is the charter guard: every SYSTEM-generated
// string (subject + headlines + non-AUTHORED line text) is register-clean — no causal
// token, and a PROJECTED clause says "projected", never "will". The AUTHORED notes and
// the console route URLs are exempt (a curator's verbatim note legitimately uses
// "propagates"; a URL path is structural, not a claim). Every Line carries a valid class.
func TestCharterScaffoldingCleanAndClassed(t *testing.T) {
	forbidden := []string{"cause", "caused", "because of", "due to", "anomaly score", " will ", "root-caused"}
	sel := []Alert{cascadeAlert(), oomAlert(), forecastAlert()}
	m := render(baseCfg(), sel)

	scan := func(label, s string) {
		low := strings.ToLower(s)
		for _, tok := range forbidden {
			if strings.Contains(low, tok) {
				t.Errorf("forbidden causal token %q in system text (%s): %q", tok, label, s)
			}
		}
	}
	scan("subject", m.Subject)
	// Scan the WHOLE rendered body line-by-line, exempting only the verbatim AUTHORED
	// notes and the route-URL lines (structural, not system claims).
	for _, bl := range strings.Split(m.Body, "\n") {
		tl := strings.TrimSpace(bl)
		if strings.HasPrefix(tl, "[AUTHORED]") || strings.HasPrefix(tl, "→") {
			continue
		}
		scan("body", bl)
	}
	for _, a := range sel {
		scan("headline", a.Headline)
		sawProjected := false
		for _, ln := range a.Lines {
			switch ln.Class {
			case ClassMeasured, ClassProjected, ClassAuthored:
			default:
				t.Errorf("line has invalid/empty class %q: %q", ln.Class, ln.Text)
			}
			if ln.Class == ClassProjected {
				sawProjected = true
				if strings.Contains(strings.ToLower(ln.Text), " will ") {
					t.Errorf("PROJECTED clause uses forbidden 'will' register: %q", ln.Text)
				}
				if !strings.Contains(strings.ToLower(ln.Text), "projected") {
					t.Errorf("PROJECTED clause must use the 'projected' register: %q", ln.Text)
				}
			}
			if ln.Class != ClassAuthored { // authored notes are verbatim, exempt
				scan("measured/projected line", ln.Text)
			}
		}
		if a.Kind == "forecast-crossing" && !sawProjected {
			t.Errorf("forecast alert %q carries no PROJECTED clause", a.Headline)
		}
	}
	// the AUTHORED note that uses "propagates" must NOT have been censored
	if !strings.Contains(m.Body, "propagates impact to its callers") {
		t.Error("the verbatim AUTHORED note should be surfaced, not stripped")
	}
}
