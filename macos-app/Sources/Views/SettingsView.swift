import SwiftUI

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

    var body: some View {
        TabView {
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

            LMStudioTab()
                .tabItem { Label("Local LLM", systemImage: "server.rack") }

            ModelTab(settings: settings)
                .tabItem { Label("Modèle", systemImage: "cpu") }

            NotificationsTab()
                .tabItem { Label("Notifications", systemImage: "bell") }

            SystemTab(
                showResetConfirmation: $showResetConfirmation,
                isResetting: $isResetting,
                resetMessage: $resetMessage
            )
            .tabItem { Label("Système", systemImage: "gearshape") }

            AboutTab(sidecarVersion: sidecarVersion)
                .tabItem { Label("À propos", systemImage: "info.circle") }
        }
        .frame(
            minWidth: 560,
            idealWidth: 600,
            maxWidth: .infinity,
            minHeight: 500,
            idealHeight: 560,
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

    private func testConnection() async {
        guard settings.isValidURL else { connectionStatus = .disconnected; return }
        isTestingConnection = true
        connectionStatus = .testing
        do {
            let sidecar = SidecarService(baseURL: settings.sidecarURLValue!)
            let health = try await sidecar.health()
            connectionStatus = health.status == "ok" ? .connected : .disconnected
            sidecarVersion = health.version
        } catch {
            connectionStatus = .disconnected
            sidecarVersion = "—"
        }
        isTestingConnection = false
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

    private enum SaveStatus: Equatable {
        case idle, saving, saved, error(String)
    }

    private let sidecar = SidecarService.fromSettings()
    private let logLevels = ["debug", "info", "warn", "error"]

    var body: some View {
        TabScrollContainer {
            if isLoading {
                HStack { Spacer(); LoadingIndicator(style: .small); Spacer() }
                    .padding(.top, HygurSpacing.xxl)
            } else {
                lmStudioSection
                retrievalSection
                briefSection
                loggingSection
                saveBar
            }
        }
        .task { await loadConfig() }
    }

    // MARK: - Sections

    private var lmStudioSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            SettingsSectionHeader(title: "Local LLM — Endpoints")
            SettingsCard {
                LabeledURLField(
                    label: "Inference",
                    placeholder: "http://192.168.x.x:8082",
                    hint: "Chat completions & model listing (LM Studio, Ollama, llama.cpp…)",
                    text: $inferenceURL
                )
                CardDivider()
                LabeledURLField(
                    label: "Embeddings",
                    placeholder: "http://192.168.x.x:8081",
                    hint: "Leave empty to reuse the inference URL",
                    text: $embeddingURL
                )
                CardDivider()
                LabeledTextField(
                    label: "Chat model",
                    placeholder: "e.g. mistral-7b-instruct",
                    hint: "Default model when none is specified in the request",
                    text: $modelDefault
                )
                CardDivider()
                LabeledTextField(
                    label: "Embed model",
                    placeholder: "e.g. text-embedding-nomic-embed-text-v1.5",
                    hint: "Leave empty to use the server default",
                    text: $embeddingModel
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
                case .saved:
                    HStack(spacing: HygurSpacing.xs) {
                        Image(systemName: "checkmark.circle.fill")
                            .foregroundStyle(HygurColors.success)
                        Text("Enregistré — redémarrez le sidecar pour appliquer")
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

    private func saveConfig() async {
        isSaving = true
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
            saveStatus = .saved
        } catch {
            saveStatus = .error(error.localizedDescription)
        }
        isSaving = false
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
                Button("Redémarrer") { Task { await supervisor.restart() } }
                    .buttonStyle(.bordered).controlSize(.small)
            }
            if let err = supervisor.lastError {
                Text(err).font(HygurTypography.caption).foregroundStyle(HygurColors.danger)
            }
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
}

// MARK: - Preview

#Preview { SettingsView() }
