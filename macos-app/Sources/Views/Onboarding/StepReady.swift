import SwiftUI

/// Step 5 — onboarding recap. Reads back the state the user assembled across
/// the previous steps (model, mail connectors, indexed documents) so they
/// land in the chat with a concrete picture of what's wired up. The footer
/// "Start chatting" CTA owns the dismissal.
struct StepReady: View {
    @State private var modelName: String?
    @State private var modelURL: String?
    @State private var mailConnectorCount: Int = 0
    @State private var documentCount: Int = 0
    @State private var isLoading: Bool = true

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        VStack(spacing: HygurSpacing.lg) {
            header

            if isLoading {
                ProgressView()
                    .padding(.vertical, HygurSpacing.xxl)
            } else {
                VStack(spacing: HygurSpacing.sm) {
                    summaryRow(
                        icon: "cpu",
                        title: "AI model",
                        value: modelName ?? "Not configured",
                        subtitle: modelURL,
                        isConfigured: modelName != nil
                    )
                    summaryRow(
                        icon: "envelope.fill",
                        title: "Mail connectors",
                        value: mailConnectorCount == 0
                            ? "None installed"
                            : "\(mailConnectorCount) installed",
                        subtitle: mailConnectorCount == 0
                            ? "Connect later from Settings → Connectors."
                            : "Finish credential setup in Settings → Connectors.",
                        isConfigured: mailConnectorCount > 0
                    )
                    summaryRow(
                        icon: "doc.on.doc.fill",
                        title: "Knowledge base",
                        value: documentCount == 0
                            ? "Empty"
                            : "\(documentCount) document\(documentCount == 1 ? "" : "s")",
                        subtitle: documentCount == 0
                            ? "Drop files anytime — the Knowledge Base view accepts drag & drop."
                            : "Indexing runs in the background.",
                        isConfigured: documentCount > 0
                    )
                }
                .padding(.horizontal, HygurSpacing.xxxl)
                .frame(maxWidth: 580)
            }

            Spacer()
        }
        .padding(.top, HygurSpacing.xl)
        .padding(.bottom, HygurSpacing.lg)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task { await loadSummary() }
    }

    // MARK: - Layout

    private var header: some View {
        VStack(spacing: HygurSpacing.sm) {
            Image(systemName: "checkmark.seal.fill")
                .font(.system(size: 36, weight: .light))
                .foregroundStyle(HygurColors.success)
            Text("You're ready")
                .font(HygurTypography.title)
                .foregroundStyle(HygurColors.textPrimary)
            Text("Here's what's wired up so far. You can change any of it later in Settings.")
                .font(HygurTypography.body)
                .foregroundStyle(HygurColors.textSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 480)
        }
    }

    private func summaryRow(
        icon: String,
        title: String,
        value: String,
        subtitle: String?,
        isConfigured: Bool
    ) -> some View {
        HStack(spacing: HygurSpacing.md) {
            ZStack {
                RoundedRectangle(cornerRadius: HygurRadius.md)
                    .fill((isConfigured ? HygurColors.accent : HygurColors.textTertiary).opacity(0.15))
                    .frame(width: 36, height: 36)
                Image(systemName: icon)
                    .font(.system(size: 16, weight: .medium))
                    .foregroundStyle(isConfigured ? HygurColors.accent : HygurColors.textTertiary)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
                Text(value)
                    .font(HygurTypography.subheadline.weight(.semibold))
                    .foregroundStyle(HygurColors.textPrimary)
                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            Spacer()

            Image(systemName: isConfigured ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(isConfigured ? HygurColors.success : HygurColors.textTertiary)
        }
        .padding(HygurSpacing.lg)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.surface)
        )
    }

    // MARK: - Data

    /// Fetch the three summary numbers in parallel. Failures degrade silently
    /// — the recap is informational, not load-bearing, so we never block the
    /// user from finishing onboarding because of a transient sidecar hiccup.
    private func loadSummary() async {
        async let cfgTask: SidecarConfig? = try? sidecar.getConfig()
        async let listTask: [ConnectorSummary]? = try? sidecar.listConnectors()
        async let kbTask: KnowledgeListResponse? = try? sidecar.listKnowledgeItems(limit: 1, offset: 0)

        let (cfg, summaries, kb) = await (cfgTask, listTask, kbTask)

        if let cfg, !cfg.lmStudio.modelDefault.isEmpty {
            modelName = cfg.lmStudio.modelDefault
            modelURL = cfg.lmStudio.url
        }

        if let summaries {
            // The sidecar tags mail-class connectors with "email" /
            // "communication"; fall back to id heuristics in case a third
            // party connector skips the tag.
            mailConnectorCount = summaries.filter { summary in
                let tags = summary.info.tags.map { $0.lowercased() }
                if tags.contains("email") || tags.contains("mail") || tags.contains("imap") {
                    return true
                }
                let id = summary.info.id.lowercased()
                return id.contains("mail") || id.contains("imap")
            }.count
        }

        if let kb {
            documentCount = kb.total
        }

        isLoading = false
    }
}
