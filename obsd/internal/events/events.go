package events

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// EventFinding is a MEASURED finding sourced from one discrete Kubernetes Event
// (doc 03 join by CEI, NOT via the fingerprint matcher). Reason is the authored
// event reason (OOMKilled, CrashLoopBackOff, …); the involved object is resolved to
// its durable role CEI via the identity store. When the role is unresolvable —
// the store has not seen the object, or the kind has no role (a Node) — RoleCEI
// holds the instance key and RoleUnresolved is set: NEVER a guessed role.
type EventFinding struct {
	Reason         string    `json:"reason"`
	EntityCEI      string    `json:"entityCei"` // the instance CEI of the involved object
	RoleCEI        string    `json:"roleCei"`   // the role CEI (or instance key when unresolved)
	RoleUnresolved bool      `json:"roleUnresolved"`
	Namespace      string    `json:"namespace"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	Count          int32     `json:"count"`          // the Event's count (repeats coalesced by the API server)
	FirstTimestamp time.Time `json:"firstTimestamp"` // first occurrence (UTC)
	LastTimestamp  time.Time `json:"lastTimestamp"`  // most recent occurrence (UTC)
}

// Involved are the coordinates of an Event's involvedObject — the minimal mirror of
// the fields role-resolution needs. The collector maps the real ObjectReference
// onto this so the package carries no Kubernetes dependency.
type Involved struct {
	Namespace string
	Name      string
	Kind      string // Pod, Node, …
	UID       string
}

// ResolveEventRole resolves an Event's involved object to (instance key, role key,
// unresolved). It mints the instance CEI from the object's own coordinates and
// looks it up in the identity store — the SAME resolution the flow collector and
// the incident wiring use (flowcollect.go listPodInfo; main.go incident upsert):
// the role is the store's authoritative role CEI, never derived here. A miss (the
// store has not observed the object) or a role-less kind (Node) yields the instance
// key with unresolved = true. Pure given (store snapshot, clusterID, involved).
func ResolveEventRole(store *identity.Store, clusterID string, in Involved) (entityCEI, roleCEI string, unresolved bool) {
	inst := identity.CEI{
		Layer: identity.LayerInstance, Cluster: clusterID, Namespace: in.Namespace,
		Kind: in.Kind, Name: in.Name, UID: in.UID,
	}
	entityCEI = inst.Key()
	if store != nil {
		if rec, ok := store.Get(entityCEI); ok && rec.RoleCEI.RoleKey != "" {
			return entityCEI, rec.RoleCEI.Key(), false
		}
	}
	// No store record, or a kind with no role (Node): keep the instance key and
	// state the role is unresolved — never invent a workload role.
	return entityCEI, entityCEI, true
}

// Corroboration is one AUTHORED corroborating condition (the v3 T-C overlay): a
// curated mapping from an Event reason (on an involved kind) to the gauge
// phenomenon it corroborates, surfaced verbatim with provenance. Corroborates is
// empty for a STANDALONE-visible reason (e.g. CrashLoopBackOff today, which has no
// gauge phenomenon to corroborate) — the event is still a MEASURED finding.
type Corroboration struct {
	Reason        string `yaml:"reason" json:"reason"`
	InvolvedKind  string `yaml:"involved_kind" json:"involvedKind"`
	Corroborates  string `yaml:"corroborates" json:"corroborates"` // gauge phenomenon id ("" = standalone)
	Role          string `yaml:"role" json:"role"`                 // corroborating (the only legal role here)
	TemporalOrder string `yaml:"temporal_order" json:"temporalOrder"`
	Why           string `yaml:"why" json:"why"`
	Author        string `yaml:"author" json:"author"`
	Version       string `yaml:"version" json:"version"`
	Status        string `yaml:"status" json:"status"`
}

// CorroboratedEvent is one EventFinding joined to the gauge findings on the SAME
// role (join, never fuse). GaugeRoleMatch is true only when a gauge finding for the
// authored Corroborates phenomenon exists on this event's EXACT role CEI — the
// exact-CEI equality IS the join-fidelity guarantee (an event on role A can never
// corroborate a gauge finding on role B). Why is the AUTHORED corroboration,
// surfaced verbatim, set only when the join actually holds.
type CorroboratedEvent struct {
	Event          EventFinding `json:"event"`
	Corroborates   string       `json:"corroborates"`   // authored gauge phenomenon id ("" = no authored target)
	GaugeRoleMatch bool         `json:"gaugeRoleMatch"` // a gauge finding for Corroborates on the same role this snapshot
	Why            string       `json:"why"`            // AUTHORED corroboration text, verbatim (only when GaugeRoleMatch)
	Author         string       `json:"author"`
	Version        string       `json:"version"`
}

// Corroborate joins event findings to the gauge phenomena on the same role using
// the authored conditions. gaugeRoles maps a phenomenon id to the set of role CEI
// keys that have a gauge finding for it THIS snapshot (built by the caller from the
// tick's findings, resolved through the same identity store). A corroboration is
// asserted iff the authored Corroborates phenomenon has a gauge finding on the
// event's exact role CEI; otherwise the event is surfaced standalone (visible,
// MEASURED, NOT upgraded to a match). Pure and deterministic: the output is sorted
// by (reason, role, entity) so the same inputs always yield byte-identical bytes.
func Corroborate(evs []EventFinding, gaugeRoles map[string]map[string]bool, conds []Corroboration) []CorroboratedEvent {
	byReasonKind := make(map[string]Corroboration, len(conds))
	for _, c := range conds {
		byReasonKind[condKey(c.Reason, c.InvolvedKind)] = c
	}
	out := make([]CorroboratedEvent, 0, len(evs))
	for _, e := range evs {
		ce := CorroboratedEvent{Event: e}
		if c, ok := byReasonKind[condKey(e.Reason, e.Kind)]; ok && c.Corroborates != "" {
			ce.Corroborates = c.Corroborates
			// Join only on an EXACT role-CEI match. A role-unresolved event (RoleCEI ==
			// instance key) can never match a gauge finding's role CEI, so it is never
			// corroborated — honest, never guessed.
			if roles, has := gaugeRoles[c.Corroborates]; has && !e.RoleUnresolved && roles[e.RoleCEI] {
				ce.GaugeRoleMatch = true
				ce.Why = c.Why
				ce.Author = c.Author
				ce.Version = c.Version
			}
		}
		out = append(out, ce)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Event, out[j].Event
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		if a.RoleCEI != b.RoleCEI {
			return a.RoleCEI < b.RoleCEI
		}
		return a.EntityCEI < b.EntityCEI
	})
	return out
}

// Reasons returns the distinct event reasons the authored conditions cover — the
// filter the collector applies to the Events stream (we ingest only authored
// reasons, never the whole firehose).
func Reasons(conds []Corroboration) []string {
	seen := map[string]bool{}
	for _, c := range conds {
		seen[c.Reason] = true
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func condKey(reason, kind string) string { return reason + "\x00" + kind }
