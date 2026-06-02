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
WEBUI     := webui
TOKEN_FILE := $(HOME)/Library/Application Support/Hygur/token
SIDECAR_URL := http://localhost:8420

# Stable code-signing identity, kept in the LOGIN keychain (always searched and
# unlocked, no separate keychain to pollute the search list). A stable identity
# is what makes macOS keep its grants — Automation (Mail.app) and Keychain ACLs —
# across rebuilds. Ad-hoc signing ("-") changes the identity every build, which
# resets every permission (the "redemande à chaque lancement" / mail-sync-breaks
# problem). Create the cert once with `make dev-cert`.
CODESIGN_ID    := Hygur Dev
LOGIN_KEYCHAIN := $(HOME)/Library/Keychains/login.keychain-db

.PHONY: all test test-go test-binary check-api dev open reset-db dev-cert \
        webui build-sidecar build-app sign-app package-dmg verify-dmg release clean

all: test

# ── Test complet local ────────────────────────────────────────────────────────

## Lance les tests Go, vérifie le binaire universel et compile l'app.
## C'est la cible à lancer avant de pousser un tag.
test: test-go test-binary build-app
	@echo ""
	@echo "✅ Tout est vert. Lance \`make verify-dmg\` pour tester le packaging complet."

# sqlite_fts5 must match the sidecar Makefile's GO_TAGS: the FTS5 lexical index
# is a runtime SQLite module, absent without this tag (build stays green, app breaks).
# Depends on `webui`: the api/webui package go:embed's dist/, which is generated
# (not committed), so the Go build/test can't compile until the SPA is built.
test-go: webui
	@echo "→ Tests Go (race detector)..."
	cd $(SIDECAR) && go test -tags sqlite_fts5 -race ./...
	@echo "→ go vet..."
	cd $(SIDECAR) && go vet -tags sqlite_fts5 ./...
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
## Quitte proprement l'instance précédente avant de lancer la nouvelle
## pour éviter le conflit de port 8420 entre deux sidecars simultanés.
open: sign-app
	@echo "→ Arrêt de l'instance précédente..."
	@osascript -e 'tell application "$(APP_NAME)" to quit' 2>/dev/null || true
	@killall hygur-sidecar 2>/dev/null || true
	@sleep 1
	@echo "→ Ouverture de $(APP_NAME).app..."
	open $(APP_PATH)
	@echo "→ UI web (servie par le sidecar) : http://localhost:8420"

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
	@echo "✅ Base supprimée. Lance \`make open\` : le schéma est recréé et les connecteurs réindexent."

# ── Build ─────────────────────────────────────────────────────────────────────

## Build the React web UI (Vite + TS) into the sidecar's go:embed dir. Runs
## before any sidecar build so `dist/` exists when go:embed bundles it. Installs
## node deps only when node_modules is missing (fast on rebuilds).
webui:
	@echo "→ Build WebUI (Vite + React + TypeScript)..."
	@cd $(WEBUI) && (test -d node_modules || npm ci) && npm run build
	@echo "✅ WebUI prête (embarquée dans le sidecar via go:embed)"

build-sidecar: webui
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

## Crée (une seule fois) un certificat de signature stable dans le TROUSSEAU
## LOGIN, pour que macOS garde les autorisations (Automation Mail, Keychain)
## d'un build à l'autre — fini les re-demandes à chaque lancement. Demande ton
## mot de passe de session UNE fois (pour autoriser codesign à utiliser la clé).
## Réversible : `security delete-certificate -c "$(CODESIGN_ID)" "$(LOGIN_KEYCHAIN)"`.
dev-cert:
	@if security find-certificate -c "$(CODESIGN_ID)" "$(LOGIN_KEYCHAIN)" >/dev/null 2>&1; then \
		echo "✅ Certificat '$(CODESIGN_ID)' déjà présent dans le trousseau login"; \
	else \
		echo "→ Création du certificat de signature stable '$(CODESIGN_ID)'..."; \
		TMP=$$(mktemp -d); \
		printf '[req]\ndistinguished_name=dn\nx509_extensions=v3\nprompt=no\n[dn]\nCN=%s\n[v3]\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=critical,codeSigning\nbasicConstraints=critical,CA:false\n' "$(CODESIGN_ID)" > $$TMP/cs.cnf; \
		/usr/bin/openssl req -x509 -newkey rsa:2048 -keyout $$TMP/key.pem -out $$TMP/cert.pem -days 3650 -nodes -config $$TMP/cs.cnf >/dev/null 2>&1; \
		/usr/bin/openssl pkcs12 -export -inkey $$TMP/key.pem -in $$TMP/cert.pem -out $$TMP/id.p12 -name "$(CODESIGN_ID)" -passout pass:hygur >/dev/null 2>&1; \
		security import $$TMP/id.p12 -k "$(LOGIN_KEYCHAIN)" -P hygur -T /usr/bin/codesign >/dev/null; \
		rm -rf $$TMP; \
		printf "→ Mot de passe de session (1 seule fois, pour autoriser codesign à utiliser la clé) : "; \
		stty -echo 2>/dev/null; read PW; stty echo 2>/dev/null; echo; \
		if security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$$PW" "$(LOGIN_KEYCHAIN)" >/dev/null 2>&1; then \
			echo "✅ '$(CODESIGN_ID)' installé (identité stable)."; \
		else \
			echo "⚠️  partition-list non posée (mot de passe ?) — codesign demandera l'accès au 1er build, clique « Toujours autoriser »."; \
		fi; \
		echo "   Au 1er \`make open\` ensuite, macOS redemande Automation Mail + Keychain UNE dernière fois (changement d'identité), puis s'en souvient pour tous les builds suivants."; \
	fi

# NOTE: on NE réinjecte PAS les entitlements (keychain-access-groups,
# application-groups). Ce sont des entitlements RESTREINTS qui exigent une équipe
# Apple Developer / provisioning ; un cert self-signed qui les revendique fait
# échouer le lancement (Launchd job spawn failed, err 163). On signe donc sans —
# l'app se lance ; le revers est que le Keychain redemande l'accès aux secrets
# connecteurs (limite inhérente au dev signing sans compte développeur).
sign-app: build-app
	@if security find-certificate -c "$(CODESIGN_ID)" "$(LOGIN_KEYCHAIN)" >/dev/null 2>&1; then \
		echo "→ Signature avec identité stable '$(CODESIGN_ID)'..."; \
		codesign --deep --force --sign "$(CODESIGN_ID)" $(APP_PATH); \
		echo "✅ Signé ('$(CODESIGN_ID)') : $(APP_PATH)"; \
	else \
		echo "→ Signature ad-hoc (\`make dev-cert\` une fois pour une identité stable)..."; \
		codesign --deep --force --sign "-" $(APP_PATH); \
		echo "✅ Signé (ad-hoc) : $(APP_PATH)"; \
	fi

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
	@echo "→ Draft release v$(VERSION) sur hygurlabs/hygur-app..."
	gh release create "v$(VERSION)" \
		"$(BUILD_DIR)/$(DMG_NAME)" \
		--repo hygurlabs/hygur-app \
		--title "Hygur $(VERSION)" \
		--draft \
		--generate-notes
	@echo "✅ Draft créé — à publier sur github.com/hygurlabs/hygur-app/releases"

# ── Nettoyage ────────────────────────────────────────────────────────────────

clean:
	rm -rf $(BUILD_DIR)
	$(MAKE) -C $(SIDECAR) clean
