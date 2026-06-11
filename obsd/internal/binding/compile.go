package binding

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// Compile is the discovery-time compilation (doc 04): ontology graph (02) +
// identified cluster (03) + the customer's declared config -> the bound customer
// graph + coverage report. Pure function of its inputs: `now` is injected, every
// output slice is sorted, and no learned value exists anywhere.
//
// Phase-0b core scope (M1/M2/M4 of doc 04): instantiation of the authored
// threshold rules across entity instances and identity layers, with per-instance
// threshold resolution and resolvability accounting. Equivalence-group resolution
// and semantic QA (M3) come next; until then every binding is suspect by
// construction, and the report says so.
func Compile(g *graph.Graph, inventory []identity.InstanceRecord, cfg EntityConfig, now time.Time) *Result {
	res := &Result{GraphVersion: g.Version, At: now}

	// Deterministic input views: pods and nodes sorted by CEI key; PVC list sorted.
	var pods, nodes []identity.InstanceRecord
	for _, r := range inventory {
		switch r.Kind {
		case "Pod":
			pods = append(pods, r)
		case "Node":
			nodes = append(nodes, r)
		}
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].CEI.Key() < pods[j].CEI.Key() })
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].CEI.Key() < nodes[j].CEI.Key() })
	pvcs := cfg.PVCs()
	sort.Slice(pvcs, func(i, j int) bool {
		if pvcs[i].Namespace != pvcs[j].Namespace {
			return pvcs[i].Namespace < pvcs[j].Namespace
		}
		return pvcs[i].Name < pvcs[j].Name
	})

	// Rules are already sorted by ID (graph loader invariant).
	for _, rule := range g.Rules {
		cov := RuleCoverage{RuleID: rule.ID, Kind: rule.Kind, EntityScope: rule.EntityScope}
		switch rule.EntityScope {
		case "Container":
			for _, pod := range pods {
				bindContainers(res, &cov, rule, pod, cfg, now)
			}
		case "Pod":
			// No v1 rule uses Pod scope; fan-out shape is the container path minus
			// the per-container loop. Listed for vocabulary completeness.
		case "Node":
			for _, node := range nodes {
				bindNode(res, &cov, rule, node, cfg, now)
			}
		case "PVC":
			for _, ref := range pvcs {
				bindPVC(res, &cov, rule, ref, cfg, now)
			}
		}
		res.Coverage.PerRule = append(res.Coverage.PerRule, cov)
	}

	finalize(res)
	return res
}

// bindContainers instantiates a container-scoped rule across one pod's declared
// containers (doc 04 §3.3 axis 2: every identified entity of the right type).
func bindContainers(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, pod identity.InstanceRecord, cfg EntityConfig, now time.Time) {
	pc, ok := cfg.Pod(pod.Namespace, pod.Name)
	if !ok {
		// The pod is in the identity inventory but its config row could not be
		// read: an unresolved pair, stated (observe lag or list-window skew).
		cov.Unresolved++
		res.Bindings = append(res.Bindings, Binding{
			CEIKey: pod.CEI.Key(), RoleKey: pod.RoleCEI.Key(), Entity: "Container",
			RuleID: rule.ID, Metric: rule.Metric, State: StateUnresolved,
			Validation: ValidationSuspect,
			Reason:     "pod config not readable at compile time (inventory/config list skew)",
		})
		return
	}
	for _, c := range pc.Containers {
		b := Binding{
			CEIKey: pod.CEI.Key(), RoleKey: pod.RoleCEI.Key(), Entity: "Container",
			Container: c.Name, RuleID: rule.ID, Metric: rule.Metric,
			State: StateBound, Validation: ValidationSuspect,
		}
		cov.Instantiated++

		// Eligibility gate (e.g. CFS throttling cannot occur without a CPU limit):
		// non-resolution is out-of-scope WITH REASON, not unbounded.
		if rule.Eligibility != "" {
			if eligible, reason := eligibilityMet(rule.Eligibility, c); !eligible {
				b.State = StateOutOfScope
				b.Reason = reason
				cov.OutOfScope++
				cov.Instantiated--
				res.Bindings = append(res.Bindings, b)
				continue
			}
		}

		switch rule.Kind {
		case graph.RuleConfigRelative:
			value, unit, declared := readContainerPath(rule.ConfigPath, c)
			if !declared {
				// The resolvability hole (doc 04 §3.4): no declared limit => no
				// crossable bar => Tier-B ineligible, LISTED, never skipped.
				b.Bar = nil
				b.Reason = "unbounded: no early-warning eligibility (" + rule.ConfigPath + " not declared)"
				cov.Unbounded++
			} else {
				b.Bar = &ResolvedBar{
					Kind: rule.Kind, Source: SourceConfig, Flagged: false,
					ConfigPath: rule.ConfigPath, Factor: rule.Factor,
					Value: value * rule.Factor, Unit: unit,
					Direction: rule.Direction, Window: rule.Window, ResolvedAt: now,
				}
				cov.ConfigBound++
			}
		case graph.RuleAbsolute, graph.RuleRateOfChange:
			b.Bar = defaultBar(rule, now)
			cov.DefaultBound++
		}
		res.Bindings = append(res.Bindings, b)
	}
}

