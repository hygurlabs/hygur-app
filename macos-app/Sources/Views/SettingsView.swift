import SwiftUI
import AppKit
import MarkdownUI

// MARK: - SettingsView

struct SettingsView: View {
    @ObservedObject private var settings = AppPreferences.shared
    @State private var connectionStatus: ConnectionStatus = .unknown
    @State private var sidecarVersion: String = "—"
    @State private var isTestingConnection = false
    @State private var tokenStatus: TokenStatus = .unknown
    @State private var tokenMessage: String = ""
    @State private var showResetConfirmation = false
    @State private var isResetting = false
    @State private var resetMessage: String = ""
    @State private var selectedTab: SettingsTab = .connection

    var body: some View {
        TabView(selection: $selectedTab) {
            ConnectionTab(
                settings: settings,
                connectionStatus: $connectionStatus,
                isTestingConnection: $isTestingConnection,
                tokenStatus: $tokenStatus,
                tokenMessage: $tokenMessage,
                testConnection: testConnection,
                loadTokenFromSidecar: loadTokenFromSidecar
            )
            .tabItem { Label("Connexion", systemImage: "network") }
            .tag(SettingsTab.connection)

            LMStudioTab()
                .tabItem { Label("Local LLM", systemImage: "server.rack") }
                .tag(SettingsTab.lmStudio)

            ModelTab(settings: settings)
                .tabItem { Label("Modèle", systemImage: "cpu") }
                .tag(SettingsTab.model)

            NotificationsTab()
                .tabItem { Label("Notifications", systemImage: "bell") }
                .tag(SettingsTab.notifications)

            SystemTab(
                showResetConfirmation: $showResetConfirmation,
                isResetting: $isResetting,
                resetMessage: $resetMessage
            )
            .tabItem { Label("Système", systemImage: "gearshape") }
            .tag(SettingsTab.system)

            AboutTab(sidecarVersion: sidecarVersion)
                .tabItem { Label("À propos", systemImage: "info.circle") }
                .tag(SettingsTab.about)
        }
        .onReceive(NotificationCenter.default.publisher(for: .openUpdatesPane)) { _ in
            selectedTab = .about
        }
        .frame(
            minWidth: 760,
            idealWidth: 860,
            maxWidth: .infinity,
            minHeight: 640,
            idealHeight: 760,
            maxHeight: .infinity
        )
        .background(HygurColors.background)
        .confirmationDialog(
            "Réinitialiser la base de connaissance ?",
            isPresented: $showResetConfirmation,
            titleVisibility: .visible
        ) {
            Button("Réinitialiser", role: .destructive) {
                Task { await resetKnowledgeBase() }
            }
            Button("Annuler", role: .cancel) {}
        } message: {
            Text("Cette action supprime définitivement tous les documents, chunks et embeddings. Impossible d'annuler.")
        }
        .onAppear {
            checkTokenStatus()
            if settings.isValidURL {
                Task { await testConnection() }
            }
        }
    }

    // MARK: - Helpers

    /// Probe the sidecar `/health` endpoint. To avoid the false-negative flicker
    /// users were seeing right after launch (the sidecar takes ~300-800 ms to
    /// bind its port even after `Process.run` returns), retry up to 3 times
    /// with a 250 ms gap before declaring the connection dead. Only the final
    /// outcome is published so the UI doesn't oscillate.
    private func testConnection() async {
        guard settings.isValidURL else { connectionStatus = .disconnected; return }
        isTestingConnection = true
        connectionStatus = .testing
        defer { isTestingConnection = false }

        let sidecar = SidecarService(baseURL: settings.sidecarURLValue!)
        for attempt in 0..<3 {
            do {
                let health = try await sidecar.health()
                connectionStatus = health.status == "ok" ? .connected : .disconnected
                sidecarVersion = health.version
                return
            } catch {
                if attempt < 2 {
                    try? await Task.sleep(nanoseconds: 250_000_000)
                    continue
                }
                connectionStatus = .disconnected
                sidecarVersion = "—"
            }
        }
    }

    private func checkTokenStatus() {
        let service = SidecarService.fromSettings()
        Task {
            if let token = await service.getToken(), !token.isEmpty {
                tokenStatus = .valid
                tokenMessage = "Token chargé depuis le Keychain"
            } else {
                tokenStatus = .missing
                tokenMessage = "Cliquez sur 'Charger le token' pour l'importer depuis le sidecar"
            }
        }
    }

    private func loadTokenFromSidecar() {
        let tokenPath = NSString(string: "~/Library/Application Support/Hygur/token").expandingTildeInPath
        do {
            let token = try String(contentsOfFile: tokenPath, encoding: .utf8)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            guard !token.isEmpty else {
                tokenStatus = .invalid
                tokenMessage = "Le fichier token est vide"
                return
            }
            try SidecarService.saveTokenToKeychain(token)
            tokenStatus = .valid
            tokenMessage = "Token importé avec succès. Redémarrez l'application pour l'appliquer."
        } catch {
            tokenStatus = .missing
            tokenMessage = "Impossible de lire ~/Library/Application Support/Hygur/token : \(error.localizedDescription)"
        }
    }

