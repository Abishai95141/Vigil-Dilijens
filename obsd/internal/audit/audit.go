package audit

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// ChangeEvent is a MEASURED record of one COMPLETED, mutating Kubernetes API call,
// parsed from an audit.k8s.io/v1 Event. It is a fact read from the audit log — the same
// provenance class as a threshold state. The changed object's coordinates resolve to a
// durable role CEI via the identity store (join by CEI, never fuse); until resolved,
// RoleCEI is empty.
type ChangeEvent struct {
	AuditID        string    `json:"auditId"`  // the audit Event's unique id (the dedup key)
	Verb           string    `json:"verb"`     // create | update | patch | delete | deletecollection
	Resource       string    `json:"resource"` // plural, as audit records it: "deployments", "configmaps"
	APIGroup       string    `json:"apiGroup,omitempty"`
	Namespace      string    `json:"namespace"`
	Name           string    `json:"name"`
	User           string    `json:"user"`           // the requesting user / serviceaccount
	ResponseCode   int32     `json:"responseCode"`   // HTTP status (2xx ⇒ the change took effect)
	RoleCEI        string    `json:"roleCei"`        // resolved role CEI (or own coordinate key when unresolved)
	RoleUnresolved bool      `json:"roleUnresolved"` // the identity store has not seen this object (no guessed role)
	Timestamp      time.Time `json:"timestamp"`      // the audit record's SOURCE completion ts (UTC), NEVER receipt time
}

// Incident is the minimal mirror of an active incident the audit lane joins against:
// the entity's role CEI, its namespace, and its onset time. main maps the incident
// store / active findings onto this, so the audit core imports neither.
type Incident struct {
	ID           string
	RoleCEI      string // the incident entity's role CEI ("" if none)
	Namespace    string
	Onset        time.Time // MEASURED onset (UTC)
	GraphVersion string
}

// Resolver maps a change object's coordinates to a durable role CEI. main supplies one
// backed by the identity store (the SAME store the events lane resolves against), so the
// audit core stays decoupled from identity + client-go. A miss returns ("", false) and
// the change is flagged RoleUnresolved — never a guessed role.
type Resolver func(namespace, resource, name string) (roleCEI string, resolved bool)

// JoinTier labels HOW a change and an incident are co-located. role-cei is the exact-CEI
// join (the join-fidelity guarantee, as in the events lane); namespace is the weaker
// shared-namespace adjacency, surfaced EXPLICITLY so the operator sees the join strength;
// "" means not co-located (no hypothesis is staged).
type JoinTier string

const (
	JoinRoleCEI   JoinTier = "role-cei"
	JoinNamespace JoinTier = "namespace"
	JoinNone      JoinTier = ""
)

// mutatingVerbs is the closed set of audit verbs that represent a CHANGE. Reads
// (get/list/watch) are not changes and are never ingested.
var mutatingVerbs = map[string]bool{
	"create": true, "update": true, "patch": true, "delete": true, "deletecollection": true,
}

// resourceKind maps a Kubernetes resource (plural, as audit records it) to its Kind (the
// identity store's role-key form). AUTHORED + finite — the workload-owning resources a
// change-to-incident join cares about. An unmapped resource resolves unresolved, never a
// guessed Kind.
var resourceKind = map[string]string{
	"pods":                     "Pod",
	"deployments":              "Deployment",
	"replicasets":              "ReplicaSet",
	"statefulsets":             "StatefulSet",
	"daemonsets":               "DaemonSet",
	"jobs":                     "Job",
	"cronjobs":                 "CronJob",
	"services":                 "Service",
	"configmaps":               "ConfigMap",
	"secrets":                  "Secret",
	"ingresses":                "Ingress",
	"horizontalpodautoscalers": "HorizontalPodAutoscaler",
	"persistentvolumeclaims":   "PersistentVolumeClaim",
}

// KindForResource returns the Kind for an audit resource (plural), and whether it is a
// mapped workload-owning kind. Exposed so main's identity-backed resolver and the
// audit-gate share one authored map.
func KindForResource(resource string) (string, bool) {
	k, ok := resourceKind[strings.ToLower(strings.TrimSpace(resource))]
	return k, ok
}