// bindNode instantiates a node-scoped rule against one identified node.
func bindNode(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, node identity.InstanceRecord, cfg EntityConfig, now time.Time) {
	b := Binding{
		CEIKey: node.CEI.Key(), Entity: "Node",
		RuleID: rule.ID, Metric: rule.Metric, State: StateBound, Validation: ValidationSuspect,
	}
	cov.Instantiated++
	switch rule.Kind {
	case graph.RuleConfigRelative:
		nc, ok := cfg.Node(node.Name)
		if !ok || nc.AllocatableMemoryBytes == 0 {
			b.Bar = nil
			b.Reason = "unbounded: " + rule.ConfigPath + " not readable"
			cov.Unbounded++
		} else {
			b.Bar = &ResolvedBar{
				Kind: rule.Kind, Source: SourceConfig, Flagged: false,
				ConfigPath: rule.ConfigPath, Factor: rule.Factor,
				Value: float64(nc.AllocatableMemoryBytes) * rule.Factor, Unit: "bytes",
				Direction: rule.Direction, Window: rule.Window, ResolvedAt: now,
			}
			cov.ConfigBound++
		}
	case graph.RuleAbsolute, graph.RuleRateOfChange:
		b.Bar = defaultBar(rule, now)
		cov.DefaultBound++
	}
	res.Bindings = append(res.Bindings, b)
}

// bindPVC instantiates a PVC-scoped rule against one claim.
func bindPVC(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, ref PVCRef, cfg EntityConfig, now time.Time) {
	b := Binding{
		CEIKey: "pvc|" + ref.Namespace + "|" + ref.Name, Entity: "PVC",
		RuleID: rule.ID, Metric: rule.Metric, State: StateBound, Validation: ValidationSuspect,
	}
	cov.Instantiated++
	pc, ok := cfg.PVC(ref.Namespace, ref.Name)
	if rule.Kind == graph.RuleConfigRelative {
		if !ok || pc.RequestedStorageBytes == 0 {
			b.Bar = nil
			b.Reason = "unbounded: " + rule.ConfigPath + " not declared"
			cov.Unbounded++
		} else {
			b.Bar = &ResolvedBar{
				Kind: rule.Kind, Source: SourceConfig, Flagged: false,
				ConfigPath: rule.ConfigPath, Factor: rule.Factor,
				Value: float64(pc.RequestedStorageBytes) * rule.Factor, Unit: "bytes",
				Direction: rule.Direction, Window: rule.Window, ResolvedAt: now,
			}
			cov.ConfigBound++
		}
	} else {
		b.Bar = defaultBar(rule, now)
		cov.DefaultBound++
	}
	res.Bindings = append(res.Bindings, b)
}

// eligibilityMet checks a rule's eligibility gate against a container's config.
func eligibilityMet(path string, c ContainerConfig) (bool, string) {
	switch path {
	case graph.PathContainerLimitsCPU:
		if c.CPULimitMilli == 0 {
			return false, "out-of-scope: no CPU limit declared, CFS throttling cannot occur (eligibility: " + path + ")"
		}
	case graph.PathContainerLimitsMemory:
		if c.MemLimitBytes == 0 {
			return false, "out-of-scope: no memory limit declared (eligibility: " + path + ")"
		}
	}
	return true, ""
}

// readContainerPath reads a machine-resolvable container config path (the
// vocabulary declared in the ontology overlay; doc 02 §3.4 "config path").
func readContainerPath(path string, c ContainerConfig) (value float64, unit string, declared bool) {
	switch path {
	case graph.PathContainerLimitsMemory:
		if c.MemLimitBytes == 0 {
			return 0, "bytes", false
		}
		return float64(c.MemLimitBytes), "bytes", true
	case graph.PathContainerLimitsCPU:
		if c.CPULimitMilli == 0 {
			return 0, "millicores", false
		}
		return float64(c.CPULimitMilli), "millicores", true
	default:
		return 0, "", false
	}
}