    private func resetKnowledgeBase() async {
        isResetting = true
        resetMessage = ""
        do {
            let sidecar = SidecarService.fromSettings()
            try await sidecar.resetKnowledgeBase()
            resetMessage = "Réinitialisation réussie. Réimportez vos documents."
        } catch {
            resetMessage = "Échec : \(error.localizedDescription)"
        }
        isResetting = false
    }
}

// MARK: - Shared UI primitives

private enum ConnectionStatus { case unknown, testing, connected, disconnected }
private enum TokenStatus { case unknown, valid, missing, invalid }

private enum SettingsTab: Hashable {
    case connection, lmStudio, model, notifications, system, about
}

private struct SettingsCard<Content: View>: View {
    let content: () -> Content
    init(@ViewBuilder content: @escaping () -> Content) { self.content = content }
    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content() }
            .background(HygurColors.surface, in: RoundedRectangle(cornerRadius: HygurRadius.md))
            .overlay(RoundedRectangle(cornerRadius: HygurRadius.md).stroke(HygurColors.border, lineWidth: 1))
    }
}

private struct CardDivider: View {
    var body: some View {
        Divider().background(HygurColors.border).padding(.leading, HygurSpacing.lg)
    }
}

private struct SettingsSectionHeader: View {
    let title: String
    var body: some View {
        Text(title.uppercased())
            .font(HygurTypography.caption)
            .foregroundStyle(HygurColors.textTertiary)
            .tracking(0.5)
            .padding(.bottom, HygurSpacing.xs)
    }
}

private struct TabScrollContainer<Content: View>: View {
    let content: () -> Content
    init(@ViewBuilder content: @escaping () -> Content) { self.content = content }
    var body: some View {
        ScrollView(.vertical, showsIndicators: false) {
            VStack(alignment: .leading, spacing: HygurSpacing.xxl) { content() }
                .padding(HygurSpacing.xxl)
        }
        .background(HygurColors.background)
    }
}

/// Compact row inside a card: icon + title/subtitle on the left, trailing content on the right.
private struct CardRow<Trailing: View>: View {
    let icon: String
    let iconColor: Color
    let title: String
    let subtitle: String
    let trailing: () -> Trailing

    init(icon: String, iconColor: Color = HygurColors.accent,
         title: String, subtitle: String = "",
         @ViewBuilder trailing: @escaping () -> Trailing) {
        self.icon = icon; self.iconColor = iconColor
        self.title = title; self.subtitle = subtitle; self.trailing = trailing
    }

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: icon)
                .font(.system(size: 15))
                .foregroundStyle(iconColor)
                .frame(width: 22, height: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textPrimary)
                if !subtitle.isEmpty {
                    Text(subtitle)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
            }
            Spacer()
            trailing()
        }
        .padding(HygurSpacing.lg)
    }
}

// MARK: - Tab 1: Connexion

private struct ConnectionTab: View {
    @ObservedObject var settings: AppPreferences
    @Binding var connectionStatus: ConnectionStatus
    @Binding var isTestingConnection: Bool
    @Binding var tokenStatus: TokenStatus
    @Binding var tokenMessage: String
    let testConnection: () async -> Void
    let loadTokenFromSidecar: () -> Void

    var body: some View {
        TabScrollContainer {
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Authentification")
                SettingsCard {
                    HStack(spacing: HygurSpacing.sm) {
                        tokenStatusBadge
                        Spacer()
                        Button("Charger le token") { loadTokenFromSidecar() }
                            .buttonStyle(.bordered).controlSize(.small)
                    }
                    .padding(HygurSpacing.lg)
                    if !tokenMessage.isEmpty {
                        CardDivider()
                        Text(tokenMessage)
                            .font(HygurTypography.caption)
                            .foregroundStyle(tokenStatus == .valid ? HygurColors.success : HygurColors.warning)
                            .padding(.horizontal, HygurSpacing.lg)
                            .padding(.vertical, HygurSpacing.md)
                    }
                }
            }

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "URL du sidecar")
                SettingsCard {
                    TextField("http://localhost:8420", text: $settings.sidecarURL)
                        .textFieldStyle(.plain)
                        .font(HygurTypography.body)
                        .padding(HygurSpacing.lg)
                    CardDivider()
                    HStack(spacing: HygurSpacing.sm) {
                        connectionStatusBadge
                        Spacer()
                        Button("Tester la connexion") { Task { await testConnection() } }
                            .buttonStyle(.bordered).controlSize(.small)
                            .disabled(isTestingConnection || !settings.isValidURL)
                    }
                    .padding(HygurSpacing.lg)
                    if !settings.isValidURL && !settings.sidecarURL.isEmpty {
                        CardDivider()
                        Text("Format d'URL invalide. Utilisez http:// ou https://")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.danger)
                            .padding(.horizontal, HygurSpacing.lg)
                            .padding(.vertical, HygurSpacing.md)
                    }
                }
            }
        }
    }

    @ViewBuilder private var connectionStatusBadge: some View {
        HStack(spacing: HygurSpacing.xs) {
            switch connectionStatus {
            case .unknown:
                Image(systemName: "circle").foregroundStyle(HygurColors.textTertiary)
                Text("Non testé").foregroundStyle(HygurColors.textSecondary)
            case .testing:
                LoadingIndicator(style: .small)
                Text("Test en cours…").foregroundStyle(HygurColors.textSecondary)
            case .connected:
                Image(systemName: "checkmark.circle.fill").foregroundStyle(HygurColors.success)
                Text("Connecté").foregroundStyle(HygurColors.success)
            case .disconnected:
                Image(systemName: "xmark.circle.fill").foregroundStyle(HygurColors.danger)
                Text("Inaccessible").foregroundStyle(HygurColors.danger)
            }
        }
        .font(HygurTypography.caption)
    }

    @ViewBuilder private var tokenStatusBadge: some View {
        HStack(spacing: HygurSpacing.xs) {
            switch tokenStatus {
            case .unknown:
                Image(systemName: "key").foregroundStyle(HygurColors.textTertiary)
                Text("Non vérifié").foregroundStyle(HygurColors.textSecondary)
            case .valid:
                Image(systemName: "key.fill").foregroundStyle(HygurColors.success)
                Text("Token chargé").foregroundStyle(HygurColors.success)
            case .missing:
                Image(systemName: "key.slash").foregroundStyle(HygurColors.warning)
                Text("Aucun token").foregroundStyle(HygurColors.warning)
            case .invalid:
                Image(systemName: "exclamationmark.triangle").foregroundStyle(HygurColors.danger)
                Text("Token invalide").foregroundStyle(HygurColors.danger)
            }
        }
        .font(HygurTypography.caption)
    }
}

