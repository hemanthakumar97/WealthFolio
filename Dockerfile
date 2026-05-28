# syntax=docker/dockerfile:1.7

############################
# 1. Frontend build
############################
FROM --platform=$BUILDPLATFORM node:22-alpine AS web

WORKDIR /web
RUN corepack enable

COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build


############################
# 2. Backend build (embeds SPA via go:embed)
############################
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS api

ARG TARGETOS=linux
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Place the freshly built SPA where //go:embed expects it
RUN rm -rf internal/web/dist
COPY --from=web /web/dist ./internal/web/dist

# GOARM is only relevant for arm/v6 and arm/v7; strip the leading 'v'
RUN set -eux; \
    GOARM=""; \
    if [ "${TARGETARCH}" = "arm" ] && [ -n "${TARGETVARIANT}" ]; then \
      GOARM="${TARGETVARIANT#v}"; \
    fi; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${GOARM} \
    go build -trimpath -ldflags="-w -s" -o /out/wealthfolio ./cmd/server


############################
# 3. Runtime — distroless, ~3MB base
############################
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=api /out/wealthfolio /wealthfolio

EXPOSE 8000
USER nonroot:nonroot
ENTRYPOINT ["/wealthfolio"]
