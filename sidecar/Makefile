# Hygur Sidecar Makefile

BINARY_NAME=hygur
REINDEX_BINARY_NAME=hygur-reindex
PROBE_BINARY_NAME=relevance-probe
BACKFILL_BINARY_NAME=extract-backfill
BUILD_DIR=bin
CMD_DIR=cmd/hygur
REINDEX_CMD_DIR=cmd/hygur-reindex
PROBE_CMD_DIR=cmd/relevance-probe
BACKFILL_CMD_DIR=cmd/extract-backfill

# Install target — must match what the macOS SidecarSupervisor expects.
INSTALL_DIR=$(HOME)/.hygur/bin

# CGO is required for the sqlite3 driver.
CGO_ENABLED=1

# Build tags. sqlite_fts5 enables SQLite's FTS5 extension in mattn/go-sqlite3,
# which powers the lexical/BM25 half of the hybrid retriever. Without it,
# `CREATE VIRTUAL TABLE ... USING fts5` fails at RUNTIME with "no such module:
# fts5" — the build stays green, so this tag must be on every build path.
GO_TAGS := sqlite_fts5

# Version information injected at build time
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "\
	-X github.com/hygur/sidecar/internal/version.Version=$(VERSION) \
	-X github.com/hygur/sidecar/internal/version.Commit=$(COMMIT) \
	-X github.com/hygur/sidecar/internal/version.BuildTime=$(BUILD_TIME)"

# Common Go build invocation — used by every target that compiles Go.
# Stays in sync with `make run` so all binaries are built identically.
GO_BUILD = CGO_ENABLED=$(CGO_ENABLED) go build -tags '$(GO_TAGS)' $(LDFLAGS)

.PHONY: all build build-sidecar build-reindex build-probe build-backfill build-arm64 build-amd64 build-universal build-for-bundle run reindex probe backfill install uninstall test lint clean fmt vet check

all: build

# Build everything — sidecar, the reindex CLI, the relevance probe, and the Tier 2 backfill CLI.
build: build-sidecar build-reindex build-probe build-backfill

# Build the main sidecar binary with version info.
build-sidecar:
	@mkdir -p $(BUILD_DIR)
	$(GO_BUILD) -o $(BUILD_DIR)/$(BINARY_NAME) ./$(CMD_DIR)

# Build arm64 slice (native on Apple Silicon).
build-arm64:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=darwin GOARCH=arm64 \
		go build -tags '$(GO_TAGS)' $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-arm64 ./$(CMD_DIR)

# Build amd64 slice (cross-compiled via clang -arch x86_64 on Apple Silicon).
build-amd64:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=darwin GOARCH=amd64 \
		CC="clang -arch x86_64" \
		go build -tags '$(GO_TAGS)' $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-amd64 ./$(CMD_DIR)

# Combine arm64 + amd64 into a universal (fat) binary using lipo.
build-universal: build-arm64 build-amd64
	lipo -create -output $(BUILD_DIR)/$(BINARY_NAME)-universal \
		$(BUILD_DIR)/$(BINARY_NAME)-arm64 \
		$(BUILD_DIR)/$(BINARY_NAME)-amd64

# Build the arm64 sidecar slice and copy it into the macOS app bundle.
# Called by the Xcode pre-build script in macos-app/Project.yml and by the
# release workflow.
#
# Apple Silicon only — Hygur ships local LLM inference via MLX (and friends),
# which has no path on Intel Macs. Universal binaries doubled the bundle size
# for an audience we can't actually serve.
build-for-bundle: build-arm64
	@DEST="$$(cd ..; pwd)/macos-app/Resources/hygur-sidecar"; \
	mkdir -p "$$(dirname $$DEST)"; \
	cp $(BUILD_DIR)/$(BINARY_NAME)-arm64 "$$DEST"; \
	echo "✅ arm64 sidecar → $$DEST"

# Build the reindex CLI (Tier 1 entity backfill).
build-reindex:
	@mkdir -p $(BUILD_DIR)
	$(GO_BUILD) -o $(BUILD_DIR)/$(REINDEX_BINARY_NAME) ./$(REINDEX_CMD_DIR)

# Build the relevance probe CLI (offline retrieval-strategy comparison).
build-probe:
	@mkdir -p $(BUILD_DIR)
	$(GO_BUILD) -o $(BUILD_DIR)/$(PROBE_BINARY_NAME) ./$(PROBE_CMD_DIR)

# Build the Tier 1+2 extraction backfill CLI.
build-backfill:
	@mkdir -p $(BUILD_DIR)
	$(GO_BUILD) -o $(BUILD_DIR)/$(BACKFILL_BINARY_NAME) ./$(BACKFILL_CMD_DIR)

# Build the sidecar then run it from ./bin.
# On macOS, HYGUR_DATA_DIR is set to the canonical App Support location so that
# `make dev` and the bundled app share the same database and config file.
DATA_DIR_DARWIN := $(HOME)/Library/Application Support/Hygur
run: build-sidecar
	HYGUR_DATA_DIR="$(DATA_DIR_DARWIN)" ./$(BUILD_DIR)/$(BINARY_NAME)

# Build the reindex CLI then run it (Tier 1 entity backfill on an existing DB).
reindex: build-reindex
	HYGUR_DATA_DIR="$(DATA_DIR_DARWIN)" ./$(BUILD_DIR)/$(REINDEX_BINARY_NAME)

# Build the probe and run it (forwards extra args, e.g.: make probe ARGS='-q "..."').
probe: build-probe
	HYGUR_DATA_DIR="$(DATA_DIR_DARWIN)" ./$(BUILD_DIR)/$(PROBE_BINARY_NAME) $(ARGS)

# Build the backfill CLI and run it (forwards extra args, e.g.: make backfill ARGS='--dry-run').
backfill: build-backfill
	HYGUR_DATA_DIR="$(DATA_DIR_DARWIN)" ./$(BUILD_DIR)/$(BACKFILL_BINARY_NAME) $(ARGS)

# Install both binaries to ~/.hygur/bin so the macOS app's SidecarSupervisor
# can launch the sidecar as a child process and so `hygur-reindex` is on PATH
# for one-off CLI use.
install: build
	@mkdir -p $(INSTALL_DIR)
	cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	cp $(BUILD_DIR)/$(REINDEX_BINARY_NAME) $(INSTALL_DIR)/$(REINDEX_BINARY_NAME)
	@echo "Installed:"
	@echo "  $(INSTALL_DIR)/$(BINARY_NAME)"
	@echo "  $(INSTALL_DIR)/$(REINDEX_BINARY_NAME)"

# Remove installed binaries.
uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(REINDEX_BINARY_NAME)

# Run the full test suite.
test:
	CGO_ENABLED=$(CGO_ENABLED) go test -tags '$(GO_TAGS)' -race ./...

# Run golangci-lint (if installed).
lint:
	@which golangci-lint > /dev/null 2>&1 || (echo "golangci-lint not installed, skipping" && exit 0)
	golangci-lint run ./...

# Format code.
fmt:
	go fmt ./...

# Run go vet.
vet:
	go vet -tags '$(GO_TAGS)' ./...

# Clean build artifacts.
clean:
	rm -rf $(BUILD_DIR)
	go clean

# Run all checks.
check: fmt vet lint test
