# Hygur

> Your local digital twin — private memory powered by LLM.

Hygur is a personal AI assistant that runs entirely on your machine. It combines a Go sidecar for LM Studio communication and a native macOS app for the user interface.

## Requirements

- macOS 14.0+ (Sonoma)
- Go 1.21+
- LM Studio running on localhost:1234
- Xcode 15+
- XcodeGen (`brew install xcodegen`)
- create-dmg (`brew install create-dmg`)

## Quick Start

```bash
# Run tests, build sidecar and macOS app
make test

# Start the sidecar in dev mode
make dev

# Build + sign + launch the app directly
make open
```

## Installation

### From DMG (recommended)

1. Download `Hygur-x.y.z.dmg` from [Releases](https://github.com/hygurlabs/hygur-app/releases)
2. Drag `Hygur.app` to `/Applications`
3. Right-click → Open (one-time Gatekeeper bypass — no Apple Developer account yet)

### From source

```bash
git clone git@github.com:hygurlabs/hygur-app.git
cd hygur-app
make open
```

The sidecar starts on port 8420 and writes its auth token to `~/Library/Application Support/Hygur/token`.

## Development

### Makefile targets

| Target | Description |
|--------|-------------|
| `make test` | Go tests + binary check + app build |
| `make dev` | Start sidecar in watch mode |
| `make check-api` | Smoke-test all API endpoints (sidecar must be running) |
| `make open` | Build + ad-hoc sign + launch the app |
| `make verify-dmg` | Full DMG build, mount, verify, unmount |
| `make release` | Build DMG + draft GitHub release |
| `make clean` | Remove all build artifacts |

### Running tests

```bash
# All Go tests with race detector
cd sidecar && go test -race ./...

# Integration tests only
cd sidecar && go test ./tests/integration/... -v
```

### Building the sidecar

```bash
cd sidecar && make build-for-bundle   # universal binary (arm64 + amd64)
```

## Architecture

```
hygur/
├── sidecar/                  # Go backend
│   ├── cmd/hygur/            # Entry point
│   └── internal/
│       ├── api/              # HTTP server + handlers
│       ├── auth/             # Token management
│       ├── config/           # YAML config loader
│       ├── connectors/       # IMAP, Files, Notes connectors
│       ├── ingest/           # Document parsing + chunking
│       ├── llm/              # LM Studio client
│       ├── marketplace/      # Connector catalog
│       ├── plugin/           # Connector factory + scheduler
│       ├── retrieval/        # Hybrid RAG (BM25 + vector)
│       └── store/            # SQLite + vector store
├── macos-app/                # SwiftUI app
│   ├── Sources/
│   │   ├── App/
│   │   ├── DesignSystem/     # Tokens, modifiers
│   │   ├── Models/
│   │   ├── Services/         # SidecarService, SidecarSupervisor
│   │   ├── ViewModels/
│   │   └── Views/
│   └── project.yml           # XcodeGen config
├── .github/workflows/        # CI/CD — release on v*.*.* tag
└── Makefile                  # Root build orchestration
```

## API

The sidecar exposes a REST/SSE API on `http://localhost:8420`.

All endpoints except `/health` require the `X-Hygur-Token` header:

```bash
TOKEN=$(cat ~/Library/Application\ Support/Hygur/token)
curl -H "X-Hygur-Token: $TOKEN" http://localhost:8420/marketplace/connectors
```

Key endpoints:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Sidecar + LM Studio status |
| `/models` | GET | Available LLM models |
| `/chat` | POST | Streaming SSE chat |
| `/knowledge` | GET/POST | Document knowledge base |
| `/connectors/instances` | GET | Connector instances |
| `/connectors/{type}/instances` | POST | Create connector instance |
| `/marketplace/connectors` | GET | Connector catalog |

## Release

Tagging triggers the GitHub Actions pipeline automatically:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The workflow builds a universal binary, packages a DMG, and creates a GitHub release.

## License

AGPL-3.0 — see [LICENSE](LICENSE).
