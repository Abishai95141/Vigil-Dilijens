package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Authored overlays (doc 02 §3.6, doc 12). The base KG release is a clean vendored
// mirror; authored deltas — phenomenon span declarations and structured threshold
// rules — live in ontology/graph/overlays/*.yaml and are merged here at load.
// Overlays are AUTHORED-class content with author/version/status provenance; the
// loader merges and validates, it never invents. Merge order is the sorted file
// name order, and the release Version pin hashes base + overlays, so a changed
// overlay is a changed release (doc 12 §3.1).

// Span vocabulary (doc 02 §3.5): the complete set; nothing else exists.
const (
	SpanEntityLocal = "entity-local"
	SpanFirstOrder  = "first-order"
	SpanSecondOrder = "second-order"
)

// Threshold-rule kinds (doc 02 §3.4): the complete vocabulary of checks.
const (
	RuleConfigRelative = "config-relative"
	RuleAbsolute       = "absolute"
	RuleRateOfChange   = "rate-of-change"
	RuleCoOccurrence   = "co-occurrence"
)

// Machine-resolvable config paths (doc 02 §3.4 "config path"): the vocabulary the
// binding engine (04) knows how to read from live cluster objects. Adding a path
// here requires a matching resolver in binding.
const (
	PathContainerLimitsMemory = "container.resources.limits.memory"
	PathContainerLimitsCPU    = "container.resources.limits.cpu"
	PathPVCRequestsStorage    = "pvc.spec.resources.requests.storage"
	PathNodeAllocatableMemory = "node.status.allocatable.memory"
)

var knownConfigPaths = map[string]bool{
	PathContainerLimitsMemory: true,
	PathContainerLimitsCPU:    true,
	PathPVCRequestsStorage:    true,
	PathNodeAllocatableMemory: true,
}

// SLOConfigPathPrefix names the customer-declared application-SLO config-path family
// (doc 15 cap. A): "slo.queue.max_depth", "slo.freshness.max_age", etc. — resolved at
// bind time from the workload's own vigil.io/slo.* annotation (borrowed normativity).
// The family is open (any slo.* path), since SLO vocabulary is per-application, but the
// VALUE is always customer-declared, never learned or defaulted.
const SLOConfigPathPrefix = "slo."

// knownConfigPath reports whether a config_path is resolvable: a fixed k8s-resource path,
// or any customer-declared slo.* application SLO path.
func knownConfigPath(p string) bool {
	return knownConfigPaths[p] || strings.HasPrefix(p, SLOConfigPathPrefix)
}

var knownSpans = map[string]bool{SpanEntityLocal: true, SpanFirstOrder: true, SpanSecondOrder: true}

// knownTraversalEdgeTypes is the instance-topology edge vocabulary spans may walk
// (doc 02 §3.2; instance edges and their timestamps belong to identity, doc 03).
// "flow" is the v2 observed-flow (conntrack) edge (doc 15 Phase C): a cross-service
// span may declare it, so the authored cross-service relation is walkable in the
// bound graph. It stays OUT of params.requiredEdgeBudgets (flow is optional) and OUT
// of the deterministic digest — adding it to the vocabulary changes no existing span.
var knownTraversalEdgeTypes = map[string]bool{"runs-on": true, "mounts": true, "selects": true, "node-lease": true, "flow": true}

var knownEntityScopes = map[string]bool{"Container": true, "Pod": true, "Node": true, "PVC": true}

var knownDirections = map[string]bool{"above": true, "below": true}

// ThresholdRule is a structured, authored threshold rule (doc 02 §3.4): where one
// concrete variable's bar comes from. Config-relative rules read the customer's own
// config per instance at binding time (doc 04 §3.4); rules carrying an ontology
// Default produce flagged, lower-trust bars wherever surfaced.
type ThresholdRule struct {
	ID            string   `yaml:"id"`
	Signal        string   `yaml:"signal"`         // KG Signal node id (referential)
	Metric        string   `yaml:"metric"`         // concrete series within the signal (families bundle many)
	DivisorMetric string   `yaml:"divisor_metric"` // optional: evaluated quantity is Metric/DivisorMetric
	Kind          string   `yaml:"kind"`
	ConfigPath    string   `yaml:"config_path"`             // config-relative only
	Eligibility   string   `yaml:"eligibility_config_path"` // optional: instantiate only where this path resolves
	Factor        float64  `yaml:"factor"`                  // bar = config value x Factor
	Default       *float64 `yaml:"default"`                 // ontology default; ALWAYS flagged when used
	Direction     string   `yaml:"direction"`               // above | below
	EntityScope   string   `yaml:"entity_scope"`            // instantiation fan-out target (doc 04 axis 2)
	Window        string   `yaml:"window"`
	Rationale     string   `yaml:"rationale"`
}

