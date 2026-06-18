// Package e2e holds Vigil's automated live end-to-end suite (doc 11 §3.5 / the
// audit-v3 roadmap item #2). It is the missing proof that the WHOLE live pipeline —
// k8s scrape → identity join → binding → fingerprints → deterministic detection →
// the operator API — produces the right output against a real cluster, and that
// epistemic RESTRAINT holds: a loud-but-unmodeled workload routes to the unexplained
// channel, never a fabricated cause.
//
// Unlike the hermetic unit suite (which proves determinism by construction) and the
// offline corpus gates (which grade the real engine against hand-authored oracles),
// this suite boots the SHIPPED bin/obsd against a live single-node cluster, injects a
// fault from corpus/chaos/e2e/, and asserts the resulting state over HTTP — exactly
// what an operator (or an ABB evaluator) would see. It is the difference between
// "the engine is correct in a test harness" and "the product works on a cluster."
//
// # Scope (Theme 2 acceptance criteria → assertion)
//
//	MEMORY_LEAK         /api/findings    "which workload is leaking / needs optimization?"
//	VOLUME_MOUNT_FAILURE/api/findings    "how are PVC operations linked to pods not starting?" (KSM lane)
//	IMAGE_PULL_FAILURE  /api/events      a Deployment that silently never becomes Available (events lane)
//	restraint           /api/unexplained "I see something, I do not know what" — no invented cause
//	determinism         bin/replay       capture-here, replay-anywhere, byte-identical (portability proof)
//
// # Running
//
// The suite is behind the `integration` build tag (like identity/clock integration
// tests) so the default `go test -race ./...` stays cluster-free. It needs a reachable
// single-node cluster and `kubectl` on PATH. The kubeconfig is resolved from
// VIGIL_TEST_KUBECONFIG, then KUBECONFIG, then the default loading rules.
//
//	# local k3s (this repo's dev box): the default ~/.kube/config CA can be stale,
//	# so point at the canonical k3s kubeconfig (obsd does not tolerate a wrong CA):
//	VIGIL_TEST_KUBECONFIG=/etc/rancher/k3s/k3s.yaml just e2e
//	# or directly:
//	go test -tags=integration -count=1 -timeout=20m ./obsd/internal/e2e/ -run TestLive -v
//
// The suite builds bin/obsd + bin/replay itself (no prior `just build` required),
// deploys kube-state-metrics if the KSM lane scenario needs it, and cleans up the
// vigil-e2e namespace on exit. See docs/testing/ for the full strategy and the GCP
// single-node runbook.
package e2e
