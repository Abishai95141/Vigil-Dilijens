# Vigil — the single command surface (techstack §2.2).
# Every action an agent or human takes is a `just` recipe. Recipes are written to
# behave IDENTICALLY on macOS (dev) and Linux/Ubuntu (cluster + tests). Bash is the
# shell on both; we avoid GNU-only flags and bash-4 features (macOS ships bash 3.2).

set shell := ["bash", "-uc"]

mod := "github.com/Abishai95141/Vigil-Dilijens"
ldflags_pkg := mod + "/obsd/internal/version"

# List all recipes.
default:
    @just --list

# --- Go runtime core (obsd) -------------------------------------------------

# Resolve and tidy Go module dependencies.
tidy:
    go mod tidy

# Build obsd + replay into ./bin with version stamping (works on macOS + Linux).
build:
    #!/usr/bin/env bash
    set -euo pipefail
    commit="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    builddate="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    flags="-X {{ldflags_pkg}}.Commit=${commit} -X {{ldflags_pkg}}.Date=${builddate}"
    mkdir -p bin
    go build -trimpath -ldflags "${flags}" -o bin/obsd ./obsd/cmd/obsd
    go build -trimpath -ldflags "${flags}" -o bin/replay ./obsd/cmd/replay
    echo "built ./bin/obsd ./bin/replay"

# Run the obsd runtime (loads embedded dev params; pass --params to override).
obsd *ARGS:
    go run ./obsd/cmd/obsd {{ARGS}}

# Run the harness replay runner.
replay *ARGS:
    go run ./obsd/cmd/replay {{ARGS}}

# Hermetic unit tests with the race detector (the standing test command, techstack §2.4).
test:
    go test -race ./...

# Integration tests (behind the `integration` build tag; require a kind cluster).
test-integration:
    go test -race -tags=integration ./...

# Automated LIVE end-to-end suite (doc 11 §3.5 / audit roadmap #2): boots the shipped
# obsd against a REAL single-node cluster, injects corpus/chaos/e2e/ faults, and asserts
# the right phenomenon fires via /api (MEMORY_LEAK, VOLUME_MOUNT_FAILURE via KSM,
# IMAGE_PULL_FAILURE via events) + RESTRAINT holds on a non-modeled fault (the rogue
# stress pod routes to /api/unexplained, never a fabricated cause) + LIVE replay
# determinism (capture-here, replay-anywhere, byte-identical). Needs a reachable cluster
# + kubectl. Point VIGIL_TEST_KUBECONFIG at the kubeconfig — on k3s use
# /etc/rancher/k3s/k3s.yaml (obsd rejects a stale ~/.kube/config CA). It builds obsd +
# replay itself and deploys kube-state-metrics. Exit 0 = the live pipeline + restraint
# + determinism all hold.
e2e *ARGS:
    go test -tags=integration -count=1 -timeout=25m ./obsd/internal/e2e/ -run TestLive -v {{ARGS}}

# Hermetic scale benchmarks (audit roadmap #6): the per-tick DETECTION cost curve
# (BenchmarkMatch, to 5000 synthetic entities on the real graph) + the SQLite
# single-writer findings WRITE cost (BenchmarkUpsertFindings, to 1000 findings/tick).
# No cluster needed; read ns/op as per-tick latency and B/op as GC pressure.
bench *ARGS:
    go test -bench='BenchmarkMatch|BenchmarkUpsertFindings' -benchmem -run='^$' ./obsd/internal/detect/ ./obsd/internal/store/ {{ARGS}}

# Live scale test (audit roadmap #6 / the theme's "hundreds of pods on a single node"):
# pack N synthetic pods (VIGIL_SCALE_N, default 50 — safe under k3s --max-pods=110),
# assert obsd discovers them all, RSS stays bounded, ZERO mis-joins, and churn-to-0 GCs
# cleanly. Needs a cluster + kubectl (point VIGIL_TEST_KUBECONFIG, as for `just e2e`).
scale *ARGS:
    go test -tags=integration -count=1 -timeout=20m ./obsd/internal/e2e/ -run TestLiveScale -v {{ARGS}}

# Live soak (leak/stability over time): obsd runs for VIGIL_SOAK_DURATION (e.g. 2h),
# sampling RSS and asserting it stays bounded with ZERO mis-joins throughout. Opt-in —
# unset VIGIL_SOAK_DURATION => the test skips (so it never slows the default loop).
soak *ARGS:
    go test -tags=integration -count=1 -timeout=4h ./obsd/internal/e2e/ -run TestLiveSoak -v {{ARGS}}

