package eventdetect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay"
)

// The event-detection gate corpus is a STANDALONE deterministic corpus (graph-robustness
// #2 G1): discrete events are NOT in the replay bundle and never enter the fingerprint
// digest, so a bundle-replay pass would have nothing to read. The anti-shallow core is a
// LABEL ORACLE — every scenario's ground truth (which phenomenon finding SHOULD be
// produced, on which role, and which cascade SHOULD light up) is fixed by construction,
// recorded INDEPENDENTLY of the producer. The whole batch folds through the REAL
// eventdetect.Findings + detect.Matcher.Cascades + replay.Digest — no proxy.
//
// Regenerate with: REGEN_EVENTDETECT_CORPUS=1 go test ./obsd/internal/eventdetect -run RegenEventDetectCorpus

const (
	corpusDir   = "../../../corpus/event-detection"
	clusterID   = "evt-cluster"
	overlayPath = "../../../ontology/graph/overlays/experimental/event-conditions-v1.yaml"
)

var t0 = time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)

// podKey mints the instance CEI key ResolveEventRole produces for a Pod — the SAME key
// a container fp finding rides (the compiler anchors a container on its pod), so a
// trigger fp finding and the event finding share it and relate as same-entity.
func podKey(ns, name, uid string) string {
	return identity.CEI{Layer: identity.LayerInstance, Cluster: clusterID, Namespace: ns, Kind: "Pod", Name: name, UID: uid}.Key()
}

