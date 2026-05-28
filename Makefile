.PHONY: help dev dev-api dev-web build build-api build-web migrate sqlc tidy fmt test \
        docker docker-up docker-down docker-logs \
        docker-builder docker-load docker-push

IMAGE     ?= hemanthhku/wealthfolio
PLATFORMS ?= linux/amd64,linux/arm64,linux/arm/v7

# Auto-derived from git — override with TAG=v1.2.3 for a named release
GIT_SHA   := $(shell git rev-parse --short HEAD)
GIT_TAG   := $(shell git describe --tags --exact-match 2>/dev/null)
# If HEAD is on a semver tag use it, otherwise fall back to the SHA
VERSION   := $(if $(GIT_TAG),$(GIT_TAG),$(GIT_SHA))

help:
	@echo "Targets:"
	@echo "  dev            Run API + web dev servers in parallel"
	@echo "  dev-api        Run Go API on :8000"
	@echo "  dev-web        Run Vite dev server on :5173 (proxies /api -> :8000)"
	@echo "  build          Build production binary with embedded frontend"
	@echo "  build-web      Build frontend bundle into internal/web/dist"
	@echo "  build-api      Build Go binary (assumes web is already built)"
	@echo "  migrate        Run pending migrations (uses DATABASE_URL)"
	@echo "  sqlc           Regenerate typed DB code from internal/db/queries/*.sql"
	@echo "  tidy           go mod tidy"
	@echo "  fmt            gofmt + pnpm format"
	@echo "  test           go test ./..."
	@echo "  docker         docker compose build (host platform only)"
	@echo "  docker-up      docker compose up -d"
	@echo "  docker-down    docker compose down"
	@echo "  docker-logs    docker compose logs -f app"
	@echo "  docker-builder Create/activate the multi-platform buildx builder"
	@echo "  docker-load    Build for current host and load into Docker daemon (IMAGE=)"
	@echo "  docker-push    Build all platforms, tag :sha :version :latest, push (IMAGE=)"
	@echo "  docker-tag     Show what tags will be applied on next push"

dev-api:
	go run ./cmd/server

dev-web:
	cd web && pnpm dev

dev:
	@trap 'kill 0' INT; \
	$(MAKE) -j2 dev-api dev-web

build-web:
	cd web && pnpm install --frozen-lockfile && pnpm build
	rm -rf internal/web/dist
	cp -r web/dist internal/web/dist

build-api:
	CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/wealthfolio ./cmd/server

build: build-web build-api

migrate:
	go run ./cmd/server -migrate-only

sqlc:
	sqlc generate

tidy:
	go mod tidy

fmt:
	gofmt -w .
	cd web && pnpm format

test:
	go test ./...

docker:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f app

# Creates (or reuses) a buildx builder that can cross-compile for all target platforms.
# Run once per machine; safe to re-run.
docker-builder:
	docker buildx inspect wealthfolio-builder > /dev/null 2>&1 \
	  || docker buildx create --name wealthfolio-builder --driver docker-container --bootstrap
	docker buildx use wealthfolio-builder

# Show what tags will be applied without building anything.
docker-tag:
	@echo "GIT_SHA  : $(IMAGE):$(GIT_SHA)"
	@echo "VERSION  : $(IMAGE):$(VERSION)"
	@echo "latest   : $(IMAGE):latest"

# Build for the current host platform only and load into the local Docker daemon.
# Useful for testing the image locally before a multi-platform push.
docker-load: docker-builder
	docker buildx build \
	  --platform $$(docker version -f '{{.Server.Os}}/{{.Server.Arch}}') \
	  --tag $(IMAGE):$(GIT_SHA) \
	  --tag $(IMAGE):$(VERSION) \
	  --tag $(IMAGE):latest \
	  --load \
	  .

# Build for all supported platforms and push to a registry.
# Tags the image with the git SHA, version (or SHA if no tag), and latest.
# Requires IMAGE to be a fully-qualified name, e.g. IMAGE=ghcr.io/you/wealthfolio
# To cut a named release: git tag v1.2.3 && make docker-push IMAGE=ghcr.io/you/wealthfolio
docker-push: docker-builder
	docker buildx build \
	  --platform $(PLATFORMS) \
	  --tag $(IMAGE):$(GIT_SHA) \
	  --tag $(IMAGE):$(VERSION) \
	  --tag $(IMAGE):latest \
	  --push \
	  .
