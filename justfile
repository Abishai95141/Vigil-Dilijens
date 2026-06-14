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

# --- Web surfaces (deferred install; Phase 0b+) -----------------------------

web-install:
    cd web && pnpm install

web-dev:
    cd web && pnpm dev

web-test:
    cd web && pnpm test

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
