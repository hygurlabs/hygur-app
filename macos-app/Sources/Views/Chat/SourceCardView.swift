import SwiftUI

/// Individual source card showing document/email info with expand/collapse
struct SourceCardView: View {
    let source: RAGSource
    let index: Int
    let isHighlighted: Bool
    @Binding var isExpanded: Bool

    @State private var isHovered = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header row
            headerRow

            // Expanded content
            if isExpanded {
                expandedContent
                    .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .padding(HygurSpacing.sm + 2)
        .background(cardBackground)
        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
        .overlay(
            RoundedRectangle(cornerRadius: HygurRadius.md)
                .stroke(isHighlighted ? source.color : Color.clear, lineWidth: 2)
        )
        .onHover { hovering in
            withAnimation(.easeInOut(duration: 0.15)) {
                isHovered = hovering
            }
        }
        .onTapGesture {
            withAnimation(.easeInOut(duration: 0.2)) {
                isExpanded.toggle()
            }
        }
        .contentShape(Rectangle())
    }

    // MARK: - Header Row

    private var headerRow: some View {
        HStack(spacing: 8) {
            // Citation number badge
            Text("[\(index + 1)]")
                .font(.caption.monospaced().bold())
                .foregroundStyle(source.color)
                .padding(.horizontal, 4)
                .padding(.vertical, 2)
                .background(source.color.opacity(0.15))
                .clipShape(RoundedRectangle(cornerRadius: 4))

            // Source icon
            Image(systemName: source.icon)
                .font(.caption)
                .foregroundStyle(source.color)

            // Title
            Text(source.title)
                .font(.subheadline.weight(.medium))
                .lineLimit(1)
                .foregroundStyle(.primary)

            Spacer()

            // Score badge
            scoreBadge

            // Expand/collapse chevron
            Image(systemName: isExpanded ? "chevron.up" : "chevron.down")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: - Score Badge

    private var scoreBadge: some View {
        HStack(spacing: 2) {
            Image(systemName: scoreIcon)
                .font(.system(size: 8))
            Text(source.scorePercentage)
                .font(.caption2.monospacedDigit())
        }
        .foregroundStyle(scoreColor)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(scoreColor.opacity(0.1))
        .clipShape(Capsule())
    }

    private var scoreIcon: String {
        if source.score >= 0.8 {
            return "star.fill"
        } else if source.score >= 0.6 {
            return "star.leadinghalf.filled"
        } else {
            return "star"
        }
    }

    private var scoreColor: Color {
        if source.score >= 0.8 {
            return HygurColors.success
        } else if source.score >= 0.6 {
            return HygurColors.warning
        } else {
            return HygurColors.textSecondary
        }
    }

    // MARK: - Expanded Content

    private var expandedContent: some View {
        VStack(alignment: .leading, spacing: 8) {
            Divider()
                .padding(.vertical, 4)

            // Mail metadata if available
            if source.isEmail {
                mailMetadata
            }

            // Excerpt
            Text(source.excerpt)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(isExpanded ? nil : 2)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)

            // Source type label
            HStack {
                Label(source.sourceLabel, systemImage: source.icon)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)

                Spacer()

                Text(source.contentId.prefix(8))
                    .font(.caption2.monospaced())
                    .foregroundStyle(.tertiary)
            }
        }
    }

    // MARK: - Mail Metadata

    @ViewBuilder
    private var mailMetadata: some View {
        VStack(alignment: .leading, spacing: 4) {
            if let from = source.mailFrom {
                HStack(spacing: 4) {
                    Image(systemName: "person.fill")
                        .font(.caption2)
                    Text(from)
                        .font(.caption)
                }
                .foregroundStyle(.secondary)
            }

            if let subject = source.mailSubject {
                HStack(spacing: 4) {
                    Image(systemName: "text.quote")
                        .font(.caption2)
                    Text(subject)
                        .font(.caption.weight(.medium))
                        .lineLimit(1)
                }
                .foregroundStyle(.secondary)
            }

            if let date = source.mailDate {
                HStack(spacing: 4) {
                    Image(systemName: "calendar")
                        .font(.caption2)
                    Text(date)
                        .font(.caption)
                }
                .foregroundStyle(.tertiary)
            }
        }
        .padding(HygurSpacing.sm)
        .background(HygurColors.surface.opacity(0.5))
        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.sm))
    }

    // MARK: - Card Background

    private var cardBackground: some View {
        Group {
            if isHighlighted {
                source.color.opacity(0.08)
            } else if isHovered {
                HygurColors.surface
            } else {
                HygurColors.surface.opacity(0.5)
            }
        }
    }
}

// MARK: - Preview

#Preview {
    VStack(spacing: 12) {
        SourceCardView(
            source: RAGSource(
                contentId: "doc-123",
                sourceType: "document",
                title: "Project Requirements.md",
                excerpt: "The system shall support real-time collaboration with multiple users editing simultaneously...",
                score: 0.92,
                mailFrom: nil,
                mailDate: nil,
                mailSubject: nil
            ),
            index: 0,
            isHighlighted: false,
            isExpanded: .constant(true)
        )

        SourceCardView(
            source: RAGSource(
                contentId: "mail-456",
                sourceType: "email",
                title: "Re: Project Update",
                excerpt: "Following up on our discussion about the deployment timeline...",
                score: 0.75,
                mailFrom: "john@example.com",
                mailDate: "2024-01-15",
                mailSubject: "Re: Project Update - Q1 Goals"
            ),
            index: 1,
            isHighlighted: true,
            isExpanded: .constant(false)
        )
    }
    .padding()
    .frame(width: 300)
}
