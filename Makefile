.PHONY: all build test test-race test-cov test-cov-check test-vrules test-integration test-e2e test-e2e-verbose test-e2e-scenario test-log test-leak scan verify report clean

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCOVER=$(GOCMD) tool cover
GOVET=$(GOCMD) vet
GOFMT=$(GOCMD) fmt

# Coverage threshold
COV_THRESHOLD=73

# Build
all: build test

build:
	$(GOBUILD) ./...

# Basic test
test:
	$(GOTEST) -count=1 ./...

# Race detection
test-race:
	$(GOTEST) -race -count=1 ./...

# Coverage
test-cov:
	$(GOTEST) -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCOVER) -func=coverage.out | tail -1

test-cov-check: test-cov
	@COVERAGE=$$(awk '/total:/ {print $$3}' coverage.out | sed 's/%//'); \
	echo "Coverage: $$COVERAGE% (threshold: $(COV_THRESHOLD)%)"; \
	if [ "$$(echo "$$COVERAGE < $(COV_THRESHOLD)" | bc -l)" -eq 1 ]; then \
		echo "FAIL: coverage below threshold"; exit 1; \
	fi

# V* rules specific tests
test-vrules:
	$(GOTEST) -race -count=1 -run "TestVT_|TestVQ_|TestVS_|TestVC_|TestVH_|TestVP_|TestVE_|TestVG_|TestSCAN_" ./...

# Integration tests (no LLM needed)
test-integration:
	$(GOTEST) -race -count=1 -timeout 120s ./...

# E2E tests (needs mock LLM server)
test-e2e:
	@echo "=== Building E2E test components ==="
	@cd e2e_testing/mock_llm_server && $(GOBUILD) -o /tmp/mock_llm_server_e2e . || (echo "FAIL: mock LLM server build failed"; exit 1)
	@cd e2e_testing/e2e_runner && $(GOBUILD) -o /tmp/e2e_runner . || (echo "FAIL: e2e runner build failed"; exit 1)
	@echo "=== Starting Mock LLM Server ==="
	@/tmp/mock_llm_server_e2e -port 18099 -config $(CURDIR)/e2e_testing/mock_llm_server/scenarios.json &
	@sleep 1
	@echo "=== Running E2E scenarios (S-01 through S-15) ==="
	@cd e2e_testing/e2e_runner && /tmp/e2e_runner run -llm-port 18099 -timeout 30s || (pkill -f mock_llm_server_e2e || true; echo "FAIL: E2E tests failed"; exit 1)
	@pkill -f mock_llm_server_e2e || true
	@echo "=== E2E tests complete ==="

# E2E tests with verbose output
test-e2e-verbose:
	@cd e2e_testing/mock_llm_server && $(GOBUILD) -o /tmp/mock_llm_server_e2e .
	@cd e2e_testing/e2e_runner && $(GOBUILD) -o /tmp/e2e_runner .
	@/tmp/mock_llm_server_e2e -port 18099 -config $(CURDIR)/e2e_testing/mock_llm_server/scenarios.json -verbose &
	@sleep 1
	@cd e2e_testing/e2e_runner && /tmp/e2e_runner run -llm-port 18099 -timeout 30s -verbose; EXIT=$$?; pkill -f mock_llm_server_e2e || true; exit $$EXIT

# E2E test for a single scenario (usage: make test-e2e-scenario SCENARIO=S-01)
test-e2e-scenario:
	@cd e2e_testing/mock_llm_server && $(GOBUILD) -o /tmp/mock_llm_server_e2e .
	@cd e2e_testing/e2e_runner && $(GOBUILD) -o /tmp/e2e_runner .
	@/tmp/mock_llm_server_e2e -port 18099 -config $(CURDIR)/e2e_testing/mock_llm_server/scenarios.json &
	@sleep 1
	@cd e2e_testing/e2e_runner && /tmp/e2e_runner run -llm-port 18099 -timeout 30s -scenario $(SCENARIO) -verbose; EXIT=$$?; pkill -f mock_llm_server_e2e || true; exit $$EXIT

# Log integrity tests
test-log:
	$(GOTEST) -race -count=1 -run "TestLog" ./memory/log/...

# Goroutine leak detection
test-leak:
	$(GOTEST) -race -count=1 -run "TestInterrupt|TestClose" ./agent/loop/...

# AST scanning
scan:
	cd verify/cmd/scanner && $(GOCMD) run . -dir .. -format text

# Full verification pipeline
verify: build test-race test-log test-leak test-vrules scan test-e2e
	@echo "=== Verification complete ==="

# Generate reports
report:
	cd verify/cmd/scanner && $(GOCMD) run . -dir .. -format json > ../verify-scan.json
	$(GOTEST) -json -race -count=1 ./... > verify-test.json 2>/dev/null
	@echo "Reports generated: verify-scan.json, verify-test.json"

# Clean
clean:
	rm -f coverage.out verify-scan.json verify-test.json
	pkill -f mock_llm_server_e2e || true
	pkill -f mock_llm_server || true
