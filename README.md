# Hygur

> Ton double numerique local -- memoire privee alimentee par LLM.

Hygur est un assistant personnel local qui s'execute entierement sur ta machine. Il combine un sidecar Go pour la communication avec LM Studio et une application macOS native pour l'interface utilisateur.

## Prerequis

- macOS 14.0+ (Sonoma)
- Go 1.21+
- LM Studio running on localhost:1234
- Xcode 15+ (pour l'app macOS)
- XcodeGen (pour generer le projet Xcode)

## Installation

### Sidecar Go

```bash
cd sidecar
make build
./bin/hygur
```

Le sidecar demarre sur le port 8420 par defaut et genere un token d'authentification dans `~/.hygur/.token`.

### App macOS

```bash
# Installer XcodeGen si necessaire
brew install xcodegen

cd macos-app
xcodegen generate
open Hygur.xcodeproj
# Build & Run (Cmd+R)
```

## Configuration

Le sidecar lit `config.yaml` a la racine du repertoire sidecar :

```yaml
server:
  host: "127.0.0.1"
  port: 8420
  read_timeout: "30s"
  write_timeout: "30s"
  shutdown_timeout: "5s"

lm_studio:
  url: "http://localhost:1234/v1"
  model_default: "llama-3.2-3b-instruct"
  timeout: "60s"
  max_retries: 3
```

### Options de configuration

| Option | Description | Defaut |
|--------|-------------|--------|
| `server.port` | Port HTTP du sidecar | 8420 |
| `server.host` | Adresse d'ecoute | 127.0.0.1 |
| `lm_studio.url` | URL de l'API LM Studio | http://localhost:1234/v1 |
| `lm_studio.model_default` | Modele par defaut | - |
| `lm_studio.timeout` | Timeout des requetes LLM | 60s |
| `lm_studio.max_retries` | Nombre de tentatives | 3 |

## API

Le sidecar expose une API REST/SSE sur localhost :

| Endpoint | Methode | Auth | Description |
|----------|---------|------|-------------|
| `/health` | GET | Non | Statut du sidecar et de LM Studio |
| `/models` | GET | Oui | Liste des modeles disponibles |
| `/chat` | POST | Oui | Chat avec streaming SSE |

### Authentification

Tous les endpoints (sauf `/health`) necessitent le header `X-Hygur-Token` :

```bash
curl -H "X-Hygur-Token: $(cat ~/.hygur/.token)" http://localhost:8420/models
```

### Exemples

#### Health Check

```bash
curl http://localhost:8420/health
```

Reponse :
```json
{
  "status": "ok",
  "version": "0.1.0",
  "lm_studio": "connected",
  "uptime_seconds": 3842
}
```

#### Liste des modeles

```bash
curl -H "X-Hygur-Token: $(cat ~/.hygur/.token)" http://localhost:8420/models
```

Reponse :
```json
{
  "models": [
    {"id": "llama-3.2-3b-instruct", "name": "llama-3.2-3b-instruct"},
    {"id": "qwen2.5-7b-instruct", "name": "qwen2.5-7b-instruct"}
  ]
}
```

#### Chat streaming

```bash
curl -X POST http://localhost:8420/chat \
  -H "Content-Type: application/json" \
  -H "X-Hygur-Token: $(cat ~/.hygur/.token)" \
  -d '{"messages": [{"role": "user", "content": "Bonjour!"}], "stream": true}'
```

Reponse (SSE) :
```
data: {"delta": "Bonjour", "done": false}
data: {"delta": " ! Comment", "done": false}
data: {"delta": " puis-je vous aider ?", "done": false}
data: {"done": true, "usage": {"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18}}
```

Voir `docs/api-contract.md` pour la documentation complete de l'API.

## Architecture

```
hygur/
├── sidecar/           # Backend Go
│   ├── cmd/hygur/     # Point d'entree
│   ├── internal/
│   │   ├── api/       # Serveur HTTP et handlers
│   │   ├── auth/      # Gestion des tokens
│   │   ├── config/    # Configuration YAML
│   │   ├── llm/       # Client LM Studio
│   │   └── version/   # Informations de version
│   └── tests/
│       └── integration/  # Tests E2E
├── macos-app/         # App SwiftUI
│   ├── Sources/       # Code Swift
│   │   ├── Views/     # ChatView, SettingsView
│   │   ├── Models/    # Message, Model
│   │   └── Services/  # SidecarService
│   └── project.yml    # Configuration XcodeGen
└── docs/              # Documentation
    ├── api-contract.md
    └── IMPLEMENTATION_PLAN.md
```

Voir `docs/IMPLEMENTATION_PLAN.md` pour le plan d'implementation complet.

## Developpement

### Tests

```bash
# Backend - tous les tests
cd sidecar && make test

# Backend - avec couverture
cd sidecar && go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out

# Tests d'integration uniquement
cd sidecar && go test ./tests/integration/... -v
```

### Build

```bash
# Backend
cd sidecar && make build

# Frontend (necessite Xcode)
cd macos-app && xcodebuild -scheme Hygur build
```

### Lint

```bash
cd sidecar && golangci-lint run
```

## Statut du projet

### Lot 1 - Core (Complet)

- [x] Sidecar Go avec API REST
- [x] Endpoint /health avec statut LM Studio
- [x] Endpoint /models pour lister les modeles
- [x] Endpoint /chat avec streaming SSE
- [x] Authentification par token
- [x] App macOS avec ChatView
- [x] Tests unitaires > 80% couverture
- [x] Tests d'integration E2E

### Lots suivants (A venir)

- [ ] Lot 2 - Knowledge Base
- [ ] Lot 3 - Email Integration
- [ ] Lot 4 - Tools

## Licence

Projet personnel - Tous droits reserves.
