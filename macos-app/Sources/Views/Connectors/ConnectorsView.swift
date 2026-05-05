import SwiftUI

struct ConnectorsView: View {
    @State private var viewModel = ConnectorsViewModel()
    @State private var selectedId: String?
    @State private var addInstanceConnector: ConnectorSummary?

    var body: some View {
        HStack(spacing: 0) {
            sidebarPane
            Divider()
            detailPane
        }
        .navigationTitle("Connectors")
        .errorBannerOverlay(Binding(
            get: { viewModel.error },
            set: { _ in viewModel.clearError() }
        ))
        .sheet(item: $addInstanceConnector) { connector in
            AddConnectorInstanceSheet(typeID: connector.info.id, typeName: connector.info.name) { instanceID, displayName, settings in
                Task {
                    await viewModel.createInstance(typeID: connector.info.id, instanceID: instanceID, displayName: displayName, settings: settings)
                }
            }
        }
    }

    // MARK: - Sidebar

    private var sidebarPane: some View {
        Group {
            if viewModel.isLoading && viewModel.connectors.isEmpty {
                LoadingIndicator(style: .large)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if viewModel.connectors.isEmpty {
                EmptyStateView(
                    icon: "puzzlepiece.extension",
                    title: "No connectors",
                    subtitle: "No connectors are configured yet."
                )
            } else {
                List(viewModel.connectors, selection: $selectedId) { connector in
                    ConnectorRow(connector: connector, onAddInstance: connector.info.multiInstance ? {
                        addInstanceConnector = connector
                    } : nil)
                    .tag(connector.id)
                }
                .listStyle(.sidebar)
            }
        }
        .frame(width: 220)
        .task { await viewModel.load() }
        .onChange(of: selectedId) { _, newId in
            guard let id = newId else { return }
            Task { await viewModel.loadDetail(id: id) }
        }
    }

    // MARK: - Detail

    @ViewBuilder
    private var detailPane: some View {
        if let connector = viewModel.selectedConnector {
            ConnectorDetailView(connector: connector, viewModel: viewModel)
                .id(connector.info.id)
        } else {
            EmptyStateView(
                icon: "puzzlepiece.extension",
                title: "Select a connector",
                subtitle: "Choose a connector from the list to view its details."
            )
        }
    }
}

// MARK: - Connector Row

struct ConnectorRow: View {
    let connector: ConnectorSummary
    var onAddInstance: (() -> Void)?

    var body: some View {
        HStack(spacing: HygurSpacing.sm + 2) {
            ZStack {
                RoundedRectangle(cornerRadius: HygurRadius.md)
                    .fill(connector.info.accentColor.opacity(0.15))
                    .frame(width: 32, height: 32)
                Image(systemName: connector.info.icon)
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(connector.info.accentColor)
                    .accessibilityLabel(connector.info.name)
            }

            VStack(alignment: .leading, spacing: HygurSpacing.xxs) {
                Text(connector.info.name)
                    .font(HygurTypography.body)
                    .lineLimit(1)
                Text(connector.enabled ? "Enabled" : "Disabled")
                    .font(HygurTypography.caption)
                    .foregroundStyle(connector.enabled ? HygurColors.textSecondary : HygurColors.textTertiary)
            }

            Spacer()

            if let onAddInstance {
                Button {
                    onAddInstance()
                } label: {
                    Image(systemName: "plus")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(HygurColors.textSecondary)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Add \(connector.info.name) account")
            }

            Circle()
                .fill(connector.health.statusEnum.color)
                .frame(width: 8, height: 8)
                .accessibilityLabel(connector.health.statusEnum.label)
                .accessibilityHidden(false)
        }
        .padding(.vertical, HygurSpacing.xxs)
    }
}

#Preview {
    ConnectorsView()
}
