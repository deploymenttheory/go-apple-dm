SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
COVERAGE_MIN ?= 95
COVER_DIR := cover
PKGS := ./...
INTEGRATION_PKGS := ./storage/... ./ddm/sqlstore/... ./dep/sqlstore/... ./acme/sqlstore/... ./adminauth/sqlstore/... ./audit/sqlstore/... ./internal/app/...
E2E_PKGS := ./e2e/...
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
	$(GO) run ./cmd/admgen generate

## verify: fail if regeneration changes anything or removes an exported identifier
verify: submodule
	@if [ -d cmd/admgen ]; then $(GO) run ./cmd/admgen verify; else echo "cmd/admgen not present yet (phase 1)"; fi

## lint: run golangci-lint with the repository configuration
lint:
	golangci-lint run --config=.golangci.yml ./...

## test: unit tests with race detector, coverage written to cover/unit
test:
	@rm -rf $(COVER_DIR)/unit && mkdir -p $(COVER_DIR)/unit
	$(GO) test -race -shuffle=on -count=1 -cover -coverpkg=$(PKGS) $(PKGS) -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/unit

## test-storage: storage contract suites against SQL backends (needs TEST_POSTGRES_DSN / TEST_MYSQL_DSN; `make testdb-up` starts both in Docker and prints the exports)
test-storage:
	@rm -rf $(COVER_DIR)/storage && mkdir -p $(COVER_DIR)/storage
	@if $(GO) list $(INTEGRATION_PKGS) >/dev/null 2>&1; then \
		$(GO) test -race -count=1 -tags integration -cover -coverpkg=$(PKGS) $(INTEGRATION_PKGS) -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/storage; \
	else echo "no storage packages yet"; fi

## test-storage-perf: the 100k-row Clear timing gate on PostgreSQL, without the race detector (needs TEST_POSTGRES_DSN)
test-storage-perf:
	$(GO) test -count=1 -tags integration -run 'TestClear100kUnderOneSecond' -v ./storage/postgres/

## test-conformance: generated schema conformance tests only
test-conformance:
	@if $(GO) list ./schema/... >/dev/null 2>&1; then \
		$(GO) test -count=1 -run 'Conformance' ./schema/...; \
	else echo "no schema packages yet"; fi

## test-e2e: reference server plus simulator scenarios on E2E_STORE (sqlite, postgres, inmem)
test-e2e: export E2E_STORE := $(E2E_STORE)
test-e2e:
	@rm -rf $(COVER_DIR)/e2e-$(E2E_STORE) && mkdir -p $(COVER_DIR)/e2e-$(E2E_STORE)
	@if $(GO) list $(E2E_PKGS) >/dev/null 2>&1; then \
		$(GO) test -race -count=1 -tags e2e -cover -coverpkg=$(PKGS) $(E2E_PKGS) -args -test.gocoverdir=$(PWD)/$(COVER_DIR)/e2e-$(E2E_STORE); \
	else echo "no e2e packages yet"; fi

## docker-build: build the reference server image from this repository (never pulled from a third party)
docker-build:
	docker build -t go-apple-mdm:test .

## testdb-ddm-up: build the image and run our ddm role in Docker for TestE2E_DDMSplitDeployment; prints the exports
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
ci: lint verify test test-storage test-storage-perf test-e2e fuzz-smoke coverage

## clean: remove coverage output
clean:
	rm -rf $(COVER_DIR)

.PHONY: help tools submodule generate verify lint test test-storage test-storage-perf test-conformance test-e2e testdb-up testdb-down docker-build testdb-ddm-up testdb-ddm-down fuzz-smoke fuzz coverage vuln refs refs-activity ci clean
