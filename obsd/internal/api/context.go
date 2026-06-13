package api

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Context windows (doc 10 M6, begun in Phase 2 per doc 13). An operator names a
// span of time around a KNOWN event — a deploy, a config change, an incident, a
// maintenance window — and annotates it. Two purposes:
//
//   - Surfacing: the timeline + insights can frame "what was happening" against
//     operator-meaningful boundaries instead of raw wall-clock.
//   - Phase-3 SPLICE POINTS (doc 09 §3.4): a known event boundary is exactly where
//     the forecasting clock's context should be CUT — feeding the pre-event past
//     into a post-event forecast pollutes it. Context windows are the operator-
//     authored source of those boundaries (the most gating Phase-3 unknown, doc 13
//     §6 Q9). This is why they begin in Phase 2: the inventory of clean splice
//     points is audited now so Phase-3 decomposition has them.
//
// A context window is an operator ANNOTATION, not a measurement and not a forecast —
// it carries no provenance class of its own; it frames the classed data, never
// fuses with it.

// ContextWindowKind is the event class an operator marks.
type ContextWindowKind string

const (
	WindowDeploy      ContextWindowKind = "deploy"
	WindowConfig      ContextWindowKind = "config-change"
	WindowIncident    ContextWindowKind = "incident"
	WindowMaintenance ContextWindowKind = "maintenance"
)

var knownWindowKinds = map[ContextWindowKind]bool{
	WindowDeploy: true, WindowConfig: true, WindowIncident: true, WindowMaintenance: true,
}

// ContextWindow is one operator-defined annotated time span.
type ContextWindow struct {
	ID         string            `json:"id"`
	Label      string            `json:"label"`
	Kind       ContextWindowKind `json:"kind"`
	StartAt    time.Time         `json:"startAt"`
	EndAt      time.Time         `json:"endAt"` // zero = open / instantaneous marker
	Annotation string            `json:"annotation"`
	Author     string            `json:"author"`
	CreatedAt  time.Time         `json:"createdAt"`
	// SpliceEligible marks a window whose boundary is a clean forecasting splice
	// point (doc 09 §3.4). Deploys + config changes are clean container/process
	// boundaries; an incident span is a region to AVOID forecasting across, not a
	// splice. Derived from Kind, surfaced so Phase 3 can consume it.
	SpliceEligible bool `json:"spliceEligible"`
}

// ContextWindowStore is the thread-safe registry of operator-defined windows. It is
// off the deterministic detection path (annotations, not findings); it persists
// across the process only if backed by a store (Phase 3) — v1 is in-memory, stated.
type ContextWindowStore struct {
	mu      sync.RWMutex
	windows map[string]ContextWindow
	nextID  int
}

// NewContextWindowStore creates an empty store.
func NewContextWindowStore() *ContextWindowStore {
	return &ContextWindowStore{windows: map[string]ContextWindow{}, nextID: 1}
}

// Add validates and stores a window, returning the stored copy (with id + derived
// fields). now is injected (no time.Now in logic — testable + deterministic).
func (s *ContextWindowStore) Add(w ContextWindow, now time.Time) (ContextWindow, error) {
	if w.Label == "" {
		return ContextWindow{}, fmt.Errorf("context window needs a label")
	}
	if !knownWindowKinds[w.Kind] {
		return ContextWindow{}, fmt.Errorf("unknown window kind %q (deploy|config-change|incident|maintenance)", w.Kind)
	}
	if w.StartAt.IsZero() {
		return ContextWindow{}, fmt.Errorf("context window needs a startAt")
	}
	if !w.EndAt.IsZero() && w.EndAt.Before(w.StartAt) {
		return ContextWindow{}, fmt.Errorf("endAt %s is before startAt %s", w.EndAt, w.StartAt)
	}
	if w.Author == "" {
		return ContextWindow{}, fmt.Errorf("context window needs an author (it is an authored annotation)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w.ID = fmt.Sprintf("cw-%d", s.nextID)
	s.nextID++
	w.CreatedAt = now
	// A deploy or config change is a clean process/container boundary → a splice
	// point. An incident is a region to avoid forecasting across, not a splice.
	w.SpliceEligible = w.Kind == WindowDeploy || w.Kind == WindowConfig
	s.windows[w.ID] = w
	return w, nil
}

// List returns all windows, newest start first (deterministic; id as tiebreak).
func (s *ContextWindowStore) List() []ContextWindow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ContextWindow, 0, len(s.windows))
	for _, w := range s.windows {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartAt.Equal(out[j].StartAt) {
			return out[i].StartAt.After(out[j].StartAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// SplicePoints returns the splice-eligible window boundaries in time order — the
// Phase-3 decomposition consumer (doc 09 §3.4). Each clean boundary is a StartAt
// (the event instant) of a deploy/config window.
func (s *ContextWindowStore) SplicePoints() []time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pts []time.Time
	for _, w := range s.windows {
		if w.SpliceEligible {
			pts = append(pts, w.StartAt)
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Before(pts[j]) })
	return pts
}

// ContextWindowsView is the surface payload.
type ContextWindowsView struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	Windows     []ContextWindow `json:"windows"`
	SpliceCount int             `json:"spliceCount"` // how many are Phase-3 splice points
	Note        string          `json:"note"`
}
