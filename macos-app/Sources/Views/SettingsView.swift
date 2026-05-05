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
            minWidth: 520,
            idealWidth: 560,
            maxWidth: .infinity,
            minHeight: 480,
            idealHeight: 540,
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

    // MARK: - Connection test

    private func testConnection() async {
        guard settings.isValidURL else {
            connectionStatus = .disconnected
            return
        }

        isTestingConnection = true
        connectionStatus = .testing

        do {
            let sidecar = SidecarService(baseURL: settings.sidecarURLValue!)
            let healthResponse = try await sidecar.health()
            connectionStatus = healthResponse.status == "ok" ? .connected : .disconnected
            sidecarVersion = healthResponse.version
        } catch {
            connectionStatus = .disconnected
            sidecarVersion = "—"
        }

        isTestingConnection = false
    }

    // MARK: - Token loading

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
            tokenMessage = "Impossible de lire ~/.hygur/.hygur-token : \(error.localizedDescription)"
        }
    }

    // MARK: - Knowledge base reset

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

// MARK: - Connection Status

private enum ConnectionStatus {
    case unknown
    case testing
    case connected
    case disconnected
}

// MARK: - Token Status

private enum TokenStatus {
    case unknown
    case valid
    case missing
    case invalid
}

// MARK: - Shared card container

private struct SettingsCard<Content: View>: View {
    let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            content()
        }
        .background(HygurColors.surface, in: RoundedRectangle(cornerRadius: HygurRadius.md))
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .stroke(HygurColors.border, lineWidth: 1)
        )
    }
}

// MARK: - Card row divider

private struct CardDivider: View {
    var body: some View {
        Divider()
            .background(HygurColors.border)
            .padding(.leading, HygurSpacing.lg)
    }
}

// MARK: - Section header

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

// MARK: - Tab scroll container

private struct TabScrollContainer<Content: View>: View {
    let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        ScrollView(.vertical, showsIndicators: false) {
            VStack(alignment: .leading, spacing: HygurSpacing.xxl) {
                content()
            }
            .padding(HygurSpacing.xxl)
        }
        .background(HygurColors.background)
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
            // Authentication card
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Authentification")
                SettingsCard {
                    HStack(spacing: HygurSpacing.sm) {
                        tokenStatusBadge
                        Spacer()
                        Button("Charger le token") {
                            loadTokenFromSidecar()
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
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

            // Connection card
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "URL du sidecar")
                SettingsCard {
                    TextField("URL du sidecar", text: $settings.sidecarURL)
                        .textFieldStyle(.plain)
                        .font(HygurTypography.body)
                        .padding(HygurSpacing.lg)

                    CardDivider()

                    HStack(spacing: HygurSpacing.sm) {
                        connectionStatusBadge
                        Spacer()
                        Button("Tester la connexion") {
                            Task { await testConnection() }
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
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

    @ViewBuilder
    private var connectionStatusBadge: some View {
        HStack(spacing: HygurSpacing.xs) {
            switch connectionStatus {
            case .unknown:
                Image(systemName: "circle")
                    .foregroundStyle(HygurColors.textTertiary)
                Text("Non testé")
                    .foregroundStyle(HygurColors.textSecondary)
            case .testing:
                LoadingIndicator(style: .small)
                Text("Test en cours…")
                    .foregroundStyle(HygurColors.textSecondary)
            case .connected:
                Image(systemName: "checkmark.circle.fill")
                    .foregroundStyle(HygurColors.success)
                Text("Connecté")
                    .foregroundStyle(HygurColors.success)
            case .disconnected:
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(HygurColors.danger)
                Text("Inaccessible")
                    .foregroundStyle(HygurColors.danger)
            }
        }
        .font(HygurTypography.caption)
    }

    @ViewBuilder
    private var tokenStatusBadge: some View {
        HStack(spacing: HygurSpacing.xs) {
            switch tokenStatus {
            case .unknown:
                Image(systemName: "key")
                    .foregroundStyle(HygurColors.textTertiary)
                Text("Non vérifié")
                    .foregroundStyle(HygurColors.textSecondary)
            case .valid:
                Image(systemName: "key.fill")
                    .foregroundStyle(HygurColors.success)
                Text("Token chargé")
                    .foregroundStyle(HygurColors.success)
            case .missing:
                Image(systemName: "key.slash")
                    .foregroundStyle(HygurColors.warning)
                Text("Aucun token")
                    .foregroundStyle(HygurColors.warning)
            case .invalid:
                Image(systemName: "exclamationmark.triangle")
                    .foregroundStyle(HygurColors.danger)
                Text("Token invalide")
                    .foregroundStyle(HygurColors.danger)
            }
        }
        .font(HygurTypography.caption)
    }
}

// MARK: - Tab 2: Modèle

private struct ModelTab: View {
    @ObservedObject var settings: AppPreferences

    var body: some View {
        TabScrollContainer {
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Modèle LLM")
                SettingsCard {
                    VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                        Text("Modèle par défaut")
                            .font(HygurTypography.subheadline)
                            .foregroundStyle(HygurColors.textPrimary)
                        TextField("ex. gpt-4o, mistral-large…", text: $settings.defaultModel)
                            .textFieldStyle(.plain)
                            .font(HygurTypography.body)
                            .foregroundStyle(HygurColors.textPrimary)
                    }
                    .padding(HygurSpacing.lg)

                    CardDivider()

                    VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                        HStack {
                            Text("Timeout")
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
                            Text("30 s")
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textTertiary)
                            Spacer()
                            Text("300 s")
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textTertiary)
                        }
                    }
                    .padding(HygurSpacing.lg)
                }
            }
        }
    }
}