// defaultBar builds a flagged, default-sourced bar (doc 04 §3.4: permitted but
// visible; lower trust than a config-sourced bar, high-blast-radius under doc 12).
func defaultBar(rule *graph.ThresholdRule, now time.Time) *ResolvedBar {
	unit := "ratio"
	if rule.Kind == graph.RuleRateOfChange {
		unit = "count"
	}
	return &ResolvedBar{
		Kind: rule.Kind, Source: SourceDefault, Flagged: true,
		Value: *rule.Default, Unit: unit,
		Direction: rule.Direction, Window: rule.Window, ResolvedAt: now,
	}
}

// finalize derives the role-layer bindings (axis 3), the resolvability metric,
// the unbounded list, and the standing honesty notes; sorts everything.
func finalize(res *Result) {
	sort.Slice(res.Bindings, func(i, j int) bool {
		a, b := res.Bindings[i], res.Bindings[j]
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		if a.CEIKey != b.CEIKey {
			return a.CEIKey < b.CEIKey
		}
		return a.Container < b.Container
	})

	// Role-layer aggregation: role bindings absorb instance churn by construction.
	type roleKey struct{ role, rule, container string }
	agg := map[roleKey]*RoleBinding{}
	var order []roleKey
	for i := range res.Bindings {
		b := &res.Bindings[i]
		if b.RoleKey == "" || b.State != StateBound {
			continue
		}
		k := roleKey{b.RoleKey, b.RuleID, b.Container}
		rb, ok := agg[k]
		if !ok {
			rb = &RoleBinding{RoleKey: k.role, RuleID: k.rule, Container: k.container}
			agg[k] = rb
			order = append(order, k)
		}
		rb.Members++
		if b.Bar != nil {
			rb.Bound++
		} else {
			rb.Unbounded++
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].rule != order[j].rule {
			return order[i].rule < order[j].rule
		}
		if order[i].role != order[j].role {
			return order[i].role < order[j].role
		}
		return order[i].container < order[j].container
	})
	for _, k := range order {
		res.Roles = append(res.Roles, *agg[k])
	}

	// Resolvability metric + the unbounded list (doc 04 §3.4) + default-bar count.
	for _, b := range res.Bindings {
		if b.State != StateBound {
			continue
		}
		if b.Bar != nil && b.Bar.Source == SourceDefault {
			res.Coverage.DefaultBars++
		}
	}
	for _, rc := range res.Coverage.PerRule {
		if rc.Kind == graph.RuleConfigRelative {
			res.Coverage.ConfigEligible += rc.ConfigBound + rc.Unbounded
			res.Coverage.ConfigBound += rc.ConfigBound
		}
	}
	if res.Coverage.ConfigEligible > 0 {
		res.Coverage.Resolvability = float64(res.Coverage.ConfigBound) / float64(res.Coverage.ConfigEligible)
	}
	for _, b := range res.Bindings {
		if b.State == StateBound && b.Bar == nil {
			// Name BOTH layers: the durable role (the workload the operator knows)
			// and the instance (so N unbounded replicas are N distinguishable lines).
			tag := ceiKeyHuman(b.CEIKey)
			if b.RoleKey != "" {
				tag = roleKeyHuman(b.RoleKey) + " " + tag
			}
			if b.Container != "" {
				tag += " container=" + b.Container
			}
			res.Coverage.UnboundedWorkloads = append(res.Coverage.UnboundedWorkloads,
				fmt.Sprintf("%s rule=%s — unbounded: no early-warning eligibility", tag, b.RuleID))
		}
	}
	sort.Strings(res.Coverage.UnboundedWorkloads)

	res.Coverage.Notes = []string{
		"all bindings suspect by construction: semantic-validation suite (doc 04 M3) pending",
		"bound = instantiated with bar resolution recorded; collection wiring (doc 05) pending",
		"equivalence-group resolution (doc 04 §3.1.2) pending: rules address canonical metrics directly",
	}
}

// ceiKeyHuman renders an instance CEI key ("i|cluster|ns|Kind|name|uid") as
// "ns/name"; non-namespaced kinds render as "name". Unknown shapes pass through.
func ceiKeyHuman(key string) string {
	parts := strings.Split(key, "|")
	if len(parts) >= 5 && parts[0] == "i" {
		if parts[2] != "" {
			return parts[2] + "/" + parts[4]
		}
		return parts[4]
	}
	return key
}

// roleKeyHuman renders a role CEI key ("r|cluster|ns|Kind|RoleKey") as
// "ns role=RoleKey".
func roleKeyHuman(key string) string {
	parts := strings.Split(key, "|")
	if len(parts) >= 5 && parts[0] == "r" {
		return parts[2] + " role=" + parts[4]
	}
	return key
}
