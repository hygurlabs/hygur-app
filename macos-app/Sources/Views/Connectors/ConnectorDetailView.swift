import SwiftUI

enum ConnectorDetailTab {
    case configuration, threads
}

struct ConnectorDetailView: View {
    let connector: ConnectorDetail
    @Bindable var viewModel: ConnectorsViewModel

    @State private var isSyncing = false
    @State private var syncError: String?
    @State private var syncResultMessage: String? = nil
    @State private var selectedTab: ConnectorDetailTab = .configuration
    @State private var eventStreamTask: Task<Void, Never>? = nil

    var supportsThreads: Bool { connector.info.id == "mail" }

    var body: some View {
        VStack(spacing: 0) {
            if supportsThreads {
                Picker("View", selection: $selectedTab) {
                    Text("Configuration").tag(ConnectorDetailTab.configuration)
                    Text("Threads").tag(ConnectorDetailTab.threads)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .padding(.horizontal, HygurSpacing.xl)
                .padding(.top, HygurSpacing.lg)
                .padding(.bottom, HygurSpacing.sm)
            }

            if selectedTab == .configuration || !supportsThreads {
                configurationContent
            } else {
                EmailThreadsView()
            }
        }
        .navigationTitle(connector.info.name)
        .task(id: connector.info.id) {
            // Push first so the sidecar can re-init with credentials, then start
            // the long-running health poll. The previous order let the polling
            // loop block the push call indefinitely.
            await viewModel.pushSecretsToSidecar(connectorId: connector.info.id, schema: connector.configSchema)

            // For the mail connector, run a live verify on each configured
            // account before showing the health card. The 30 s server-side
            // cache prevents quota burn when the user toggles the screen
            // rapidly. We swallow errors — individual account brief reasons
            // are surfaced through the health endpoint payload itself.
            if connector.info.id == "mail" {
                if let accounts = try? await viewModel.service.listMailAccounts() {
                    await withTaskGroup(of: Void.self) { group in
                        for account in accounts {
                            group.addTask {
                                _ = try? await viewModel.service.verifyMailAccount(accountId: account.accountId)
                            }
                        }
                    }
                }
            }

            await viewModel.startHealthPolling(id: connector.info.id)
        }
        .onAppear {
            startEventStream()
        }
        .onDisappear {
            eventStreamTask?.cancel()
            eventStreamTask = nil
        }
        .errorBannerOverlay(Binding(
            get: { syncError },
            set: { _ in syncError = nil }
        ))
    }

    // MARK: - Configuration Content

    private var configurationContent: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: HygurSpacing.xl) {
                headerSection
                ConnectorHealthCard(health: connector.health)
                configSection
                actionBar

                if let msg = syncResultMessage {
                    Text(msg)
                        .font(HygurTypography.caption)
                        .padding(.horizontal, HygurSpacing.sm + 2)
                        .padding(.vertical, HygurSpacing.xs + 2)
                        .background(HygurColors.success.opacity(0.15))
                        .foregroundStyle(HygurColors.success)
                        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
                        .transition(.opacity)
                }
            }
            .padding(HygurSpacing.xl)
            .frame(maxWidth: 560)
            .frame(maxWidth: .infinity, alignment: .topLeading)
        }
    }

    // MARK: - Header

    private var headerSection: some View {
        HStack(spacing: HygurSpacing.lg - 2) {
            ZStack {
                RoundedRectangle(cornerRadius: HygurRadius.lg)
                    .fill(connector.info.accentColor.opacity(0.15))
                    .frame(width: 52, height: 52)
                Image(systemName: connector.info.icon)
                    .font(.system(size: 24, weight: .medium))
                    .foregroundStyle(connector.info.accentColor)
                    .accessibilityHidden(true)
            }

            VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                Text(connector.info.name)
                    .font(.title2)
                    .fontWeight(.semibold)
                    .foregroundStyle(HygurColors.textPrimary)

                Text(connector.info.description)
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
                    .lineLimit(2)
            }

            Spacer()

            // Status badge
            HStack(spacing: HygurSpacing.xs + 2) {
                Circle()
                    .fill(connector.health.statusEnum.color)
                    .frame(width: 8, height: 8)
                Text(connector.health.statusEnum.label)
                    .font(HygurTypography.subheadline)
                    .fontWeight(.medium)
                    .foregroundStyle(connector.health.statusEnum.color)
            }
            .padding(.horizontal, HygurSpacing.sm + 2)
            .padding(.vertical, HygurSpacing.xs + 2)
            .background(
                Capsule()
                    .fill(connector.health.statusEnum.color.opacity(0.12))
            )
        }
    }

    // MARK: - Config section

    private var configSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            Text("Configuration")
                .font(HygurTypography.headline)
                .foregroundStyle(HygurColors.textPrimary)

            ConnectorConfigForm(
                schema: connector.configSchema,
                config: connector.config,
                connectorId: connector.info.id,
                viewModel: viewModel
            ) { updatedSettings in
                let updatedConfig = ConnectorConfig(
                    enabled: connector.config.enabled,
                    settings: updatedSettings,
                    schedule: connector.config.schedule
                )
                await viewModel.saveConfig(id: connector.info.id, config: updatedConfig, schema: connector.configSchema)
            }
        }
    }

    // MARK: - Action bar

    private var actionBar: some View {
        HStack(spacing: HygurSpacing.md) {
            if connector.config.enabled {
                Button {
                    Task { await viewModel.disable(id: connector.info.id) }
                } label: {
                    Label("Disable", systemImage: "pause.circle")
                }
                .buttonStyle(.bordered)
            } else {
                Button {
                    Task { await viewModel.enable(id: connector.info.id) }
                } label: {
                    Label("Enable", systemImage: "play.circle")
                }
                .buttonStyle(.borderedProminent)
            }

            if connector.capabilities.canSync {
                Button {
                    triggerSync()
                } label: {
                    if isSyncing {
                        HStack(spacing: HygurSpacing.xs + 2) {
                            LoadingIndicator(style: .small)
                            Text("Syncing...")
                        }
                    } else {
                        Label("Sync Now", systemImage: "arrow.triangle.2.circlepath")
                    }
                }
                .buttonStyle(.bordered)
                .disabled(isSyncing)
            }

            Spacer()

            Text("v\(connector.info.version)")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
        }
    }

    // MARK: - Helpers

    private func triggerSync() {
        isSyncing = true
        syncError = nil
        Task {
            do {
                try await viewModel.sync(id: connector.info.id)
                // 202 returned: sync is running in background.
                // isSyncing stays true — the event stream will clear it.
            } catch let error as SidecarError {
                isSyncing = false
                if case .httpError(let code) = error, code == 409 {
                    syncError = "A sync is already in progress."
                } else {
                    syncError = error.localizedDescription
                }
            } catch {
                isSyncing = false
                syncError = error.localizedDescription
            }
        }
    }

    private func startEventStream() {
        eventStreamTask?.cancel()
        let connectorID = connector.info.id
        eventStreamTask = Task {
            let svc = viewModel.service
            do {
                for try await event in await svc.streamEvents() {
                    guard event.source == connectorID || event.source == "mail" else { continue }
                    guard !Task.isCancelled else { break }
                    await MainActor.run {
                        if event.isSyncRunning {
                            isSyncing = true
                        } else if event.isSyncDone {
                            isSyncing = false
                            if event.status == "completed" {
                                syncResultMessage = event.message ?? "Sync completed"
                                Task {
                                    try? await Task.sleep(for: .seconds(5))
                                    syncResultMessage = nil
                                }
                            } else if event.status == "failed" {
                                syncError = event.message ?? "Sync failed"
                            }
                        }
                    }
                }
            } catch {
                // Stream disconnected; spinner cleared to avoid stuck state.
                await MainActor.run { isSyncing = false }
            }
        }
    }
}
