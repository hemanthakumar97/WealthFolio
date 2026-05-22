.PHONY: help dev dev-api dev-web build build-api build-web migrate sqlc tidy fmt test docker docker-up docker-down docker-logs

help:
	@echo "Targets:"
	@echo "  dev          Run API + web dev servers in parallel"
	@echo "  dev-api      Run Go API on :8000"
	@echo "  dev-web      Run Vite dev server on :5173 (proxies /api -> :8000)"
	@echo "  build        Build production binary with embedded frontend"
	@echo "  build-web    Build frontend bundle into internal/web/dist"
	@echo "  build-api    Build Go binary (assumes web is already built)"
	@echo "  migrate      Run pending migrations (uses DATABASE_URL)"
	@echo "  sqlc         Regenerate typed DB code from internal/db/queries/*.sql"
	@echo "  tidy         go mod tidy"
	@echo "  fmt          gofmt + pnpm format"
	@echo "  test         go test ./..."
	@echo "  docker       docker compose build"
	@echo "  docker-up    docker compose up -d"
	@echo "  docker-down  docker compose down"
	@echo "  docker-logs  docker compose logs -f app"

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