// MARK: - Tab 2: LM Studio

private struct LMStudioTab: View {
    @Environment(SidecarSupervisor.self) private var supervisor
    @State private var inferenceURL: String = ""
    @State private var embeddingURL: String = ""
    @State private var modelDefault: String = ""
    @State private var embeddingModel: String = ""
    @State private var useLlmIntent: Bool = false
    @State private var useJudge: Bool = false
    @State private var dailyBriefEnabled: Bool = false
    @State private var dailyBriefHour: String = "08:00"
    @State private var logLevel: String = "info"
    @State private var isLoading: Bool = false
    @State private var isSaving: Bool = false
    @State private var saveStatus: SaveStatus = .idle

    /// Lists of model IDs returned by the LM Studio /v1/models endpoint for
    /// the inference URL and the embedding URL. Used to drive autocomplete.
    @State private var inferenceModelOptions: [String] = []
    @State private var embeddingModelOptions: [String] = []
    @State private var isLoadingInferenceModels: Bool = false
    @State private var isLoadingEmbeddingModels: Bool = false

    private enum SaveStatus: Equatable {
        case idle, saving, saved, restarting, error(String)
    }

    private let sidecar = SidecarService.fromSettings()
    private let logLevels = ["debug", "info", "warn", "error"]

    var body: some View {
        TabScrollContainer {
            if isLoading {
                HStack { Spacer(); LoadingIndicator(style: .small); Spacer() }
                    .padding(.top, HygurSpacing.xxl)
            } else {
                inferenceSection
                embeddingsSection
                retrievalSection
                briefSection
                loggingSection
                saveBar
            }
        }
        .task {
            await loadConfig()
            await refreshInferenceModels()
            await refreshEmbeddingModels()
        }
    }

    // MARK: - Sections

