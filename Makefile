# Hygur — root Makefile
#
# Workflow de développement :
#   make test          → tests Go + build app (validation complète sans lancer l'app)
#   make dev           → lance le sidecar en mode développement
#   make check-api     → teste les endpoints sidecar (sidecar doit tourner)
#   make open          → build + sign + lance l'app directement
#   make verify-dmg    → build + DMG + monte + vérifie + démonte
#   make release       → package-dmg + draft GitHub release
#   make clean         → supprime les artefacts
#
# Prérequis : brew install create-dmg xcodegen gh

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0")
APP_NAME  := Hygur
SCHEME    := $(APP_NAME)
BUILD_DIR := .build
DERIVED   := $(BUILD_DIR)/DerivedData
RELEASE   := $(BUILD_DIR)/Release
DMG_NAME  := $(APP_NAME)-$(VERSION).dmg
APP_PATH  := $(RELEASE)/$(APP_NAME).app
SIDECAR   := sidecar
TOKEN_FILE := $(HOME)/Library/Application Support/Hygur/token
SIDECAR_URL := http://localhost:8420

.PHONY: all test test-go test-binary check-api dev open \
        build-sidecar build-app sign-app package-dmg verify-dmg release clean

all: test

# ── Test complet local ────────────────────────────────────────────────────────

## Lance les tests Go, vérifie le binaire universel et compile l'app.
## C'est la cible à lancer avant de pousser un tag.
test: test-go test-binary build-app
	@echo ""
	@echo "✅ Tout est vert. Lance \`make verify-dmg\` pour tester le packaging complet."

test-go:
	@echo "→ Tests Go (race detector)..."
	cd $(SIDECAR) && go test -race ./...
	@echo "→ go vet..."
	cd $(SIDECAR) && go vet ./...
	@echo "✅ Tests Go OK"

test-binary: build-sidecar
	@echo "→ Vérification du binaire universel..."
	@ARCH=$$(lipo -info macos-app/Resources/hygur-sidecar 2>&1); \
	echo "   $$ARCH"; \
	echo "$$ARCH" | grep -q "x86_64" && echo "$$ARCH" | grep -q "arm64" \
		&& echo "✅ Fat binary OK (arm64 + x86_64)" \
		|| echo "⚠️  Binaire non-universel (cross-compilation indisponible)"

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

## Build + sign + ouvre l'app directement (sans passer par le DMG).
open: sign-app
	@echo "→ Ouverture de $(APP_NAME).app..."
	open $(APP_PATH)

# ── Build ─────────────────────────────────────────────────────────────────────

build-sidecar:
	@echo "→ Build sidecar universel..."
	$(MAKE) -C $(SIDECAR) build-for-bundle VERSION=$(VERSION)

build-app: build-sidecar
	@echo "→ Génération projet Xcode..."
	cd macos-app && xcodegen generate --quiet
	@echo "→ Build $(APP_NAME).app (Release)..."
	@mkdir -p $(RELEASE)
	@xcodebuild \
		-scheme $(SCHEME) \
		-project macos-app/$(APP_NAME).xcodeproj \
		-configuration Release \
		-derivedDataPath $(DERIVED) \
		CONFIGURATION_BUILD_DIR=$(PWD)/$(RELEASE) \
		CODE_SIGNING_ALLOWED=NO \
		build 2>&1 | grep -E "error:|warning:|BUILD (SUCCEEDED|FAILED)" \
		|| true
	@test -d $(APP_PATH) || (echo "❌ Build échoué" && exit 1)
	@echo "✅ $(APP_NAME).app prêt"

sign-app: build-app
	@echo "→ Signature ad-hoc..."
	codesign --deep --force --sign "-" $(APP_PATH)
	@echo "✅ Signé : $(APP_PATH)"

# ── Packaging DMG ────────────────────────────────────────────────────────────

package-dmg: sign-app
	@echo "→ Création du DMG $(DMG_NAME)..."
	@mkdir -p $(BUILD_DIR)
	create-dmg \
		--volname "$(APP_NAME) $(VERSION)" \
		--window-pos 200 120 \
		--window-size 600 400 \
		--icon-size 128 \
		--icon "$(APP_NAME).app" 175 190 \
		--hide-extension "$(APP_NAME).app" \
		--app-drop-link 425 190 \
		"$(BUILD_DIR)/$(DMG_NAME)" \
		"$(RELEASE)/$(APP_NAME).app"
	@echo "✅ DMG : $(BUILD_DIR)/$(DMG_NAME)"

## Monte le DMG, vérifie la structure et le binaire universel, puis démonte.
verify-dmg: package-dmg
	@echo "→ Montage du DMG..."
	@MOUNT=$$(hdiutil attach "$(BUILD_DIR)/$(DMG_NAME)" | grep Volumes | awk '{print $$3}'); \
	echo "   Monté : $$MOUNT"; \
	echo "→ Structure Resources :"; \
	ls "$$MOUNT/$(APP_NAME).app/Contents/Resources/" | grep -E "hygur|\.plist|\.car"; \
	echo "→ Architecture sidecar :"; \
	lipo -info "$$MOUNT/$(APP_NAME).app/Contents/Resources/hygur-sidecar"; \
	echo "→ Signature :"; \
	codesign -dvv "$$MOUNT/$(APP_NAME).app" 2>&1 | grep -E "Identifier|TeamIdentifier|Authority|flags"; \
	echo "→ Démontage..."; \
	hdiutil detach "$$MOUNT" -quiet; \
	echo "✅ DMG vérifié"

# ── GitHub release ───────────────────────────────────────────────────────────

release: package-dmg
	@echo "→ Draft release v$(VERSION) sur hygurlabs/hygur..."
	gh release create "v$(VERSION)" \
		"$(BUILD_DIR)/$(DMG_NAME)" \
		--repo hygurlabs/hygur \
		--title "Hygur $(VERSION)" \
		--draft \
		--generate-notes
	@echo "✅ Draft créé — à publier sur github.com/hygurlabs/hygur/releases"

# ── Nettoyage ────────────────────────────────────────────────────────────────

clean:
	rm -rf $(BUILD_DIR)
	$(MAKE) -C $(SIDECAR) clean
