import SwiftUI
import MarkdownUI

/// `BriefDetailView` is the read-only sheet shown when the user clicks a
/// `brief` row in `ActivityView`. Fetches the persisted knowledge item by
/// `content_id` and renders its Markdown body.
///
/// The brief is stored as a `knowledge_item` with `source_type="brief"` —
/// see `internal/scheduler/daily_brief.go:persistBrief`. The `content_id`
/// for daily briefs is `brief:YYYY-MM-DD`, and `brief:project:<id>:YYYY-MM-DD`
/// for project-scoped briefs.
struct BriefDetailView: View {
    @Environment(\.dismiss) private var dismiss

    let contentId: String
    let fallbackTitle: String

    @State private var item: KnowledgeItemResponse?
    @State private var loading = true
    @State private var loadError: String?

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
        .frame(minWidth: 560, idealWidth: 640, minHeight: 420, idealHeight: 560)
        .task { await load() }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            Image(systemName: "doc.text.below.ecg")
                .font(.title2)
                .foregroundStyle(.tint)
            VStack(alignment: .leading, spacing: 2) {
                Text(item?.title ?? fallbackTitle)
                    .font(.headline)
                if let updated = item?.updatedAtDate {
                    Text(updated, style: .date)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
            Button("Done") { dismiss() }
                .keyboardShortcut(.defaultAction)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    @ViewBuilder
    private var content: some View {
        if loading {
            VStack(spacing: 8) {
                ProgressView()
                Text("Loading brief…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let err = loadError {
            VStack(spacing: 12) {
                Image(systemName: "exclamationmark.triangle")
                    .font(.largeTitle)
                    .foregroundStyle(HygurColors.warning)
                Text("Brief unavailable")
                    .font(.title3)
                Text(err)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 24)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let body = item?.normalizedText, !body.isEmpty {
            ScrollView {
                Markdown(body)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(20)
            }
        } else {
            VStack(spacing: 8) {
                Image(systemName: "tray")
                    .font(.largeTitle)
                    .foregroundStyle(.secondary)
                Text("No content")
                    .font(.title3)
                Text("This brief has no body yet — it may still be generating.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func load() async {
        loading = true
        loadError = nil
        // Phase 1 (pair mode) signal: brief opened. Logged before the fetch
        // so the metric reflects user intent even if the body fails to load.
        InteractionLogger.shared.briefOpened(briefId: contentId)
        do {
            let svc = SidecarService.fromSettings()
            let resp = try await svc.getKnowledgeItemFull(contentId: contentId)
            if let resp {
                item = resp
            } else {
                loadError = "Brief not found in the knowledge base."
            }
        } catch {
            loadError = error.localizedDescription
        }
        loading = false
    }
}
