package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

// IncidentsView surfaces the durable cross-run incident memory (v3 T-B): each
// phenomenon-on-a-role joined across time, with how many distinct episodes it has had
// (recurrence) and over what span. MEASURED — recurrence is a deterministic arithmetic
// consequence of MEASURED findings (doc 01); the phenomenon id is surfaced verbatim.
// No incident field asserts a cause or a projection: "occurred 3 times" is a count,
// never a "because" or a "will".
type IncidentsView struct {
	Class        string           `json:"class"` // MEASURED
	GeneratedAt  time.Time        `json:"generatedAt"`
	GraphVersion string           `json:"graphVersion"`
	Available    bool             `json:"available"` // false ⇒ incident memory not enabled / store absent
	Summary      IncidentsSummary `json:"summary"`
	Incidents    []IncidentCard   `json:"incidents"`
	Note         string           `json:"note"`
}

// IncidentsSummary is the headline rollup.
type IncidentsSummary struct {
	Total      int `json:"total"`
	Recurring  int `json:"recurring"`  // recurrenceCount > 1
	Unresolved int `json:"unresolved"` // keyed on an instance because the role was unresolvable
}

// IncidentCard is one durable incident, rendered for an operator.
type IncidentCard struct {
	Phenomenon         string    `json:"phenomenon"`
	Role               string    `json:"role"` // the role CEI key (or instance key when unresolved)
	RoleLabel          string    `json:"roleLabel"`
	RoleUnresolved     bool      `json:"roleUnresolved"`
	RecurrenceCount    int       `json:"recurrenceCount"`
	FirstSeen          time.Time `json:"firstSeen"`
	LastSeen           time.Time `json:"lastSeen"`
	LastSeenAgoSeconds float64   `json:"lastSeenAgoSeconds"` // derived at serve time
	LifespanSeconds    int64     `json:"lifespanSeconds"`
	Summary            string    `json:"summary"` // a MEASURED, register-clean one-liner
}

const incidentsNote = "Cross-run incident memory: each phenomenon joined across time on its role, with how many distinct episodes (recurrence) it has had. MEASURED — counts and timestamps, never a cause or a forecast."

// BuildIncidents composes the view from durable incident rows. Pure given (rows, now);
// off the deterministic path. LastSeenAgoSeconds is derived at serve time (like the
// findings feed), never stored.
func BuildIncidents(graphVersion string, now time.Time, rows []store.IncidentRow) *IncidentsView {
	v := &IncidentsView{
		Class: "MEASURED", GeneratedAt: now.UTC(), GraphVersion: graphVersion,
		Available: true, Incidents: []IncidentCard{}, Note: incidentsNote,
	}
	for _, r := range rows {
		label := roleHuman(r.RoleCEI)
		ago := now.Sub(r.LastSeen)
		if ago < 0 {
			ago = 0
		}
		v.Incidents = append(v.Incidents, IncidentCard{
			Phenomenon: r.Phenomenon, Role: r.RoleCEI, RoleLabel: label,
			RoleUnresolved: r.RoleUnresolved, RecurrenceCount: r.RecurrenceCount,
			FirstSeen: r.FirstSeen, LastSeen: r.LastSeen,
			LastSeenAgoSeconds: ago.Seconds(), LifespanSeconds: r.LifespanSeconds,
			Summary: incidentSummary(r.Phenomenon, label, r.RecurrenceCount),
		})
		v.Summary.Total++
		if r.RecurrenceCount > 1 {
			v.Summary.Recurring++
		}
		if r.RoleUnresolved {
			v.Summary.Unresolved++
		}
	}
	return v
}

// unavailableIncidents is the honest empty state when the incident memory is off (no
// durable store, or --incident-memory not set): say so rather than imply no incidents.
func unavailableIncidents(graphVersion string, now time.Time) *IncidentsView {
	return &IncidentsView{
		Class: "MEASURED", GeneratedAt: now.UTC(), GraphVersion: graphVersion,
		Available: false, Incidents: []IncidentCard{},
		Note: "Incident memory is not enabled (needs --incident-memory and a --db store). No cross-run recurrence is tracked.",
	}
}

// roleHuman renders a CEI key as "ns/tail" (role or instance), best-effort.
func roleHuman(key string) string {
	parts := strings.Split(key, "|")
	if len(parts) >= 5 {
		if parts[2] != "" {
			return parts[2] + "/" + parts[4]
		}
		return parts[4]
	}
	return key
}

// incidentSummary is a MEASURED, register-clean one-liner — a count, never a cause.
func incidentSummary(phenomenon, roleLabel string, recurrence int) string {
	if recurrence <= 1 {
		return fmt.Sprintf("%s on %s — seen once", phenomenon, roleLabel)
	}
	return fmt.Sprintf("%s on %s — %d distinct occurrences (recurring)", phenomenon, roleLabel, recurrence)
}
