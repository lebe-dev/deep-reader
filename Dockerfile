# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Stage 1 — Frontend build
# ---------------------------------------------------------------------------
FROM node:24-alpine AS frontend

WORKDIR /fe

# Install dependencies first (layer cache)
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

# Build the SvelteKit PWA. Stamp the version from the repo's VERSION file into
# package.json so the frontend ships with the same version as the backend.
COPY frontend/ ./
COPY VERSION ./VERSION
RUN npm pkg set version="$(cat VERSION)"
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2 — Go build
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS backend

WORKDIR /src

# Download dependencies first (layer cache)
COPY go.mod go.sum ./
RUN go mod download

# Copy the full Go module source
COPY cmd/    ./cmd/
COPY internal/ ./internal/
COPY web/    ./web/
COPY VERSION ./VERSION

# Embed the built frontend into web/dist so go:embed picks it up at compile time
COPY --from=frontend /fe/build/ ./web/dist/

# Build a fully-static binary (no CGO — using modernc.org/sqlite which is pure Go).
# Stamp the version from the VERSION file into the binary via -ldflags -X.
RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w -X deep-reader/internal/version.Version=$(cat VERSION)" \
    -o /out/deep-reader ./cmd/server

# ---------------------------------------------------------------------------
# Stage 3 — Minimal runtime image
# ---------------------------------------------------------------------------
FROM alpine:3.24 AS runtime

# ca-certificates: HTTPS calls to LLM providers; tzdata: correct timestamps
RUN apk add --no-cache ca-certificates tzdata wget

# Non-root user for security
RUN addgroup -g 10001 app && adduser -u 10001 -G app -s /sbin/nologin -D app

# Only the binary — the frontend is embedded inside it
COPY --from=backend /out/deep-reader /usr/local/bin/deep-reader

# Persistent data volume mount point
RUN mkdir /data && chown app:app /data
VOLUME ["/data"]

ENV DATABASE_PATH=/data/deep-reader.db
ENV HTTP_PORT=8080

EXPOSE 8080

# wget-based healthcheck; /healthz requires no auth
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:${HTTP_PORT}/healthz || exit 1

USER 10001

ENTRYPOINT ["/usr/local/bin/deep-reader"]
