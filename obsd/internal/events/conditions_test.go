package events

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConds(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "event-conditions.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const validConds = `
overlay: event-conditions-test
version: v1
author: vigil-engineering
status: experimental
event_conditions:
  - reason: OOMKilled
    involved_kind: Pod
    corroborates: PHEN_OOM_KILL_CGROUP
    role: corroborating
    temporal_order: "T0"
    why: >-
      An OOMKilled event co-occurs with the cgroup-OOM phenomenon on the same role.
  - reason: CrashLoopBackOff
    involved_kind: Pod
    corroborates: ""
    role: corroborating
    temporal_order: "T0"
    why: A restart-loop fact, visible standalone.
`

func TestLoadEventConditionsValid(t *testing.T) {
	cs, err := LoadEventConditions(writeConds(t, validConds))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("want 2 conditions, got %d", len(cs))
	}
	// File-level provenance defaults onto a condition that omits its own.
	if cs[0].Author != "vigil-engineering" || cs[0].Version != "v1" || cs[0].Status != "experimental" {
		t.Errorf("provenance defaults not applied: %+v", cs[0])
	}
	if tg := CorroborationTargets(cs); len(tg) != 1 || tg[0] != "PHEN_OOM_KILL_CGROUP" {
		t.Errorf("CorroborationTargets = %v, want [PHEN_OOM_KILL_CGROUP]", tg)
	}
	if rs := Reasons(cs); len(rs) != 2 {
		t.Errorf("Reasons = %v", rs)
	}
}

func TestLoadEventConditionsRejectsMissingWhy(t *testing.T) {
	body := `
author: a
version: v1
event_conditions:
  - reason: OOMKilled
    involved_kind: Pod
    corroborates: PHEN_OOM_KILL_CGROUP
`
	if _, err := LoadEventConditions(writeConds(t, body)); err == nil {
		t.Fatal("a condition without a why must be rejected (authored-knowledge discipline)")
	}
}

func TestLoadEventConditionsRejectsIllegalRole(t *testing.T) {
	body := `
author: a
event_conditions:
  - reason: OOMKilled
    involved_kind: Pod
    corroborates: PHEN_OOM_KILL_CGROUP
    role: trigger
    why: x
`
	if _, err := LoadEventConditions(writeConds(t, body)); err == nil {
		t.Fatal("an event condition with role=trigger must be rejected (an event never triggers)")
	}
}

func TestLoadEventConditionsRejectsDuplicate(t *testing.T) {
	body := `
author: a
event_conditions:
  - reason: OOMKilled
    involved_kind: Pod
    corroborates: PHEN_OOM_KILL_CGROUP
    why: x
  - reason: OOMKilled
    involved_kind: Pod
    corroborates: PHEN_OOM_KILL_CGROUP
    why: y
`
	if _, err := LoadEventConditions(writeConds(t, body)); err == nil {
		t.Fatal("a duplicate (reason, kind) condition must be rejected (order-dependent collapse)")
	}
}

func TestLoadEventConditionsMissingAuthor(t *testing.T) {
	body := `
event_conditions:
  - reason: OOMKilled
    involved_kind: Pod
    why: x
`
	if _, err := LoadEventConditions(writeConds(t, body)); err == nil {
		t.Fatal("a condition with no author (file or per-condition) must be rejected")
	}
}