# Format all Go and proto sources in place.
fmt:
    gofmt -w .
    cd proto && buf format -w

# Lint: go vet + gofmt check + buf lint (no external installs required).
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    go vet ./...
    unformatted="$(gofmt -l .)"
    if [ -n "${unformatted}" ]; then
        echo "gofmt needs these files:"; echo "${unformatted}"; exit 1
    fi
    cd proto && buf lint
    cd "{{justfile_directory()}}"
    # Graph release immutability (doc 12 M1): the NEWEST committed release must
    # still describe the actual ontology (newest chosen by created-date, not
    # filename). Editing the graph without cutting a new release fails here (and
    # in release_test.go).
    go run ./tools/graphlint -release-latest ontology/releases
    echo "lint OK"

# Optional heavier linters, go-installed on demand (pure Go, no CGO, cross-platform).
tools:
    go install mvdan.cc/gofumpt@v0.9.1
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.1
    go install golang.org/x/vuln/cmd/govulncheck@latest

# --- Protobuf contracts -----------------------------------------------------

# Lint protos and regenerate committed Go stubs.
gen:
    #!/usr/bin/env bash
    set -euo pipefail
    # The python template's out dir is clockd/src (shared with the handwritten
    # clockd package), so it cannot use buf's clean — remove the generated tree
    # explicitly instead, then regenerate.
    rm -rf clockd/src/vigil
    cd proto && buf lint && buf generate
    buf generate --template buf.gen.python.yaml
    cd ..
    # The generated dirs carry no __init__.py; make them regular packages so the
    # editable install (hatchling `packages`) and the absolute imports inside the
    # generated grpc stubs ("from vigil.clock.v1 import ...") resolve everywhere.
    find clockd/src/vigil -type d -exec touch {}/__init__.py \;
    echo "regenerated proto/gen + clockd/src/vigil — remember to commit both"

# Validate the ontology KG (schema + referential integrity) and print the authoring
# gap report (doc 02 M3 / doc 14 A14). Add --strict to fail on gaps.
graphlint *ARGS:
    go run ./tools/graphlint {{ARGS}}

# Print the content-hash pin for the current ontology (base + overlays) — paste it
# into a new ontology/releases/vX.Y.Z.yaml to cut a release (doc 12 M1).
graph-version:
    @go run ./tools/graphlint -print-version

# --- Graph governance (doc 12) ----------------------------------------------

# Derive the change class + diff between two releases (doc 12 M2).
govern-classify from to:
    @go run ./obsd/cmd/govern classify --from {{from}} --to {{to}}

# Run the review workflow over a proposal; non-zero exit if blocked (doc 12 M2/M3).
# Pass a harness ledger (just govern-gates) to make the regression gates binding.
govern-verify proposal ledger="":
    @go run ./obsd/cmd/govern verify --proposal {{proposal}} {{ if ledger != "" { "--ledger " + ledger } else { "" } }}

# Run the harness-wired regression gates for a proposal, writing the ledger the
# workflow consumes (doc 12 M3). The harness BLOCKS on any failed gate; never approves.
govern-gates proposal out:
    cd harness && uv run --extra analytics python -m harness.governance run-gates --proposal {{proposal}} --out {{out}}

# Fail if generated code is stale relative to the protos (CI guard).
gen-check:
    #!/usr/bin/env bash
    set -euo pipefail
    cd proto && buf generate
    buf generate --template buf.gen.python.yaml
    cd ..
    if ! git diff --quiet -- proto/gen clockd/src/vigil; then
        echo "generated stubs are stale — run 'just gen' and commit"; git --no-pager diff --stat -- proto/gen clockd/src/vigil; exit 1
    fi
    echo "proto/gen + clockd/src/vigil up to date"

# Check protos for backwards-incompatible changes against the main branch.
buf-breaking:
    cd proto && buf breaking --against '../.git#branch=main,subdir=proto'

# --- Python: clockd (forecasting service) + harness (analytics) -------------

# Create/refresh the clockd uv environment.
clockd-setup:
    cd clockd && uv sync

# Run clockd tests + lint.
clockd-test:
    cd clockd && uv run ruff check . && uv run pytest -q

