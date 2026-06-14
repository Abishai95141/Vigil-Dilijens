package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/replay"
)

// The events-gate corpus is a STANDALONE deterministic JOIN corpus (k8s events are
// not in the replay bundle and never enter the digest, so a bundle-replay pass would
// have nothing real to read). The anti-shallow core is a LABEL ORACLE: every event's
// ground-truth role identity is fixed by how the test builds the identity store, and
// is recorded INDEPENDENTLY of the emitter's own resolution. The scorer grades the
// resolved role against the oracle (join-fidelity), and a deliberate identity-mismatch
// scenario MUST fail to corroborate. The whole batch folds through the REAL
// events.ResolveEventRole + events.Corroborate + replay.Digest — no proxy.
//
// Regenerate with: REGEN_EVENTS_CORPUS=1 go test ./obsd/internal/events -run RegenEventsCorpus

const corpusDir = "../../../corpus/events"

// eventOracle is the ground-truth for one event, fixed by construction (not by the
// emitter): the role it SHOULD resolve to, and whether it SHOULD corroborate.
type eventOracle struct {
	EntityCEI         string `json:"entityCei"`
	OracleRole        string `json:"oracleRole"`
	OracleResolved    bool   `json:"oracleResolved"`
	ShouldCorroborate bool   `json:"shouldCorroborate"`
}

type scenarioLabel struct {
	Bundle             string        `json:"bundle"`
	Scenario           string        `json:"scenario"`
	Events             []eventOracle `json:"events"`
	DigestBefore       string        `json:"digestBefore"`
	DigestAfter        string        `json:"digestAfter"`
	ExpectCorroborated int           `json:"expectCorroborated"`
	ExpectStandalone   int           `json:"expectStandalone"`
	ExpectUnresolved   int           `json:"expectUnresolved"`
	Note               string        `json:"note"`
}

// roleOf mints a Deployment role CEI key for a workload in the boutique namespace.
func roleOf(name string) string {
	r, _ := identity.MintRole(identity.RoleCoords{
		Cluster: clusterID, Namespace: "online-boutique", Kind: "Deployment", RoleKey: "Deployment/" + name,
	}, t0)
	return r.Key()
}

// regenStore builds a real identity store holding the workloads the scenarios use —
// so role resolution runs through the REAL store, and the oracle role is the store's
// own truth (constructed here, recorded in the label).
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
	// A node carries no role: its OOM event is honestly unresolved.
	if _, err := store.Observe(identity.InstanceCoords{
		Cluster: clusterID, Kind: "Node", Name: "vigil-worker", UID: "u-node",
	}, identity.CEI{}, t0, identity.StateActive); err != nil {
		t.Fatal(err)
	}
	return store
}

// involved for a known boutique pod / node.
func pod(name, uid string) Involved {
	return Involved{Namespace: "online-boutique", Name: name, Kind: "Pod", UID: uid}
}

// gaugeFinding is a minimal real detect.Finding for the gauge side — enough to feed
// replay.Digest and store.Get role resolution (its EntityCEI is a real instance key).
func gaugeFinding(phenomenon, podName, uid string) detect.Finding {
	inst := identity.CEI{Layer: identity.LayerInstance, Cluster: clusterID, Namespace: "online-boutique", Kind: "Pod", Name: podName, UID: uid}
	return detect.Finding{Phenomenon: phenomenon, EntityCEI: inst.Key(), Namespace: "online-boutique", Name: podName, Kind: "Pod", EvaluatedAt: t0}
}

// gaugeRolesFromFindings mirrors main.go's inventoryLoop: resolve each gauge finding
// to its role CEI via the SAME store the events resolve through.
func gaugeRolesFromFindings(store *identity.Store, findings []detect.Finding) map[string]map[string]bool {
	out := make(map[string]map[string]bool)
	for i := range findings {
		rec, ok := store.Get(findings[i].EntityCEI)
		if !ok || rec.RoleCEI.RoleKey == "" {
			continue
		}
		if out[findings[i].Phenomenon] == nil {
			out[findings[i].Phenomenon] = make(map[string]bool)
		}
		out[findings[i].Phenomenon][rec.RoleCEI.Key()] = true
	}
	return out
}

