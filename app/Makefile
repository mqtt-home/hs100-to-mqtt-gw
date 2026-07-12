GO = go
GOFLAGS = -v

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

MODULE = github.com/mqtt-home/hs100-to-mqtt-gw
LDFLAGS = -ldflags "-s -w -X '$(MODULE)/version.Version=$(VERSION)' -X '$(MODULE)/version.GitCommit=$(GIT_COMMIT)' -X '$(MODULE)/version.BuildTime=$(BUILD_TIME)'"

BINARY_NAME = hs2mqtt
CONFIG ?= $(PWD)/../production/config/config-example.json

DOCKER_IMAGE_NAME = ghcr.io/mqtt-home/hs100-to-mqtt-gw:dev

.PHONY: build test lint run docker clean deps version vet

build:
	@echo "Building $(BINARY_NAME)..."
	@$(GO) build $(GOFLAGS) $(LDFLAGS) -trimpath -o $(BINARY_NAME) .

test:
	@echo "Running tests..."
	@$(GO) test ./... -race -cover

vet:
	@$(GO) vet ./...

lint: vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "Running golangci-lint..."; \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping (go vet already ran)."; \
	fi

run: build
	@echo "Running $(BINARY_NAME) with $(CONFIG)..."
	@$(GO) run . $(CONFIG)

docker:
	@echo "Building Docker image (amd64)..."
	@docker buildx build --platform=linux/amd64 --load -f Dockerfile.goreleaser -t $(DOCKER_IMAGE_NAME) . \
		|| docker build -f Dockerfile.goreleaser -t $(DOCKER_IMAGE_NAME) .

clean:
	@rm -f $(BINARY_NAME)

deps:
	@$(GO) mod download
	@$(GO) mod tidy

version:
	@echo "Version:    $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
