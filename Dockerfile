# syntax=docker/dockerfile:1.4

# ============================================
# Stage 1: Build
# ============================================
FROM golang:1.26-alpine AS build

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

# Download dependencies with cache
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source
COPY . .

# Build static binary (CGO_ENABLED=0 — uses wazero for sqlite)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o btts .

# ============================================
# Stage 2: Runtime
# ============================================
FROM alpine:3.21 AS runtime

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata

# Create data directory for SQLite DB + Telegram session
RUN mkdir -p /app/data

# Create non-root user
RUN adduser -D -u 1001 appuser && chown -R appuser:appuser /app
USER 1001

COPY --from=build /app/btts .

# REST API port (enabled via BTTS_API_ENABLE=true)
EXPOSE 39415

CMD ["./btts"]
