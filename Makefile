SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOLANGCI_LINT_VERSION := $(shell cat .golangci-version)
COVERAGE_MIN ?= 95
COVER_DIR := cover
PKGS := ./...
# Unit tests cover both modules. Coverage includes library packages exercised
# by server scenarios; individual specialized targets select their relevant packages.
SERVER_DIR := server
LIB_MOD := github.com/deploymenttheory/go-apple-dm
SRV_MOD := github.com/deploymenttheory/go-apple-dm/server
ALL_PKGS := $(LIB_MOD)/...,$(SRV_MOD)/...
INTEGRATION_PKGS := ./apppush/... ./statestore/... ./sqlstore/... ./ddmstore/... ./blueprints/... ./depstore/... ./inventorystore/... ./acmestore/... ./adminauth/... ./audit/... ./eventstore/... ./webhook/... ./maintenance/... ./recovery/... ./internal/app/...
E2E_STORE ?= sqlite
# The embedded catalogue uses SQLite regardless of E2E_STORE; run it once.
E2E_PKGS := ./e2e/...
ifeq ($(E2E_STORE),sqlite)
E2E_PKGS += ./acceptance/...
endif
FUZZ_SMOKE_TIME ?= 20s
FUZZ_TIME ?= 10m
SCHEMA_DIR := third_party/apple-device-management/current

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'

## tools: install developer tools, built with the Go version go.mod declares so they can load this module
GO_VERSION := $(shell sed -n 's/^go \(.*\)$$/\1/p' go.mod)
tools:
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install gotest.tools/gotestsum@latest
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install mvdan.cc/gofumpt@latest
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	GOTOOLCHAIN=go$(GO_VERSION) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

## submodule: initialise the pinned Apple schema sources
submodule:
	git submodule update --init --recursive

## generate: regenerate schema packages from the vendored Apple YAML
generate: submodule
	$(GO) generate ./...
	$(GO) run ./cmd/schemagen generate

## verify: fail if regeneration changes anything or removes an exported identifier
verify: submodule
	$(GO) test ./internal/layout -run TestWorkflowSecurity
	python3 -B -m unittest discover -s .github/scripts -p '*_test.py'
	python3 -B -m unittest discover -s scripts -p 'device_management_schema_contracts_test.py'
	$(GO) run ./cmd/schemagen verify

## lint: compile and lint both workspace modules, including tagged tests, without rewriting
lint:
	python3 scripts/lint.py --go "$(GO)" --linter "$(GOLANGCI_LINT)"

## fmt: explicitly format authored Go files in both modules
fmt:
	python3 scripts/lint.py --format --go "$(GO)" --linter "$(GOLANGCI_LINT)"

.PHONY: fmt

## docs-check: check authored function comments, documentation links, diagram evidence and route permissions
docs-check:
	$(GO) test ./internal/layout -run TestDocumentation -count=1
	python3 -B -m unittest discover -s scripts -p check_docs_test.py
	python3 -B scripts/check-docs.py
	cd $(SERVER_DIR) && $(GO) test ./internal/app -run TestDocumentation -count=1

.PHONY: docs-check

## verify-server-module-installation: resolve dependencies, build/install with GOWORK=off and run installed-server acceptance
verify-server-module-installation:
	python3 scripts/verify-server-module-installation.py

## test: unit tests with race detector, coverage written to cover/unit
test:
	@rm -rf $(COVER_DIR)/unit && mkdir -p $(COVER_DIR)/unit
	$(GO) test -race -shuffle=on -count=1 -cover -coverpkg=$(LIB_MOD)/... $(PKGS) -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/unit
	cd $(SERVER_DIR) && $(GO) test -race -shuffle=on -count=1 -cover -coverpkg=$(ALL_PKGS) ./... -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/unit
	$(MAKE) test-schema-contracts

## test-schema-contracts: require passing evidence for every published OS 27 contract
test-schema-contracts:
	python3 scripts/device-management-schema-contracts.py --output $(COVER_DIR)/schema-contracts --coverage-dir $(COVER_DIR)/unit

.PHONY: test-schema-contracts

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

## test-quickstart: validate onboarding examples and isolated Compose startup, admin handoff and persistence (needs Docker)
test-quickstart:
	$(GO) build -o cover/quickstart/bin/dmctl ./server/cmd/dmctl
	DM_QUICKSTART_DMCTL="$(PWD)/cover/quickstart/bin/dmctl" python3 -m unittest discover -s deploy/quickstart -v
	python3 scripts/check-onboarding.py
	python3 scripts/quickstart-smoke.py

.PHONY: test-quickstart

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
ci: lint verify docs-check verify-server-module-installation test test-storage test-storage-perf test-e2e test-acceptance lab-docs-check fuzz-smoke coverage

## clean: remove coverage output
clean:
	rm -rf $(COVER_DIR)

.PHONY: help tools submodule generate verify verify-server-module-installation lint test test-storage test-storage-perf test-conformance test-e2e testdb-up testdb-down docker-build fuzz-smoke fuzz coverage vuln refs refs-activity ci clean

# Lab recipes delegate to dmctl; Go owns workspace and module behavior.
LAB_WORKSPACE ?= test-lab/local
LAB_MODE ?= simulated
LAB_STORAGE ?= sqlite
LAB_LISTEN ?= 127.0.0.1:8443
LAB_ADAPTER ?= process
LAB_HOSTS ?=
GUESTWEAVE_REPO ?= https://github.com/deploymenttheory/guestweave-cli-macos.git
GUESTWEAVE_REF ?=
GUESTWEAVE_DIR := test-lab/local/tools/guestweave
LAB_MODULES ?= all
LAB_DEVICE_ID ?=
LAB_USER_ID ?=
LAB_ATTACH_URL ?=
LAB_IDENTITY ?= acme
LAB_DESTRUCTIVE ?=
LAB_RUN ?=
LAB_PROFILE_FILE ?= $(LAB_WORKSPACE)/enrollment.mobileconfig
LAB_TRUST_FILE ?= $(LAB_WORKSPACE)/trust.mobileconfig
LAB_REPORT_DIR ?= cover/acceptance
LAB_REVISION := $(shell git describe --always --dirty)
LAB_BIN_DIR := test-lab/local/bin

