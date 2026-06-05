# Hygur — root Makefile
#
# Workflow de développement :
#   make test          → tests Go (validation complète sans lancer l'app)
#   make dev           → lance le sidecar en mode développement
#   make check-api     → teste les endpoints sidecar (sidecar doit tourner)
#   make tauri-dev     → lance l'app Tauri (shell + sidecar embarqué) en dev
#   make tauri-build   → build l'app Tauri + DMG distribuable
#   make build-server  → build le binaire serveur headless (hôte natif)
#   make docker-image  → build l'image serveur Linux
#   make clean         → supprime les artefacts
#
# Prérequis : Go, Node/npm, Rust (rustup) ; `gh` pour les releases.

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0")
APP_NAME  := Hygur
BUILD_DIR := .build
SIDECAR   := sidecar
WEBUI     := webui
TOKEN_FILE := $(HOME)/Library/Application Support/Hygur/token
SIDECAR_URL := http://localhost:8420

.PHONY: all test test-go check-api dev reset-db \
        webui build-server docker-image \
        tauri-sidecar tauri-dev tauri-build clean

all: test

# ── Test complet local ────────────────────────────────────────────────────────

## Lance les tests Go. C'est la cible à lancer avant de pousser un tag.
test: test-go
	@echo ""
	@echo "✅ Tout est vert. Lance \`make tauri-build\` pour packager l'app."

# sqlite_fts5 + sqlite_json1 must match the sidecar Makefile's GO_TAGS: both are
# runtime SQLite features under mutecomm/go-sqlcipher, absent without these tags
# (build stays green, app breaks: "no such module: fts5" / "no such function:
# json_extract").
# Depends on `webui`: the api/webui package go:embed's dist/, which is generated
# (not committed), so the Go build/test can't compile until the SPA is built.
test-go: webui
	@echo "→ Tests Go (race detector)..."
	cd $(SIDECAR) && go test -tags 'sqlite_fts5 sqlite_json1' -race ./...
	@echo "→ go vet..."
	cd $(SIDECAR) && go vet -tags 'sqlite_fts5 sqlite_json1' ./...
	@echo "✅ Tests Go OK"

# ── Développement ─────────────────────────────────────────────────────────────

## Lance le sidecar en mode dev (reconstruction auto au premier lancement).
dev:
	@echo "→ Lancement du sidecar (Ctrl+C pour arrêter)..."
	$(MAKE) -C $(SIDECAR) run

## Teste les endpoints du sidecar en cours d'exécution.
## Requires: make dev (dans un autre terminal)
check-api:
	@TOKEN=$$(cat "$(TOKEN_FILE)" 2>/dev/null); \
	if [ -z "$$TOKEN" ]; then \
		echo "❌ Token introuvable — le sidecar tourne-t-il ? (make dev)"; \
		exit 1; \
	fi; \
	echo "→ /health"; \
	curl -sf $(SIDECAR_URL)/health | python3 -m json.tool --no-ensure-ascii; \
	echo ""; \
	echo "→ GET /marketplace/connectors"; \
	curl -sf -H "X-Hygur-Token: $$TOKEN" $(SIDECAR_URL)/marketplace/connectors \
		| python3 -m json.tool --no-ensure-ascii; \
	echo ""; \
	echo "→ GET /connectors/instances"; \
	curl -sf -H "X-Hygur-Token: $$TOKEN" $(SIDECAR_URL)/connectors/instances \
		| python3 -m json.tool --no-ensure-ascii; \
	echo ""; \
	echo "→ POST /connectors/imap/instances (instance de test)"; \
	curl -sf -X POST \
		-H "X-Hygur-Token: $$TOKEN" \
		-H "Content-Type: application/json" \
		-d '{"id":"imap_ci_test","display_name":"CI Test","enabled":false}' \
		$(SIDECAR_URL)/connectors/imap/instances | python3 -m json.tool --no-ensure-ascii; \
	echo ""; \
	echo "→ DELETE /connectors/instances/imap_ci_test (nettoyage)"; \
	curl -sf -X DELETE \
		-H "X-Hygur-Token: $$TOKEN" \
		$(SIDECAR_URL)/connectors/instances/imap_ci_test; \
	echo "✅ check-api OK"

## Repart d'une base de connaissances vierge : supprime la DB SQLite dans
## Application Support. Au prochain lancement, le schéma (migration v9 : sections
## + FTS5) est recréé et les connecteurs réindexent depuis zéro via le nouveau
## pipeline structurel. DESTRUCTIF — à lancer volontairement.
HYGUR_DATA := $(HOME)/Library/Application Support/Hygur
reset-db:
	@echo "→ Arrêt de l'app et du sidecar..."
	@osascript -e 'tell application "$(APP_NAME)" to quit' 2>/dev/null || true
	@killall hygur-sidecar 2>/dev/null || true
	@sleep 1
	@echo "→ Suppression de la base : $(HYGUR_DATA)/hygur.db*"
	@rm -f "$(HYGUR_DATA)/hygur.db" "$(HYGUR_DATA)/hygur.db-shm" "$(HYGUR_DATA)/hygur.db-wal"
	@echo "✅ Base supprimée. Lance \`make tauri-dev\` : le schéma est recréé et les connecteurs réindexent."

