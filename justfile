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
    cd proto && buf lint && buf generate
    @echo "regenerated proto/gen — remember to commit it"

# Validate the ontology KG (schema + referential integrity) and print the authoring
# gap report (doc 02 M3 / doc 14 A14). Add --strict to fail on gaps.
graphlint *ARGS:
    go run ./tools/graphlint {{ARGS}}

# Print the content-hash pin for the current ontology (base + overlays) — paste it
# into a new ontology/releases/vX.Y.Z.yaml to cut a release (doc 12 M1).
graph-version:
    @go run ./tools/graphlint -print-version

# Fail if generated code is stale relative to the protos (CI guard).
gen-check:
    #!/usr/bin/env bash
    set -euo pipefail
    cd proto && buf generate
    cd ..
    if ! git diff --quiet -- proto/gen; then
        echo "proto/gen is stale — run 'just gen' and commit"; git --no-pager diff --stat -- proto/gen; exit 1
    fi
    echo "proto/gen up to date"

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

# Create/refresh the harness uv environment.
harness-setup:
    cd harness && uv sync

# Run harness tests + lint. The analytics extra (pyarrow et al.) is on: the
# replay-bundle Parquet bridge is live (doc 05 M5) and its regression reads
# real Parquet, never a stub.
harness-test:
    cd harness && uv run --extra analytics ruff check . && uv run --extra analytics pytest -q

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
