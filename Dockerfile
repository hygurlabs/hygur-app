# Hygur Server — headless Linux image.
#
# Multi-stage: (1) build the React WebUI, (2) compile the Go server with CGO
# (sqlite_fts5 needs the C SQLite driver + the FTS5 module), (3) slim runtime.
# The WebUI dist/ is generated (not committed) and embedded via go:embed, so it
# must exist before the Go build — stage 1 produces it, stage 2 copies it in.
#
# Build context = repo root.  Build:  docker build -t hygur-server .
# Run:  docker run -p 8420:8420 -v hygur-data:/data hygur-server

# ── Stage 1: WebUI (Vite + React + TS) ────────────────────────────────────────
FROM node:22-bookworm-slim AS webui
WORKDIR /src
COPY webui ./webui
# vite's outDir is ../sidecar/internal/api/webui/dist (relative to webui/), so
# the sibling path must exist before the build writes into it.
RUN mkdir -p sidecar/internal/api/webui
WORKDIR /src/webui
RUN npm ci && npm run build
# → /src/sidecar/internal/api/webui/dist

# ── Stage 2: Go server (CGO + sqlite_fts5) ────────────────────────────────────
FROM golang:1.26-bookworm AS build
WORKDIR /src/sidecar
# Cache module downloads on go.mod/go.sum changes only.
COPY sidecar/go.mod sidecar/go.sum ./
RUN go mod download
COPY sidecar ./
# Bring in the freshly built SPA so go:embed has something to embed.
COPY --from=webui /src/sidecar/internal/api/webui/dist ./internal/api/webui/dist
ARG VERSION=docker
RUN CGO_ENABLED=1 go build -tags 'sqlite_fts5 sqlite_json1' \
    -ldflags "-X github.com/hygur/sidecar/internal/version.Version=${VERSION}" \
    -o /out/hygur-server ./cmd/hygur

# ── Stage 3: runtime ──────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS runtime
# poppler-utils provides pdftotext (the robust PRIMARY PDF text extractor) and
# pdftoppm (page rasteriser for the OCR fallback). Without it the server silently
# degrades to the pure-Go ledongthuc extractor, which emits spaced-glyph garbage
# on some PDFs (the TARA « Contractor Agreement » failure).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates poppler-utils \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/hygur-server /usr/local/bin/hygur-server

# Headless defaults: data on a mounted volume, bind all interfaces (the image is
# meant to sit behind a reverse proxy / on a private network — see docs/deploy).
ENV HYGUR_DATA_DIR=/data \
    HYGUR_SERVER_HOST=0.0.0.0 \
    HYGUR_SERVER_PORT=8420
VOLUME /data
EXPOSE 8420
ENTRYPOINT ["/usr/local/bin/hygur-server"]