## lab-build: build dmserver and dmctl for lab and acceptance runs
lab-build:
	@mkdir -p "$(LAB_BIN_DIR)"
	$(GO) build -o "$(LAB_BIN_DIR)/dmserver" ./server/cmd/dmserver
	$(GO) build -o "$(LAB_BIN_DIR)/dmctl" ./server/cmd/dmctl

## lab-init: initialize LAB_WORKSPACE (LAB_MODE, LAB_STORAGE, LAB_LISTEN, LAB_ADAPTER, LAB_HOSTS)
lab-init: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab init -workspace "$(LAB_WORKSPACE)" -mode "$(LAB_MODE)" -storage "$(LAB_STORAGE)" -listen "$(LAB_LISTEN)" -adapter "$(LAB_ADAPTER)" -hosts "$(LAB_HOSTS)"

## lab-tls: reissue the workspace HTTPS leaf for LAB_HOSTS, keeping the lab CA
lab-tls: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab tls -workspace "$(LAB_WORKSPACE)" -hosts "$(LAB_HOSTS)"

## lab-tools: build and verify the guestweave CLI used to drive virtual Macs
lab-tools:
	@scripts/guestweave.sh "$(GUESTWEAVE_DIR)" "$(GUESTWEAVE_REPO)" "$(GUESTWEAVE_REF)"

## lab-doctor: inspect workspace and live prerequisites without changing device state
lab-doctor: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab doctor -workspace "$(LAB_WORKSPACE)"

## lab-up: supervise dmserver and fixtures in the foreground; use another terminal for lab-run
lab-up: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab up -workspace "$(LAB_WORKSPACE)" -dmserver "$(LAB_BIN_DIR)/dmserver"

## lab-down: stop the workspace supervisor and drain its server processes
lab-down:
	"$(LAB_BIN_DIR)/dmctl" lab down -workspace "$(LAB_WORKSPACE)"

## lab-list: list stable module IDs, lifecycle stages, modes and retained regressions
lab-list: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab list

## lab-run: run LAB_MODULES against the workspace; LAB_DEVICE_ID selects a live MDM device
lab-run: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab run -workspace "$(LAB_WORKSPACE)" -modules "$(LAB_MODULES)" -device-id "$(LAB_DEVICE_ID)" -user-id "$(LAB_USER_ID)" -attach-url "$(LAB_ATTACH_URL)" -revision "$(LAB_REVISION)" $(if $(LAB_DESTRUCTIVE),-destructive,)

## lab-report: rerender report.html from an existing run directory (LAB_RUN)
lab-report: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab report -run "$(LAB_RUN)"

## lab-status: query the workspace supervisor
lab-status:
	"$(LAB_BIN_DIR)/dmctl" lab status -workspace "$(LAB_WORKSPACE)"

.PHONY: lab-preflight lab-trust lab-profile lab-replace lab-report lab-tls lab-tools
## lab-preflight: check enrollment credentials, HTTPS trust and identity method
lab-preflight: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab preflight -workspace "$(LAB_WORKSPACE)" -identity "$(LAB_IDENTITY)"

## lab-trust: export the local HTTPS trust profile before enrollment
lab-trust: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab trust -workspace "$(LAB_WORKSPACE)" -file "$(LAB_TRUST_FILE)"

## lab-profile: export an ACME or SCEP enrollment profile for LAB_DEVICE_ID
lab-profile: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab profile -workspace "$(LAB_WORKSPACE)" -device-id "$(LAB_DEVICE_ID)" -identity "$(LAB_IDENTITY)" -attach-url "$(LAB_ATTACH_URL)" -file "$(LAB_PROFILE_FILE)"

## lab-replace: start an authorized profile replacement and wake the enrolled device
lab-replace: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab replace -workspace "$(LAB_WORKSPACE)" -device-id "$(LAB_DEVICE_ID)" -identity "$(LAB_IDENTITY)" -attach-url "$(LAB_ATTACH_URL)"

## test-acceptance: shared modules against built dmserver processes, using unified device management
# Absolute paths survive go test's package working directory.
test-acceptance: lab-build
	LAB_DMSERVER="$(abspath $(LAB_BIN_DIR))/dmserver" LAB_DMCTL="$(abspath $(LAB_BIN_DIR))/dmctl" LAB_REPORT_DIR="$(abspath $(LAB_REPORT_DIR))" LAB_REVISION="$(LAB_REVISION)" $(GO) test -race -count=1 -timeout 300s -tags acceptance ./server/acceptance/...

## lab-docs: regenerate the catalogue from executable module metadata
lab-docs: lab-build
	"$(LAB_BIN_DIR)/dmctl" lab list -format markdown > docs/testing/lab-catalogue.md

## lab-docs-check: verify the documented module catalogue matches the implementation
lab-docs-check: lab-build
	@tmp=$$(mktemp); "$(LAB_BIN_DIR)/dmctl" lab list -format markdown > "$$tmp" && diff -u docs/testing/lab-catalogue.md "$$tmp"; status=$$?; rm -f "$$tmp"; exit $$status

.PHONY: test-contract test-acceptance lab-tls lab-tools lab-build lab-init lab-doctor lab-up lab-down lab-list lab-run lab-status lab-docs lab-docs-check