    private var inferenceSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            SettingsSectionHeader(title: "Inférence (chat)")
            SettingsCard {
                LabeledURLField(
                    label: "URL",
                    placeholder: "http://192.168.x.x:8082",
                    hint: "Chat completions (LM Studio, Ollama, llama.cpp…)",
                    text: $inferenceURL
                )
                .onChange(of: inferenceURL) { _, _ in
                    inferenceModelOptions = []
                }
                CardDivider()
                ModelAutocompleteField(
                    label: "Modèle",
                    placeholder: "ex. mistral-7b-instruct",
                    hint: "Modèle par défaut pour le chat",
                    text: $modelDefault,
                    options: inferenceModelOptions,
                    isLoading: isLoadingInferenceModels,
                    onRefresh: { Task { await refreshInferenceModels(force: true) } }
                )
            }
        }
    }

    private var embeddingsSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            SettingsSectionHeader(title: "Embeddings")
            SettingsCard {
                LabeledURLField(
                    label: "URL",
                    placeholder: "http://192.168.x.x:8081",
                    hint: "Laisser vide pour réutiliser l'URL d'inférence",
                    text: $embeddingURL
                )
                .onChange(of: embeddingURL) { _, _ in
                    embeddingModelOptions = []
                }
                CardDivider()
                ModelAutocompleteField(
                    label: "Modèle",
                    placeholder: "ex. text-embedding-nomic-embed-text-v1.5",
                    hint: "Laisser vide pour utiliser le modèle par défaut du serveur",
                    text: $embeddingModel,
                    options: embeddingModelOptions,
                    isLoading: isLoadingEmbeddingModels,
                    onRefresh: { Task { await refreshEmbeddingModels(force: true) } }
                )
            }
        }
    }

    private var retrievalSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            SettingsSectionHeader(title: "Recherche (RAG)")
            SettingsCard {
                CardRow(
                    icon: "brain",
                    title: "Classificateur d'intention (LLM)",
                    subtitle: "Améliore la précision — ajoute ~0,5 s par requête"
                ) {
                    Toggle("", isOn: $useLlmIntent).toggleStyle(.switch).labelsHidden()
                }
                CardDivider()
                CardRow(
                    icon: "checkmark.seal",
                    title: "Juge de pertinence (LLM)",
                    subtitle: "Post-filtre les résultats faibles — ajoute 1–3 s par requête"
                ) {
                    Toggle("", isOn: $useJudge).toggleStyle(.switch).labelsHidden()
                }
            }
        }
    }

    private var briefSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            SettingsSectionHeader(title: "Brief quotidien")
            SettingsCard {
                CardRow(icon: "sun.horizon", title: "Activer le brief automatique",
                        subtitle: "Récapitulatif IA de l'activité de la veille") {
                    Toggle("", isOn: $dailyBriefEnabled).toggleStyle(.switch).labelsHidden()
                }
                if dailyBriefEnabled {
                    CardDivider()
                    HStack {
                        Text("Heure")
                            .font(HygurTypography.subheadline)
                            .foregroundStyle(HygurColors.textPrimary)
                        Spacer()
                        TextField("08:00", text: $dailyBriefHour)
                            .textFieldStyle(.plain)
                            .font(HygurTypography.captionMono)
                            .foregroundStyle(HygurColors.textSecondary)
                            .frame(width: 52)
                            .multilineTextAlignment(.trailing)
                    }
                    .padding(HygurSpacing.lg)
                }
            }
        }
    }

    private var loggingSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            SettingsSectionHeader(title: "Logs sidecar")
            SettingsCard {
                HStack(spacing: HygurSpacing.md) {
                    Image(systemName: "doc.text.magnifyingglass")
                        .font(.system(size: 15))
                        .foregroundStyle(HygurColors.accent)
                        .frame(width: 22)
                    Text("Niveau de log")
                        .font(HygurTypography.subheadline)
                        .foregroundStyle(HygurColors.textPrimary)
                    Spacer()
                    Picker("", selection: $logLevel) {
                        ForEach(logLevels, id: \.self) { level in
                            Text(level).tag(level)
                        }
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 220)
                }
                .padding(HygurSpacing.lg)
            }
        }
    }

    private var saveBar: some View {
        HStack {
            // Status feedback
            Group {
                switch saveStatus {
                case .idle:
                    EmptyView()
                case .saving:
                    HStack(spacing: HygurSpacing.xs) {
                        LoadingIndicator(style: .small)
                        Text("Enregistrement…").font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                case .restarting:
                    HStack(spacing: HygurSpacing.xs) {
                        LoadingIndicator(style: .small)
                        Text("Redémarrage du sidecar…")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                case .saved:
                    HStack(spacing: HygurSpacing.xs) {
                        Image(systemName: "checkmark.circle.fill")
                            .foregroundStyle(HygurColors.success)
                        Text("Enregistré et appliqué")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                case .error(let msg):
                    HStack(spacing: HygurSpacing.xs) {
                        Image(systemName: "exclamationmark.triangle")
                            .foregroundStyle(HygurColors.danger)
                        Text(msg).font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.danger)
                    }
                }
            }
            Spacer()
            Button("Enregistrer") { Task { await saveConfig() } }
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
                .disabled(isSaving)
                .tint(HygurColors.accent)
        }
    }

    // MARK: - Load / Save

    private func loadConfig() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let cfg = try await sidecar.getConfig()
            inferenceURL    = cfg.lmStudio.url
            embeddingURL    = cfg.lmStudio.embeddingUrl
            modelDefault    = cfg.lmStudio.modelDefault
            embeddingModel  = cfg.lmStudio.embeddingModel
            useLlmIntent    = cfg.retrieval.useLlmIntent
            useJudge        = cfg.retrieval.useJudge
            dailyBriefEnabled = cfg.dailyBrief.enabled
            dailyBriefHour  = cfg.dailyBrief.hourLocal.isEmpty ? "08:00" : cfg.dailyBrief.hourLocal
            logLevel        = cfg.logging.level
        } catch {
            // Sidecar not yet available — leave defaults, user can try again.
        }
    }

    /// Persist the patched config and immediately bounce the sidecar so the
    /// new endpoints/model take effect without the user having to do it
    /// manually. Errors at either step are surfaced inline; the supervisor
    /// only restarts when it's currently running (otherwise the user is
    /// using a remote sidecar and we leave it alone).
    private func saveConfig() async {
        isSaving = true
        defer { isSaving = false }
        saveStatus = .saving
        let patch = SidecarConfigPatch(
            lmStudio: .init(
                url: inferenceURL,
                embeddingUrl: embeddingURL.isEmpty ? nil : embeddingURL,
                modelDefault: modelDefault.isEmpty ? nil : modelDefault,
                embeddingModel: embeddingModel.isEmpty ? nil : embeddingModel
            ),
            logging: .init(level: logLevel),
            dailyBrief: .init(
                enabled: dailyBriefEnabled,
                hourLocal: dailyBriefHour
            ),
            retrieval: .init(
                useLlmIntent: useLlmIntent,
                useJudge: useJudge
            )
        )
        do {
            try await sidecar.patchConfig(patch)
        } catch {
            saveStatus = .error(error.localizedDescription)
            return
        }

        if supervisor.isRunning {
            saveStatus = .restarting
            await supervisor.restart()
        }
        saveStatus = .saved

        // Reload the model lists from the new endpoints so the autocomplete
        // reflects what the just-saved server actually exposes.
        await refreshInferenceModels(force: true)
        await refreshEmbeddingModels(force: true)
    }

    // MARK: - Model Autocomplete

    /// Hits the OpenAI-compatible /v1/models endpoint at `inferenceURL` and
    /// caches the result in `inferenceModelOptions`. No-op when the URL is
    /// empty or invalid; silently degrades if the server is unreachable.
    private func refreshInferenceModels(force: Bool = false) async {
        let url = inferenceURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !url.isEmpty else { return }
        if !force, !inferenceModelOptions.isEmpty { return }
        isLoadingInferenceModels = true
        defer { isLoadingInferenceModels = false }
        let models = await Self.fetchModelIDs(baseURL: url)
        inferenceModelOptions = models
    }

    private func refreshEmbeddingModels(force: Bool = false) async {
        let raw = embeddingURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let url = raw.isEmpty
            ? inferenceURL.trimmingCharacters(in: .whitespacesAndNewlines)
            : raw
        guard !url.isEmpty else { return }
        if !force, !embeddingModelOptions.isEmpty { return }
        isLoadingEmbeddingModels = true
        defer { isLoadingEmbeddingModels = false }
        let models = await Self.fetchModelIDs(baseURL: url)
        embeddingModelOptions = models
    }

    /// Probe the OpenAI-compatible `/v1/models` endpoint and return the list
    /// of model IDs. Returns an empty array on any failure — the UI treats
    /// "no options" the same as "free-form input", so the field stays
    /// usable even when the LLM server is offline.
    private static func fetchModelIDs(baseURL raw: String) async -> [String] {
        guard let base = URL(string: raw) else { return [] }
        // LM Studio exposes /v1/models. Path-stripping isn't needed since
        // we expect the user to enter the LLM server root, not a sub-path.
        let endpoint = base.appendingPathComponent("v1/models")
        var request = URLRequest(url: endpoint)
        request.httpMethod = "GET"
        request.timeoutInterval = 5
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
                return []
            }
            struct ModelsResponse: Decodable {
                struct Model: Decodable { let id: String }
                let data: [Model]
            }
            let decoded = try JSONDecoder().decode(ModelsResponse.self, from: data)
            return decoded.data.map(\.id)
        } catch {
            return []
        }
    }
}

