# ajq — Makefile
#
# Common developer tasks for building, testing, and linting ajq.
# Run `make help` for a categorized list of targets.

# ---- Project metadata --------------------------------------------------------

BINARY      := ajq
MODULE      := github.com/ricardocabral/ajq
MAIN_PKG    := ./cmd/ajq
VERSION_PKG := $(MODULE)/internal/version

# Version stamped into the binary. Prefers an exact git tag, otherwise a
# `git describe` string, falling back to "dev" outside a git checkout.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# ---- Build configuration -----------------------------------------------------

# Output directories (both already covered by .gitignore).
BIN_DIR  := bin
DIST_DIR := dist

# Website build configuration. Override these in CI/CD if the site source or
# package manager changes.
WEBSITE_DIR         ?= website
WEBSITE_BUILD_CMD   ?= npm run build
WEBSITE_INSTALL_CMD ?= npm ci
WEBSITE_SERVE_CMD   ?= npm run serve

# Strip debug info and stamp the version. -trimpath keeps builds reproducible.
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(VERSION)
GOFLAGS := -trimpath

# Host platform (used as the default `make build` target).
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Cross-compilation matrix for `make dist`.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

# Tool versions resolved lazily so targets that don't need them stay fast.
GO           := go
GOLANGCI_LINT := golangci-lint
GORELEASER    := goreleaser

# Coverage output file (gitignored).
COVERAGE := coverage.out

# Use bash with strict flags for recipe reliability.
SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c

.DEFAULT_GOAL := help

# ---- Meta --------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Build

.PHONY: build
build: ## Build the ajq binary into ./bin
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(MAIN_PKG)
	@echo "built $(BIN_DIR)/$(BINARY) ($(VERSION))"

.PHONY: install
install: ## Install ajq into $GOBIN / $GOPATH/bin
	$(GO) install $(GOFLAGS) -ldflags '$(LDFLAGS)' $(MAIN_PKG)

.PHONY: run
run: ## Run ajq from source (use ARGS="..." to pass arguments)
	$(GO) run $(MAIN_PKG) $(ARGS)

.PHONY: release-snapshot
release-snapshot: ## Build checksummed snapshot archives with GoReleaser into ./dist
	@command -v $(GORELEASER) >/dev/null 2>&1 || { \
		echo "goreleaser not found; install from https://goreleaser.com" >&2; exit 1; }
	$(GORELEASER) release --snapshot --clean

.PHONY: dist
dist: ## Cross-compile release binaries for all platforms into ./dist
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="$(DIST_DIR)/$(BINARY)_$${os}_$${arch}$${ext}"; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o "$$out" $(MAIN_PKG); \
	done

.PHONY: website-build
website-build: ## Build the website locally (override WEBSITE_DIR if needed)
	@test -d "$(WEBSITE_DIR)" || { \
		echo "website source directory '$(WEBSITE_DIR)' not found; set WEBSITE_DIR=path/to/site" >&2; \
		exit 1; \
	}
	@if [ -f "$(WEBSITE_DIR)/package.json" ] && [ ! -d "$(WEBSITE_DIR)/node_modules" ]; then \
		echo "installing website dependencies in $(WEBSITE_DIR)"; \
		cd "$(WEBSITE_DIR)" && $(WEBSITE_INSTALL_CMD); \
	fi
	cd "$(WEBSITE_DIR)" && PATH="$$(pwd)/node_modules/.bin:$$PATH" $(WEBSITE_BUILD_CMD)

.PHONY: website-deps
website-deps: ## Install website dependencies
	@test -d "$(WEBSITE_DIR)" || { \
		echo "website source directory '$(WEBSITE_DIR)' not found; set WEBSITE_DIR=path/to/site" >&2; \
		exit 1; \
	}
	cd "$(WEBSITE_DIR)" && $(WEBSITE_INSTALL_CMD)

.PHONY: website-ci-build
website-ci-build: ## Install website dependencies and build, suitable for CI/CD
	$(MAKE) website-deps
	$(MAKE) website-build

.PHONY: website-serve
website-serve: ## Build and serve the website locally
	$(MAKE) website-build
	cd "$(WEBSITE_DIR)" && PATH="$$(pwd)/node_modules/.bin:$$PATH" $(WEBSITE_SERVE_CMD)

##@ Test

.PHONY: test
test: ## Run the full test suite (including the golden corpus)
	$(GO) test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	$(GO) test -race ./...

.PHONY: cover
cover: ## Run tests and write a coverage profile
	$(GO) test -coverprofile=$(COVERAGE) -covermode=atomic ./...
	$(GO) tool cover -func=$(COVERAGE) | tail -n1

.PHONY: cover-html
cover-html: cover ## Open the HTML coverage report
	$(GO) tool cover -html=$(COVERAGE)

.PHONY: golden
golden: ## Run only the golden-output tests in verify mode
	$(GO) test ./... -run Golden

.PHONY: golden-update
golden-update: ## Regenerate golden fixtures (inspect the diff before committing)
	AJQ_UPDATE_GOLDEN=1 $(GO) test ./... -run Golden

.PHONY: agent-routing-eval
agent-routing-eval: ## Validate and score the hermetic blind-agent routing corpus
	$(GO) test ./internal/testharness -run TestAgentRouting
	$(GO) run ./cmd/agent-routing-eval \
		-corpus testdata/agent-routing/v1/corpus.json \
		-responses testdata/agent-routing/v1/responses/scorer-fixture-local-guidance.json

.PHONY: differential
differential: ## Run the live jq differential tests (skips cleanly if jq is not installed)
	$(GO) test ./internal/cli -run TestPureJQLiveDifferentialAgainstJQ -v

.PHONY: bench
bench: ## Run benchmarks
	$(GO) test -run '^$$' -bench=. -benchmem ./...

.PHONY: bench-phase2
bench-phase2: ## Run the Phase 2.5 fake-mode bench harness (deterministic, CI-safe)
	$(GO) test -run '^$$' -bench=. -benchmem -benchtime=200ms ./internal/bench/...

.PHONY: bench-phase2-real
bench-phase2-real: ## Run the Phase 2.5 real local-inference bench (needs provisioned llama-server + model)
	AJQ_BENCH_REAL=1 $(GO) test -v -run TestRealBench -timeout 5m ./internal/bench/...

##@ Quality

.PHONY: fmt
fmt: ## Format all Go source with gofmt
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (install: https://golangci-lint.run)
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { \
		echo "golangci-lint not found; install from https://golangci-lint.run" >&2; exit 1; }
	$(GOLANGCI_LINT) run ./...

.PHONY: tidy
tidy: ## Tidy and verify go.mod / go.sum
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: check
check: tidy vet lint test ## Run the full pre-commit gate (tidy, vet, lint, test)

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artifacts and coverage output
	$(GO) clean
	rm -rf $(BIN_DIR) $(DIST_DIR) $(COVERAGE)

.PHONY: version
version: ## Print the version that would be stamped into the binary
	@echo $(VERSION)