# ── Build ─────────────────────────────────────────────────────────────────────

## Build the React web UI (Vite + TS) into the sidecar's go:embed dir. Runs
## before any sidecar build so `dist/` exists when go:embed bundles it. Installs
## node deps only when node_modules is missing (fast on rebuilds).
webui:
	@echo "→ Build WebUI (Vite + React + TypeScript)..."
	@cd $(WEBUI) && (test -d node_modules || npm ci) && npm run build
	@echo "✅ WebUI prête (embarquée dans le sidecar via go:embed)"

# ── Hygur Server (headless) ─────────────────────────────────────────────────────

## Build the standalone server binary for the current host. Depends on `webui`
## so the embedded dist/ is fresh (go:embed needs it). For Linux/Windows builds,
## use `make docker-image` (CGO toolchain per target lives in the build image)
## or a per-OS CI runner — cross-compiling CGO+sqlite from macOS is brittle.
build-server: webui
	@echo "→ Build hygur-server (host natif)..."
	cd $(SIDECAR) && CGO_ENABLED=1 go build -tags 'sqlite_fts5 sqlite_json1' \
		-ldflags "-X github.com/hygur/sidecar/internal/version.Version=$(VERSION)" \
		-o bin/hygur-server ./cmd/hygur
	@echo "✅ $(SIDECAR)/bin/hygur-server"

# ── Tauri 2 (cross-platform shell ; remplace l'ancien macos-app SwiftUI) ──────
# The Tauri app embeds + supervises the sidecar and points its window at the
# sidecar-served WebUI (:8420). The sidecar is bundled as a Tauri externalBin
# named hygur-sidecar-<target-triple>.
TAURI_DIR     := webui
TARGET_TRIPLE := $(shell rustc -Vv 2>/dev/null | sed -n 's/host: //p')
SIDECAR_STAGE := $(TAURI_DIR)/src-tauri/binaries/hygur-sidecar-$(TARGET_TRIPLE)

## Build the Go sidecar for the host (embedding the fresh WebUI via `webui`)
## and stage it as the Tauri external binary.
tauri-sidecar: webui
	@echo "→ Build + stage sidecar for $(TARGET_TRIPLE)..."
	@mkdir -p $(TAURI_DIR)/src-tauri/binaries
	cd $(SIDECAR) && CGO_ENABLED=1 go build -tags 'sqlite_fts5 sqlite_json1' \
		-ldflags "-X github.com/hygur/sidecar/internal/version.Version=$(VERSION)" \
		-o $(CURDIR)/$(SIDECAR_STAGE) ./cmd/hygur
	@chmod +x $(SIDECAR_STAGE)
	@echo "✅ $(SIDECAR_STAGE)"

## Run the Tauri app in dev (stages the sidecar first).
tauri-dev: tauri-sidecar
	cd $(TAURI_DIR) && npm run tauri dev

## Build the Tauri app bundle + DMG (stages the sidecar first).
## Par défaut la signature est ad-hoc (dogfood : clic droit → Ouvrir au 1er
## lancement). Pour un DMG signé + notarisé distribuable, exporte ton identité
## Apple Developer avant : APPLE_SIGNING_IDENTITY="Developer ID Application: …"
## APPLE_ID / APPLE_PASSWORD (app-specific) / APPLE_TEAM_ID — Tauri les lit
## automatiquement et notarise. DMG produit sous
## webui/src-tauri/target/release/bundle/dmg/.
tauri-build: tauri-sidecar
	cd $(TAURI_DIR) && npm run tauri build

## Build the headless Linux server image (multi-stage: WebUI + CGO Go build).
DOCKER_IMAGE ?= hygur-server:$(VERSION)
docker-image:
	@echo "→ Build image $(DOCKER_IMAGE)..."
	docker build -t $(DOCKER_IMAGE) --build-arg VERSION=$(VERSION) .
	@echo "✅ image $(DOCKER_IMAGE) — run: docker run -p 8420:8420 -v hygur-data:/data $(DOCKER_IMAGE)"

# ── Nettoyage ────────────────────────────────────────────────────────────────

clean:
	rm -rf $(BUILD_DIR) $(TAURI_DIR)/src-tauri/binaries $(WEBUI)/dist
	$(MAKE) -C $(SIDECAR) clean
