import SwiftUI

/// Side panel showing RAG context sources and intent for a message
struct ContextPanelView: View {
    let context: RAGContext
    let highlightedSourceIndex: Int?
    var onSourceTap: ((Int) -> Void)?

    @State private var expandedSources: Set<String> = []
    @State private var isPanelCollapsed = false

    var body: some View {
        VStack(spacing: 0) {
            // Panel header
            panelHeader

            if !isPanelCollapsed {
                Divider()

                // Content
                ScrollViewReader { proxy in
                    ScrollView {
                        VStack(alignment: .leading, spacing: HygurSpacing.md) {
                            // Intent section (if available)
                            if let intent = context.intent {
                                intentSection(intent: intent)
                            }

                            // Sources section
                            sourcesSection

                            Spacer(minLength: 20)
                        }
                        .padding(HygurSpacing.md)
                    }
                    .onChange(of: highlightedSourceIndex) { _, newIndex in
                        if let index = newIndex, index < context.sources.count {
                            let sourceId = context.sources[index].id
                            withAnimation {
                                proxy.scrollTo(sourceId, anchor: .center)
                            }
                        }
                    }
                }
            }
        }
        .frame(minWidth: 240, idealWidth: 280, maxWidth: 320)
        .background(HygurColors.background)
    }

    // MARK: - Panel Header

    private var panelHeader: some View {
        HStack(spacing: 8) {
            Image(systemName: "doc.text.magnifyingglass")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)

            Text("Context")
                .font(.subheadline.weight(.semibold))

            Spacer()

            // Source count badge
            BadgeView(
                text: "\(context.sources.count)",
                color: HygurColors.accent,
                style: .capsule,
                size: HygurTypography.captionMono
            )

            // Collapse button
            IconButton(
                systemImage: isPanelCollapsed ? "chevron.left" : "chevron.right",
                label: isPanelCollapsed ? "Expand panel" : "Collapse panel",
                action: {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        isPanelCollapsed.toggle()
                    }
                }
            )
        }
        .padding(.horizontal, HygurSpacing.md)
        .padding(.vertical, HygurSpacing.sm + 2)
        .background(HygurColors.surface.opacity(0.5))
    }

    // MARK: - Intent Section

    private func intentSection(intent: RAGIntent) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm - 2) {
            // Intent header
            HStack {
                Label("Query Intent", systemImage: "lightbulb.fill")
                    .font(HygurTypography.caption.weight(.medium))
                    .foregroundStyle(HygurColors.textSecondary)

                Spacer()

                // Confidence indicator
                confidenceIndicator(confidence: intent.confidence)
            }

            // Query interpretation
            Text(intent.query)
                .font(HygurTypography.caption)
                .foregroundStyle(HygurColors.textPrimary)
                .padding(HygurSpacing.sm)
                .background(HygurColors.surface)
                .clipShape(RoundedRectangle(cornerRadius: HygurRadius.sm))

            // Weights (if available)
            if let weights = intent.weights, !weights.isEmpty {
                weightsView(weights: weights)
            }
        }
        .padding(HygurSpacing.sm + 2)
        .background(HygurColors.accent.opacity(0.05))
        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
    }

    private func confidenceIndicator(confidence: Double) -> some View {
        HStack(spacing: 4) {
            Circle()
                .fill(confidenceColor(confidence))
                .frame(width: 6, height: 6)
            Text(String(format: "%.0f%%", confidence * 100))
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.secondary)
        }
    }

    private func confidenceColor(_ confidence: Double) -> Color {
        if confidence >= 0.8 {
            return HygurColors.success
        } else if confidence >= 0.5 {
            return HygurColors.warning
        } else {
            return HygurColors.danger
        }
    }

    private func weightsView(weights: [String: Double]) -> some View {
        VStack(alignment: .leading, spacing: HygurSpacing.xs) {
            Text("Search Weights")
                .font(HygurTypography.captionMono)
                .foregroundStyle(HygurColors.textTertiary)

            FlowLayout(spacing: HygurSpacing.xs) {
                ForEach(weights.sorted(by: { $0.value > $1.value }), id: \.key) { key, value in
                    BadgeView(
                        text: "\(key) \(String(format: "%.0f%%", value * 100))",
                        color: HygurColors.textSecondary,
                        style: .capsule,
                        size: HygurTypography.captionMono
                    )
                }
            }
        }
    }

    // MARK: - Sources Section

    private var sourcesSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            // Section header
            HStack {
                Label("Sources", systemImage: "doc.on.doc.fill")
                    .font(HygurTypography.caption.weight(.medium))
                    .foregroundStyle(HygurColors.textSecondary)

                Spacer()

                // Expand/collapse all
                Button {
                    withAnimation(.easeInOut(duration: 0.2)) {
                        if expandedSources.isEmpty {
                            expandedSources = Set(context.sources.map(\.id))
                        } else {
                            expandedSources.removeAll()
                        }
                    }
                } label: {
                    Text(expandedSources.isEmpty ? "Expand All" : "Collapse All")
                        .font(HygurTypography.captionMono)
                        .foregroundStyle(HygurColors.accent)
                }
                .buttonStyle(.plain)
            }

            // Source cards
            ForEach(Array(context.sources.enumerated()), id: \.element.id) { index, source in
                SourceCardView(
                    source: source,
                    index: index,
                    isHighlighted: highlightedSourceIndex == index,
                    isExpanded: Binding(
                        get: { expandedSources.contains(source.id) },
                        set: { newValue in
                            if newValue {
                                expandedSources.insert(source.id)
                            } else {
                                expandedSources.remove(source.id)
                            }
                        }
                    )
                )
                .id(source.id)
                .onTapGesture {
                    onSourceTap?(index)
                }
            }
        }
    }
}

