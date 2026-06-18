//go:build integration

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const e2eNamespace = "vigil-e2e"

// The exact not-yet-explained mark every unexplained card MUST carry (doc 08 §3.7,
// obsd/internal/unexplained.Mark). Pinned here so the live surface is held to the
// same constant the unit suite enforces.
const expectedUnexplainedMark = "anomalous — investigate · not-yet-explained"

// causalVocabulary is the register an unexplained card must NEVER use — the ABSENCE
// of a reason is the defining property (doc 08). Mirrors the charter audit's denylist.
var causalVocabulary = []string{
	"because", "caused", "causes", "causing", "due to", "triggers", "triggered",
	"results in", "leads to", "led to", "reason", "explains", "responsible for",
}

// --- minimal black-box decode structs (the operator's HTTP surface) ---------

type findingsResp struct {
	Findings []struct {
		Phenomenon string `json:"phenomenon"`
		Namespace  string `json:"namespace"`
		Name       string `json:"name"`
		Quality    string `json:"quality"`
		Stale      bool   `json:"stale"`
	} `json:"findings"`
}

type unexplainedResp struct {
	OpenCards []struct {
		Scope      string `json:"scope"`
		Namespace  string `json:"namespace"`
		Name       string `json:"name"`
		Mark       string `json:"mark"`
		MatchCheck string `json:"matchCheck"`
		Status     string `json:"status"`
		LoudStates []struct {
			Metric string `json:"metric"`
		} `json:"loudStates"`
	} `json:"openCards"`
	BlindSpot string `json:"blindSpot"`
}

type eventsResp struct {
	Available bool `json:"available"`
	Events    []struct {
		Reason       string `json:"reason"`
		Namespace    string `json:"namespace"`
		Name         string `json:"name"`
		Corroborates string `json:"corroborates"`
	} `json:"events"`
}

type insightsResp struct {
	Findings []struct {
		Phenomenon string `json:"phenomenon"`
		Namespace  string `json:"namespace"`
		Name       string `json:"name"`
	} `json:"findings"`
}