// MARK: - Model Autocomplete Field

/// Text field with a dropdown surfacing the IDs returned by `/v1/models`.
/// Falls back to a plain text field when no options are available — the user
/// can always type a model name freely (LM Studio servers occasionally hide
/// the listing behind auth or expose models the API doesn't enumerate).
private struct ModelAutocompleteField: View {
    let label: String
    let placeholder: String
    let hint: String
    @Binding var text: String
    let options: [String]
    let isLoading: Bool
    let onRefresh: () -> Void

    @State private var isShowingPopover = false

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            HStack {
                Text(label)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textPrimary)
                    .frame(width: 100, alignment: .leading)
                TextField(placeholder, text: $text)
                    .textFieldStyle(.plain)
                    .font(HygurTypography.body)
                    .foregroundStyle(HygurColors.textPrimary)
                if isLoading {
                    LoadingIndicator(style: .small)
                } else if !options.isEmpty {
                    Button {
                        isShowingPopover = true
                    } label: {
                        Image(systemName: "list.bullet")
                    }
                    .buttonStyle(.plain)
                    .help("Choisir un modèle disponible")
                    .popover(isPresented: $isShowingPopover, arrowEdge: .top) {
                        modelList
                    }
                }
                Button {
                    onRefresh()
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.caption)
                }
                .buttonStyle(.plain)
                .help("Rafraîchir la liste depuis /v1/models")
            }
            HStack(spacing: HygurSpacing.xs) {
                Text(hint)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
                if !options.isEmpty {
                    Text("· \(options.count) modèle\(options.count > 1 ? "s" : "") détecté\(options.count > 1 ? "s" : "")")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }
            }
            // Inline filtered suggestions appear when the user starts typing
            // and there's at least one non-exact match. Keeps the chooser
            // discoverable without forcing a popover click.
            if !text.isEmpty, !options.isEmpty {
                let matches = options.filter {
                    $0.localizedCaseInsensitiveContains(text) && $0 != text
                }.prefix(4)
                if !matches.isEmpty {
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(Array(matches), id: \.self) { match in
                            Button {
                                text = match
                            } label: {
                                Text(match)
                                    .font(HygurTypography.captionMono)
                                    .foregroundStyle(HygurColors.textPrimary)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(.vertical, 3)
                                    .padding(.horizontal, HygurSpacing.sm)
                            }
                            .buttonStyle(.plain)
                            .background(HygurColors.surface, in: RoundedRectangle(cornerRadius: HygurRadius.xs))
                        }
                    }
                    .padding(.top, HygurSpacing.xs)
                }
            }
        }
        .padding(HygurSpacing.lg)
    }

    private var modelList: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(options, id: \.self) { option in
                Button {
                    text = option
                    isShowingPopover = false
                } label: {
                    HStack {
                        Text(option)
                            .font(HygurTypography.captionMono)
                            .foregroundStyle(HygurColors.textPrimary)
                        Spacer()
                        if option == text {
                            Image(systemName: "checkmark")
                                .foregroundStyle(HygurColors.accent)
                        }
                    }
                    .padding(.vertical, HygurSpacing.xs)
                    .padding(.horizontal, HygurSpacing.md)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .buttonStyle(.plain)
            }
        }
        .frame(minWidth: 280)
        .padding(.vertical, HygurSpacing.xs)
    }
}