// auditLine is the minimal mirror of an audit.k8s.io/v1 Event the parser needs. The
// package carries no client-go dependency; the collector points the apiserver's audit
// log at us as JSONL.
type auditLine struct {
	AuditID string `json:"auditID"`
	Stage   string `json:"stage"`
	Verb    string `json:"verb"`
	User    struct {
		Username string `json:"username"`
	} `json:"user"`
	ObjectRef struct {
		Resource  string `json:"resource"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		UID       string `json:"uid"`
		APIGroup  string `json:"apiGroup"`
	} `json:"objectRef"`
	ResponseStatus struct {
		Code int32 `json:"code"`
	} `json:"responseStatus"`
	StageTimestamp time.Time `json:"stageTimestamp"`
}

// ParseEvents parses audit JSONL lines into MEASURED ChangeEvents. It keeps ONLY the
// authoritative final stage (ResponseComplete), only MUTATING verbs, only calls that
// actually took effect (2xx — a 4xx/5xx change did NOT happen, so it is not a change),
// and only named objects (something to join to). Lines are deduped by auditID and the
// result is sorted by (timestamp, auditID): byte-identical across runs and INVARIANT to
// the order lines arrive in. A malformed line is skipped, never fatal.
func ParseEvents(lines []string) []ChangeEvent {
	byID := make(map[string]ChangeEvent, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var a auditLine
		if err := json.Unmarshal([]byte(ln), &a); err != nil {
			continue
		}
		if a.Stage != "ResponseComplete" {
			continue // only the authoritative final stage is a completed change
		}
		verb := strings.ToLower(strings.TrimSpace(a.Verb))
		if !mutatingVerbs[verb] {
			continue
		}
		// A non-2xx response means the change did not take effect (a denied/conflicting
		// call). Code 0 (absent) is treated as success only when no status was recorded.
		if c := a.ResponseStatus.Code; c != 0 && (c < 200 || c >= 300) {
			continue
		}
		if a.AuditID == "" || a.ObjectRef.Name == "" {
			continue
		}
		ce := ChangeEvent{
			AuditID:      a.AuditID,
			Verb:         verb,
			Resource:     a.ObjectRef.Resource,
			APIGroup:     a.ObjectRef.APIGroup,
			Namespace:    a.ObjectRef.Namespace,
			Name:         a.ObjectRef.Name,
			User:         a.User.Username,
			ResponseCode: a.ResponseStatus.Code,
			Timestamp:    a.StageTimestamp.UTC(),
		}
		// Deterministic dedup: a clean audit log has one ResponseComplete per auditID, but a
		// merged/duplicated/replayed log can collide. Keep a FIXED winner by a total order
		// (never last-write-wins), so the result is invariant to line arrival order.
		if existing, ok := byID[a.AuditID]; ok && lessChange(existing, ce) {
			continue
		}
		byID[a.AuditID] = ce
	}
	out := make([]ChangeEvent, 0, len(byID))
	for _, ce := range byID {
		out = append(out, ce)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].Timestamp.Before(out[j].Timestamp)
		}
		return out[i].AuditID < out[j].AuditID
	})
	return out
}

// Resolve fills each change's RoleCEI / RoleUnresolved using the injected resolver. A
// resolved object carries the store's durable role CEI (the exact-CEI join key); an
// unresolved object keeps its own coordinate key and is flagged — never a guessed role.
// Pure given (changes, resolver); order preserved.
func Resolve(changes []ChangeEvent, r Resolver) []ChangeEvent {
	out := make([]ChangeEvent, len(changes))
	copy(out, changes)
	for i := range out {
		if r != nil {
			if cei, ok := r(out[i].Namespace, out[i].Resource, out[i].Name); ok && cei != "" {
				out[i].RoleCEI = cei
				out[i].RoleUnresolved = false
				continue
			}
		}
		out[i].RoleCEI = ownKey(out[i])
		out[i].RoleUnresolved = true
	}
	return out
}

func ownKey(c ChangeEvent) string {
	return "audit:" + c.Namespace + "/" + c.Resource + "/" + c.Name
}

// lessChange is a total order over change events sharing one auditID, used to pick a
// deterministic winner on a (pathological) auditID collision so dedup never depends on
// line arrival order.
func lessChange(a, b ChangeEvent) bool {
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	if a.Verb != b.Verb {
		return a.Verb < b.Verb
	}
	if a.Resource != b.Resource {
		return a.Resource < b.Resource
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.User != b.User {
		return a.User < b.User
	}
	return a.ResponseCode < b.ResponseCode
}

// Antecedents returns the changes that could be ANTECEDENT to an incident at `onset`:
// those whose SOURCE timestamp lies in [onset-lookback, onset). A change AT or AFTER
// onset is PRUNED — by the arrow of time it cannot precede the incident. This is a
// deterministic filter over MEASURED timestamps, NOT an inference of cause. Input is
// assumed sorted by timestamp (ParseEvents guarantees it); output preserves that order.
func Antecedents(changes []ChangeEvent, onset time.Time, lookback time.Duration) []ChangeEvent {
	if lookback <= 0 || onset.IsZero() {
		return nil
	}
	lo := onset.Add(-lookback)
	var out []ChangeEvent
	for _, c := range changes {
		if c.Timestamp.Before(onset) && !c.Timestamp.Before(lo) {
			out = append(out, c)
		}
	}
	return out
}

// coLocate returns the strongest co-location tier between a change and an incident: an
// exact role-CEI match (only for a resolved change), else a shared namespace, else none.
func coLocate(c ChangeEvent, inc Incident) JoinTier {
	if !c.RoleUnresolved && c.RoleCEI != "" && inc.RoleCEI != "" && c.RoleCEI == inc.RoleCEI {
		return JoinRoleCEI
	}
	if c.Namespace != "" && inc.Namespace != "" && c.Namespace == inc.Namespace {
		return JoinNamespace
	}
	return JoinNone
}

// Hypothesize stages a direction-free co-occurrence candidate for each change that is
// BOTH antecedent (arrow-of-time) AND co-located (role-CEI or namespace) with the
// incident. It NEVER asserts the change caused the incident: each candidate is a
// KindCausalHypothesis with relation "observed-adjacency" — "a change to X completed N
// seconds before incident Y, on the same <tier>" — staged for HUMAN verification. Pure +
// deterministic given (changes, incident, lookback): output sorted by subject.
func Hypothesize(changes []ChangeEvent, inc Incident, lookback time.Duration) []candidate.Candidate {
	ante := Antecedents(changes, inc.Onset, lookback)
	out := make([]candidate.Candidate, 0, len(ante))
	for _, c := range ante {
		tier := coLocate(c, inc)
		if tier == JoinNone {
			continue
		}
		deltaSec := int64(inc.Onset.Sub(c.Timestamp) / time.Second)
		object := c.Resource + "/" + c.Namespace + "/" + c.Name
		out = append(out, candidate.Candidate{
			Kind:     candidate.KindCausalHypothesis,
			Relation: "observed-adjacency",
			Subject:  "change:" + c.AuditID + " ~ incident:" + inc.ID,
			Payload: map[string]any{
				"verb":          c.Verb,
				"resource":      c.Resource,
				"object":        object,
				"user":          c.User,
				"changeTs":      c.Timestamp.UTC().Format(time.RFC3339Nano),
				"incidentOnset": inc.Onset.UTC().Format(time.RFC3339Nano),
				"deltaSeconds":  deltaSec,
				"joinTier":      string(tier),
				"namespace":     inc.Namespace,
			},
			Evidence: []candidate.EvidenceRef{
				{Kind: "audit-record", Ref: "auditID=" + c.AuditID, Detail: c.Verb + " " + object + " by " + c.User},
				{Kind: "incident", Ref: "incident=" + inc.ID},
				{Kind: "temporal-adjacency", Ref: "delta=" + strconv.FormatInt(deltaSec, 10) + "s",
					Detail: "change completed before incident onset (arrow-of-time)"},
				{Kind: "co-location", Ref: string(tier), Detail: coLocateDetail(tier, c, inc)},
			},
			Lineage: candidate.Lineage{
				Source: "audit", Method: "arrow-of-time-adjacency", GraphVersion: inc.GraphVersion,
				Inputs: []string{"auditID=" + c.AuditID, "incident=" + inc.ID},
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}

func coLocateDetail(tier JoinTier, c ChangeEvent, inc Incident) string {
	switch tier {
	case JoinRoleCEI:
		return "exact role-CEI match (" + c.RoleCEI + ")"
	case JoinNamespace:
		return "shared namespace (" + inc.Namespace + "); weaker than an exact-CEI join"
	default:
		return ""
	}
}

// HypothesizeAndStage runs Hypothesize for every incident and stages the resulting
// direction-free candidates into the firewalled P0 store, returning the count staged.
// `now` is injected (the store never reads the wall clock). This is the testable
// per-cycle core of the audit loop; the runtime loop is a thin mapper around it.
func HypothesizeAndStage(s *candidate.Store, now time.Time, changes []ChangeEvent, incidents []Incident, lookback time.Duration) (int, error) {
	total := 0
	for _, inc := range incidents {
		for _, c := range Hypothesize(changes, inc, lookback) {
			if _, err := s.Put(now, c); err != nil {
				return total, err
			}
			total++
		}
	}
	return total, nil
}