// WindowDuration parses the rule's evaluation window.
func (r *ThresholdRule) WindowDuration() (time.Duration, error) {
	return time.ParseDuration(r.Window)
}

// spanDecl is one phenomenon's authored span declaration.
type spanDecl struct {
	Span               string   `yaml:"span"`
	TraversalEdgeTypes []string `yaml:"traversal_edge_types"`
	Rationale          string   `yaml:"rationale"`
}

// MemberCheck is an authored machine-readable check for one phenomenon member
// (doc 07 §3.1): how to satisfy the member from a fingerprint variable. It does not
// invent a member — it binds an already-authored one to a fingerprint facet.
type MemberCheck struct {
	Phenomenon string `yaml:"-"`
	Signal     string `yaml:"signal"` // KG Signal node id (must be a member of the phenomenon)
	Metric     string `yaml:"metric"` // the fingerprint variable consulted
	Facet      string `yaml:"facet"`  // level | slope | ratio | rate-guard
	Expect     string `yaml:"expect"` // rising | falling | crossed | at-or-above | breached
	// MinState is an optional materiality guard (doc 07 §3.6 sensitivity): for a
	// slope check it requires the variable to ALSO be at least this ladder rung, so
	// a trivial rise on an idle entity does not fire — only a rise that is material
	// relative to the bar. Empty = no guard.
	MinState string `yaml:"min_state"` // "" | at-threshold | above | well-above
	// On says WHERE the member's variable lives relative to the phenomenon's
	// anchor (doc 07 §3.2): "anchor" (default) — on the evaluated entity itself;
	// "neighbour" — on an entity one topology hop away along the phenomenon's
	// declared traversal edge types; "two-hop" — two hops of propagation (the
	// second-order walk, doc 07 M3: the traversal IS the detection path).
	// Neighbour checks are only legal on spanned phenomena, two-hop only on
	// second-order ones; the matcher satisfies them across VALID edges only,
	// degrades when ANY hop of the path is suspect, and never counts evidence
	// across an absent edge (the degrade-never-fabricate contract).
	On   string `yaml:"on"`   // "" | anchor | neighbour | two-hop
	Note string `yaml:"note"` // honesty caveat surfaced on the finding
}

// OnAnchor reports whether the check evaluates on the anchor entity itself.
func (c *MemberCheck) OnAnchor() bool { return c.On == "" || c.On == "anchor" }

var knownFacets = map[string]bool{"level": true, "slope": true, "ratio": true, "rate-guard": true}
var knownExpects = map[string]bool{"rising": true, "falling": true, "crossed": true, "at-or-above": true, "breached": true}
var knownMinStates = map[string]bool{"": true, "at-threshold": true, "above": true, "well-above": true}
var knownOns = map[string]bool{"": true, "anchor": true, "neighbour": true, "two-hop": true}

// overlayFile is the on-disk overlay shape. A file declares spans, rules, checks,
// and anchors (the entity kind a spanned phenomenon's checks evaluate at), and —
// doc 15 Phase C — authored phenomenon NODES + phenomenon_relation edges, so the
// base KG stays an immutable vendored mirror while curated deltas (new correlation
// groups, cascade relations) live in versioned overlays like every other authored
// delta. Added phenomena are applied BEFORE this file's spans/checks so they can
// be spanned/checked in the same overlay.
type overlayFile struct {
	Overlay   string                     `yaml:"overlay"`
	Version   int                        `yaml:"version"`
	Author    string                     `yaml:"author"`
	Status    string                     `yaml:"status"`
	Signals   []overlaySignal            `yaml:"signals"`
	Phenomena []overlayPhenomenon        `yaml:"phenomena"`
	Members   map[string][]overlayMember `yaml:"members"`
	Relations []overlayRelation          `yaml:"relations"`
	Spans     map[string]spanDecl        `yaml:"spans"`
	Rules     []ThresholdRule            `yaml:"rules"`
	Checks    map[string][]MemberCheck   `yaml:"checks"`
	Anchors   map[string]string          `yaml:"anchors"`
}

