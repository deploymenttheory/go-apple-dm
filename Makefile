SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
GOLANGCI_LINT ?= golangci-lint
COVERAGE_MIN ?= 95
COVER_DIR := cover
PKGS := ./...
# Unit tests cover both modules. Coverage includes library packages exercised
# by server scenarios; individual specialized targets select their relevant packages.
SERVER_DIR := server
LIB_MOD := github.com/deploymenttheory/go-apple-dm
SRV_MOD := github.com/deploymenttheory/go-apple-dm/server
ALL_PKGS := $(LIB_MOD)/...,$(SRV_MOD)/...
INTEGRATION_PKGS := ./apppush/... ./statestore/... ./sqlstore/... ./ddmstore/... ./depstore/... ./acmestore/... ./adminauth/... ./audit/... ./internal/app/...
E2E_PKGS := ./e2e/... ./acceptance/...
E2E_STORE ?= sqlite
FUZZ_SMOKE_TIME ?= 20s
FUZZ_TIME ?= 10m
SCHEMA_DIR := third_party/device-management

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'

## tools: install developer tools, built with the Go version go.mod declares so they can load this module
GO_VERSION := $(shell sed -n 's/^go \(.*\)$$/\1/p' go.mod)
tools:
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install gotest.tools/gotestsum@latest
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install mvdan.cc/gofumpt@latest
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

## submodule: initialise the pinned Apple schema submodule
submodule:
	git submodule update --init --recursive

## generate: regenerate schema packages from the vendored Apple YAML
generate: submodule
	$(GO) generate ./...
	$(GO) run ./cmd/schemagen generate

## verify: fail if regeneration changes anything or removes an exported identifier
verify: submodule
	$(GO) test ./internal/layout -run TestWorkflowSecurity
	@if [ -d cmd/schemagen ]; then $(GO) run ./cmd/schemagen verify; else echo "schema generator directory is missing"; fi

## lint: run golangci-lint with the repository configuration
lint:
	$(GOLANGCI_LINT) run --fix=false --config=.golangci.yml ./...
	cd $(SERVER_DIR) && $(GOLANGCI_LINT) run --fix=false --config=../.golangci.yml ./...

## verify-server-module-installation: resolve declared dependencies, build server packages and install dmserver/dmctl with GOWORK=off
verify-server-module-installation:
	python3 scripts/verify-server-module-installation.py

## test: unit tests with race detector, coverage written to cover/unit
test:
	@rm -rf $(COVER_DIR)/unit && mkdir -p $(COVER_DIR)/unit
	$(GO) test -race -shuffle=on -count=1 -cover -coverpkg=$(LIB_MOD)/... $(PKGS) -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/unit
	cd $(SERVER_DIR) && $(GO) test -race -shuffle=on -count=1 -cover -coverpkg=$(ALL_PKGS) ./... -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/unit

## test-storage: storage contract suites against SQL backends (needs TEST_POSTGRES_DSN / TEST_MYSQL_DSN; `make testdb-up` starts both in Docker and prints the exports)
test-storage: test-contract

## test-contract: storage and interface contract suites across configured SQL backends
test-contract:
	$(GO) test -race ./devicemanagement/storage/... ./devicemanagement/state/...
	@rm -rf $(COVER_DIR)/storage && mkdir -p $(COVER_DIR)/storage
	# Integration packages share test databases; serialize their schema resets.
	cd $(SERVER_DIR) && $(GO) test -p 1 -race -count=1 -tags integration -cover -coverpkg=$(ALL_PKGS) $(INTEGRATION_PKGS) -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/storage

## test-storage-perf: the 100k-row Clear timing gate on PostgreSQL, without the race detector (needs TEST_POSTGRES_DSN)
test-storage-perf:
	cd $(SERVER_DIR) && $(GO) test -count=1 -tags integration -run 'TestClear100kUnderOneSecond' -v ./sqlstore/postgres/

