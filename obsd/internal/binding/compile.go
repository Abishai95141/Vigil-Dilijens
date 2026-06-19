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
func Compile(g *graph.Graph, inventory []identity.InstanceRecord, cfg EntityConfig, avail *AvailabilityReport, now time.Time) *Result {
	res := &Result{GraphVersion: g.Version, At: now}

	// Deterministic input views: pods, nodes, and PVCs sorted by CEI key. PVCs are now
	// first-class identity instances (the Watcher Observe()s them, doc 03), so they ride
	// the SAME inventory as pods/nodes — their KSM object-state series join the real
	// instance CEI, not a pseudo-key (the documented dark-bar fix).
	var pods, nodes, pvcs []identity.InstanceRecord
	for _, r := range inventory {
		switch r.Kind {
		case "Pod":
			pods = append(pods, r)
		case "Node":
			nodes = append(nodes, r)
		case "PersistentVolumeClaim":
			pvcs = append(pvcs, r)
		}
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].CEI.Key() < pods[j].CEI.Key() })
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].CEI.Key() < nodes[j].CEI.Key() })
	sort.Slice(pvcs, func(i, j int) bool { return pvcs[i].CEI.Key() < pvcs[j].CEI.Key() })

	// Rules are already sorted by ID (graph loader invariant).
	for _, rule := range g.Rules {
		cov := RuleCoverage{RuleID: rule.ID, Kind: rule.Kind, EntityScope: rule.EntityScope}
		// Emission metadata (doc 04 §3.1 mechanism 3): collection glue copied
		// verbatim from the authored signal onto every binding of this rule.
		var em Emission
		if s := g.Signals[rule.Signal]; s != nil {
			em = Emission{Source: s.Source, CollectionMethod: s.CollectionMethod}
		}
		// Availability gating (the M1<->M2 join): a rule whose signal is not
		// obtainable on THIS cluster instantiates every pair OUT-OF-SCOPE with the
		// availability reason — a bar nothing can ever evaluate is not "bound",
		// and the absence is enumerated, never hidden (doc 04 §3.1.1).
		if avail != nil {
			if av, ok := avail.PerSignal[rule.Signal]; ok && av.State == OutOfScopeUnobtainable {
				reason := "signal unobtainable on this cluster: " + strings.Join(av.Reasons, "; ")
				bindAllOutOfScope(res, &cov, rule, em, reason, pods, nodes, pvcs)
				res.Coverage.PerRule = append(res.Coverage.PerRule, cov)
				continue
			}
		}
		switch rule.EntityScope {
		case "Container":
			for _, pod := range pods {
				bindContainers(res, &cov, rule, em, pod, cfg, now)
			}
		case "Pod":
			// Application-signal rules (doc 15 cap. A) bind to the pod that exposes
			// the metric, resolving the bar from the pod's customer-declared SLO.
			for _, pod := range pods {
				bindPod(res, &cov, rule, em, pod, cfg, now)
			}
		case "Node":
			for _, node := range nodes {
				bindNode(res, &cov, rule, em, node, cfg, now)
			}
		case "PVC":
			for _, ref := range pvcs {
				bindPVC(res, &cov, rule, em, ref, cfg, now)
			}
		}
		res.Coverage.PerRule = append(res.Coverage.PerRule, cov)
	}

	finalize(res)
	return res
}

// bindAllOutOfScope lands every would-be instantiation of a rule in the
// out-of-scope state with one stated reason (signal unobtainable here). The pairs
// still EXIST in the report — "recorded as out-of-scope, not failure" and never
// silently absent (doc 04 §3.1.1, §3.5).
func bindAllOutOfScope(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, em Emission, reason string, pods, nodes, pvcs []identity.InstanceRecord) {
	add := func(b Binding) {
		b.State = StateOutOfScope
		b.Validation = ValidationSuspect
		b.Reason = reason
		b.Emission = em
		cov.OutOfScope++
		res.Bindings = append(res.Bindings, b)
	}
	switch rule.EntityScope {
	case "Container":
		for _, pod := range pods {
			add(Binding{CEIKey: pod.CEI.Key(), RoleKey: pod.RoleCEI.Key(), Entity: "Container", RuleID: rule.ID, Metric: rule.Metric})
		}
	case "Pod":
		for _, pod := range pods {
			add(Binding{CEIKey: pod.CEI.Key(), RoleKey: pod.RoleCEI.Key(), Entity: "Pod", RuleID: rule.ID, Metric: rule.Metric})
		}
	case "Node":
		for _, node := range nodes {
			add(Binding{CEIKey: node.CEI.Key(), Entity: "Node", RuleID: rule.ID, Metric: rule.Metric})
		}
	case "PVC":
		for _, rec := range pvcs {
			add(Binding{CEIKey: rec.CEI.Key(), Entity: "PVC", RuleID: rule.ID, Metric: rule.Metric})
		}
	}
}

