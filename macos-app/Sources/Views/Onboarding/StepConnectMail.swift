import SwiftUI

/// Step 3 of the onboarding flow — surface the available mail connectors and
/// let the user install one with a single click. The full credential setup
/// (OAuth, IMAP host/credentials, mailbox selection) lives in Settings →
/// Connectors and is intentionally out-of-scope here: the goal of this step
/// is *exposure*, not exhaustive configuration. Skip is always available.
struct StepConnectMail: View {
    @State private var viewModel = ConnectorMarketplaceViewModel()

    private var mailListings: [MarketplaceListing] {
        viewModel.listings.filter { listing in
            listing.categories.contains("email") || listing.categories.contains("mail")
        }
    }

    var body: some View {
        VStack(spacing: HygurSpacing.lg) {
            header

            if viewModel.isLoading && mailListings.isEmpty {
                Spacer()
                ProgressView()
                Spacer()
            } else if mailListings.isEmpty {
                emptyState
            } else {
                ScrollView {
                    LazyVStack(spacing: HygurSpacing.sm) {
                        ForEach(mailListings) { listing in
                            MailListingRow(
                                listing: listing,
                                isInstalling: viewModel.installingID == listing.typeName,
                                onInstall: {
                                    Task { await viewModel.install(typeID: listing.typeName) }
                                }
                            )
                        }
                    }
                    .padding(.horizontal, HygurSpacing.xxxl)
                    .padding(.bottom, HygurSpacing.lg)
                    .frame(maxWidth: 580)
                    .frame(maxWidth: .infinity)
                }
            }

            if let error = viewModel.error {
                errorBanner(error)
                    .padding(.horizontal, HygurSpacing.xxxl)
            }
        }
        .padding(.top, HygurSpacing.xl)
        .padding(.bottom, HygurSpacing.lg)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task { await viewModel.load() }
    }

    // MARK: - Layout

    private var header: some View {
        VStack(spacing: HygurSpacing.sm) {
            Image(systemName: "envelope.fill")
                .font(.system(size: 36, weight: .light))
                .foregroundStyle(HygurColors.accent)
            Text("Connect a mail account")
                .font(HygurTypography.title)
                .foregroundStyle(HygurColors.textPrimary)
            Text("Optional — install a mail connector now and finish the credential setup in Settings → Connectors when you have a moment.")
                .font(HygurTypography.body)
                .foregroundStyle(HygurColors.textSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 480)
        }
    }

    private var emptyState: some View {
        VStack(spacing: HygurSpacing.sm) {
            Spacer()
            Image(systemName: "envelope.open")
                .font(.system(size: 32, weight: .light))
                .foregroundStyle(HygurColors.textTertiary)
            Text("No mail connectors are reachable right now.")
                .font(HygurTypography.subheadline)
                .foregroundStyle(HygurColors.textSecondary)
            Text("Skip this step — you can connect later in Settings.")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textTertiary)
            Spacer()
        }
    }

    private func errorBanner(_ message: String) -> some View {
        HStack(spacing: HygurSpacing.sm) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(HygurColors.danger)
            Text(message)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Spacer()
            Button("Dismiss") { viewModel.clearError() }
                .buttonStyle(.borderless)
                .font(HygurTypography.caption)
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .fill(HygurColors.surface)
        )
    }
}

// MARK: - Row

private struct MailListingRow: View {
    let listing: MarketplaceListing
    let isInstalling: Bool
    let onInstall: () -> Void

    var body: some View {
        HStack(spacing: HygurSpacing.md) {
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
                        .font(HygurTypography.subheadline.weight(.semibold))
                        .foregroundStyle(HygurColors.textPrimary)
                    if listing.verified {
                        Image(systemName: "checkmark.seal.fill")
                            .font(.caption2)
                            .foregroundStyle(HygurColors.accent)
                    }
                }
                Text(listing.description)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer()

            actionButton
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .strokeBorder(HygurColors.border, lineWidth: 0.5)
        )
    }

    @ViewBuilder
    private var actionButton: some View {
        if isInstalling {
            ProgressView().controlSize(.small)
        } else if listing.isInstalled {
            HStack(spacing: HygurSpacing.xs) {
                Image(systemName: "checkmark.seal.fill")
                    .foregroundStyle(HygurColors.success)
                Text("Installed")
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
        } else {
            Button("Install", action: onInstall)
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .tint(HygurColors.accent)
        }
    }
}