## test-conformance: generated schema conformance tests only
test-conformance:
	@if $(GO) list ./devicemanagement/schema/... >/dev/null 2>&1; then \
		$(GO) test -count=1 -run 'Conformance' ./devicemanagement/schema/...; \
	else echo "no schema packages yet"; fi

## test-e2e: reference server plus simulator scenarios on E2E_STORE (sqlite, postgres, inmem)
test-e2e: export E2E_STORE := $(E2E_STORE)
test-e2e:
	@rm -rf $(COVER_DIR)/e2e-$(E2E_STORE) && mkdir -p $(COVER_DIR)/e2e-$(E2E_STORE)
	cd $(SERVER_DIR) && $(GO) test -race -count=1 -tags e2e -cover -coverpkg=$(ALL_PKGS) $(E2E_PKGS) -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/e2e-$(E2E_STORE)

## docker-build: build the reference server image from this repository
docker-build:
	docker build -t go-apple-dm:test .

## testdb-ddm-up: build the image and run the ddm role in Docker for TestE2E_DDMSplitDeployment; prints the exports
testdb-ddm-up:
	scripts/testdb.sh ddm-up

## testdb-ddm-down: stop the ddm role container
testdb-ddm-down:
	scripts/testdb.sh ddm-down

## testdb-up: start PostgreSQL and MySQL in Docker for test-storage and E2E_STORE=postgres test-e2e; prints the exports
testdb-up:
	@scripts/testdb.sh up

## testdb-down: remove the Docker test databases
testdb-down:
	@scripts/testdb.sh down

## fuzz-smoke: run every fuzz target briefly
fuzz-smoke:
	@scripts/fuzz.sh $(FUZZ_SMOKE_TIME)

## fuzz: run every fuzz target for FUZZ_TIME each
fuzz:
	@scripts/fuzz.sh $(FUZZ_TIME)

## coverage: merge profiles and enforce COVERAGE_MIN per package and overall
coverage:
	@COVERAGE_MIN=$(COVERAGE_MIN) scripts/coverage-gate.sh $(COVER_DIR)

## vuln: govulncheck
vuln:
	govulncheck ./...

## refs: clone reference implementations read-only into third_party/refs
refs:
	@scripts/refs.sh

## refs-activity: list reference repos pushed in the last 30 days
refs-activity:
	@scripts/refs-activity.sh

## ci: everything CI runs, in order
ci: lint verify verify-server-module-installation test test-storage test-storage-perf test-e2e test-acceptance bench-docs-check fuzz-smoke coverage

## clean: remove coverage output
clean:
	rm -rf $(COVER_DIR)

.PHONY: help tools submodule generate verify verify-server-module-installation lint test test-storage test-storage-perf test-conformance test-e2e testdb-up testdb-down docker-build testdb-ddm-up testdb-ddm-down fuzz-smoke fuzz coverage vuln refs refs-activity ci clean

# Bench recipes delegate to dmctl; Go owns workspace and scenario behavior.
BENCH_WORKSPACE ?= test-lab/local
BENCH_MODE ?= simulated
BENCH_STORAGE ?= sqlite
BENCH_TOPOLOGY ?= all
BENCH_LISTEN ?= 127.0.0.1:8443
BENCH_SCENARIO ?= all
BENCH_DEVICE_ID ?=
BENCH_IDENTITY ?= acme
BENCH_PROFILE_FILE ?= $(BENCH_WORKSPACE)/enrollment.mobileconfig
BENCH_TRUST_FILE ?= $(BENCH_WORKSPACE)/trust.mobileconfig
BENCH_REPORT_DIR ?= cover/acceptance
BENCH_REVISION := $(shell git describe --always --dirty)
BENCH_BIN_DIR := test-lab/local/bin

## bench-build: build dmserver and dmctl for bench and acceptance runs
bench-build:
	@mkdir -p "$(BENCH_BIN_DIR)"
	$(GO) build -o "$(BENCH_BIN_DIR)/dmserver" ./server/cmd/dmserver
	$(GO) build -o "$(BENCH_BIN_DIR)/dmctl" ./server/cmd/dmctl

