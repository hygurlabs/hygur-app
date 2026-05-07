import SwiftUI
import AppKit

/// `HygurCard`-based row for the Knowledge Base list. Image source items get
/// a thumbnail leading via the accessory slot trick (still composes into the
/// shared chrome) — the card primitive's icon stays for non-image types.
struct KnowledgeCard: View {
    let item: KnowledgeItemResponse
    var projectName: String?
    var fillContainer: Bool = false

    private let maxVisibleTags = 3

    var body: some View {
        HygurCard(
            icon: HygurColors.sourceTypeIcon(item.sourceType),
            iconTint: HygurColors.sourceTypeColor(item.sourceType),
            title: item.title,
            subtitle: subtitle,
            fillContainer: fillContainer,
            accessory: {
                Text(item.documentDate, style: .date)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textSecondary)
            },
            content: {
                if let nsImage = imageThumbnail {
                    Image(nsImage: nsImage)
                        .resizable()
                        .scaledToFill()
                        .frame(maxWidth: .infinity, maxHeight: 120)
                        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.sm))
                }
            },
            footer: {
                HStack(spacing: HygurSpacing.sm) {
                    BadgeView(
                        text: item.sourceType.uppercased(),
                        color: HygurColors.sourceTypeColor(item.sourceType),
                        style: .rounded
                    )

                    if let projectName {
                        BadgeView(
                            text: projectName,
                            color: .purple,
                            style: .rounded,
                            icon: "folder.fill"
                        )
                    }

                    if !item.tags.isEmpty {
                        tagsView
                    }

                    Spacer()
                }
            }
        )
    }

    private var subtitle: String {
        "\(item.chunkCount) chunks"
    }

    private var imageThumbnail: NSImage? {
        guard item.sourceType == "image",
              let path = item.sourcePath else { return nil }
        return NSImage(contentsOfFile: path)
    }

    @ViewBuilder
    private var tagsView: some View {
        let visibleSummaries = Array(item.tags.prefix(maxVisibleTags))
        let visibleTags = visibleSummaries.map { Tag(id: $0.id, name: $0.name, color: $0.color, usageCount: 0) }
        let remaining = item.tags.count - maxVisibleTags

        HStack(spacing: 4) {
            ForEach(visibleTags) { tag in
                TagPillView(tag: tag)
            }
            if remaining > 0 {
                Text("+\(remaining)")
                    .font(.caption2)
                    .fontWeight(.medium)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 4)
                    .background(Color.secondary.opacity(0.15))
                    .foregroundStyle(.secondary)
                    .clipShape(Capsule())
            }
        }
    }
}