// MARK: - LM Studio field components

private struct LabeledURLField: View {
    let label: String
    let placeholder: String
    let hint: String
    @Binding var text: String

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            HStack {
                Text(label)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textPrimary)
                    .frame(width: 100, alignment: .leading)
                TextField(placeholder, text: $text)
                    .textFieldStyle(.plain)
                    .font(HygurTypography.captionMono)
                    .foregroundStyle(HygurColors.textPrimary)
            }
            Text(hint)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
        }
        .padding(HygurSpacing.lg)
    }
}

private struct LabeledTextField: View {
    let label: String
    let placeholder: String
    let hint: String
    @Binding var text: String

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            HStack {
                Text(label)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textPrimary)
                    .frame(width: 100, alignment: .leading)
                TextField(placeholder, text: $text)
                    .textFieldStyle(.plain)
                    .font(HygurTypography.body)
                    .foregroundStyle(HygurColors.textPrimary)
            }
            Text(hint)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
        }
        .padding(HygurSpacing.lg)
    }
}

// MARK: - Tab 3: Modèle (app-side preferences)

private struct ModelTab: View {
    @ObservedObject var settings: AppPreferences

    var body: some View {
        TabScrollContainer {
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Préférences de chat")
                SettingsCard {
                    VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                        Text("Modèle préféré")
                            .font(HygurTypography.subheadline)
                            .foregroundStyle(HygurColors.textPrimary)
                        TextField("ex. gpt-4o, mistral-large…", text: $settings.defaultModel)
                            .textFieldStyle(.plain)
                            .font(HygurTypography.body)
                            .foregroundStyle(HygurColors.textPrimary)
                        Text("Affiché dans le sélecteur de modèle du chat")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textTertiary)
                    }
                    .padding(HygurSpacing.lg)

                    CardDivider()

                    VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                        HStack {
                            Text("Timeout réseau")
                                .font(HygurTypography.subheadline)
                                .foregroundStyle(HygurColors.textPrimary)
                            Spacer()
                            Text("\(Int(settings.timeout)) s")
                                .font(HygurTypography.captionMono)
                                .foregroundStyle(HygurColors.textSecondary)
                                .monospacedDigit()
                        }
                        Slider(value: $settings.timeout, in: 30...300, step: 10)
                            .tint(HygurColors.accent)
                        HStack {
                            Text("30 s").font(HygurTypography.caption).foregroundStyle(HygurColors.textTertiary)
                            Spacer()
                            Text("300 s").font(HygurTypography.caption).foregroundStyle(HygurColors.textTertiary)
                        }
                    }
                    .padding(HygurSpacing.lg)
                }
            }
        }
    }
}

// MARK: - Tab 4: Notifications

private struct NotificationsTab: View {
    @State private var dailyBrief: Bool = UserDefaults.standard.bool(forKey: "notify.dailyBrief")
    @State private var priorityMail: Bool = UserDefaults.standard.bool(forKey: "notify.priorityMail")

    var body: some View {
        TabScrollContainer {
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Alertes système")
                SettingsCard {
                    NotificationToggleRow(
                        title: "Brief quotidien",
                        description: "Notification quand le brief du jour est prêt",
                        icon: "sun.horizon",
                        isOn: $dailyBrief
                    )
                    .onChange(of: dailyBrief) { _, v in
                        UserDefaults.standard.set(v, forKey: "notify.dailyBrief")
                        if v { Task { await NotificationsService.shared.ensureAuthorization() } }
                    }
                    CardDivider()
                    NotificationToggleRow(
                        title: "Emails prioritaires",
                        description: "Notification à la réception d'un email prioritaire",
                        icon: "envelope.badge",
                        isOn: $priorityMail
                    )
                    .onChange(of: priorityMail) { _, v in
                        UserDefaults.standard.set(v, forKey: "notify.priorityMail")
                        if v { Task { await NotificationsService.shared.ensureAuthorization() } }
                    }
                }
            }
            Text("Les notifications système s'affichent en bannière. L'entrée dans la sidebar Activité reste disponible quelle que soit la configuration ci-dessus.")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
        }
    }
}

private struct NotificationToggleRow: View {
    let title: String
    let description: String
    let icon: String
    @Binding var isOn: Bool

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
            Image(systemName: icon)
                .font(.system(size: 16))
                .foregroundStyle(HygurColors.accent)
                .frame(width: 24, height: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(HygurTypography.subheadline).foregroundStyle(HygurColors.textPrimary)
                Text(description).font(HygurTypography.caption).foregroundStyle(HygurColors.textSecondary)
            }
            Spacer()
            Toggle("", isOn: $isOn).toggleStyle(.switch).labelsHidden()
        }
        .padding(HygurSpacing.lg)
    }
}

// MARK: - Tab 5: Système