# Run the clockd gRPC service (doc 09 M1). CLOCK=stub|timesfm (timesfm needs
# the `model` extra: pinned package 2.0.x serving the 2.5 checkpoint, doc 14 A15).
clockd CLOCK="stub" PORT="50051":
    cd clockd && uv run --extra {{ if CLOCK == "timesfm" { "model" } else { "serve" } }} python -m clockd.server --clock {{CLOCK}} --port {{PORT}}

# clockd tests INCLUDING the gRPC serving layer (needs the `serve` extra).
clockd-serve-test:
    cd clockd && uv run --extra serve ruff check . && uv run --extra serve pytest -q

# The opt-in model conformance run (doc 09 M1 swap test, Python half): the SAME
# fixtures as the stub, against pinned TimesFM 2.5. Heavy — downloads/loads the
# checkpoint on first run.
clockd-model-conformance:
    cd clockd && VIGIL_MODEL_CONFORMANCE=1 uv run --extra model pytest tests/test_conformance.py -q

# Create/refresh the harness uv environment.
harness-setup:
    cd harness && uv sync

# Run harness tests + lint. The analytics extra (pyarrow et al.) is on: the
# replay-bundle Parquet bridge is live (doc 05 M5) and its regression reads
# real Parquet, never a stub.
harness-test:
    cd harness && uv run --extra analytics ruff check . && uv run --extra analytics pytest -q

# Cross-service cascade backtest gate (doc 15 phase D / 11 §3.5) over the frozen,
# live-captured corpus in corpus/crossservice/ — offline, no cluster needed. Exit
# 0 = PASSED (the class may be operator-visible); 1 = FAILED/INSUFFICIENT.
xsvc-gate:
    cd harness && uv run python -m harness.crossservice_gate \
      --events ../corpus/crossservice/events-pair.jsonl ../corpus/crossservice/events-fanin.jsonl ../corpus/crossservice/events-healthy.jsonl \
      --labels ../corpus/crossservice/label-pair.json ../corpus/crossservice/label-fanin.json ../corpus/crossservice/label-healthy.json

# Anticipatory (phase E) cross-service backtest gate (doc 15 phase E / 11 §3.5) over
# the frozen, live-captured corpus in corpus/crossservice-projected/ — two independent
# forecast-led leaks, each anticipated minutes before its measured cross-service
# cascade, plus a negative the forecast never anticipates. Offline, no cluster needed.
# Exit 0 = PASSED (the PROJECTED lane may be operator-visible); 1 = FAILED/INSUFFICIENT.
xsvc-projected-gate:
    cd harness && uv run python -m harness.projected_crossservice_gate \
      --events ../corpus/crossservice-projected/events-pos1.jsonl ../corpus/crossservice-projected/events-pos2.jsonl ../corpus/crossservice-projected/events-negative.jsonl \
      --labels ../corpus/crossservice-projected/label-pos1.json ../corpus/crossservice-projected/label-pos2.json ../corpus/crossservice-projected/label-negative.json

# MCP read-only harness gate (v3 T-A / doc 11 §3.5) over the frozen corpus in
# corpus/mcp/ — certifies silence-ledger determinism + absence-completeness +
# reconciliation with per-rule coverage + ADVISORY refusal teeth (no content leak).
# Runs BOTH halves: the Go unit gate (no-write-back, non-gating, class round-trip,
# teeth) and the offline Python corpus gate. Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
mcp-gate:
    go test -race ./obsd/internal/mcp/ ./obsd/internal/api/ -run 'MCP|Silence|Advisory|NoWriteBack|Warnings|Initialize|ToolsList|LaneOff|Notification|Malformed|Dispatch|HTTPTransport'
    cd harness && uv run python -m harness.mcp_gate \
      --ledger ../corpus/mcp/silence-ledger.json \
      --advisory ../corpus/mcp/advisory-drafts.jsonl

