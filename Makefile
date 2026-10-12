SHELL := /bin/sh

BINARY := astrotui
BIN_DIR := bin
GOFLAGS ?=
LDFLAGS := -s -w

.PHONY: all build test lint clean envtest e2e e2e-actions e2e-logs bench-palette perf coverage-nfr nfr-live release-snapshot test-llm-real

all: lint test build

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) ./cmd/astrotui
	@if [ "$$(uname -s)" = "Darwin" ]; then stat -f '%z bytes' $(BIN_DIR)/$(BINARY); else stat -c '%s bytes' $(BIN_DIR)/$(BINARY); fi

test:
	go test -race ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found; running go vet ./..."; \
		go vet ./...; \
	fi

envtest:
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.24.0 use -p path 1.36.x)" go test ./test/envtest -count=1

bench-palette:
	go test -bench=. ./internal/palette

e2e:
	go test -tags=e2e ./test/kind/... ./internal/ui/... -skip TestHeadlessRenderProcessGoldenPath

e2e-actions:
	go test -tags=e2e ./test/kind/... -run Actions

e2e-logs:
	go test -tags=e2e ./test/kind/... -run Logs

perf:
	go test -run TestPerfHarness ./test/kind/...

coverage-nfr:
	@for pkg in ./internal/cluster ./internal/actions ./internal/logs; do \
		out=$$(go test -cover "$$pkg") || { echo "$$out"; exit 1; }; \
		echo "$$out"; \
		pct=$$(echo "$$out" | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p'); \
		awk -v p="$$pct" 'BEGIN { if (p < 70) exit 1 }' || { echo "$$pkg coverage $$pct% is below 70%"; exit 1; }; \
	done

nfr-live:
	ASTROTUI_E2E=1 ASTROTUI_E2E_PROCESS_IDLE=5m go test -tags=e2e ./test/kind/... -run TestHeadlessRenderProcessGoldenPath -count=1 -timeout=8m

release-snapshot:
	goreleaser release --snapshot --clean --skip=sign,publish,docker

test-llm-real:
	ASTROTUI_AI_REAL=1 go test ./internal/ai/...

clean:
	rm -rf $(BIN_DIR) dist
