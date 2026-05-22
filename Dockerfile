# syntax=docker/dockerfile:1.7

############################
# 1. Frontend build
############################
FROM node:22-alpine AS web

WORKDIR /web
RUN corepack enable

COPY web/package.json web/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install

COPY web/ ./
RUN pnpm build


############################
# 2. Backend build (embeds SPA via go:embed)
############################
FROM golang:1.23-alpine AS api

WORKDIR /src
RUN apk add --no-cache git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# Place the freshly built SPA where //go:embed expects it
RUN rm -rf internal/web/dist
COPY --from=web /web/dist ./internal/web/dist

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-w -s" \
    -o /out/wealthfolio ./cmd/server


############################
# 3. Runtime — distroless, ~3MB base
############################
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=api /out/wealthfolio /wealthfolio

EXPOSE 8000
USER nonroot:nonroot
ENTRYPOINT ["/wealthfolio"]