# Incident-memory gate (v3 T-B / doc 11 §3.5) over the frozen corpus in
# corpus/incident-memory/ — certifies the durable cross-run recurrence semantics
# (key-purity, grouping N->1 count==N, continuous-span no-inflation, restart-
# invariance, separation) deterministically, offline. Runs the Go unit gate (the
# incident core + store cross-check + engine fold) and the Python corpus gate.
# Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
incident-gate:
    go test -race ./obsd/internal/incident/
    go test -race ./obsd/internal/store/ -run Incident
    go test -race ./obsd/internal/replay/ -run Incident
    cd harness && uv run python -m harness.incident_memory_gate \
      --events ../corpus/incident-memory/events-recurring.jsonl ../corpus/incident-memory/events-restart.jsonl ../corpus/incident-memory/events-continuous.jsonl ../corpus/incident-memory/events-multirole.jsonl \
      --labels ../corpus/incident-memory/label-recurring.json ../corpus/incident-memory/label-restart.json ../corpus/incident-memory/label-continuous.json ../corpus/incident-memory/label-multirole.json

# Events-corroboration gate (v3 T-C / doc 11 §3.5) over the frozen corpus in
# corpus/events/ — certifies the discrete-event JOIN deterministically, offline:
# JOIN-FIDELITY==1.0 vs a label oracle (a deliberate identity-mismatch MUST NOT
# corroborate), no-phantom, standalone-visibility, digest-invariance (join read-only
# w.r.t. the digest), charter. Runs the Go unit gate (events pkg incl. the join +
# resolution + digest-invariance) and the Python corpus gate. Exit 0 = PASSED; 1 =
# FAILED/INSUFFICIENT.
events-gate:
    go test -race ./obsd/internal/events/ ./obsd/internal/api/ -run 'Events|Corroborate|ResolveEventRole|LoadEventConditions|EventConditions'
    cd harness && uv run python -m harness.events_corroboration_gate \
      --events ../corpus/events/events-corroborated-oom.jsonl ../corpus/events/events-multi-role.jsonl ../corpus/events/events-identity-mismatch.jsonl ../corpus/events/events-standalone-crashloop.jsonl ../corpus/events/events-healthy-negative.jsonl \
      --labels ../corpus/events/label-corroborated-oom.json ../corpus/events/label-multi-role.json ../corpus/events/label-identity-mismatch.json ../corpus/events/label-standalone-crashloop.json ../corpus/events/label-healthy-negative.json

# validate-claim referee gate (v3 T-D / doc 01 / doc 11 §3.5) over the frozen corpus in
# corpus/validate-claim/ — certifies the deterministic LLM-claim referee: FALSE-BLOCK==0
# (ABSOLUTE — a true claim is never flagged), RECALL>=0.90, per-category recall, the
# MUTATION teeth (with the substring denylist OFF the STRUCTURAL backstop still catches
# relation-absent fabrications), labelled-best-effort, never-block. The Go half runs the
# referee unit tests + the frozen-corpus drift guard (verdicts == a fresh ValidateClaim
# run); the Python half grades the frozen verdicts. Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
validate-claim-gate:
    go test -race ./obsd/internal/api/ -run 'ValidateClaim|Legit|RelationAbsent|AuthoredCausal|ClassFusion|FutureCertainty|StructuralHoneypot|Mutation|NeverBlocks|Deterministic'
    cd harness && uv run python -m harness.validate_claim_gate --verdicts ../corpus/validate-claim/verdicts.jsonl

# event-detection gate (graph-robustness #2 G1 / doc 07 §3.1+§3.4 / doc 11 §3.5) over the
# frozen corpus in corpus/event-detection/ — certifies that a discrete event the KG
# authors as a REQUIRED member produces a DEGRADED MEASURED phenomenon finding and lights
# up the AUTHORED cascade (MEMORY_LEAK->OOM_KILL_CGROUP, THROTTLING_CASCADE->
# PROBE_FAILURE_RESTART), OFF the fingerprint digest. Floors: detection-fidelity (produced
# == oracle), no-false-upgrade==0 (role-unresolved/unauthored fire nothing), cascade-
# recognition (authored why verbatim, no phantom), degraded-honest (missing members named),
# digest-invariance, charter==0. The Go half runs the producer unit tests + the always-on
# digest guard + the frozen-corpus drift guard; the Python half grades the frozen corpus.
# Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
event-detection-gate:
    go test -race ./obsd/internal/eventdetect/
    cd harness && uv run python -m harness.event_detection_gate \
      --findings ../corpus/event-detection/findings-oom-detection.jsonl ../corpus/event-detection/findings-leak-to-oom-cascade.jsonl ../corpus/event-detection/findings-throttle-to-probe-cascade.jsonl ../corpus/event-detection/findings-image-pull-failure.jsonl ../corpus/event-detection/findings-role-unresolved-no-upgrade.jsonl ../corpus/event-detection/findings-unrelated-reason.jsonl ../corpus/event-detection/findings-healthy-negative.jsonl \
      --cascades ../corpus/event-detection/cascades-oom-detection.jsonl ../corpus/event-detection/cascades-leak-to-oom-cascade.jsonl ../corpus/event-detection/cascades-throttle-to-probe-cascade.jsonl ../corpus/event-detection/cascades-image-pull-failure.jsonl ../corpus/event-detection/cascades-role-unresolved-no-upgrade.jsonl ../corpus/event-detection/cascades-unrelated-reason.jsonl ../corpus/event-detection/cascades-healthy-negative.jsonl \
      --labels ../corpus/event-detection/label-oom-detection.json ../corpus/event-detection/label-leak-to-oom-cascade.json ../corpus/event-detection/label-throttle-to-probe-cascade.json ../corpus/event-detection/label-image-pull-failure.json ../corpus/event-detection/label-role-unresolved-no-upgrade.json ../corpus/event-detection/label-unrelated-reason.json ../corpus/event-detection/label-healthy-negative.json

