package audit

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

func fixtureLines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/audit_sample.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

var (
	onset    = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	lookback = 10 * time.Minute
	// incidentRole is the role CEI the test resolver maps the gunicorn deployment to —
	// the SAME key the (mock) incident carries, so the change↔incident exact-CEI join holds.
	incidentRole = "role:Deployment/erpnext-gunicorn"
)

// testResolver resolves ONLY the gunicorn deployment to the incident's role CEI;
// everything else is unresolved (the honest "store has not seen it" path).
func testResolver(ns, resource, name string) (string, bool) {
	if ns == "erpnext" && resource == "deployments" && name == "erpnext-gunicorn" {
		return incidentRole, true
	}
	return "", false
}

func TestParseEventsFiltersAndSorts(t *testing.T) {
	got := ParseEvents(fixtureLines(t))
	// Kept: secret-update, deploy-patch, cm-update, coredns-patch, pod-delete-after — 5.
	// Dropped: the RequestReceived stage (dup of deploy-patch), get (non-mutating),
	// create-forbidden (403), and the malformed line.
	wantIDs := []string{"a-secret-update", "a-deploy-patch", "a-cm-update", "a-coredns-patch-otherns", "a-pod-delete-after"}
	if len(got) != len(wantIDs) {
		t.Fatalf("ParseEvents = %d events, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, ce := range got {
		if ce.AuditID != wantIDs[i] {
			t.Errorf("event[%d] = %q, want %q (must be sorted by timestamp,auditID)", i, ce.AuditID, wantIDs[i])
		}
	}
	// The deploy-patch must reflect the ResponseComplete stage (200), not RequestReceived.
	for _, ce := range got {
		if ce.AuditID == "a-deploy-patch" {
			if ce.Verb != "patch" || ce.ResponseCode != 200 || ce.User != "system:serviceaccount:ci:deployer" {
				t.Errorf("deploy-patch parsed wrong: %+v", ce)
			}
			if !ce.Timestamp.Equal(time.Date(2026, 6, 18, 11, 58, 0, 500000000, time.UTC)) {
				t.Errorf("deploy-patch ts = %v, want the ResponseComplete stageTimestamp", ce.Timestamp)
			}
		}
	}
}

func TestResolveFillsRoleHonestly(t *testing.T) {
	got := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	for _, ce := range got {
		switch ce.AuditID {
		case "a-deploy-patch":
			if ce.RoleUnresolved || ce.RoleCEI != incidentRole {
				t.Errorf("deploy-patch should resolve to %q, got %+v", incidentRole, ce)
			}
		default:
			if !ce.RoleUnresolved {
				t.Errorf("%s should be RoleUnresolved (store has not seen it), got %+v", ce.AuditID, ce)
			}
			if !strings.HasPrefix(ce.RoleCEI, "audit:") {
				t.Errorf("%s unresolved role should be its own coordinate key, got %q", ce.AuditID, ce.RoleCEI)
			}
		}
	}
}

func TestAntecedentsArrowOfTime(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	ante := Antecedents(changes, onset, lookback)
	for _, c := range ante {
		if !c.Timestamp.Before(onset) {
			t.Errorf("ANTECEDENT after onset leaked through the arrow-of-time prune: %+v", c)
		}
		if c.AuditID == "a-pod-delete-after" {
			t.Errorf("the after-onset delete must be PRUNED, it appeared as an antecedent: %+v", c)
		}
	}
	// secret(300s) deploy(120s) cm(60s) coredns(30s) are all within the 10m lookback.
	if len(ante) != 4 {
		t.Fatalf("antecedents = %d, want 4 (the after-onset delete pruned): %+v", len(ante), ante)
	}
}

func TestHypothesizeIsDirectionFreeAndCoLocated(t *testing.T) {
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	inc := Incident{ID: "inc-1", RoleCEI: incidentRole, Namespace: "erpnext", Onset: onset, GraphVersion: "vtest"}
	hyps := Hypothesize(changes, inc, lookback)

	// Expect 3: deploy-patch (role-cei), cm-update (namespace), secret-update (namespace).
	// coredns-patch is a different namespace (no join); pod-delete-after is pruned.
	if len(hyps) != 3 {
		t.Fatalf("hypotheses = %d, want 3: %+v", len(hyps), subjects(hyps))
	}
	tierByChange := map[string]string{}
	for _, h := range hyps {
		// CARDINAL: a hypothesis is NEVER a causal edge — only a direction-free hypothesis.
		if h.Kind != candidate.KindCausalHypothesis {
			t.Fatalf("CHARTER BREACH: audit produced kind %q (want %q — never an authoritative/causal edge): %+v",
				h.Kind, candidate.KindCausalHypothesis, h)
		}
		if h.Relation != "observed-adjacency" {
			t.Errorf("relation = %q, want direction-free observed-adjacency: %+v", h.Relation, h)
		}
		// It must pass the store's structural guard (a causal edge would be rejected here).
		if err := candidate.Validate(h); err != nil {
			t.Errorf("candidate failed the structural guard: %v (%+v)", err, h)
		}
		// delta must be positive (the change precedes onset).
		if d, ok := h.Payload["deltaSeconds"].(int64); !ok || d <= 0 {
			t.Errorf("deltaSeconds must be positive, got %v", h.Payload["deltaSeconds"])
		}
		if strings.Contains(h.Subject, "a-deploy-patch") {
			tierByChange["deploy"] = h.Payload["joinTier"].(string)
		}
		if strings.Contains(h.Subject, "a-cm-update") {
			tierByChange["cm"] = h.Payload["joinTier"].(string)
		}
	}
	if tierByChange["deploy"] != string(JoinRoleCEI) {
		t.Errorf("deploy-patch join tier = %q, want role-cei (exact CEI)", tierByChange["deploy"])
	}
	if tierByChange["cm"] != string(JoinNamespace) {
		t.Errorf("cm-update join tier = %q, want namespace (weaker)", tierByChange["cm"])
	}
}

// THE AUDIT-GATE (doc 20 P4): the change events + the staged hypotheses are byte-identical
// across runs AND invariant to the order audit lines arrive in (ParseEvents sorts + dedups).
// And the cardinal charter rule: the lane NEVER emits a causal edge — only direction-free
// hypotheses. This is the determinism + discipline guarantee before any audit-derived
// signal is trusted.
func TestAuditGateDeterministicAndCharterClean(t *testing.T) {
	lines := fixtureLines(t)
	inc := Incident{ID: "inc-1", RoleCEI: incidentRole, Namespace: "erpnext", Onset: onset, GraphVersion: "vtest"}

	run := func(ls []string) ([]ChangeEvent, []candidate.Candidate) {
		ch := Resolve(ParseEvents(ls), testResolver)
		return ch, Hypothesize(ch, inc, lookback)
	}

	ch1, h1 := run(lines)
	ch2, h2 := run(lines)
	if !reflect.DeepEqual(ch1, ch2) || !reflect.DeepEqual(h1, h2) {
		t.Fatalf("audit core not deterministic across runs")
	}
	// Reverse the input order — the outputs must be identical.
	rev := make([]string, len(lines))
	for i := range lines {
		rev[i] = lines[len(lines)-1-i]
	}
	chR, hR := run(rev)
	if !reflect.DeepEqual(ch1, chR) {
		t.Fatalf("change events not invariant to input order:\n%+v\n%+v", ch1, chR)
	}
	if !reflect.DeepEqual(h1, hR) {
		t.Fatalf("hypotheses not invariant to input order:\n%+v\n%+v", subjects(h1), subjects(hR))
	}
	// Cardinal: zero causal/structural edges, ever.
	for _, h := range h1 {
		if h.Kind == candidate.KindEdge {
			t.Fatalf("CHARTER BREACH: audit emitted a KindEdge (a change is co-occurrence, never a cause): %+v", h)
		}
	}
}

func TestHypothesizeAndStage(t *testing.T) {
	st, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	changes := Resolve(ParseEvents(fixtureLines(t)), testResolver)
	incs := []Incident{{ID: "inc-1", RoleCEI: incidentRole, Namespace: "erpnext", Onset: onset, GraphVersion: "vtest"}}
	now := time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC)
	n, err := HypothesizeAndStage(st, now, changes, incs, lookback)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("staged = %d, want 3", n)
	}
	rows, err := st.List(candidate.Filter{Kind: candidate.KindCausalHypothesis})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("store has %d causal_hypothesis rows, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Status != candidate.StatusCandidate {
			t.Errorf("staged row status = %q, want candidate (awaits human promotion)", r.Status)
		}
		if r.Lineage.Source != "audit" {
			t.Errorf("lineage source = %q, want audit", r.Lineage.Source)
		}
	}
	// Re-staging the same content is idempotent (content-derived id), not a duplicate.
	n2, err := HypothesizeAndStage(st, now, changes, incs, lookback)
	if err != nil {
		t.Fatal(err)
	}
	rows2, _ := st.List(candidate.Filter{Kind: candidate.KindCausalHypothesis})
	if n2 != 3 || len(rows2) != 3 {
		t.Errorf("re-stage should be idempotent: n2=%d rows=%d, want 3/3", n2, len(rows2))
	}
}