// overlaySignal is an authored Signal node added by an overlay (doc 15 cap. A): a new
// observable variable not in the base KG — e.g. an application's own /metrics gauge,
// which exists only because the customer's app exposes it. id + name + data_type are the
// minimum the runtime needs (data_type drives the gauge/counter shape derivation). The
// base KG stays an immutable vendored mirror; new signals live in versioned overlays.
type overlaySignal struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	DataType string `yaml:"data_type"` // "gauge" | "counter" | ... (shape derived)
	Entity   string `yaml:"entity"`    // the entity kind the signal attaches to (Pod, Container, ...)
	Modality string `yaml:"modality"`  // "Metric" (the only numeric series) — defaulted if empty
	Notes    string `yaml:"notes"`
}

// overlayMember is one STRUCTURED member of an overlay phenomenon (doc 15 cap. A):
// signal -> phenomenon, the SignalID-bearing membership the matcher reads (p.Members)
// and a detection check binds against (validateCheck.isMember). Authored exactly like a
// participates_in edge; the loader also appends the edge so the graph stays self-consistent.
type overlayMember struct {
	Signal   string `yaml:"signal"`
	Role     string `yaml:"role"`     // required | corroborating
	Temporal string `yaml:"temporal"` // T0, T0+, ...
	Why      string `yaml:"why"`
}

// overlayPhenomenon is an authored CorrelationGroup added by an overlay (doc 15
// Phase C). Same shape as a base KG phenomenon: id, label, and member-signal
// tuples [pattern, role, temporal_tag, note] (schema-required). Carries no
// detection condition by itself — a check/rule (if any) is authored separately.
type overlayPhenomenon struct {
	ID      string     `yaml:"id"`
	Label   string     `yaml:"label"`
	Signals [][]string `yaml:"signals"`
	Notes   string     `yaml:"notes"`
}

// overlayRelation is an authored phenomenon_relation edge added by an overlay
// (doc 15 Phase C): a directed cascade/corroboration link between two phenomena,
// surfaced verbatim as the AUTHORED "why".
type overlayRelation struct {
	Src           string `yaml:"src"` // trigger phenomenon id (the relation's source)
	Dst           string `yaml:"dst"` // downstream/corroborating phenomenon id
	Role          string `yaml:"role"`
	TemporalOrder string `yaml:"temporal_order"`
	Why           string `yaml:"why"`
}

// OverlayInfo is the provenance record of one applied overlay (surfaced, per the
// authored-knowledge discipline: author + version travel with the content).
type OverlayInfo struct {
	File    string
	Name    string
	Version int
	Author  string
	Status  string
	Spans   int
	Rules   int
	Checks  int
}

// LoadWithOverlays loads the base KG release and merges every overlay file
// (*.yaml/*.yml) in overlayDir, in sorted file-name order. The returned graph's
// Version pins base + overlays together. An empty overlayDir, or a directory with
// no overlay files, yields the base graph unchanged (gaps then stay visible to
// graphlint — never silently defaulted).
func LoadWithOverlays(kgPath, overlayDir string) (*Graph, error) {
	if overlayDir == "" {
		return loadWithOverlayPaths(kgPath, nil)
	}
	paths, err := overlayPaths(overlayDir)
	if err != nil {
		return nil, err
	}
	return loadWithOverlayPaths(kgPath, paths)
}

// LoadWithOverlayPaths loads the base graph and merges EXACTLY the overlay files
// named in `paths`, in the order given (callers pass sorted-path order to match the
// cut-time hash). Unlike LoadWithOverlays (which globs the whole dir), this lets a
// caller reconstruct a HISTORICAL release from just the overlays its manifest pins —
// essential for governance, which must load arbitrary past releases to diff them.
func LoadWithOverlayPaths(kgPath string, paths []string) (*Graph, error) {
	return loadWithOverlayPaths(kgPath, paths)
}

