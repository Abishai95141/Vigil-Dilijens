package binding

import (
	"strings"
	"time"
)

// EntityConfig is the binding engine's read-only view of the customer's declared
// configuration — the borrowed-normativity source (doc 04 §3.4). Implementations:
// a live API-backed view in cmd/obsd (discovery-time List calls, never hot path)
// and fakes in tests. All lookups are by the same coordinates identity (03) uses,
// so config rows join bindings on exact CEI coordinates, never fuzzily.
type EntityConfig interface {
	// Pod returns the declared per-container resource config of a pod.
	Pod(namespace, name string) (PodConfig, bool)
	// Node returns a node's declared (API-reported) allocatable capacity.
	Node(name string) (NodeConfig, bool)
	// PVCs enumerates the cluster's PersistentVolumeClaims at discovery time.
	// (PVC instances are not yet part of the identity inventory; the enumeration
	// is config-sourced and joins identity once 03 tracks PVC lifecycles.)
	PVCs() []PVCRef
	// PVC returns a claim's declared storage request.
	PVC(namespace, name string) (PVCConfig, bool)
}

// PodConfig is a pod's declared container resources. Zero values mean UNDECLARED —
// the resolvability hole, never silently defaulted.
type PodConfig struct {
	Containers []ContainerConfig // in spec order (deterministic)

	// SLOs are the pod's CUSTOMER-DECLARED application SLO bars (doc 15 cap. A),
	// keyed by config-path ("slo.queue.max_depth" -> 1000). Read from the customer's
	// OWN object (a vigil.io/slo.<metric> annotation) — borrowed normativity, exactly
	// like a resources.limit. An absent key means UNDECLARED: the app rule binds
	// unbounded/listed, NEVER a learned-or-default capacity (the charter ban). nil
	// when the pod declares no SLO.
	SLOs map[string]float64
}

// ContainerConfig carries one container's declared limits. 0 = not declared.
type ContainerConfig struct {
	Name          string
	MemLimitBytes int64
	CPULimitMilli int64
}

// NodeConfig carries a node's declared allocatable capacity. 0 = unknown.
type NodeConfig struct {
	AllocatableMemoryBytes int64
}

// PVCRef names a claim.
type PVCRef struct {
	Namespace string
	Name      string
}

// PVCConfig carries a claim's declared storage request. 0 = not declared.
type PVCConfig struct {
	RequestedStorageBytes int64
}

// State is the exhaustive binding state (doc 04 §3.5): every (entity, variable)
// pair lands in exactly one — hidden gaps are impossible by construction.
type State string

const (
	StateBound      State = "bound"        // instantiated against a live entity; bar resolution recorded
	StateUnresolved State = "unresolved"   // expected for the entity but its config row could not be read
	StateOutOfScope State = "out-of-scope" // excluded with a stated reason (e.g. eligibility gate)
)

// Validation is the semantic-QA status (doc 04 §3.2). The semantic validation
// suite is doc 04 M3; until it runs, every binding is SUSPECT BY CONSTRUCTION —
// usable but marked, never silently promoted to verified.
type Validation string

const (
	ValidationVerified Validation = "verified"
	ValidationSuspect  Validation = "suspect"
	ValidationFailed   Validation = "failed"
)

// BarSource records where a resolved bar came from (doc 04 §3.4 precedence:
// customer config -> operator override -> ontology default). Operator overrides
// do not exist yet (no override store); the vocabulary carries them for when they do.
type BarSource string

const (
	SourceConfig   BarSource = "config"
	SourceOverride BarSource = "override"
	SourceDefault  BarSource = "default" // ALWAYS flagged where surfaced
)

// ResolvedBar is doc 04 §3.7's "Resolved bar" record: one instance's crossable limit.
type ResolvedBar struct {
	Kind       string // config-relative | absolute | rate-of-change
	Source     BarSource
	Flagged    bool      // true iff Source != config (lower-trust bar, doc 04 §3.4)
	ConfigPath string    // the path read (config-relative only)
	Factor     float64   // multiplier applied to the config value
	Value      float64   // the bar itself
	Unit       string    // bytes | millicores | ratio | count
	Direction  string    // above | below — which side is the violation
	Window     string    // evaluation window (from the rule)
	ResolvedAt time.Time // resolution stamp (re-binding staleness, doc 04 §3.6)
}