// bindContainers instantiates a container-scoped rule across one pod's declared
// containers (doc 04 §3.3 axis 2: every identified entity of the right type).
func bindContainers(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, em Emission, pod identity.InstanceRecord, cfg EntityConfig, now time.Time) {
	pc, ok := cfg.Pod(pod.Namespace, pod.Name)
	if !ok {
		// The pod is in the identity inventory but its config row could not be
		// read: an unresolved pair, stated (observe lag or list-window skew).
		cov.Unresolved++
		res.Bindings = append(res.Bindings, Binding{
			CEIKey: pod.CEI.Key(), RoleKey: pod.RoleCEI.Key(), Entity: "Container",
			RuleID: rule.ID, Metric: rule.Metric, State: StateUnresolved,
			Validation: ValidationSuspect, Emission: em,
			Reason: "pod config not readable at compile time (inventory/config list skew)",
		})
		return
	}
	for _, c := range pc.Containers {
		b := Binding{
			CEIKey: pod.CEI.Key(), RoleKey: pod.RoleCEI.Key(), Entity: "Container",
			Container: c.Name, RuleID: rule.ID, Metric: rule.Metric,
			State: StateBound, Validation: ValidationSuspect, Emission: em,
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

// bindPod instantiates a Pod-scoped rule against one identified pod (doc 15 cap. A:
// application-signal rules bind to the pod that EXPOSES the metric, so the bar lives on
// the same Pod CEI the app fingerprint resolves to). A config-relative rule resolves its
// bar from the pod's CUSTOMER-DECLARED SLO (PodConfig.SLOs, read from a vigil.io/slo.*
// annotation — borrowed normativity, exactly like a resources.limit). UNDECLARED =>
// unbounded/listed, NEVER a learned or default capacity (the charter ban). An
// absolute/rate Pod rule may carry a flagged default (defensible for a freshness/queue
// floor, never for load capacity — an authoring discipline, not a code default).
func bindPod(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, em Emission, pod identity.InstanceRecord, cfg EntityConfig, now time.Time) {
	b := Binding{
		CEIKey: pod.CEI.Key(), RoleKey: pod.RoleCEI.Key(), Entity: "Pod",
		RuleID: rule.ID, Metric: rule.Metric, State: StateBound, Validation: ValidationSuspect, Emission: em,
	}
	cov.Instantiated++

	pc, pcOK := cfg.Pod(pod.Namespace, pod.Name)

	// Eligibility gate (doc 15 cap. A — the app-SLO regime predicate, the Pod analogue of
	// bindContainers' CPU-limit gate): a Pod-scoped application rule applies only to a
	// workload the operator placed INSIDE the regime by declaring at least one vigil.io/slo.*
	// annotation. An infrastructure pod that carries no application SLO (kube-system,
	// monitoring, CNI) is OUT-OF-SCOPE with a stated reason — the rule cannot apply — never
	// unbounded, so it leaves the resolvability denominator instead of dragging it down. A
	// workload IN the regime that has declared SOME but not all SLOs stays in scope and is
	// listed UNBOUNDED for the bars it has not declared (the config-relative branch below) —
	// that coverage gap is honest, never silently scoped away. A pod whose config row is
	// unreadable is UNRESOLVED (we cannot read its declarations to judge eligibility), never
	// guessed in or out.
	if rule.Eligibility != "" {
		if !pcOK {
			b.State = StateUnresolved
			b.Reason = "pod config not readable at compile time (inventory/config list skew)"
			cov.Unresolved++
			cov.Instantiated--
			res.Bindings = append(res.Bindings, b)
			return
		}
		if eligible, reason := podEligibilityMet(rule.Eligibility, pc); !eligible {
			b.State = StateOutOfScope
			b.Reason = reason
			cov.OutOfScope++
			cov.Instantiated--
			res.Bindings = append(res.Bindings, b)
			return
		}
	}

	switch rule.Kind {
	case graph.RuleConfigRelative:
		value, declared := 0.0, false
		if pcOK {
			value, declared = readSLOPath(rule.ConfigPath, pc)
		}
		if !declared {
			b.Bar = nil
			b.Reason = "unbounded: no declared SLO (" + rule.ConfigPath + " not set on the workload)"
			cov.Unbounded++
		} else {
			b.Bar = &ResolvedBar{
				Kind: rule.Kind, Source: SourceConfig, Flagged: false,
				ConfigPath: rule.ConfigPath, Factor: rule.Factor,
				Value: value * rule.Factor, Unit: "declared",
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

// readSLOPath reads a customer-declared application SLO bar (doc 15 cap. A). The
// vocabulary is the slo.* config-path family; the value is read verbatim from the pod's
// declared SLOs (borrowed normativity), NEVER inferred from observed traffic. An absent
// declaration is the resolvability hole, never silently defaulted.
func readSLOPath(path string, pc PodConfig) (value float64, declared bool) {
	if pc.SLOs == nil {
		return 0, false
	}
	v, ok := pc.SLOs[path]
	return v, ok
}

// bindNode instantiates a node-scoped rule against one identified node.
func bindNode(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, em Emission, node identity.InstanceRecord, cfg EntityConfig, now time.Time) {
	b := Binding{
		CEIKey: node.CEI.Key(), Entity: "Node",
		RuleID: rule.ID, Metric: rule.Metric, State: StateBound, Validation: ValidationSuspect, Emission: em,
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

// bindPVC instantiates a PVC-scoped rule against one claim. The claim is a first-class
// identity instance (rec), so its binding carries the REAL instance CEI key — the same
// key its KSM object-state streams and its mounts edge use — not a pseudo-key. This is
// the documented dark-bar fix (binding.go: "join identity once 03 tracks PVC lifecycles"):
// StreamUID() now resolves, so a PVC pair becomes genuinely watchable.
func bindPVC(res *Result, cov *RuleCoverage, rule *graph.ThresholdRule, em Emission, rec identity.InstanceRecord, cfg EntityConfig, now time.Time) {
	b := Binding{
		CEIKey: rec.CEI.Key(), Entity: "PVC",
		RuleID: rule.ID, Metric: rule.Metric, State: StateBound, Validation: ValidationSuspect, Emission: em,
	}
	cov.Instantiated++
	pc, ok := cfg.PVC(rec.Namespace, rec.Name)
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

// podEligibilityMet checks a Pod-scoped rule's eligibility gate against a workload's
// DECLARED config (doc 15 cap. A; the Pod analogue of eligibilityMet). The only predicate
// is the app-SLO regime marker (slo.*): met iff the workload declares at least one customer
// SLO annotation. A workload that declares none is outside the regime and the application
// rule does not apply — borrowed normativity, read from the customer's OWN declarations,
// never learned. An unrecognized predicate is fail-open (no gate), matching eligibilityMet;
// the overlay loader already rejects unknown predicates, so this is never reached in
// practice.
func podEligibilityMet(path string, pc PodConfig) (bool, string) {
	switch path {
	case graph.EligibilityAppSLODeclared:
		if len(pc.SLOs) == 0 {
			return false, "out-of-scope: workload declares no application SLO (vigil.io/slo.*); the app-SLO regime does not apply (eligibility: " + path + ")"
		}
	}
	return true, ""
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
	case graph.PathContainerLimitsEphemeralStorage:
		if c.EphemeralStorageLimitBytes == 0 {
			return false, "out-of-scope: no ephemeral-storage limit declared, fill-toward-limit has no bar (eligibility: " + path + ")"
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
	case graph.PathContainerLimitsEphemeralStorage:
		if c.EphemeralStorageLimitBytes == 0 {
			return 0, "bytes", false
		}
		return float64(c.EphemeralStorageLimitBytes), "bytes", true
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
		"bindings start suspect; semantic QA (04 M3) promotes/demotes per compile against hot-window evidence — verified is never permanent",
		"evidence is hot-window only: warm storage + replay bundles (qss M2, 05 M5) pending",
		"equivalence resolver compiled (35 groups); v1 rules address canonical names directly — the dialect sweep activates with non-native exporters",
		"primitive evaluation (05 M2: threshold/rate/co-occurrence over these bars) is the next milestone",
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