// regenStore builds a real identity store: two workloads with resolvable Deployment
// roles, and a role-less Node — so role resolution runs through the REAL store and the
// oracle role is construction truth.
func regenStore(t *testing.T) *identity.Store {
	t.Helper()
	store := identity.NewStore(func() time.Time { return t0.Add(time.Minute) }, time.Hour, 24*time.Hour, 100000)
	obs := func(name, uid, role string) {
		var roleCEI identity.CEI
		if role != "" {
			roleCEI, _ = identity.MintRole(identity.RoleCoords{
				Cluster: clusterID, Namespace: "online-boutique", Kind: "Deployment", RoleKey: "Deployment/" + role,
			}, t0)
		}
		if _, err := store.Observe(identity.InstanceCoords{
			Cluster: clusterID, Namespace: "online-boutique", Kind: "Pod", Name: name, UID: uid,
		}, roleCEI, t0, identity.StateActive); err != nil {
			t.Fatal(err)
		}
	}
	obs("currency-abc", "u-currency", "currency")
	obs("cart-xyz", "u-cart", "cart")
	if _, err := store.Observe(identity.InstanceCoords{
		Cluster: clusterID, Kind: "Node", Name: "vigil-worker", UID: "u-node",
	}, identity.CEI{}, t0, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	return store
}

// triggerFinding is a minimal real detect.Finding for the trigger side (MEMORY_LEAK /
// THROTTLING_CASCADE), keyed on the pod the event also lands on — enough for cascade
// recognition (same-entity) and replay.Digest.
func triggerFinding(phenomenon, ns, podName, uid string) detect.Finding {
	return detect.Finding{
		Phenomenon: phenomenon, EntityCEI: podKey(ns, podName, uid),
		Namespace: ns, Name: podName, Kind: "Container", EvaluatedAt: t0,
	}
}

// finding/cascade oracle row.
type eventFindingOracle struct {
	Phenomenon    string `json:"phenomenon"`
	EntityCEI     string `json:"entityCei"`
	Quality       string `json:"quality"`
	RequiredMet   int    `json:"requiredMet"`
	RequiredTotal int    `json:"requiredTotal"`
}

type cascadeOracle struct {
	Trigger    string `json:"trigger"`
	Downstream string `json:"downstream"`
	Why        string `json:"why"`
	Related    string `json:"related"`
}

type scenarioLabel struct {
	Bundle          string               `json:"bundle"`
	Scenario        string               `json:"scenario"`
	DigestBefore    string               `json:"digestBefore"`
	DigestAfter     string               `json:"digestAfter"`
	ExpectFindings  []eventFindingOracle `json:"expectFindings"`
	ExpectCascades  []cascadeOracle      `json:"expectCascades"`
	ExpectNoUpgrade int                  `json:"expectNoUpgrade"` // events that MUST NOT produce a finding
	Note            string               `json:"note"`
}

// scenarioOutput is one scenario folded through the REAL producer + cascade recognizer.
type scenarioOutput struct {
	name     string
	findings []detect.Finding
	cascades []detect.Cascade
	label    scenarioLabel
}

type rawEvent struct {
	in     events.Involved
	reason string
}
type scenarioDef struct {
	name     string
	events   []rawEvent
	triggers []detect.Finding
	note     string
}

func evtPod(name, uid string) events.Involved {
	return events.Involved{Namespace: "online-boutique", Name: name, Kind: "Pod", UID: uid}
}

// gateScenarios is the single source of truth for the corpus — used by BOTH the regen
// (writes the frozen files) and the always-on drift guard (re-runs and compares). So a
// code change that alters the producer's output is caught in CI without REGEN.
func gateScenarios() []scenarioDef {
	return []scenarioDef{
		{
			name:   "oom-detection",
			events: []rawEvent{{evtPod("currency-abc", "u-currency"), "OOMKilled"}},
			note:   "OOMKilled on a resolved workload -> ONE degraded OOM_KILL_CGROUP event finding (the blind-spot-closing detection). No trigger -> no cascade.",
		},
		{
			name:     "leak-to-oom-cascade",
			events:   []rawEvent{{evtPod("currency-abc", "u-currency"), "OOMKilled"}},
			triggers: []detect.Finding{triggerFinding("PHEN_MEMORY_LEAK", "online-boutique", "currency-abc", "u-currency")},
			note:     "MEMORY_LEAK (gauge) + OOMKilled (event) on the SAME workload -> the authored MEMORY_LEAK->OOM_KILL_CGROUP cascade lights up live (the flagship).",
		},
		{
			name:     "throttle-to-probe-cascade",
			events:   []rawEvent{{evtPod("cart-xyz", "u-cart"), "CrashLoopBackOff"}},
			triggers: []detect.Finding{triggerFinding("PHEN_THROTTLING_CASCADE", "online-boutique", "cart-xyz", "u-cart")},
			note:     "THROTTLING_CASCADE (gauge) + CrashLoopBackOff (event) on the SAME workload -> the authored THROTTLING_CASCADE->PROBE_FAILURE_RESTART cascade lights up.",
		},
		{
			name:   "image-pull-failure",
			events: []rawEvent{{evtPod("currency-abc", "u-currency"), "ImagePullBackOff"}},
			note:   "ImagePullBackOff on a resolved workload -> ONE degraded IMAGE_PULL_FAILURE event finding (closes a CRITICAL, common operator blind spot: a bad tag / missing pull-secret / unreachable registry). No trigger -> no cascade.",
		},
		{
			name:   "role-unresolved-no-upgrade",
			events: []rawEvent{{events.Involved{Name: "vigil-worker", Kind: "Node", UID: "u-node"}, "OOMKilled"}},
			note:   "OOMKilled on a role-less Node -> ZERO phenomenon findings (never assert a workload phenomenon on an unidentified entity).",
		},
		{
			name:   "unrelated-reason",
			events: []rawEvent{{evtPod("currency-abc", "u-currency"), "Started"}},
			note:   "an event reason with no authored detection -> ZERO findings (never the firehose).",
		},
		{
			name:     "healthy-negative",
			events:   nil,
			triggers: []detect.Finding{triggerFinding("PHEN_MEMORY_LEAK", "online-boutique", "currency-abc", "u-currency")},
			note:     "a gauge trigger exists but NO events -> zero event findings, zero cascades (nothing manufactured).",
		},
	}
}

// runScenarios folds every scenario through the REAL eventdetect.Findings +
// detect.Matcher.Cascades + replay.Digest, building the label oracle from construction
// truth. The single computation both the regen and the drift guard use.
func runScenarios(t *testing.T, g *graph.Graph, m *detect.Matcher, store *identity.Store, dets []events.Detection) []scenarioOutput {
	t.Helper()
	var out []scenarioOutput
	for _, sc := range gateScenarios() {
		// Resolve events through the REAL store (exactly as the collector does).
		var efs []events.EventFinding
		for i, re := range sc.events {
			ent, role, unresolved := events.ResolveEventRole(store, clusterID, re.in)
			efs = append(efs, events.EventFinding{
				Reason: re.reason, EntityCEI: ent, RoleCEI: role, RoleUnresolved: unresolved,
				Namespace: re.in.Namespace, Name: re.in.Name, Kind: re.in.Kind, Count: int32(i + 1),
				FirstTimestamp: t0, LastTimestamp: t0,
			})
		}

		// DIGEST-INVARIANCE: producing the event findings + cascades must NOT perturb the
		// deterministic fingerprint digest. Hash the trigger (fp) findings, run the
		// producer + cascade recognition, hash again — equal proves the event lane is
		// off the fp digest (join, never fuse).
		before, _, derr := replay.Digest(t0, nil, sc.triggers, nil, nil)
		if derr != nil {
			t.Fatal(derr)
		}
		eventFindings := Findings(g, efs, dets, t0)
		union := append(append([]detect.Finding{}, sc.triggers...), eventFindings...)
		tracker := detect.NewCascadeTracker(10 * time.Minute)
		cascades := m.Cascades(t0, union, tracker, nil, identity.TimeWindow{Start: t0.Add(-90 * time.Second), End: t0})
		after, _, derr := replay.Digest(t0, nil, sc.triggers, nil, nil)
		if derr != nil {
			t.Fatal(derr)
		}

		// Oracle (construction truth): which findings + cascades SHOULD appear.
		var fOracle []eventFindingOracle
		noUpgrade := 0
		for _, re := range sc.events {
			ent, _, unresolved := events.ResolveEventRole(store, clusterID, re.in)
			authored := re.reason == "OOMKilled" || re.reason == "CrashLoopBackOff" || re.reason == "ImagePullBackOff"
			if unresolved || !authored {
				noUpgrade++
				continue
			}
			phen := "PHEN_OOM_KILL_CGROUP"
			total := 8
			switch re.reason {
			case "CrashLoopBackOff":
				phen, total = "PHEN_PROBE_FAILURE_RESTART", 11
			case "ImagePullBackOff":
				phen, total = "PHEN_IMAGE_PULL_FAILURE", 12
			}
			fOracle = append(fOracle, eventFindingOracle{
				Phenomenon: phen, EntityCEI: ent, Quality: "degraded", RequiredMet: 1, RequiredTotal: total,
			})
		}
		var cOracle []cascadeOracle
		for _, tr := range sc.triggers {
			for _, ef := range eventFindings {
				if tr.EntityCEI != ef.EntityCEI {
					continue
				}
				if tr.Phenomenon == "PHEN_MEMORY_LEAK" && ef.Phenomenon == "PHEN_OOM_KILL_CGROUP" {
					cOracle = append(cOracle, cascadeOracle{Trigger: tr.Phenomenon, Downstream: ef.Phenomenon, Why: "Eventual outcome", Related: "same-entity"})
				}
				if tr.Phenomenon == "PHEN_THROTTLING_CASCADE" && ef.Phenomenon == "PHEN_PROBE_FAILURE_RESTART" {
					cOracle = append(cOracle, cascadeOracle{Trigger: tr.Phenomenon, Downstream: ef.Phenomenon, Related: "same-entity"})
				}
			}
		}

		out = append(out, scenarioOutput{
			name: sc.name, findings: eventFindings, cascades: cascades,
			label: scenarioLabel{
				Bundle: "eventdetect-" + sc.name, Scenario: sc.name,
				DigestBefore: before, DigestAfter: after,
				ExpectFindings: fOracle, ExpectCascades: cOracle, ExpectNoUpgrade: noUpgrade,
				Note: sc.note,
			},
		})
	}
	return out
}

func TestRegenEventDetectCorpus(t *testing.T) {
	if os.Getenv("REGEN_EVENTDETECT_CORPUS") != "1" {
		t.Skip("set REGEN_EVENTDETECT_CORPUS=1 to regenerate corpus/event-detection")
	}
	g := loadGraph(t)
	dets, err := events.LoadEventDetections(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, so := range runScenarios(t, g, detect.NewMatcher(g), regenStore(t), dets) {
		writeJSONL(t, "findings-"+so.name, so.findings)
		writeJSONL(t, "cascades-"+so.name, so.cascades)
		writeLabel(t, so.label)
	}
	t.Logf("regenerated event-detection scenarios into %s", corpusDir)
}

// TestEventDetectCorpusFrozenConsistent is the always-on drift guard: re-running the
// producer + cascade recognizer over the scenarios must reproduce the FROZEN corpus
// byte-for-byte. A code change that alters the output is caught in CI without REGEN.
func TestEventDetectCorpusFrozenConsistent(t *testing.T) {
	g := loadGraph(t)
	dets, err := events.LoadEventDetections(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, so := range runScenarios(t, g, detect.NewMatcher(g), regenStore(t), dets) {
		assertJSONLMatchesFrozen(t, "findings-"+so.name, so.findings)
		assertJSONLMatchesFrozen(t, "cascades-"+so.name, so.cascades)
		gotLabel, _ := json.MarshalIndent(so.label, "", "  ")
		wantLabel, rerr := os.ReadFile(filepath.Join(corpusDir, "label-"+so.name+".json"))
		if rerr != nil {
			t.Fatalf("frozen label missing for %s: %v", so.name, rerr)
		}
		if string(append(gotLabel, '\n')) != string(wantLabel) {
			t.Errorf("label drift for %s — re-run differs from frozen corpus (REGEN to update intentionally)", so.name)
		}
	}
}

func assertJSONLMatchesFrozen[T any](t *testing.T, name string, rows []T) {
	t.Helper()
	var buf []byte
	for i := range rows {
		b, _ := json.Marshal(rows[i])
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	want, err := os.ReadFile(filepath.Join(corpusDir, name+".jsonl"))
	if err != nil {
		t.Fatalf("frozen corpus missing %s: %v", name, err)
	}
	if string(buf) != string(want) {
		t.Errorf("corpus drift for %s — re-run differs from frozen (REGEN to update intentionally)", name)
	}
}

func writeJSONL[T any](t *testing.T, name string, rows []T) {
	t.Helper()
	var buf []byte
	for i := range rows {
		b, err := json.Marshal(rows[i])
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(filepath.Join(corpusDir, name+".jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLabel(t *testing.T, lbl scenarioLabel) {
	t.Helper()
	b, err := json.MarshalIndent(lbl, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corpusDir, "label-"+lbl.Scenario+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEventDetectDoesNotPerturbDigest is the always-on structural guard behind the
// corpus DIGEST-INVARIANCE floor: producing event-driven findings + recognizing the
// augmented cascades is read-only w.r.t. the detect.Findings that feed replay.Digest.
// A future edit that folds an event finding into the digested slice breaks this in CI.
func TestEventDetectDoesNotPerturbDigest(t *testing.T) {
	store := regenStore(t)
	g := loadGraph(t)
	dets, err := events.LoadEventDetections(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	fpFindings := []detect.Finding{triggerFinding("PHEN_MEMORY_LEAK", "online-boutique", "currency-abc", "u-currency")}
	before, _, err := replay.Digest(t0, nil, fpFindings, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ent, role, unresolved := events.ResolveEventRole(store, clusterID, events.Involved{Namespace: "online-boutique", Name: "currency-abc", Kind: "Pod", UID: "u-currency"})
	ef := []events.EventFinding{{Reason: "OOMKilled", EntityCEI: ent, RoleCEI: role, RoleUnresolved: unresolved, Kind: "Pod", Count: 1, LastTimestamp: t0}}
	_ = Findings(g, ef, dets, t0)
	after, _, err := replay.Digest(t0, nil, fpFindings, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("event-driven detection perturbed the deterministic fp digest (must be off the digest)")
	}
}