func loadWithOverlayPaths(kgPath string, paths []string) (*Graph, error) {
	raw, err := os.ReadFile(kgPath)
	if err != nil {
		return nil, fmt.Errorf("read ontology graph %q: %w", kgPath, err)
	}
	g, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse ontology graph %q: %w", kgPath, err)
	}
	if len(paths) == 0 {
		return g, nil
	}
	hash := sha256.New()
	hash.Write(raw)
	for _, p := range paths {
		oraw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read overlay %q: %w", p, err)
		}
		if err := g.applyOverlay(filepath.Base(p), oraw); err != nil {
			return nil, fmt.Errorf("overlay %q: %w", p, err)
		}
		hash.Write([]byte{0})
		hash.Write(oraw)
	}
	g.Version = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if err := g.finalizeOverlays(); err != nil {
		return nil, err
	}
	return g, nil
}

// finalizeOverlays validates the constraints that CROSS overlay files (spans,
// checks, and anchors may each arrive from a different file, in either merge
// order, so per-file validation cannot see them together):
//
//   - a neighbour-scoped check requires a spanned (non-entity-local) phenomenon —
//     "one hop away" is meaningless without declared traversal edges;
//   - a spanned phenomenon WITH checks must declare its anchor (where the matcher
//     evaluates it) — never inferred from where variables happen to live;
//   - an anchor on an entity-local phenomenon is a contradiction (the check set
//     already lives on the one entity).
func (g *Graph) finalizeOverlays() error {
	ids := make([]string, 0, len(g.Phenomena))
	for id := range g.Phenomena {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := g.Phenomena[id]
		spanned := p.HasSpan() && p.Span != SpanEntityLocal
		if p.Anchor != "" && !spanned {
			return fmt.Errorf("phenomenon %s: anchor %q declared but span is %q — anchors are for spanned phenomena only", id, p.Anchor, p.Span)
		}
		hasNeighbour, hasTwoHop := false, false
		for _, c := range g.Checks[id] {
			if !c.OnAnchor() {
				hasNeighbour = true
			}
			if c.On == "two-hop" {
				hasTwoHop = true
			}
		}
		if hasNeighbour && !spanned {
			return fmt.Errorf("phenomenon %s: neighbour-scoped check on a non-spanned phenomenon (span %q)", id, p.Span)
		}
		if hasTwoHop && p.Span != SpanSecondOrder {
			return fmt.Errorf("phenomenon %s: two-hop check requires a second-order span, got %q (doc 02 §3.5 — the span bounds the walk)", id, p.Span)
		}
		if spanned && len(g.Checks[id]) > 0 && p.Anchor == "" {
			return fmt.Errorf("phenomenon %s: spanned phenomenon with checks must declare an anchor (doc 07 §3.2 — evaluation site is authored, never inferred)", id)
		}
	}
	return nil
}

func overlayPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no overlay dir: base graph only, gaps stay visible
		}
		return nil, fmt.Errorf("read overlay dir %q: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out) // deterministic merge order
	return out, nil
}