private struct SystemTab: View {
    @Binding var showResetConfirmation: Bool
    @Binding var isResetting: Bool
    @Binding var resetMessage: String

    var body: some View {
        TabScrollContainer {
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Démarrage")
                SettingsCard {
                    CardRow(icon: "power.circle", title: "Lancer Hygur à la connexion",
                            subtitle: LaunchAgentService.shared.statusDescription) {
                        Toggle("", isOn: Binding(
                            get: { LaunchAgentService.shared.isRegistered },
                            set: { v in
                                try? v ? LaunchAgentService.shared.register()
                                       : LaunchAgentService.shared.unregister()
                            }
                        ))
                        .toggleStyle(.switch).labelsHidden()
                    }
                }
            }

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Processus sidecar")
                SettingsCard {
                    SidecarStatusRow().padding(HygurSpacing.lg)
                }
            }

            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Base de connaissance")
                SettingsCard {
                    CardRow(icon: "trash", iconColor: HygurColors.danger,
                            title: "Réinitialiser la base de connaissance",
                            subtitle: "Supprime tous les documents, chunks et embeddings") {
                        Button(role: .destructive) { showResetConfirmation = true } label: {
                            if isResetting { LoadingIndicator(style: .small) }
                            else { Text("Réinitialiser") }
                        }
                        .buttonStyle(.bordered).controlSize(.small).disabled(isResetting)
                    }
                    if !resetMessage.isEmpty {
                        CardDivider()
                        Text(resetMessage)
                            .font(HygurTypography.caption)
                            .foregroundStyle(resetMessage.contains("réussie") ? HygurColors.success : HygurColors.danger)
                            .padding(.horizontal, HygurSpacing.lg)
                            .padding(.vertical, HygurSpacing.md)
                    }
                }
            }
        }
    }
}

// MARK: - Tab 6: À propos

private struct AboutTab: View {
    let sidecarVersion: String

    var body: some View {
        TabScrollContainer {
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Mises à jour")
                UpdateCard()
            }
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Version")
                SettingsCard {
                    AboutRow(label: "Application", value: Bundle.main.appVersion)
                    CardDivider()
                    AboutRow(label: "Build", value: Bundle.main.buildNumber)
                    CardDivider()
                    AboutRow(label: "Sidecar", value: sidecarVersion)
                }
            }
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Ressources")
                SettingsCard {
                    Link(destination: URL(string: "https://github.com/hygurlabs/hygur")!) {
                        HStack(spacing: HygurSpacing.sm) {
                            Image(systemName: "arrow.up.right.square")
                                .font(.system(size: 14)).foregroundStyle(HygurColors.accent)
                            Text("Documentation & GitHub")
                                .font(HygurTypography.subheadline).foregroundStyle(HygurColors.accent)
                            Spacer()
                        }
                        .padding(HygurSpacing.lg)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }
}

private struct UpdateCard: View {
    @Environment(Updater.self) private var updater
    @State private var showReleaseNotes = false

    var body: some View {
        @Bindable var updater = updater
        SettingsCard {
            HStack {
                Text("Vérification automatique")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textPrimary)
                Spacer()
                Toggle("", isOn: $updater.autoCheckEnabled)
                    .toggleStyle(.switch)
                    .labelsHidden()
            }
            .padding(HygurSpacing.lg)

            CardDivider()

            HStack {
                Text("Dernière vérification")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textPrimary)
                Spacer()
                Text(lastCheckedLabel)
                    .font(HygurTypography.captionMono)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .padding(HygurSpacing.lg)

            CardDivider()

            statusSection
                .padding(HygurSpacing.lg)
        }
    }

    private var lastCheckedLabel: String {
        guard let date = updater.lastCheckedAt else { return "jamais" }
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .full
        formatter.locale = Locale(identifier: "fr_FR")
        return formatter.localizedString(for: date, relativeTo: Date())
    }