## bench-init: initialize BENCH_WORKSPACE (BENCH_MODE, BENCH_STORAGE, BENCH_TOPOLOGY, BENCH_LISTEN)
bench-init: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench init -workspace "$(BENCH_WORKSPACE)" -mode "$(BENCH_MODE)" -storage "$(BENCH_STORAGE)" -topology "$(BENCH_TOPOLOGY)" -listen "$(BENCH_LISTEN)"

## bench-doctor: inspect workspace and live prerequisites without changing device state
bench-doctor: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench doctor -workspace "$(BENCH_WORKSPACE)"

## bench-up: supervise dmserver and fixtures in the foreground; use another terminal for bench-run
bench-up: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench up -workspace "$(BENCH_WORKSPACE)" -dmserver "$(BENCH_BIN_DIR)/dmserver"

## bench-down: stop the workspace supervisor and drain its server processes
bench-down:
	"$(BENCH_BIN_DIR)/dmctl" bench down -workspace "$(BENCH_WORKSPACE)"

## bench-list: list stable scenario IDs, execution modes and retained regressions
bench-list: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench list

## bench-run: run BENCH_SCENARIO against the workspace; BENCH_DEVICE_ID selects a live MDM device
bench-run: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench run -workspace "$(BENCH_WORKSPACE)" -scenario "$(BENCH_SCENARIO)" -device-id "$(BENCH_DEVICE_ID)" -revision "$(BENCH_REVISION)"

## bench-status: query the workspace supervisor
bench-status:
	"$(BENCH_BIN_DIR)/dmctl" bench status -workspace "$(BENCH_WORKSPACE)"

.PHONY: bench-enrollment-preflight bench-trust bench-profile bench-replace
## bench-enrollment-preflight: check enrollment credentials, HTTPS trust and identity method
bench-enrollment-preflight: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench enrollment-preflight -workspace "$(BENCH_WORKSPACE)" -identity "$(BENCH_IDENTITY)"

## bench-trust: export the local HTTPS trust profile before enrollment
bench-trust: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench trust -workspace "$(BENCH_WORKSPACE)" -file "$(BENCH_TRUST_FILE)"

## bench-profile: export an ACME or SCEP enrollment profile for BENCH_DEVICE_ID
bench-profile: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench profile -workspace "$(BENCH_WORKSPACE)" -device-id "$(BENCH_DEVICE_ID)" -identity "$(BENCH_IDENTITY)" -file "$(BENCH_PROFILE_FILE)"

## bench-replace: start an authorized profile replacement and wake the enrolled device
bench-replace: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench replace -workspace "$(BENCH_WORKSPACE)" -device-id "$(BENCH_DEVICE_ID)" -identity "$(BENCH_IDENTITY)"

## test-acceptance: shared scenarios against built dmserver processes, including split topology
# Absolute paths survive go test's package working directory.
test-acceptance: bench-build
	BENCH_DMSERVER="$(abspath $(BENCH_BIN_DIR))/dmserver" BENCH_DMCTL="$(abspath $(BENCH_BIN_DIR))/dmctl" BENCH_REPORT_DIR="$(abspath $(BENCH_REPORT_DIR))" BENCH_REVISION="$(BENCH_REVISION)" $(GO) test -race -count=1 -timeout 300s -tags acceptance ./server/acceptance/...

## bench-docs: regenerate the catalogue from executable scenario metadata
bench-docs: bench-build
	"$(BENCH_BIN_DIR)/dmctl" bench list -format markdown > docs/testing/bench-catalogue.md

## bench-docs-check: verify the documented scenario catalogue matches the implementation
bench-docs-check: bench-build
	@tmp=$$(mktemp); "$(BENCH_BIN_DIR)/dmctl" bench list -format markdown > "$$tmp" && diff -u docs/testing/bench-catalogue.md "$$tmp"; status=$$?; rm -f "$$tmp"; exit $$status

.PHONY: test-contract test-acceptance bench-build bench-init bench-doctor bench-up bench-down bench-list bench-run bench-status bench-docs bench-docs-check