// applyOverlay validates and merges one overlay into the graph.
func (g *Graph) applyOverlay(name string, raw []byte) error {
	var f overlayFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if f.Overlay == "" {
		return fmt.Errorf("missing 'overlay' name field")
	}
	if strings.TrimSpace(f.Author) == "" {
		return fmt.Errorf("missing author provenance (authored-knowledge discipline, doc 02 §3.6)")
	}

	// New Signal nodes (doc 15 cap. A): authored observable variables not in the base
	// KG — e.g. an application's own /metrics gauge. Applied FIRST so this file's
	// phenomena/members/rules/checks may reference them. The base KG stays immutable.
	for i := range f.Signals {
		os := f.Signals[i]
		if strings.TrimSpace(os.ID) == "" || strings.TrimSpace(os.Name) == "" {
			return fmt.Errorf("overlay signal %d: id and name are required", i)
		}
		if strings.TrimSpace(os.DataType) == "" {
			return fmt.Errorf("overlay signal %q: data_type is required (drives the gauge/counter shape)", os.ID)
		}
		if g.Signals[os.ID] != nil {
			return fmt.Errorf("overlay signal %q already defined (overlays may add, never redefine)", os.ID)
		}
		modality := os.Modality
		if modality == "" {
			modality = "Metric"
		}
		g.Signals[os.ID] = &Signal{ID: os.ID, Name: os.Name, DataType: os.DataType, Modality: modality, Entity: os.Entity, Notes: os.Notes}
	}

	// New phenomena (doc 15 Phase C): authored CorrelationGroup nodes added by the
	// overlay, so the base KG stays immutable. Applied FIRST so this file's own
	// spans/checks/relations may reference them. Mirrors the base parser: signals
	// become InlineMembers; no detection condition is implied.
	for i := range f.Phenomena {
		op := f.Phenomena[i]
		if op.ID == "" || op.Label == "" {
			return fmt.Errorf("overlay phenomenon %d: id and label are required", i)
		}
		if _, exists := g.Phenomena[op.ID]; exists {
			return fmt.Errorf("overlay phenomenon %q already defined (overlays may add, never redefine)", op.ID)
		}
		if len(op.Signals) == 0 {
			return fmt.Errorf("overlay phenomenon %q: at least one member signal is required (schema)", op.ID)
		}
		p := &Phenomenon{ID: op.ID, Label: op.Label, RawSignals: op.Signals}
		for _, tup := range op.Signals {
			if len(tup) != 4 {
				return fmt.Errorf("overlay phenomenon %q: signal tuple must be [pattern, role, temporal, note]", op.ID)
			}
			p.InlineMembers = append(p.InlineMembers, InlineMember{Pattern: tup[0], Role: tup[1], TemporalTag: tup[2], Note: tup[3]})
		}
		g.Phenomena[op.ID] = p // NodeType derives "CorrelationGroup" from this map
	}

	// New STRUCTURED members (doc 15 cap. A): signal -> phenomenon membership the
	// matcher reads (p.Members) and a detection check binds against (validateCheck).
	// Authored exactly like a participates_in edge; we populate p.Members AND append
	// the edge so the graph stays self-consistent. Applied after phenomena+signals so
	// both endpoints exist; before rules/checks so they can reference the members.
	memberPhens := make([]string, 0, len(f.Members))
	for id := range f.Members {
		memberPhens = append(memberPhens, id)
	}
	sort.Strings(memberPhens)
	addedMemberEdge := false
	for _, phen := range memberPhens {
		p, ok := g.Phenomena[phen]
		if !ok {
			return fmt.Errorf("members for unknown phenomenon %q", phen)
		}
		for _, m := range f.Members[phen] {
			if g.Signals[m.Signal] == nil {
				return fmt.Errorf("phenomenon %s: member references unknown signal %q", phen, m.Signal)
			}
			role := m.Role
			if role == "" {
				role = "required"
			}
			for _, prior := range p.Members {
				if prior.SignalID == m.Signal {
					return fmt.Errorf("phenomenon %s: duplicate member for signal %q", phen, m.Signal)
				}
			}
			p.Members = append(p.Members, Member{SignalID: m.Signal, Role: role, TemporalOrder: m.Temporal, Why: m.Why})
			g.Edges = append(g.Edges, Edge{
				Type: "participates_in", Src: m.Signal, SrcType: "Signal",
				Dst: phen, DstType: "CorrelationGroup", Role: role, TemporalOrder: m.Temporal, Why: m.Why,
			})
			addedMemberEdge = true
		}
	}
	if addedMemberEdge {
		g.reindexEdges()
	}

	// New phenomenon_relation edges (doc 15 Phase C): a directed authored link
	// between two phenomena, appended to g.Edges and attached to its source's
	// Relations (so g.Phenomena[src].Relations carries it, exactly as the base loader).
	for i := range f.Relations {
		r := f.Relations[i]
		if g.Phenomena[r.Src] == nil {
			return fmt.Errorf("overlay relation %d: unknown source phenomenon %q", i, r.Src)
		}
		if g.Phenomena[r.Dst] == nil {
			return fmt.Errorf("overlay relation %d: unknown destination phenomenon %q", i, r.Dst)
		}
		if strings.TrimSpace(r.Why) == "" {
			return fmt.Errorf("overlay relation %s->%s: a 'why' is required (surfaced verbatim, doc 02 §3.6)", r.Src, r.Dst)
		}
		g.Edges = append(g.Edges, Edge{
			Type: "phenomenon_relation", Src: r.Src, SrcType: "CorrelationGroup",
			Dst: r.Dst, DstType: "CorrelationGroup", Role: r.Role, TemporalOrder: r.TemporalOrder, Why: r.Why,
		})
		g.Phenomena[r.Src].Relations = append(g.Phenomena[r.Src].Relations,
			Relation{TargetID: r.Dst, Role: r.Role, TemporalOrder: r.TemporalOrder, Why: r.Why})
	}
	// Appending to g.Edges may reallocate its backing array, dangling the *Edge
	// pointers the base loader put in the edge indexes. Rebuild the indexes from the
	// current slice so every pointer is valid. (Cheap; runs only when an overlay adds edges.)
	if len(f.Relations) > 0 {
		g.reindexEdges()
	}

	// Spans: set each phenomenon's declared span + traversal edges.
	ids := make([]string, 0, len(f.Spans))
	for id := range f.Spans {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := f.Spans[id]
		p, ok := g.Phenomena[id]
		if !ok {
			return fmt.Errorf("span for unknown phenomenon %q", id)
		}
		if !knownSpans[d.Span] {
			return fmt.Errorf("phenomenon %s: invalid span %q (vocabulary: entity-local|first-order|second-order)", id, d.Span)
		}
		for _, t := range d.TraversalEdgeTypes {
			if !knownTraversalEdgeTypes[t] {
				return fmt.Errorf("phenomenon %s: unknown traversal edge type %q", id, t)
			}
		}
		if d.Span != SpanEntityLocal && len(d.TraversalEdgeTypes) == 0 {
			return fmt.Errorf("phenomenon %s: span %s requires traversal edge types (walks follow only declared types, doc 02 §3.5)", id, d.Span)
		}
		if d.Span == SpanEntityLocal && len(d.TraversalEdgeTypes) > 0 {
			return fmt.Errorf("phenomenon %s: entity-local span must not declare traversal edges", id)
		}
		if strings.TrimSpace(d.Rationale) == "" {
			return fmt.Errorf("phenomenon %s: span declarations are falsifiable claims and need a rationale (doc 02 §3.6)", id)
		}
		if p.HasSpan() && p.Span != d.Span {
			return fmt.Errorf("phenomenon %s: span conflict (%q already declared, overlay says %q)", id, p.Span, d.Span)
		}
		p.Span = d.Span
		p.TraversalEdgeTypes = append([]string(nil), d.TraversalEdgeTypes...)
	}

	// Threshold rules: validate coherence and attach.
	for i := range f.Rules {
		r := f.Rules[i]
		if err := g.validateRule(&r); err != nil {
			return fmt.Errorf("rule %d (%s): %w", i, r.ID, err)
		}
		g.Rules = append(g.Rules, &r)
		g.rulesByID[r.ID] = &r
	}
	sort.Slice(g.Rules, func(i, j int) bool { return g.Rules[i].ID < g.Rules[j].ID })

	// Anchors: the entity kind a spanned phenomenon's checks evaluate at (doc 07
	// §3.2). Span/anchor coherence is validated in finalizeOverlays — span and
	// anchor may arrive from DIFFERENT overlay files in either merge order.
	anchorIDs := make([]string, 0, len(f.Anchors))
	for id := range f.Anchors {
		anchorIDs = append(anchorIDs, id)
	}
	sort.Strings(anchorIDs)
	for _, id := range anchorIDs {
		kind := f.Anchors[id]
		p, ok := g.Phenomena[id]
		if !ok {
			return fmt.Errorf("anchor for unknown phenomenon %q", id)
		}
		if !knownEntityScopes[kind] {
			return fmt.Errorf("phenomenon %s: unknown anchor kind %q (Container|Pod|Node|PVC)", id, kind)
		}
		if p.Anchor != "" && p.Anchor != kind {
			return fmt.Errorf("phenomenon %s: anchor conflict (%q already declared, overlay says %q)", id, p.Anchor, kind)
		}
		p.Anchor = kind
	}

	// Detection member-checks: validate and attach (doc 07 §3.1). Each check must
	// reference a known phenomenon and one of ITS member signals — a check for a
	// non-member would be detection knowledge with no authored basis.
	nChecks := 0
	phenIDs := make([]string, 0, len(f.Checks))
	for id := range f.Checks {
		phenIDs = append(phenIDs, id)
	}
	sort.Strings(phenIDs)
	for _, phen := range phenIDs {
		p, ok := g.Phenomena[phen]
		if !ok {
			return fmt.Errorf("checks for unknown phenomenon %q", phen)
		}
		for i := range f.Checks[phen] {
			c := f.Checks[phen][i]
			c.Phenomenon = phen
			if err := g.validateCheck(p, &c); err != nil {
				return fmt.Errorf("phenomenon %s check %d: %w", phen, i, err)
			}
			// One check per (phenomenon, signal): a second would be silently collapsed
			// last-writer-wins at match time (order-dependent, breaks replay). Fail loudly.
			for _, prior := range g.Checks[phen] {
				if prior.Signal == c.Signal {
					return fmt.Errorf("phenomenon %s: duplicate check for signal %q", phen, c.Signal)
				}
			}
			g.Checks[phen] = append(g.Checks[phen], &c)
			nChecks++
		}
	}

	g.Overlays = append(g.Overlays, OverlayInfo{
		File: name, Name: f.Overlay, Version: f.Version, Author: f.Author, Status: f.Status,
		Spans: len(f.Spans), Rules: len(f.Rules), Checks: nChecks,
	})
	return nil
}

