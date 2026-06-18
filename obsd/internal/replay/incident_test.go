package replay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/incident"
)

// Engine-level test + corpus regen for the incident-memory pass (v3 T-B). It drives
// the REAL incidentTick fold (the same role-resolution obsd's store wiring uses) over
// a synthetic finding stream — exactly as the MCP corpus drove the real
// BuildSilenceLedger over a synthetic fixture. Network-free, -race.

var incEng0 = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

const (
	incEngWeek    = 7 * 24 * time.Hour
	incEngResolve = 45 * time.Second
	webRole       = "r|c|ns|Deployment|web"
	webInst       = "i|c|ns|Pod|web-a|uid-a"
	payRole       = "r|c|ns|Deployment|payment"
	payInst       = "i|c|ns|Pod|pay-x|uid-p"
	nodeInst      = "i|c||Node|n1|uid-n" // role-less → unresolved
)

// engBindings maps the pod instance keys to their durable role keys (the Node has no
// role binding, so a finding on it resolves unresolved — the honest-partial path).
var engBindings = []binding.Binding{
	{CEIKey: webInst, RoleKey: webRole},
	{CEIKey: payInst, RoleKey: payRole},
}

type obs struct {
	phen, entity string
	at           time.Time
}

// foldScenario runs a sequence of single-finding ticks through incidentTick against a
// persistent accumulator, returning the per-tick TickIncident stream.
func foldScenario(acc *incident.Accumulator, seq []obs) []TickIncident {
	var out []TickIncident
	for _, o := range seq {
		findings := []detect.Finding{{Phenomenon: o.phen, EntityCEI: o.entity}}
		out = append(out, incidentTick(acc, TickRecord{EvalNow: o.at}, findings, engBindings, "sha256:gv"))
	}
	return out
}

func finalIncidents(stream []TickIncident) []incident.Incident {
	if len(stream) == 0 {
		return nil
	}
	return stream[len(stream)-1].Incidents
}

// TestIncidentTickResolvesAndFolds: role resolution + recurrence + honest-partial.
func TestIncidentTickResolvesAndFolds(t *testing.T) {
	acc := incident.NewAccumulator(incEngResolve, incEngWeek)
	stream := foldScenario(acc, []obs{
		{"PHEN_MEMORY_LEAK", webInst, incEng0},                       // fire (resolves to web role)
		{"PHEN_MEMORY_LEAK", webInst, incEng0.Add(30 * time.Second)}, // same episode
		{"PHEN_MEMORY_LEAK", webInst, incEng0.Add(10 * time.Minute)}, // resolve→refire
		{"PHEN_OOM_KILL_SYSTEM", nodeInst, incEng0.Add(time.Minute)}, // role-less node → unresolved
	})
	inc := finalIncidents(stream)
	byPhen := map[string]incident.Incident{}
	for _, i := range inc {
		byPhen[i.Phenomenon] = i
	}
	leak, ok := byPhen["PHEN_MEMORY_LEAK"]
	if !ok || leak.RoleCEI != webRole || leak.RoleUnresolved {
		t.Fatalf("leak not resolved to web role: %+v", leak)
	}
	if leak.RecurrenceCount != 2 {
		t.Fatalf("leak recurrence = %d, want 2", leak.RecurrenceCount)
	}
	node, ok := byPhen["PHEN_OOM_KILL_SYSTEM"]
	if !ok || !node.RoleUnresolved || node.RoleCEI != nodeInst {
		t.Fatalf("node finding not recorded honestly (instance key + unresolved): %+v", node)
	}
}

// TestIncidentTickRestartInvariance: folding with a Restore() injected mid-stream
// (the durable-store hand-off at a process restart) yields the IDENTICAL final
// incident as folding straight through — the gate's load-bearing claim.
func TestIncidentTickRestartInvariance(t *testing.T) {
	seq := []obs{
		{"PHEN_MEMORY_LEAK", webInst, incEng0},
		{"PHEN_MEMORY_LEAK", webInst, incEng0.Add(30 * time.Second)},
		{"PHEN_MEMORY_LEAK", webInst, incEng0.Add(10 * time.Minute)},
	}
	straight := finalIncidents(foldScenario(incident.NewAccumulator(incEngResolve, incEngWeek), seq))

	// Restart after the 2nd observation.
	before := incident.NewAccumulator(incEngResolve, incEngWeek)
	foldScenario(before, seq[:2])
	after := incident.NewAccumulator(incEngResolve, incEngWeek)
	after.Restore(before.Snapshot())
	restarted := finalIncidents(foldScenario(after, seq[2:]))

	if len(straight) != 1 || len(restarted) != 1 {
		t.Fatalf("expected 1 incident each, got %d / %d", len(straight), len(restarted))
	}
	if straight[0].Key != restarted[0].Key || straight[0].RecurrenceCount != restarted[0].RecurrenceCount {
		t.Fatalf("restart changed the incident: straight=%+v restart=%+v", straight[0], restarted[0])
	}
	if restarted[0].RecurrenceCount != 2 {
		t.Fatalf("restart recurrence = %d, want 2", restarted[0].RecurrenceCount)
	}
}

// --- corpus regen -----------------------------------------------------------

func writeStream(t *testing.T, path string, stream []TickIncident) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ti := range stream {
		if err := enc.Encode(ti); err != nil {
			t.Fatal(err)
		}
	}
}