func TestRegenEventsCorpus(t *testing.T) {
	if os.Getenv("REGEN_EVENTS_CORPUS") != "1" {
		t.Skip("set REGEN_EVENTS_CORPUS=1 to regenerate corpus/events")
	}
	store := regenStore(t)
	conds, err := LoadEventConditions(condsV1Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}

	type scenario struct {
		name     string
		events   []Involved // raw involved objects (resolved through the real store)
		reasons  []string   // parallel to events
		findings []detect.Finding
		note     string
	}
	currencyRole, cartRole := roleOf("currency"), roleOf("cart")

	scenarios := []scenario{
		{
			name:    "corroborated-oom",
			events:  []Involved{pod("currency-abc", "u-currency")},
			reasons: []string{"OOMKilled"},
			// A cgroup-OOM gauge finding on the SAME role (currency): must corroborate.
			findings: []detect.Finding{gaugeFinding("PHEN_OOM_KILL_CGROUP", "currency-abc", "u-currency")},
			note:     "OOMKilled on currency + a cgroup-OOM gauge finding on the same role -> corroborated (the blind-spot-closing join).",
		},
		{
			name:    "multi-role",
			events:  []Involved{pod("currency-abc", "u-currency"), pod("cart-xyz", "u-cart")},
			reasons: []string{"OOMKilled", "OOMKilled"},
			// A gauge finding on EACH role: each event joins its OWN role, never the other's.
			findings: []detect.Finding{
				gaugeFinding("PHEN_OOM_KILL_CGROUP", "currency-abc", "u-currency"),
				gaugeFinding("PHEN_OOM_KILL_CGROUP", "cart-xyz", "u-cart"),
			},
			note: "two OOMKilled events on two roles, two same-role gauge findings -> two corroborations, no cross-role join (separation).",
		},
		{
			name:    "identity-mismatch",
			events:  []Involved{pod("currency-abc", "u-currency")},
			reasons: []string{"OOMKilled"},
			// The gauge finding is on a DIFFERENT role (cart). The currency event must NOT
			// corroborate it — the exact-CEI match is the fidelity guarantee.
			findings: []detect.Finding{gaugeFinding("PHEN_OOM_KILL_CGROUP", "cart-xyz", "u-cart")},
			note:     "OOMKilled on currency but the only cgroup-OOM gauge finding is on cart -> MUST NOT corroborate (identity mis-join trap).",
		},
		{
			name: "standalone-crashloop",
			events: []Involved{
				pod("cart-xyz", "u-cart"),                           // CrashLoopBackOff: corroborates nothing
				pod("currency-abc", "u-currency"),                   // OOMKilled with no gauge on its role
				{Name: "vigil-worker", Kind: "Node", UID: "u-node"}, // node OOM: role-unresolved, visible
			},
			reasons:  []string{"CrashLoopBackOff", "OOMKilled", "OOMKilled"},
			findings: nil, // no gauge findings: every event is standalone-visible
			note:     "CrashLoopBackOff + an OOMKilled with no gauge + a role-less node OOM -> all visible, none corroborated, node unresolved.",
		},
		{
			name:     "healthy-negative",
			events:   nil, // no events at all
			reasons:  nil,
			findings: []detect.Finding{gaugeFinding("PHEN_OOM_KILL_CGROUP", "currency-abc", "u-currency")},
			note:     "gauge findings exist but NO events occurred -> zero event rows, nothing manufactured.",
		},
	}

	for _, sc := range scenarios {
		// Resolve every event through the REAL store, exactly as the collector does.
		var efs []EventFinding
		for i, in := range sc.events {
			ent, role, unresolved := ResolveEventRole(store, clusterID, in)
			efs = append(efs, EventFinding{
				Reason: sc.reasons[i], EntityCEI: ent, RoleCEI: role, RoleUnresolved: unresolved,
				Namespace: in.Namespace, Name: in.Name, Kind: in.Kind, Count: int32(i + 1),
				FirstTimestamp: t0, LastTimestamp: t0.Add(time.Duration(i) * time.Minute),
			})
		}
		gaugeRoles := gaugeRolesFromFindings(store, sc.findings)

		// DIGEST-INVARIANCE: the join must not perturb the deterministic findings that
		// feed the digest. Hash the findings, run the join, hash again — equal proves
		// the events lane is read-only w.r.t. the digest (join, never fuse).
		before, _, derr := replay.Digest(t0, nil, sc.findings, nil, nil)
		if derr != nil {
			t.Fatal(derr)
		}
		corroborated := Corroborate(efs, gaugeRoles, conds)
		after, _, derr := replay.Digest(t0, nil, sc.findings, nil, nil)
		if derr != nil {
			t.Fatal(derr)
		}

		// Build the oracle from construction truth + write the rows.
		var oracle []eventOracle
		expCorr, expStand, expUnres := 0, 0, 0
		for i, in := range sc.events {
			ent, _, _ := ResolveEventRole(store, clusterID, in)
			oRole, oResolved := ent, false
			should := false
			switch {
			case in.Kind == "Node":
				oRole, oResolved = ent, false // node has no role
			case in.Name == "currency-abc":
				oRole, oResolved = currencyRole, true
			case in.Name == "cart-xyz":
				oRole, oResolved = cartRole, true
			}
			// shouldCorroborate iff this scenario has a gauge finding for the event's
			// phenomenon on the event's OWN role (construction truth).
			if oResolved && sc.reasons[i] == "OOMKilled" {
				if roles := gaugeRoles["PHEN_OOM_KILL_CGROUP"]; roles[oRole] {
					should = true
				}
			}
			oracle = append(oracle, eventOracle{EntityCEI: ent, OracleRole: oRole, OracleResolved: oResolved, ShouldCorroborate: should})
			switch {
			case !oResolved:
				expUnres++
			case should:
				expCorr++
			default:
				expStand++
			}
		}

		writeCorpusRows(t, sc.name, corroborated)
		writeCorpusLabel(t, scenarioLabel{
			Bundle: "events-" + sc.name, Scenario: sc.name, Events: oracle,
			DigestBefore: before, DigestAfter: after,
			ExpectCorroborated: expCorr, ExpectStandalone: expStand, ExpectUnresolved: expUnres,
			Note: sc.note,
		})
	}
	t.Logf("regenerated %d events-gate scenarios into %s", len(scenarios), corpusDir)
}