# app-slo gate (doc 15 cap. A / doc 11 §3.5) over the frozen corpus in corpus/app-slo/ —
# certifies the application-signal keystone: an app gauge (queue depth) crossing its
# CUSTOMER-DECLARED SLO produces a MEASURED finding, and an UNDECLARED SLO never fabricates
# a bar. Each scenario folds the REAL binding.Compile (SLO resolution) -> observe.Materialize
# -> detect.Matcher against a label oracle (fire iff declared AND crossed). Floors:
# detection-fidelity, no-fabrication==0 (the charter floor — a high queue with no declared SLO
# is never a finding), borrowed-bar (firing bar is config-sourced, not a default), charter==0.
# The Go half runs the producer unit tests + the always-on frozen-corpus drift guard; the Python
# half grades the frozen corpus. Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
app-slo-gate:
    go test -race ./obsd/internal/detect/ ./obsd/internal/binding/ ./obsd/internal/identity/ ./obsd/internal/observe/ -run 'App|AgeFromTimestamp'
    cd harness && scns="over-slo under-slo undeclared-high-queue healthy-no-stream freshness-stale freshness-fresh freshness-undeclared load-over load-under load-undeclared"; \
      f=""; l=""; for s in $scns; do f="$f ../corpus/app-slo/findings-$s.jsonl"; l="$l ../corpus/app-slo/label-$s.json"; done; \
      uv run python -m harness.app_slo_gate --findings $f --labels $l

# KSM lane gate (G2 telemetry expansion / doc 07 §3.1 / doc 11 §3.5) — certifies the
# kube-state-metrics object-state lane end-to-end, deterministically and offline. KSM
# rides the IN-digest fingerprint path (unlike the off-digest events lane), so the gate
# is a golden-fixture gate over the REAL ingest -> normalize -> fingerprint -> matcher
# pipeline. Floors proven: ingest-fidelity (the restart counter resolves to its container
# CEI; the multi-dimensional node-condition family splits into DISTINCT streams — no
# mis-join; the unmapped PDB object metric quarantines, never guessed); detection-fidelity
# (a crossed restart-rate guard fires a DEGRADED PROBE_FAILURE_RESTART, 1-of-11 members,
# the bar FLAGGED — borrowed-normativity); silence (under the guard / no variable => no
# fabricated alarm); cascade-recognition (a throttled crash-looper lights the AUTHORED
# THROTTLING_CASCADE -> PROBE_FAILURE_RESTART relation, "Probe cascade" verbatim, and
# topologically-unrelated entities NEVER pair); non-gating (the released v4 check is INERT
# without the KSM scrape — no restart stream => no finding => byte-identical detection).
# Exit 0 = PASSED; 1 = FAILED.
ksm-gate:
    go test -race ./obsd/internal/observe/ ./obsd/internal/detect/ ./obsd/internal/identity/ -run 'KSM|ProbeFailureRestart|ThrottleToProbe|ThrottleProbeRestart|Eviction|DiskPressure|PVC|VolumeMount'

