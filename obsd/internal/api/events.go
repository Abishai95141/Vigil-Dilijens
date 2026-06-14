package api

import (
	"fmt"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
)

// EventsView surfaces the discrete-event lane (v3 T-C): kubelet/control-plane
// Events (OOMKilled, CrashLoopBackOff, …) ingested as MEASURED findings and JOINED
// — never fused — to the gauge phenomena on the same workload role. MEASURED: an
// event is a fact read from the API server. A corroboration is a CO-OCCURRENCE on a
// shared role CEI, surfaced adjacently with the AUTHORED "why" verbatim; it is never
// a cause and never upgrades a standalone event into a phenomenon match.
type EventsView struct {
	Class        string        `json:"class"` // MEASURED
	GeneratedAt  time.Time     `json:"generatedAt"`
	GraphVersion string        `json:"graphVersion"`
	Available    bool          `json:"available"` // false ⇒ the events lane is not enabled
	Summary      EventsSummary `json:"summary"`
	Events       []EventCard   `json:"events"`
	Note         string        `json:"note"`
}

// EventsSummary is the headline rollup.
type EventsSummary struct {
	Total        int `json:"total"`
	Corroborated int `json:"corroborated"` // joined to a gauge phenomenon on the same role
	Standalone   int `json:"standalone"`   // visible MEASURED finding, no gauge corroboration this snapshot
	Unresolved   int `json:"unresolved"`   // involved object had no resolvable role (instance-keyed)
}

// EventCard is one ingested event rendered for an operator, with its (optional)
// adjacent corroboration. Each side is labelled; the why is AUTHORED, verbatim.
type EventCard struct {
	Reason             string    `json:"reason"`
	Class              string    `json:"class"` // MEASURED (the event itself)
	Role               string    `json:"role"`  // the role CEI (or instance key when unresolved)
	RoleLabel          string    `json:"roleLabel"`
	RoleUnresolved     bool      `json:"roleUnresolved"`
	EntityCEI          string    `json:"entityCei"`
	Namespace          string    `json:"namespace"`
	Name               string    `json:"name"`
	Kind               string    `json:"kind"`
	Count              int32     `json:"count"`
	FirstSeen          time.Time `json:"firstSeen"`
	LastSeen           time.Time `json:"lastSeen"`
	LastSeenAgoSeconds float64   `json:"lastSeenAgoSeconds"` // derived at serve time

	// Corroboration (the JOIN, adjacent — never fused into the event):
	Corroborates            string `json:"corroborates"`                      // authored gauge phenomenon id ("" = no authored target)
	Corroborated            bool   `json:"corroborated"`                      // a gauge finding for Corroborates exists on this role now
	CorroborationWhy        string `json:"corroborationWhy,omitempty"`        // AUTHORED, verbatim (only when Corroborated)
	CorroborationProvenance string `json:"corroborationProvenance,omitempty"` // author@version of the authored why
	Summary                 string `json:"summary"`                           // a MEASURED, register-clean one-liner
}

const eventsNote = "Discrete k8s events ingested as MEASURED findings, joined (never fused) to gauge phenomena on the same role. A corroboration is a co-occurrence with an AUTHORED why surfaced verbatim — never a cause; a standalone event is visible but never upgraded to a match."

// BuildEvents composes the view from this snapshot's corroborated events. Pure given
// (corroborated events, now); off the deterministic path. LastSeenAgoSeconds is
// derived at serve time, never stored (like the findings feed).
func BuildEvents(graphVersion string, now time.Time, ces []events.CorroboratedEvent) *EventsView {
	v := &EventsView{
		Class: "MEASURED", GeneratedAt: now.UTC(), GraphVersion: graphVersion,
		Available: true, Events: []EventCard{}, Note: eventsNote,
	}
	for i := range ces {
		ce := &ces[i]
		e := ce.Event
		ago := now.Sub(e.LastTimestamp)
		if ago < 0 {
			ago = 0
		}
		label := roleHuman(e.RoleCEI)
		card := EventCard{
			Reason: e.Reason, Class: "MEASURED", Role: e.RoleCEI, RoleLabel: label,
			RoleUnresolved: e.RoleUnresolved, EntityCEI: e.EntityCEI,
			Namespace: e.Namespace, Name: e.Name, Kind: e.Kind, Count: e.Count,
			FirstSeen: e.FirstTimestamp, LastSeen: e.LastTimestamp, LastSeenAgoSeconds: ago.Seconds(),
			Corroborates: ce.Corroborates, Corroborated: ce.GaugeRoleMatch,
			Summary: eventSummary(e.Reason, label, e.Count, ce),
		}
		if ce.GaugeRoleMatch {
			card.CorroborationWhy = ce.Why
			if ce.Author != "" {
				card.CorroborationProvenance = ce.Author + "@" + ce.Version
			}
		}
		v.Events = append(v.Events, card)
		v.Summary.Total++
		switch {
		case e.RoleUnresolved:
			v.Summary.Unresolved++
		case ce.GaugeRoleMatch:
			v.Summary.Corroborated++
		default:
			v.Summary.Standalone++
		}
	}
	return v
}

// unavailableEvents is the honest empty state when the events lane is off (no
// --events-enabled): say so rather than imply no events occurred.
func unavailableEvents(graphVersion string, now time.Time) *EventsView {
	return &EventsView{
		Class: "MEASURED", GeneratedAt: now.UTC(), GraphVersion: graphVersion,
		Available: false, Events: []EventCard{},
		Note: "The discrete-event lane is not enabled (needs --events-enabled). No k8s events are ingested.",
	}
}

// eventSummary is a MEASURED, register-clean one-liner — a count + an adjacency,
// never a cause. A corroboration is stated as "co-occurs with", never "because".
func eventSummary(reason, roleLabel string, count int32, ce *events.CorroboratedEvent) string {
	base := fmt.Sprintf("%s on %s (x%d)", reason, roleLabel, count)
	if ce.GaugeRoleMatch {
		return base + fmt.Sprintf(" — co-occurs with %s on the same role (corroborating)", ce.Corroborates)
	}
	return base + " — standalone (no gauge phenomenon corroborates it on this role)"
}
