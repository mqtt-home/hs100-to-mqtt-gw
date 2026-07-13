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

.PHONY: build test lint run docker clean deps version vet release-check release-snapshot release-next release

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

release-check:
	@goreleaser check

release-snapshot:
	@echo "Building local snapshot (no publish)..."
	@goreleaser release --snapshot --clean --skip=publish

# svu computes the next semver from Conventional Commits since the latest tag:
#   feat: -> minor, fix: -> patch, feat!: / BREAKING CHANGE -> major.
# Install: go install github.com/caarlos0/svu/v3@latest
SVU ?= svu

release-next:
	@command -v $(SVU) >/dev/null 2>&1 || { echo "svu not installed: go install github.com/caarlos0/svu/v3@latest"; exit 1; }
	@echo "Current: $$($(SVU) current 2>/dev/null || echo none)"
	@echo "Next:    $$($(SVU) next)"

# Trigger a release by tagging HEAD and pushing; the GitHub Actions Release
# workflow then runs goreleaser. Version is derived from Conventional Commits
# via svu; override with RELEASE_VERSION=vX.Y.Z if needed.
release:
	@command -v $(SVU) >/dev/null 2>&1 || { echo "svu not installed: go install github.com/caarlos0/svu/v3@latest"; exit 1; }
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Working tree is dirty; commit or stash before releasing."; exit 1; \
	fi
	@version="$(RELEASE_VERSION)"; \
	if [ -z "$$version" ]; then version="$$($(SVU) next)"; fi; \
	case "$$version" in v[0-9]*.[0-9]*.[0-9]*) ;; \
		*) echo "Version must match vX.Y.Z, got '$$version'"; exit 1;; \
	esac; \
	if git rev-parse "$$version" >/dev/null 2>&1; then \
		echo "Tag $$version already exists (no releasable commits since last tag?)"; exit 1; \
	fi; \
	echo "Tagging $$version and pushing..."; \
	git tag -a "$$version" -m "Release $$version" && \
	git push origin "$$version"