func writeCorpusRows(t *testing.T, name string, rows []CorroboratedEvent) {
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
	if err := os.WriteFile(filepath.Join(corpusDir, "events-"+name+".jsonl"), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCorpusLabel(t *testing.T, lbl scenarioLabel) {
	t.Helper()
	b, err := json.MarshalIndent(lbl, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corpusDir, "label-"+lbl.Scenario+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEventsJoinDoesNotPerturbDigest is the always-on structural guard behind the
// corpus DIGEST-INVARIANCE floor: Corroborate is read-only w.r.t. the detect.Findings
// that feed replay.Digest. A future edit that folds an event into the findings slice
// breaks this immediately, in CI, with no cluster.
func TestEventsJoinDoesNotPerturbDigest(t *testing.T) {
	store := regenStore(t)
	findings := []detect.Finding{gaugeFinding("PHEN_OOM_KILL_CGROUP", "currency-abc", "u-currency")}
	before, _, err := replay.Digest(t0, nil, findings, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	efs := []EventFinding{{Reason: "OOMKilled", EntityCEI: "i|x", RoleCEI: roleOf("currency"), Kind: "Pod"}}
	_ = Corroborate(efs, gaugeRolesFromFindings(store, findings), conds())
	after, _, err := replay.Digest(t0, nil, findings, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("the events join perturbed the deterministic digest (must be off the digest)")
	}
}