func writeLabel(t *testing.T, path string, label map[string]any) {
	t.Helper()
	b, _ := json.MarshalIndent(label, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRegenIncidentCorpus materializes corpus/incident-memory/. Run with
// REGEN_INCIDENT_CORPUS=1; otherwise skipped.
func incidentCorpusDir() string {
	return filepath.Join("..", "..", "..", "corpus", "incident-memory")
}

func TestRegenIncidentCorpus(t *testing.T) {
	if os.Getenv("REGEN_INCIDENT_CORPUS") == "" {
		t.Skip("set REGEN_INCIDENT_CORPUS=1 to regenerate corpus/incident-memory/")
	}
	generateIncidentCorpus(t, incidentCorpusDir())
	t.Log("wrote corpus/incident-memory/ (recurring, restart, continuous, multirole)")
}

// generateIncidentCorpus folds each scenario through the REAL incident.Accumulator and
// writes the streams + oracles into dir. Shared by the REGEN writer (-> the committed
// corpus) and the always-on drift guard (-> a temp dir compared to the committed corpus).
func generateIncidentCorpus(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// recurring: fire / same-episode / resolve→refire → 1 incident, recurrence 2.
	recurringSeq := []obs{
		{"PHEN_MEMORY_LEAK", webInst, incEng0},
		{"PHEN_MEMORY_LEAK", webInst, incEng0.Add(30 * time.Second)},
		{"PHEN_MEMORY_LEAK", webInst, incEng0.Add(10 * time.Minute)},
	}
	writeStream(t, filepath.Join(dir, "events-recurring.jsonl"),
		foldScenario(incident.NewAccumulator(incEngResolve, incEngWeek), recurringSeq))
	writeLabel(t, filepath.Join(dir, "label-recurring.json"),
		map[string]any{"bundle": "recurring", "scenario": "recurring", "expectDistinct": 1, "expectRecurrence": 2})

	// restart: the SAME observations, but with a Restore() in the gap. Must match.
	beforeAcc := incident.NewAccumulator(incEngResolve, incEngWeek)
	preStream := foldScenario(beforeAcc, recurringSeq[:2])
	afterAcc := incident.NewAccumulator(incEngResolve, incEngWeek)
	afterAcc.Restore(beforeAcc.Snapshot())
	postStream := foldScenario(afterAcc, recurringSeq[2:])
	writeStream(t, filepath.Join(dir, "events-restart.jsonl"), append(preStream, postStream...))
	writeLabel(t, filepath.Join(dir, "label-restart.json"),
		map[string]any{"bundle": "restart", "scenario": "restart", "expectDistinct": 1, "expectRecurrence": 2})

	// continuous: 8 sub-resolve-gap ticks of one condition → recurrence 1 (no inflation).
	var contSeq []obs
	for i := 0; i < 8; i++ {
		contSeq = append(contSeq, obs{"PHEN_MEMORY_LEAK", webInst, incEng0.Add(time.Duration(i) * 30 * time.Second)})
	}
	writeStream(t, filepath.Join(dir, "events-continuous.jsonl"),
		foldScenario(incident.NewAccumulator(incEngResolve, incEngWeek), contSeq))
	writeLabel(t, filepath.Join(dir, "label-continuous.json"),
		map[string]any{"bundle": "continuous", "scenario": "continuous", "expectDistinct": 1, "expectRecurrence": 1})

	// multirole: distinct phenomena/roles + the unresolved node → 3 incidents.
	multiSeq := []obs{
		{"PHEN_MEMORY_LEAK", webInst, incEng0},
		{"PHEN_OOM_KILL_CGROUP", payInst, incEng0.Add(15 * time.Second)},
		{"PHEN_OOM_KILL_SYSTEM", nodeInst, incEng0.Add(30 * time.Second)},
	}
	writeStream(t, filepath.Join(dir, "events-multirole.jsonl"),
		foldScenario(incident.NewAccumulator(incEngResolve, incEngWeek), multiSeq))
	writeLabel(t, filepath.Join(dir, "label-multirole.json"),
		map[string]any{"bundle": "multirole", "scenario": "separation", "expectDistinct": 3, "expectRecurrence": 1})
}

// TestIncidentCorpusFrozenConsistent is the always-on drift guard (audit roadmap #4):
// re-fold every scenario through the REAL incident.Accumulator into a temp dir and assert
// the committed corpus is byte-identical — so a fold change that silently alters the
// corpus fails in CI. FAILS (never skips) if the committed corpus is missing.
func TestIncidentCorpusFrozenConsistent(t *testing.T) {
	tmp := t.TempDir()
	generateIncidentCorpus(t, tmp)
	committedDir := incidentCorpusDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read fresh corpus: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		want, err := os.ReadFile(filepath.Join(committedDir, e.Name()))
		if err != nil {
			t.Fatalf("committed corpus file %q missing — regenerate with REGEN_INCIDENT_CORPUS=1: %v", e.Name(), err)
		}
		got, err := os.ReadFile(filepath.Join(tmp, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("DRIFT: committed corpus/incident-memory/%s differs from a fresh fold — the producer changed without regenerating (REGEN_INCIDENT_CORPUS=1)", e.Name())
		}
	}
}
