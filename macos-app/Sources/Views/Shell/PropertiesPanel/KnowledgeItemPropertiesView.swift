import SwiftUI

/// Right-rail inspector for a knowledge base item. Lazy-loads the full
/// item from the sidecar so the panel only needs the content id.
struct KnowledgeItemPropertiesView: View {
    let contentId: String

    @State private var item: KnowledgeItemResponse?
    @State private var loadError: String?

    private let sidecar = SidecarService.fromSettings()

    var body: some View {
        Group {
            if let item {
                content(for: item)
            } else if let loadError {
                Text(loadError)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.danger)
            } else {
                ProgressView().controlSize(.small)
            }
        }
        .task(id: contentId) { await load() }
    }

    @ViewBuilder
    private func content(for item: KnowledgeItemResponse) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.md) {
            row(label: "Title", value: item.title)
            row(label: "Source", value: item.sourceType.uppercased())
            row(label: "Chunks", value: "\(item.chunkCount)")

            if let path = item.sourcePath, !path.isEmpty {
                row(label: "Path", value: path)
            }

            if let projectId = item.projectId, !projectId.isEmpty {
                row(label: "Project", value: projectId)
            }

            row(
                label: "Updated",
                value: item.updatedAtDate.formatted(date: .abbreviated, time: .shortened)
            )

            if !item.tags.isEmpty {
                VStack(alignment: .leading, spacing: HygurSpacing.xs) {
                    Text("Tags")
                        .font(HygurTypography.caption)
                        .foregroundStyle(HygurColors.textSecondary)
                    HStack(spacing: HygurSpacing.xs) {
                        ForEach(item.tags.prefix(6), id: \.id) { tag in
                            TagPillView(tag: Tag(id: tag.id, name: tag.name, color: tag.color, usageCount: 0))
                        }
                        if item.tags.count > 6 {
                            Text("+\(item.tags.count - 6)")
                                .font(HygurTypography.caption)
                                .foregroundStyle(HygurColors.textSecondary)
                        }
                    }
                }
            }

            Spacer()
        }
    }

    @ViewBuilder
    private func row(label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textSecondary)
            Text(value)
                .font(HygurTypography.cardMeta)
                .foregroundStyle(HygurColors.textPrimary)
                .textSelection(.enabled)
                .lineLimit(3)
        }
    }

    private func load() async {
        do {
            item = try await sidecar.getKnowledgeItemFull(contentId: contentId)
        } catch {
            loadError = error.localizedDescription
        }
    }
}