// validateCheck enforces the check vocabulary + referential integrity: the signal
// must be a MEMBER of the phenomenon (so the bridge can't bind a member the graph
// never declared).
func (g *Graph) validateCheck(p *Phenomenon, c *MemberCheck) error {
	if g.Signals[c.Signal] == nil {
		return fmt.Errorf("references unknown signal %q", c.Signal)
	}
	if !knownFacets[c.Facet] {
		return fmt.Errorf("unknown facet %q (level|slope|ratio|rate-guard)", c.Facet)
	}
	if !knownExpects[c.Expect] {
		return fmt.Errorf("unknown expect %q", c.Expect)
	}
	if !knownMinStates[c.MinState] {
		return fmt.Errorf("unknown min_state %q (at-threshold|above|well-above)", c.MinState)
	}
	if c.MinState != "" && c.Facet != "slope" {
		return fmt.Errorf("min_state %q is only meaningful on a slope facet; facet %q would silently ignore it", c.MinState, c.Facet)
	}
	if !knownOns[c.On] {
		return fmt.Errorf("unknown on %q (anchor|neighbour)", c.On)
	}
	if strings.TrimSpace(c.Metric) == "" {
		return fmt.Errorf("missing metric")
	}
	isMember := false
	for _, m := range p.Members {
		if m.SignalID == c.Signal {
			isMember = true
			break
		}
	}
	if !isMember {
		return fmt.Errorf("signal %q is not a member of %s (a check cannot bind an undeclared member)", c.Signal, c.Phenomenon)
	}
	return nil
}