// MARK: - Flow Layout for Weights

/// Simple flow layout for weight badges
struct FlowLayout: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let result = arrangeSubviews(proposal: proposal, subviews: subviews)
        return result.size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let result = arrangeSubviews(proposal: proposal, subviews: subviews)
        for (index, position) in result.positions.enumerated() {
            subviews[index].place(
                at: CGPoint(x: bounds.minX + position.x, y: bounds.minY + position.y),
                proposal: .unspecified
            )
        }
    }

    private func arrangeSubviews(proposal: ProposedViewSize, subviews: Subviews) -> (size: CGSize, positions: [CGPoint]) {
        let maxWidth = proposal.width ?? .infinity
        var positions: [CGPoint] = []
        var currentX: CGFloat = 0
        var currentY: CGFloat = 0
        var lineHeight: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)

            if currentX + size.width > maxWidth && currentX > 0 {
                currentX = 0
                currentY += lineHeight + spacing
                lineHeight = 0
            }

            positions.append(CGPoint(x: currentX, y: currentY))
            currentX += size.width + spacing
            lineHeight = max(lineHeight, size.height)
        }

        let totalHeight = currentY + lineHeight
        return (CGSize(width: maxWidth, height: totalHeight), positions)
    }
}

// MARK: - Collapsed Context Indicator

/// Compact indicator shown when context panel is hidden
struct ContextIndicatorView: View {
    let sourceCount: Int
    var onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            BadgeView(
                text: "\(sourceCount)",
                color: HygurColors.accent,
                style: .capsule,
                icon: "doc.text.magnifyingglass"
            )
        }
        .buttonStyle(.plain)
        .help("Show \(sourceCount) context sources")
    }
}

// MARK: - Preview

#Preview {
    let sampleContext = RAGContext(
        sources: [
            RAGSource(
                contentId: "doc-1",
                sourceType: "document",
                title: "Project Requirements",
                excerpt: "The application must support real-time collaboration features...",
                score: 0.95,
                mailFrom: nil,
                mailDate: nil,
                mailSubject: nil
            ),
            RAGSource(
                contentId: "mail-1",
                sourceType: "email",
                title: "Re: Deployment Timeline",
                excerpt: "Following our meeting, we agreed to push the release to next quarter...",
                score: 0.82,
                mailFrom: "manager@company.com",
                mailDate: "2024-01-15",
                mailSubject: "Re: Deployment Timeline - Q1"
            ),
            RAGSource(
                contentId: "doc-2",
                sourceType: "markdown",
                title: "API Documentation",
                excerpt: "The /api/chat endpoint supports SSE streaming...",
                score: 0.68,
                mailFrom: nil,
                mailDate: nil,
                mailSubject: nil
            )
        ],
        intent: RAGIntent(
            query: "deployment timeline and requirements",
            confidence: 0.85,
            weights: ["semantic": 0.7, "keyword": 0.3]
        )
    )

    return ContextPanelView(
        context: sampleContext,
        highlightedSourceIndex: 1
    )
    .frame(height: 500)
}