// TestLiveDetectionAndRestraint is the headline e2e: one obsd against a live cluster,
// real faults injected, asserting the right phenomenon fires AND restraint holds.
//
// It runs the lanes a real operator would: the KSM object-state lane (PVC/eviction/
// disk), the discrete-event lane (image-pull/crashloop), and the validate-claim
// referee. node-exporter is NOT required by any scenario here (all use cAdvisor,
// KSM, or k8s Events), keeping the prerequisites light.
func TestLiveDetectionAndRestraint(t *testing.T) {
	kc := kubeconfigPath(t)
	requireKubectl(t)

	// KSM must be up BEFORE obsd starts so obsd discovers + scrapes it (the
	// VOLUME_MOUNT_FAILURE scenario rides the KSM lane). Best-effort: if KSM cannot
	// be deployed, that one subtest skips with a clear reason — the rest still run.
	ksmReady := ensureKSM(t, kc)

	t.Cleanup(func() {
		deleteNamespace(kc, e2eNamespace)
		unstickNamespace(t, kc, e2eNamespace) // don't leave it wedged for the next run on a degraded cluster
	})
	// Ensure a clean namespace before recreating it (clears a prior run's wedged delete).
	prepareNamespace(t, kc, e2eNamespace)
	dataDir := t.TempDir()
	obsd := startObsd(t, kc, dataDir,
		"--ksm-enabled",
		"--events-enabled",
		"--referee-enabled",
	)

	// Inject all faults up front so they progress concurrently while subtests poll.
	applyChaos(t, kc, "corpus/chaos/e2e/rogue-stress.yaml")
	applyChaos(t, kc, "corpus/chaos/e2e/mem-leak-fast.yaml")
	applyChaos(t, kc, "corpus/chaos/e2e/image-pull-failure.yaml")
	if ksmReady {
		applyChaos(t, kc, "corpus/chaos/e2e/volume-mount-failure.yaml")
	}

	// RESTRAINT (the signature). Two correct, complementary behaviours that this live
	// run actually taught us (see docs/testing): a CPU-stressed pod crosses
	// cpu_cfs_throttled_periods, which IS an authored required member of
	// PHEN_THROTTLING_CASCADE — so a DEGRADED finding there is MEASURED truth, not a
	// fabrication. Restraint is NOT "zero findings"; it is:
	//  (1) the UNCOVERED loud state (cpu_usage_seconds, a member of nothing) routes to
	//      /api/unexplained with the constant mark and ZERO causal vocabulary, and
	//  (2) the finding that DOES fire is never OVER-CLAIMED — it can only be DEGRADED
	//      here (the node-PSI half is unobservable without node-exporter), and the
	//      system must surface it as such, naming the missing member.
	t.Run("restraint_unexplained", func(t *testing.T) {
		eventually(t, 4*time.Minute, 5*time.Second, "stress-rogue's uncovered loudness to route to /api/unexplained", func() error {
			var v unexplainedResp
			if err := obsd.getJSON("/api/unexplained", &v); err != nil {
				return err
			}
			if v.BlindSpot == "" {
				return fmt.Errorf("unexplained view missing the stated blindSpot notice")
			}
			for _, c := range v.OpenCards {
				if c.Namespace != e2eNamespace || !strings.Contains(c.Name, "stress-rogue") {
					continue
				}
				if c.Mark != expectedUnexplainedMark {
					return fmt.Errorf("card mark = %q, want %q", c.Mark, expectedUnexplainedMark)
				}
				blob := strings.ToLower(c.MatchCheck + " " + c.Mark)
				for _, w := range causalVocabulary {
					if strings.Contains(blob, w) {
						return fmt.Errorf("unexplained card leaked causal vocabulary %q in: %s", w, c.MatchCheck)
					}
				}
				t.Logf("restraint OK: stress-rogue uncovered loudness is %q · matchCheck=%q · loudStates=%d",
					c.Mark, c.MatchCheck, len(c.LoudStates))
				return nil
			}
			return fmt.Errorf("no open unexplained card yet for stress-rogue (%d cards)", len(v.OpenCards))
		})

		// No over-claim: any finding touching stress-rogue must be DEGRADED, never FULL
		// (it cannot support a full claim here). This is the charter floor — claim
		// exactly as much as the evidence supports, never more.
		var f findingsResp
		if err := obsd.getJSON("/api/findings", &f); err != nil {
			t.Fatalf("/api/findings: %v", err)
		}
		saw := false
		for _, r := range f.Findings {
			if r.Namespace != e2eNamespace || !strings.Contains(r.Name, "stress-rogue") {
				continue
			}
			saw = true
			if strings.EqualFold(r.Quality, "full") {
				t.Errorf("OVER-CLAIM: stress-rogue produced a FULL %q match — without node-exporter the node-PSI member is unobservable, so honest coverage requires DEGRADED", r.Phenomenon)
			} else {
				t.Logf("honest coverage: stress-rogue %q is %q (the missing member is named, never over-claimed as full)", r.Phenomenon, r.Quality)
			}
		}
		if !saw {
			t.Logf("no phenomenon finding touched stress-rogue (its loudness stayed entirely in the unexplained channel)")
		}
	})

	// POSITIVE — fingerprint lane: a memory-ramp pod near its limit fires DEGRADED
	// PHEN_MEMORY_LEAK (working-set rising + at-threshold) on /api/findings.
	t.Run("memory_leak_finding", func(t *testing.T) {
		eventually(t, 6*time.Minute, 5*time.Second, "PHEN_MEMORY_LEAK to fire on the leaker", func() error {
			r, err := findFinding(obsd, "MEMORY_LEAK", "leaker-fast")
			if err != nil {
				return err
			}
			t.Logf("memory-leak OK: %s on %s/%s quality=%s", r.Phenomenon, r.Namespace, r.Name, r.Quality)
			return nil
		})
	})

	// POSITIVE — KSM object-state lane: a PVC stuck Pending fires DEGRADED
	// PHEN_VOLUME_MOUNT_FAILURE on /api/findings (answers "PVC ops -> pods not starting").
	t.Run("volume_mount_failure_finding", func(t *testing.T) {
		if !ksmReady {
			t.Skip("kube-state-metrics not deployable in this cluster; VOLUME_MOUNT_FAILURE rides the KSM lane")
		}
		eventually(t, 4*time.Minute, 5*time.Second, "PHEN_VOLUME_MOUNT_FAILURE to fire on the stuck PVC", func() error {
			r, err := findFinding(obsd, "VOLUME_MOUNT_FAILURE", "")
			if err != nil {
				return err
			}
			t.Logf("volume-mount OK: %s on %s/%s quality=%s", r.Phenomenon, r.Namespace, r.Name, r.Quality)
			return nil
		})
	})

	// POSITIVE — discrete-event lane: a bad image -> ImagePullBackOff -> a MEASURED
	// IMAGE_PULL_FAILURE on /api/events and/or /api/insights (a Deployment that
	// silently never becomes Available is made visible).
	t.Run("image_pull_failure", func(t *testing.T) {
		eventually(t, 4*time.Minute, 5*time.Second, "IMAGE_PULL_FAILURE to surface for bad-image", func() error {
			// (a) the raw discrete event, on /api/events.
			var ev eventsResp
			if err := obsd.getJSON("/api/events", &ev); err == nil && ev.Available {
				for _, e := range ev.Events {
					if e.Namespace != e2eNamespace {
						continue
					}
					if strings.Contains(e.Corroborates, "IMAGE_PULL") ||
						strings.Contains(strings.ToLower(e.Reason), "imagepull") ||
						strings.Contains(strings.ToLower(e.Reason), "errimagepull") {
						t.Logf("image-pull OK via /api/events: reason=%q corroborates=%q on %s/%s", e.Reason, e.Corroborates, e.Namespace, e.Name)
						return nil
					}
				}
			}
			// (b) the degraded phenomenon finding, on /api/insights.
			var in insightsResp
			if err := obsd.getJSON("/api/insights", &in); err == nil {
				for _, f := range in.Findings {
					if f.Namespace == e2eNamespace && strings.Contains(f.Phenomenon, "IMAGE_PULL") {
						t.Logf("image-pull OK via /api/insights: %s on %s/%s", f.Phenomenon, f.Namespace, f.Name)
						return nil
					}
				}
			}
			return fmt.Errorf("IMAGE_PULL_FAILURE not yet visible on /api/events or /api/insights")
		})
	})
}