// MARK: - Tab 3: Notifications

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
                    .onChange(of: dailyBrief) { _, newValue in
                        UserDefaults.standard.set(newValue, forKey: "notify.dailyBrief")
                        if newValue {
                            Task { await NotificationsService.shared.ensureAuthorization() }
                        }
                    }

                    CardDivider()

                    NotificationToggleRow(
                        title: "Emails prioritaires",
                        description: "Notification à la réception d'un email prioritaire",
                        icon: "envelope.badge",
                        isOn: $priorityMail
                    )
                    .onChange(of: priorityMail) { _, newValue in
                        UserDefaults.standard.set(newValue, forKey: "notify.priorityMail")
                        if newValue {
                            Task { await NotificationsService.shared.ensureAuthorization() }
                        }
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
                Text(title)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textPrimary)
                Text(description)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }

            Spacer()

            Toggle("", isOn: $isOn)
                .toggleStyle(.switch)
                .labelsHidden()
        }
        .padding(HygurSpacing.lg)
    }
}

// MARK: - Tab 4: Système

private struct SystemTab: View {
    @Binding var showResetConfirmation: Bool
    @Binding var isResetting: Bool
    @Binding var resetMessage: String

    var body: some View {
        TabScrollContainer {
            // Launch at login
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Démarrage")
                SettingsCard {
                    HStack(spacing: HygurSpacing.md) {
                        Image(systemName: "power.circle")
                            .font(.system(size: 16))
                            .foregroundStyle(HygurColors.accent)
                            .frame(width: 24, height: 24)

                        VStack(alignment: .leading, spacing: 2) {
                            Text("Lancer Hygur à la connexion")
                                .font(HygurTypography.subheadline)
                                .foregroundStyle(HygurColors.textPrimary)
                            Text(LaunchAgentService.shared.statusDescription)
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textSecondary)
                        }

                        Spacer()

                        Toggle("", isOn: Binding(
                            get: { LaunchAgentService.shared.isRegistered },
                            set: { newValue in
                                do {
                                    if newValue {
                                        try LaunchAgentService.shared.register()
                                    } else {
                                        try LaunchAgentService.shared.unregister()
                                    }
                                } catch {
                                    UserDefaults.standard.set(
                                        error.localizedDescription,
                                        forKey: "system.lastLoginItemError"
                                    )
                                }
                            }
                        ))
                        .toggleStyle(.switch)
                        .labelsHidden()
                    }
                    .padding(HygurSpacing.lg)
                }
            }

            // Sidecar status
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Processus sidecar")
                SettingsCard {
                    SidecarStatusRow()
                        .padding(HygurSpacing.lg)
                }
            }

            // Knowledge base reset
            VStack(alignment: .leading, spacing: HygurSpacing.sm) {
                SettingsSectionHeader(title: "Base de connaissance")
                SettingsCard {
                    HStack(spacing: HygurSpacing.md) {
                        Image(systemName: "trash")
                            .font(.system(size: 16))
                            .foregroundStyle(HygurColors.danger)
                            .frame(width: 24, height: 24)

                        VStack(alignment: .leading, spacing: 2) {
                            Text("Réinitialiser la base de connaissance")
                                .font(HygurTypography.subheadline)
                                .foregroundStyle(HygurColors.textPrimary)
                            Text("Supprime tous les documents, chunks et embeddings")
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textSecondary)
                        }

                        Spacer()

                        Button(role: .destructive) {
                            showResetConfirmation = true
                        } label: {
                            if isResetting {
                                LoadingIndicator(style: .small)
                            } else {
                                Text("Réinitialiser")
                            }
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                        .disabled(isResetting)
                    }
                    .padding(HygurSpacing.lg)

                    if !resetMessage.isEmpty {
                        CardDivider()
                        Text(resetMessage)
                            .font(HygurTypography.caption)
                            .foregroundStyle(
                                resetMessage.contains("réussie") ? HygurColors.success : HygurColors.danger
                            )
                            .padding(.horizontal, HygurSpacing.lg)
                            .padding(.vertical, HygurSpacing.md)
                    }
                }
            }
        }
    }
}

// MARK: - Tab 5: À propos

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
                    Link(destination: URL(string: "https://github.com/hygur/hygur")!) {
                        HStack(spacing: HygurSpacing.sm) {
                            Image(systemName: "arrow.up.right.square")
                                .font(.system(size: 14))
                                .foregroundStyle(HygurColors.accent)
                            Text("Documentation & GitHub")
                                .font(HygurTypography.subheadline)
                                .foregroundStyle(HygurColors.accent)
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
            Text(label)
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textPrimary)
            Spacer()
            Text(value)
                .font(HygurTypography.captionMono)
                .foregroundStyle(HygurColors.textSecondary)
                .monospacedDigit()
        }
        .padding(HygurSpacing.lg)
    }
}

// MARK: - Sidecar status row (System tab)

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
                Button("Redémarrer") {
                    Task { await supervisor.restart() }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
            if let err = supervisor.lastError {
                Text(err)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.danger)
            }
        }
    }

    private var statusLine: String {
        if !supervisor.isRunning {
            return "Sidecar arrêté (ou externe)"
        }
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

#Preview {
    SettingsView()
}