// ChecksFor returns the authored member-checks for a phenomenon.
func (g *Graph) ChecksFor(phenID string) []*MemberCheck { return g.Checks[phenID] }

func (g *Graph) validateRule(r *ThresholdRule) error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("missing id")
	}
	if g.rulesByID[r.ID] != nil {
		return fmt.Errorf("duplicate rule id")
	}
	if g.Signals[r.Signal] == nil {
		return fmt.Errorf("references unknown signal %q", r.Signal)
	}
	if strings.TrimSpace(r.Metric) == "" {
		return fmt.Errorf("missing metric")
	}
	if !knownEntityScopes[r.EntityScope] {
		return fmt.Errorf("unknown entity_scope %q", r.EntityScope)
	}
	if !knownDirections[r.Direction] {
		return fmt.Errorf("direction must be above|below, got %q", r.Direction)
	}
	if _, err := r.WindowDuration(); err != nil {
		return fmt.Errorf("bad window: %w", err)
	}
	if r.Eligibility != "" && !knownConfigPath(r.Eligibility) {
		return fmt.Errorf("unknown eligibility_config_path %q", r.Eligibility)
	}
	switch r.Kind {
	case RuleConfigRelative:
		if !knownConfigPath(r.ConfigPath) {
			return fmt.Errorf("config-relative rule needs a known config_path, got %q", r.ConfigPath)
		}
		if r.Factor <= 0 {
			return fmt.Errorf("config-relative rule needs factor > 0")
		}
	case RuleAbsolute, RuleRateOfChange:
		if r.Default == nil {
			return fmt.Errorf("%s rule needs an ontology default (flagged when used)", r.Kind)
		}
		if r.ConfigPath != "" {
			return fmt.Errorf("%s rule must not carry a config_path (use eligibility_config_path for gating)", r.Kind)
		}
	case RuleCoOccurrence:
		// participation-only; no bar fields required
	default:
		return fmt.Errorf("unknown kind %q (vocabulary: config-relative|absolute|rate-of-change|co-occurrence)", r.Kind)
	}
	return nil
}

// RuleByID returns an attached threshold rule.
func (g *Graph) RuleByID(id string) (*ThresholdRule, bool) {
	r, ok := g.rulesByID[id]
	return r, ok
}