// findFinding polls /api/findings for a finding whose phenomenon contains phenSubstr
// and (when nameSubstr != "") whose entity name contains nameSubstr, in the e2e ns.
func findFinding(obsd *obsdProc, phenSubstr, nameSubstr string) (struct {
	Phenomenon, Namespace, Name, Quality string
}, error) {
	var out struct{ Phenomenon, Namespace, Name, Quality string }
	var v findingsResp
	if err := obsd.getJSON("/api/findings", &v); err != nil {
		return out, err
	}
	for _, r := range v.Findings {
		if r.Namespace != e2eNamespace {
			continue
		}
		if !strings.Contains(r.Phenomenon, phenSubstr) {
			continue
		}
		if nameSubstr != "" && !strings.Contains(r.Name, nameSubstr) {
			continue
		}
		out.Phenomenon, out.Namespace, out.Name, out.Quality = r.Phenomenon, r.Namespace, r.Name, r.Quality
		return out, nil
	}
	return out, fmt.Errorf("no %s finding yet in %s (%d findings total)", phenSubstr, e2eNamespace, len(v.Findings))
}

// ensureKSM deploys kube-state-metrics (deploy/workloads) and waits for it to become
// Available. Returns false (not fatal) if it cannot be deployed, so the KSM-dependent
// scenario can skip honestly rather than fail the whole suite.
func ensureKSM(t *testing.T, kc string) bool {
	t.Helper()
	manifest := filepath.Join(repoRoot(t), "deploy/workloads/kube-state-metrics.yaml")
	// The manifest's objects live in the `monitoring` namespace but it does not declare
	// the Namespace itself; create it first (idempotent — AlreadyExists is fine).
	_, _ = kubectlOut(kc, "create", "namespace", "monitoring")
	if out, err := kubectlOut(kc, "apply", "-f", manifest); err != nil {
		t.Logf("kube-state-metrics apply failed (KSM lane scenario will skip): %v\n%s", err, out)
		return false
	}
	out, err := kubectlOut(kc, "wait", "--for=condition=Available", "deployment/kube-state-metrics",
		"-n", "monitoring", "--timeout=150s")
	if err != nil {
		t.Logf("kube-state-metrics not Available (KSM lane scenario will skip): %v\n%s", err, out)
		return false
	}
	t.Logf("kube-state-metrics is Available")
	return true
}