    @ViewBuilder
    private var statusSection: some View {
        switch updater.status {
        case .idle:
            statusRow(icon: nil, color: HygurColors.textSecondary, message: "Cliquez pour vérifier la disponibilité d'une mise à jour.") {
                checkButton(label: "Vérifier maintenant")
            }

        case .checking:
            statusRow(icon: nil, color: HygurColors.textSecondary, message: "Vérification en cours…") {
                LoadingIndicator(style: .small)
            }

        case .upToDate:
            statusRow(icon: "checkmark.circle.fill", color: HygurColors.success, message: "Hygur \(Bundle.main.appVersion) est à jour.") {
                checkButton(label: "Vérifier")
            }

        case .available(let release):
            VStack(alignment: .leading, spacing: HygurSpacing.md) {
                statusRow(icon: "arrow.down.circle.fill", color: HygurColors.accent, message: "Mise à jour disponible : \(release.name)") {
                    EmptyView()
                }
                if !release.body.isEmpty {
                    DisclosureGroup(isExpanded: $showReleaseNotes) {
                        ScrollView {
                            Markdown(release.body)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.vertical, HygurSpacing.sm)
                        }
                        .frame(maxHeight: 200)
                    } label: {
                        Text("Notes de version")
                            .font(HygurTypography.caption)
                            .foregroundStyle(HygurColors.textSecondary)
                    }
                }
                HStack(spacing: HygurSpacing.sm) {
                    Button {
                        Task { await updater.downloadAndInstall() }
                    } label: {
                        Text("Installer maintenant")
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(release.dmgAsset == nil)

                    Link(destination: release.htmlURL) {
                        Text("Voir sur GitHub")
                            .font(HygurTypography.caption)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(HygurColors.accent)

                    Spacer()
                }
                if release.dmgAsset == nil {
                    Text("Aucun DMG disponible pour cette version.")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.danger)
                }
            }

        case .downloading(let progress):
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                HStack {
                    Text("Téléchargement…")
                        .font(HygurTypography.subheadline)
                        .foregroundStyle(HygurColors.textPrimary)
                    Spacer()
                    Text("\(Int(progress * 100)) %")
                        .font(HygurTypography.captionMono)
                        .foregroundStyle(HygurColors.textSecondary)
                }
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
            }

        case .readyToInstall:
            statusRow(icon: "checkmark.circle.fill", color: HygurColors.success, message: "Téléchargement terminé. Prêt à installer.") {
                Button {
                    Task { await updater.downloadAndInstall() }
                } label: {
                    Text("Installer maintenant")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }

        case .installing:
            statusRow(icon: nil, color: HygurColors.textSecondary, message: "Installation en cours, l'application va redémarrer…") {
                LoadingIndicator(style: .small)
            }

        case .error(let message):
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                statusRow(icon: "exclamationmark.triangle.fill", color: HygurColors.danger, message: message) {
                    checkButton(label: "Réessayer")
                }
            }
        }
    }

    @ViewBuilder
    private func statusRow<Trailing: View>(
        icon: String?,
        color: Color,
        message: String,
        @ViewBuilder trailing: () -> Trailing
    ) -> some View {
        HStack(spacing: HygurSpacing.sm) {
            if let icon {
                Image(systemName: icon)
                    .font(.system(size: 14))
                    .foregroundStyle(color)
            }
            Text(message)
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textPrimary)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: HygurSpacing.sm)
            trailing()
        }
    }

    private func checkButton(label: String) -> some View {
        Button {
            Task { await updater.checkForUpdates() }
        } label: {
            Text(label)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
    }
}

private struct AboutRow: View {
    let label: String
    let value: String
    var body: some View {
        HStack {
            Text(label).font(HygurTypography.subheadline).foregroundStyle(HygurColors.textPrimary)
            Spacer()
            Text(value).font(HygurTypography.captionMono).foregroundStyle(HygurColors.textSecondary).monospacedDigit()
        }
        .padding(HygurSpacing.lg)
    }
}

// MARK: - Sidecar status row

private struct SidecarStatusRow: View {
    @Environment(SidecarSupervisor.self) private var supervisor

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            HStack(spacing: HygurSpacing.sm) {
                Circle()
                    .fill(supervisor.isRunning ? HygurColors.success : HygurColors.danger)
                    .frame(width: 8, height: 8)
                Text(statusLine)
                    .font(HygurTypography.callout)
                    .foregroundStyle(HygurColors.textPrimary)
                Spacer()
                Button("Voir les logs") { openLogs() }
                    .buttonStyle(.bordered).controlSize(.small)
                    .help("Ouvre sidecar.log dans Console.app")
                Button("Redémarrer") { Task { await supervisor.restart() } }
                    .buttonStyle(.bordered).controlSize(.small)
            }
            if let err = supervisor.lastError {
                Text(err).font(HygurTypography.caption).foregroundStyle(HygurColors.danger)
            }
            Text("Logs: \(supervisor.logPath.path)")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
                .textSelection(.enabled)
        }
    }

    private var statusLine: String {
        if !supervisor.isRunning { return "Sidecar arrêté (ou externe)" }
        let pidStr = supervisor.pid.map { String($0) } ?? "?"
        let uptime = supervisor.uptime.map(formatUptime) ?? "?"
        return "Sidecar actif · PID \(pidStr) · uptime \(uptime)"
    }

    private func formatUptime(_ secs: TimeInterval) -> String {
        let total = Int(secs)
        let h = total / 3600
        let m = (total % 3600) / 60
        if h > 0 { return "\(h) h \(m) m" }
        return "\(m) m"
    }

    /// Opens the rotating sidecar log inside Console.app — the standard macOS log
    /// viewer. We try Console first because it formats logs nicely and follows
    /// the file as it grows; if it isn't available for some reason we fall back
    /// to the system default for `.log` files (TextEdit) and finally to Finder.
    private func openLogs() {
        let logURL = supervisor.logPath
        let consoleURL = URL(fileURLWithPath: "/System/Applications/Utilities/Console.app")
        let cfg = NSWorkspace.OpenConfiguration()
        if FileManager.default.fileExists(atPath: consoleURL.path) {
            NSWorkspace.shared.open([logURL], withApplicationAt: consoleURL, configuration: cfg) { _, err in
                if err != nil {
                    NSWorkspace.shared.activateFileViewerSelecting([logURL])
                }
            }
            return
        }
        if !NSWorkspace.shared.open(logURL) {
            NSWorkspace.shared.activateFileViewerSelecting([logURL])
        }
    }
}

// MARK: - Preview

#Preview { SettingsView() }