// A colliding auditID (two ResponseComplete lines, differing content) must dedup to ONE
// deterministic winner, invariant to arrival order — not last-write-wins.
func TestParseEventsDedupDeterministicOnCollision(t *testing.T) {
	a := `{"auditID":"dup","stage":"ResponseComplete","verb":"patch","user":{"username":"u"},"objectRef":{"resource":"deployments","namespace":"ns","name":"x"},"responseStatus":{"code":200},"stageTimestamp":"2026-06-18T12:00:00.000000Z"}`
	b := `{"auditID":"dup","stage":"ResponseComplete","verb":"delete","user":{"username":"u"},"objectRef":{"resource":"deployments","namespace":"ns","name":"x"},"responseStatus":{"code":200},"stageTimestamp":"2026-06-18T12:00:00.000000Z"}`
	fwd := ParseEvents([]string{a, b})
	rev := ParseEvents([]string{b, a})
	if len(fwd) != 1 || len(rev) != 1 {
		t.Fatalf("collision should dedup to 1: got %d/%d", len(fwd), len(rev))
	}
	if !reflect.DeepEqual(fwd, rev) {
		t.Fatalf("dedup is order-dependent (last-write-wins): fwd=%+v rev=%+v", fwd, rev)
	}
	if fwd[0].Verb != "delete" { // same ts ⇒ tiebreak on verb; delete < patch
		t.Errorf("winner verb = %q, want the deterministic min (delete < patch)", fwd[0].Verb)
	}
}

func TestKindForResource(t *testing.T) {
	if k, ok := KindForResource("deployments"); !ok || k != "Deployment" {
		t.Errorf("deployments -> %q,%v, want Deployment,true", k, ok)
	}
	if _, ok := KindForResource("widgets"); ok {
		t.Errorf("an unmapped resource must report ok=false (no guessed Kind)")
	}
}

func subjects(cs []candidate.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Subject
	}
	return out
}