# transitive-chain gate (doc 15 cap. B / doc 11 §3.5) over the frozen corpus in
# corpus/transitive-chain/ — certifies the transitive root-cause chain: MEASURED-degraded
# workloads stitched into an ORDERED chain by walking the observed-flow topology, oriented
# ONLY by the AUTHORED relation (never timing). The CARDINAL floor: independently-coincident
# faults never merge into one chain. Each scenario folds the REAL flow.TransitiveChains (a
# pure function) against a label oracle. The Go half runs the unit tests + the always-on
# frozen-corpus determinism guard; the Python half grades. Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
transitive-chain-gate:
    go test -race ./obsd/internal/flow/ -run 'Transitive'
    cd harness && scns="linear-chain independent-faults silent-intermediate fan-in two-disjoint-real-chains healthy-no-degradation"; \
      c=""; l=""; for s in $scns; do c="$c ../corpus/transitive-chain/chains-$s.jsonl"; l="$l ../corpus/transitive-chain/label-$s.json"; done; \
      uv run python -m harness.transitive_chain_gate --chains $c --labels $l

# multi-hop projected cascade gate (doc 15 cap. D / doc 11 §3.5) over corpus/projected-transitive/
# — certifies "forecast the ripple": ONE forecast root rippling to its transitive callers, each
# inheriting the root's band WIDENED per hop. The ABSOLUTE-ZERO floor: the band never narrows
# downstream (a downstream node tighter than its parent = instant fail). Each scenario folds the
# REAL flow.ProjectedTransitiveChains (a pure fn) vs a label oracle. PROJECTED off-digest by class;
# the gate certifies the JOIN. Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
projected-transitive-gate:
    go test -race ./obsd/internal/flow/ -run 'ProjTrans|ProjectedTransitive'
    cd harness && scns="linear-2hop fan-out two-roots open-horizon no-caller no-forecast midnight-wrap tight-root maxhops-bounded"; \
      c=""; l=""; for s in $scns; do c="$c ../corpus/projected-transitive/chains-$s.jsonl"; l="$l ../corpus/projected-transitive/label-$s.json"; done; \
      uv run python -m harness.projected_transitive_gate --chains $c --labels $l

# departure (band-anomaly) gate (doc 15 cap. C / doc 11 §3.5 / task #75) over corpus/departure/ —
# certifies the anomaly half: a MEASURED sample leaving its own PROJECTED forecast band fires
# (classed PROJECTED, off-digest), and a noisy-but-stationary series (whose clock band is WIDE)
# NEVER false-fires — the band is the bar (structural FP defense). The CARDINAL floor: zero false
# departures on the near-miss/decoy scenarios. Each scenario folds the REAL departure.Detect (a
# pure fn) vs a label oracle. Exit 0 = PASSED; 1 = FAILED/INSUFFICIENT.
departure-gate:
    go test -race -count=1 ./obsd/internal/departure/
    cd harness && scns="step-above step-below noisy-decoy wide-band-absorbs near-miss-margin healthy-inside edge-inside zero-width-band"; \
      d=""; l=""; for s in $scns; do d="$d ../corpus/departure/departures-$s.jsonl"; l="$l ../corpus/departure/label-$s.json"; done; \
      uv run python -m harness.departure_gate --departures $d --labels $l

