import SwiftUI

struct ConnectorHealthCard: View {
    let health: ConnectorHealth

    /// Decides the dot color and label using `briefReason` when available;
    /// falls back to the status enum for connectors that do not classify
    /// errors (notes, files, …).
    private var renderedColor: Color {
        if !health.briefReason.isEmpty {
            return BriefReason(rawValue: health.briefReason).isHealthy
                ? HygurColors.success
                : HygurColors.danger
        }
        return health.statusEnum.color
    }

    private var renderedLabel: String {
        if !health.briefReason.isEmpty {
            return BriefReason(rawValue: health.briefReason).isHealthy
                ? "Connecté"
                : "Déconnecté"
        }
        return health.statusEnum.label
    }

    var body: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.lg - 2) {
            // Status row
            HStack(spacing: HygurSpacing.sm) {
                Image(systemName: health.statusEnum.systemImage)
                    .foregroundStyle(renderedColor)
                    .font(.system(size: 16, weight: .medium))
                    .accessibilityLabel(renderedLabel)
                Text(renderedLabel)
                    .font(HygurTypography.subheadline)
                    .fontWeight(.semibold)
                    .foregroundStyle(renderedColor)

                Spacer()

                // Show the localized brief reason next to the dot. We never
                // surface the raw `health.message` to avoid leaking
                // upstream-library error text to the user.
                if !health.briefReason.isEmpty,
                   !BriefReason(rawValue: health.briefReason).isHealthy {
                    Text(health.briefReasonLocalized)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .lineLimit(1)
                }
            }

            Divider()

            // Metrics row
            HStack(spacing: 0) {
                metricCell(
                    value: "\(health.itemCount)",
                    label: "Items",
                    icon: "doc.text"
                )
                .accessibilityLabel("\(health.itemCount) items indexed")

                Divider()
                    .frame(height: 36)

                metricCell(
                    value: "\(health.errorCount)",
                    label: "Errors",
                    icon: "exclamationmark.triangle",
                    valueColor: health.errorCount > 0 ? HygurColors.warning : HygurColors.textPrimary
                )
                .accessibilityLabel("\(health.errorCount) errors")

                Divider()
                    .frame(height: 36)

                lastSyncCell
            }
        }
        .padding(HygurSpacing.lg - 2)
        .background(.ultraThinMaterial)
        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.lg))
    }

    // MARK: - Sub-cells

    private func metricCell(
        value: String,
        label: String,
        icon: String,
        valueColor: Color = HygurColors.textPrimary
    ) -> some View {
        VStack(spacing: HygurSpacing.xs) {
            Image(systemName: icon)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
                .accessibilityHidden(true)
            Text(value)
                .font(.system(.headline, design: .rounded))
                .foregroundStyle(valueColor)
            Text(label)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
        }
        .frame(maxWidth: .infinity)
    }

    private var lastSyncCell: some View {
        VStack(spacing: HygurSpacing.xs) {
            Image(systemName: "clock")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
                .accessibilityLabel("Last sync")

            if let date = effectiveLastSync {
                Text(date, format: .relative(presentation: .named))
                    .font(.system(.headline, design: .rounded))
                    .lineLimit(1)
            } else {
                Text("Never")
                    .font(.system(.headline, design: .rounded))
                    .foregroundStyle(HygurColors.textSecondary)
            }

            Text("Last Sync")
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
        }
        .frame(maxWidth: .infinity)
    }

    // MARK: - Helpers

    private var effectiveLastSync: Date? {
        guard let raw = health.lastSync, !raw.isEmpty else { return nil }
        guard let date = parseISO8601(raw) else { return nil }
        // Treat Go's zero time.Time ("0001-01-01T00:00:00Z") as "never synced".
        if date.timeIntervalSince1970 < 946_684_800 { return nil } // before 2000-01-01
        return date
    }

    private func parseISO8601(_ string: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: string) { return date }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: string)
    }
}

#Preview("Connected") {
    ConnectorHealthCard(
        health: try! JSONDecoder().decode(
            ConnectorHealth.self,
            from: #"{"status":"healthy","last_sync":"2024-01-01T00:00:00Z","item_count":42,"error_count":0,"last_error":"","message":"","brief_reason":"ok"}"#.data(using: .utf8)!
        )
    )
    .padding()
}

#Preview("Auth issue") {
    ConnectorHealthCard(
        health: try! JSONDecoder().decode(
            ConnectorHealth.self,
            from: #"{"status":"unhealthy","last_sync":null,"item_count":0,"error_count":1,"last_error":"","message":"","brief_reason":"auth_issue"}"#.data(using: .utf8)!
        )
    )
    .padding()
}
