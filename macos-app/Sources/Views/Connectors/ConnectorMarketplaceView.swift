import SwiftUI

struct ConnectorMarketplaceView: View {
    @State private var viewModel = ConnectorMarketplaceViewModel()
    @State private var addInstanceFor: MarketplaceListing?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Marketplace")
                        .font(.title3)
                        .fontWeight(.semibold)
                    Text("Add connectors to expand Hygur's knowledge.")
                        .font(.callout)
                        .foregroundStyle(HygurColors.textSecondary)
                }
                Spacer()
                Button { dismiss() } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title3)
                        .foregroundStyle(HygurColors.textTertiary)
                }
                .buttonStyle(.plain)
            }
            .padding(HygurSpacing.lg)
            .background(.ultraThinMaterial)
            .shadow(color: .black.opacity(0.06), radius: 0.5, x: 0, y: 0.5)

            // Category filter chips
            if !viewModel.categories.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: HygurSpacing.xs) {
                        CategoryChip(label: "All", isSelected: viewModel.selectedCategory == nil) {
                            withAnimation(.spring(response: 0.25, dampingFraction: 0.8)) {
                                viewModel.selectedCategory = nil
                            }
                        }
                        ForEach(viewModel.categories, id: \.self) { cat in
                            CategoryChip(label: cat.capitalized, isSelected: viewModel.selectedCategory == cat) {
                                withAnimation(.spring(response: 0.25, dampingFraction: 0.8)) {
                                    viewModel.selectedCategory = (viewModel.selectedCategory == cat) ? nil : cat
                                }
                            }
                        }
                    }
                    .padding(.horizontal, HygurSpacing.lg)
                    .padding(.vertical, HygurSpacing.sm)
                }
                .background(HygurColors.surface)
                Divider()
            }

            // Grid
            if viewModel.isLoading && viewModel.listings.isEmpty {
                Spacer()
                ProgressView()
                Spacer()
            } else if viewModel.filteredListings.isEmpty {
                EmptyStateView(
                    icon: "puzzlepiece.extension",
                    title: "No connectors found",
                    subtitle: "Try selecting a different category."
                )
            } else {
                ScrollView {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 260, maximum: 340), spacing: HygurSpacing.md)], spacing: HygurSpacing.md) {
                        ForEach(viewModel.filteredListings) { listing in
                            MarketplaceTile(
                                listing: listing,
                                isInstalling: viewModel.installingID == listing.id,
                                onInstall: {
                                    Task { await viewModel.install(typeID: listing.typeName) }
                                },
                                onAddAccount: {
                                    addInstanceFor = listing
                                }
                            )
                        }
                    }
                    .padding(HygurSpacing.lg)
                }
            }
        }
        .frame(width: 680, height: 520)
        .background(Color(nsColor: .windowBackgroundColor))
        .errorBannerOverlay(Binding(
            get: { viewModel.error },
            set: { _ in viewModel.clearError() }
        ))
        .task { await viewModel.load() }
        .sheet(item: $addInstanceFor) { listing in
            AddConnectorInstanceSheet(typeID: listing.typeName, typeName: listing.displayName) { instanceID, displayName, settings in
                // Handled by caller (ConnectorsView)
                _ = (instanceID, displayName, settings)
            }
        }
    }
}

// MARK: - Category Chip

private struct CategoryChip: View {
    let label: String
    let isSelected: Bool
    let onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            Text(label)
                .font(.caption)
                .fontWeight(.medium)
                .padding(.horizontal, 10)
                .padding(.vertical, 4)
                .background(isSelected ? HygurColors.accent.opacity(0.15) : Color.secondary.opacity(0.10))
                .foregroundStyle(isSelected ? HygurColors.accent : HygurColors.textSecondary)
                .clipShape(Capsule())
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Marketplace Tile

private struct MarketplaceTile: View {
    let listing: MarketplaceListing
    let isInstalling: Bool
    let onInstall: () -> Void
    let onAddAccount: () -> Void

    @State private var isHovered = false

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack(spacing: HygurSpacing.sm) {
                ZStack {
                    RoundedRectangle(cornerRadius: HygurRadius.md)
                        .fill(listing.accentColor.opacity(0.15))
                        .frame(width: 40, height: 40)
                    Image(systemName: listing.iconName)
                        .font(.system(size: 18, weight: .medium))
                        .foregroundStyle(listing.accentColor)
                }

                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 4) {
                        Text(listing.displayName)
                            .font(.body)
                            .fontWeight(.medium)
                            .lineLimit(1)
                        if listing.verified {
                            Image(systemName: "checkmark.seal.fill")
                                .font(.caption2)
                                .foregroundStyle(HygurColors.accent)
                                .accessibilityLabel("Verified")
                        }
                    }
                    Text("v\(listing.version) · \(listing.author)")
                        .font(.caption)
                        .foregroundStyle(HygurColors.textTertiary)
                }

                Spacer()
            }

            Text(listing.description)
                .font(.callout)
                .foregroundStyle(HygurColors.textSecondary)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)

            Spacer(minLength: 0)

            HStack {
                // Category pills
                HStack(spacing: 4) {
                    ForEach(listing.categories.prefix(2), id: \.self) { cat in
                        Text(cat.capitalized)
                            .font(.caption2)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Color.secondary.opacity(0.10))
                            .foregroundStyle(HygurColors.textTertiary)
                            .clipShape(Capsule())
                    }
                }

                Spacer()

                actionButton
            }
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg)
                .fill(HygurColors.surface)
                .shadow(color: .black.opacity(isHovered ? 0.10 : 0.06), radius: isHovered ? 6 : 3, x: 0, y: isHovered ? 3 : 1)
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.lg)
                .strokeBorder(HygurColors.border, lineWidth: 0.5)
        )
        .scaleEffect(isHovered ? 1.01 : 1.0)
        .onHover { isHovered = $0 }
        .animation(.spring(response: 0.2, dampingFraction: 0.8), value: isHovered)
    }

    @ViewBuilder
    private var actionButton: some View {
        if isInstalling {
            ProgressView()
                .controlSize(.small)
        } else if listing.isInstalled && listing.multiInstance {
            Button("+ Account", action: onAddAccount)
                .buttonStyle(.bordered)
                .controlSize(.small)
        } else if listing.isInstalled {
            Label("Installed", systemImage: "checkmark")
                .font(.caption)
                .foregroundStyle(HygurColors.textSecondary)
        } else {
            Button("Install", action: onInstall)
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
        }
    }
}