// Emission is the operational collection glue recorded per binding (doc 04 §3.1
// mechanism 3): which exporter emits the stream and how it is collected, copied
// verbatim from the authored signal.
type Emission struct {
	Source           string // e.g. "cAdvisor"
	CollectionMethod string // e.g. "Prometheus scrape on https://node:10250/metrics/cadvisor"
}

// Binding is doc 04 §3.7's "Binding record": one (entity instance, variable) pair.
type Binding struct {
	CEIKey     string // instance-layer CEI key (the only join key, doc 03)
	RoleKey    string // durable role CEI key ("" for role-less kinds, e.g. Node)
	Entity     string // Container | Pod | Node | PVC (the rule's fan-out scope)
	Container  string // container name when Entity == Container
	RuleID     string
	Metric     string // the concrete variable (rule metric)
	State      State
	Validation Validation
	Reason     string       // honest annotation: why out-of-scope/unresolved/unbounded
	Bar        *ResolvedBar // nil <=> no crossable limit (unbounded -> Tier-B ineligible)
	Emission   Emission     // collection glue from the authored signal (mechanism 3)
}

// StreamUID returns the CEI UID a binding's observation stream carries — the
// (CEI UID, metric) join key into the hot store. Container streams are keyed by
// podUID/container (the cAdvisor scope); pod/node by the bare UID. Returns "" for
// entities with no scraped channel yet (PVC pseudo-keys).
func (b *Binding) StreamUID() string {
	parts := strings.Split(b.CEIKey, "|")
	if len(parts) < 6 || parts[0] != "i" {
		return ""
	}
	uid := parts[5]
	if b.Entity == "Container" {
		return uid + "/" + b.Container
	}
	return uid
}

// RoleBinding is the role-layer instantiation (doc 04 §3.3 axis 3): the durable
// role's aggregate over its current member instances. Role bindings absorb
// instance churn by construction.
type RoleBinding struct {
	RoleKey   string
	RuleID    string
	Container string
	Members   int // live member instances
	Bound     int // members with a resolved bar
	Unbounded int // members instantiated but without a crossable limit
}

// RuleCoverage is the per-rule slice of the coverage report (doc 04 §3.5).
type RuleCoverage struct {
	RuleID       string
	Kind         string
	EntityScope  string
	Instantiated int // (entity, variable) pairs created
	ConfigBound  int // bar from customer config
	DefaultBound int // bar from flagged ontology default
	Unbounded    int // instantiated, no crossable limit (listed)
	OutOfScope   int // excluded with reason
	Unresolved   int // config row unreadable
}

// CoverageReport is the onboarding deliverable (doc 04 §3.5): what this system can
// see and check on THIS cluster, and its honesty about the rest.
type CoverageReport struct {
	PerRule []RuleCoverage

	// Resolvability is the fraction of config-relative instantiations with a
	// config-sourced bar (doc 04 §3.4's resolvability metric).
	Resolvability float64
	// ConfigEligible / ConfigBound back the metric (numerator/denominator shown,
	// never just a percentage).
	ConfigEligible int
	ConfigBound    int

	// UnboundedWorkloads lists every unbounded (entity, variable) pair:
	// "unbounded: no early-warning eligibility" — Tier-B ineligible, NEVER hidden.
	UnboundedWorkloads []string

	DefaultBars int // flagged default-sourced bars in play

	// Validation is the semantic-QA rollup (doc 04 §3.2): verified / suspect /
	// failed counts plus the hard findings. Populated by ValidateBindings.
	Validation QASummary

	// Notes are standing honesty caveats (pending milestones), stated rather than
	// implied.
	Notes []string
}

// Result is the bound customer graph, Phase-0b core: instance and role bindings
// with resolved bars, plus the coverage report. Deterministic: same graph version +
// same inventory + same config => byte-identical result (ResolvedAt aside, which is
// the injected compile time).
type Result struct {
	GraphVersion string
	At           time.Time
	Bindings     []Binding
	Roles        []RoleBinding
	Coverage     CoverageReport
}