# REAL-MODEL forecast calibration gate (doc 09 M3 / audit roadmap #1). The reproducible
# form of the 09 M3 campaign (evidence: corpus/labels/forecast-gate-09M3.md): start
# clockd, re-run the REAL forecast pipeline over a recorded bundle ("as of" every tick),
# and grade it — band coverage, per-event recall (a warning with lead before each
# crossing), time-to-cross error, false warnings. This is DISTINCT from the SCORER unit
# test (harness/tests/test_forecast_gate.py), which only checks the grading arithmetic.
#
#   clock=timesfm : the REAL model — the gate of record (needs the clockd `model` extra:
#                   a heavy torch + checkpoint download on first run).
#   clock=stub    : wiring smoke ONLY — the zero-knowledge clock goes flat, so no skill,
#                   so INSUFFICIENT is the correct, honest outcome (proves the plumbing).
# bundle: a recorded replay bundle with a forecast-eligible series (a gauge crossing a
#   LEVEL bar over the context window). The committed bundle-v1 is short (plumbing smoke);
#   capture a real slow-creep bundle live for the gate of record (see docs/testing).
#
# Usage (params are POSITIONAL — `just` does not take name=value for recipe args):
#   just forecast-gate                                                 # real model, bundle-v1
#   just forecast-gate corpus/bundles/creep                            # real model, a real capture
#   just forecast-gate obsd/internal/replay/testdata/bundle-v1 stub    # wiring smoke (no model)
forecast-gate bundle="obsd/internal/replay/testdata/bundle-v1" clock="timesfm":
    #!/usr/bin/env bash
    set -euo pipefail
    work="$(mktemp -d)"; events="$work/events.jsonl"; readings="$work/readings.parquet"
    extra=serve; [ "{{clock}}" = "timesfm" ] && extra=model
    echo ">> starting clockd (--clock {{clock}}) on :50051 (extra: $extra)"
    ( cd clockd && uv run --extra "$extra" python -m clockd.server --clock {{clock}} --port 50051 ) &
    trap 'pkill -f "clockd.server --clock {{clock}} --port 50051" 2>/dev/null || true' EXIT
    for i in $(seq 1 120); do (exec 3<>/dev/tcp/127.0.0.1/50051) 2>/dev/null && { exec 3>&-; break; }; sleep 1; done
    sleep 2
    echo ">> replay -forecast over {{bundle}} (real pipeline, as-of every tick)"
    go run ./obsd/cmd/replay -bundle "{{bundle}}" -forecast -forecast-out "$events" -forecast-clockd 127.0.0.1:50051
    echo ">> exporting realized readings to Parquet"
    go run ./obsd/cmd/replay -bundle "{{bundle}}" -export-parquet "$readings"
    echo ">> grading (harness.forecast_gate)"
    cd harness && uv run --extra analytics python -m harness.forecast_gate --events "$events" --readings "$readings"

# --- Operator console (console/ — the doc-10 surfacing layer) ---------------
# The dummy testing UI is archived at web-legacy/. The real console is console/.

console-install:
    cd console && pnpm install

console-dev:
    cd console && pnpm dev

console-build:
    cd console && pnpm build

console-check:
    cd console && pnpm lint && pnpm typecheck

# --- Dev cluster (kind — runs on the Linux box) -----------------------------

# Create the 3-node kind cluster (1 control-plane + 2 workers, doc 14 §3.1).
up:
    kind create cluster --config deploy/kind/cluster.yaml --name vigil

# Tear down the kind cluster.
down:
    kind delete cluster --name vigil

# Apply the read-only RBAC for the in-cluster deployment (doc 14 A2).
rbac:
    kubectl apply -f deploy/rbac/clusterrole.yaml

# Deploy the Phase-0 Online Boutique workload into the `online-boutique` namespace,
# apply the doc 14 §3.3 customizations (one service unbounded -> resolvability-hole;
# one tuned near its working set), and wait for all Deployments to become Available.
boutique:
    #!/usr/bin/env bash
    set -euo pipefail
    ns=online-boutique
    kubectl create namespace "${ns}" --dry-run=client -o yaml | kubectl apply -f -
    kubectl apply -n "${ns}" -f deploy/workloads/online-boutique.yaml
    # doc 14 §3.3: strip limits from one service (paymentservice) so the unbounded ->
    # listed/Tier-B-ineligible resolvability-hole policy is exercised from day one.
    kubectl patch deployment paymentservice -n "${ns}" --type=json \
      -p '[{"op":"remove","path":"/spec/template/spec/containers/0/resources/limits"}]'
    # doc 14 §3.3: tune one service (recommendationservice) near its working set —
    # 450Mi -> 300Mi memory limit, an honest near-threshold bar (kept above the ~220Mi
    # request so it still schedules and stays Ready on kind).
    kubectl patch deployment recommendationservice -n "${ns}" --type=json \
      -p '[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"300Mi"}]'
    # cartservice: upstream's 128Mi limit OOMKills the .NET runtime under loadgenerator
    # traffic (observed live: exit 137, CrashLoopBackOff). 256Mi keeps it green; this is
    # a declared-limit change, not a removed bar, so it stays Tier-B-eligible.
    kubectl patch deployment cartservice -n "${ns}" --type=json \
      -p '[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"256Mi"}]'
    echo "waiting for all Deployments to become Available (image pulls can take a few minutes)..."
    kubectl wait --for=condition=Available deployment --all -n "${ns}" --timeout=420s
    kubectl get pods -n "${ns}" -o wide

# --- Aggregates -------------------------------------------------------------

# The full Go gate, mirroring CI: format check, vet, generated-code freshness, tests.
ci: lint gen-check test
    @echo "go CI gate passed"

# Remove build output and runtime data.
clean:
    rm -rf bin data
