import SwiftUI

/// Renders message text with clickable citation references like [Document 1] or [Email 2]
struct CitationTextView: View {
    let text: String
    let sources: [RAGSource]
    var onCitationTap: ((Int) -> Void)?

    var body: some View {
        parseAndRenderText()
    }

    // MARK: - Text Parsing

    private func parseAndRenderText() -> some View {
        let segments = parseTextSegments()

        return segments.reduce(Text("")) { result, segment in
            switch segment {
            case .plain(let content):
                return Text("\(result)\(Text(content))")
            case .citation(let index, let label):
                return Text("\(result)\(citationText(index: index, label: label))")
            }
        }
    }

    private func citationText(index: Int, label: String) -> Text {
        let source = index < sources.count ? sources[index] : nil
        let color = source?.color ?? .accentColor

        return Text("[\(label)]")
            .foregroundColor(color)
            .fontWeight(.medium)
    }

    /// Parse text into segments of plain text and citations
    private func parseTextSegments() -> [TextSegment] {
        var segments: [TextSegment] = []
        var currentIndex = text.startIndex

        // Pattern matches [Document N], [Email N], [Source N], [N]
        let pattern = #"\[(Document|Email|Source|Doc|Mail)?\s*(\d+)\]"#
        guard let regex = try? NSRegularExpression(pattern: pattern, options: .caseInsensitive) else {
            return [.plain(text)]
        }

        let nsRange = NSRange(text.startIndex..<text.endIndex, in: text)
        let matches = regex.matches(in: text, options: [], range: nsRange)

        for match in matches {
            guard let matchRange = Range(match.range, in: text) else { continue }

            // Add plain text before this match
            if currentIndex < matchRange.lowerBound {
                let plainText = String(text[currentIndex..<matchRange.lowerBound])
                segments.append(.plain(plainText))
            }

            // Extract the citation number
            if let numberRange = Range(match.range(at: 2), in: text) {
                let numberStr = String(text[numberRange])
                if let number = Int(numberStr) {
                    // Citation indices in text are 1-based, convert to 0-based
                    let zeroBasedIndex = number - 1

                    // Build the display label
                    var label = ""
                    if let typeRange = Range(match.range(at: 1), in: text) {
                        label = String(text[typeRange]) + " "
                    }
                    label += numberStr

                    segments.append(.citation(index: zeroBasedIndex, label: label))
                }
            }

            currentIndex = matchRange.upperBound
        }

        // Add remaining plain text
        if currentIndex < text.endIndex {
            let plainText = String(text[currentIndex..<text.endIndex])
            segments.append(.plain(plainText))
        }

        return segments
    }

    private enum TextSegment {
        case plain(String)
        case citation(index: Int, label: String)
    }
}

/// Interactive version that handles taps on citations
struct InteractiveCitationTextView: View {
    let text: String
    let sources: [RAGSource]
    var onCitationTap: ((Int) -> Void)?

    var body: some View {
        let segments = parseTextSegments()

        // Use a VStack with wrapping text for interactive citations
        WrappingHStack(alignment: .top, spacing: 0) {
            ForEach(Array(segments.enumerated()), id: \.offset) { _, segment in
                switch segment {
                case .plain(let content):
                    // Split by whitespace to enable proper wrapping
                    ForEach(Array(content.split(separator: " ", omittingEmptySubsequences: false).enumerated()), id: \.offset) { idx, word in
                        if idx > 0 || content.hasPrefix(" ") {
                            Text(" ")
                        }
                        Text(String(word))
                    }
                case .citation(let index, let label):
                    CitationBadgeView(
                        index: index,
                        label: label,
                        source: index < sources.count ? sources[index] : nil,
                        onTap: { onCitationTap?(index) }
                    )
                }
            }
        }
    }

    private func parseTextSegments() -> [TextSegment] {
        var segments: [TextSegment] = []
        var currentIndex = text.startIndex

        let pattern = #"\[(Document|Email|Source|Doc|Mail)?\s*(\d+)\]"#
        guard let regex = try? NSRegularExpression(pattern: pattern, options: .caseInsensitive) else {
            return [.plain(text)]
        }

        let nsRange = NSRange(text.startIndex..<text.endIndex, in: text)
        let matches = regex.matches(in: text, options: [], range: nsRange)

        for match in matches {
            guard let matchRange = Range(match.range, in: text) else { continue }

            if currentIndex < matchRange.lowerBound {
                let plainText = String(text[currentIndex..<matchRange.lowerBound])
                segments.append(.plain(plainText))
            }

            if let numberRange = Range(match.range(at: 2), in: text) {
                let numberStr = String(text[numberRange])
                if let number = Int(numberStr) {
                    let zeroBasedIndex = number - 1
                    var label = ""
                    if let typeRange = Range(match.range(at: 1), in: text) {
                        label = String(text[typeRange]) + " "
                    }
                    label += numberStr
                    segments.append(.citation(index: zeroBasedIndex, label: label))
                }
            }

            currentIndex = matchRange.upperBound
        }

        if currentIndex < text.endIndex {
            let plainText = String(text[currentIndex..<text.endIndex])
            segments.append(.plain(plainText))
        }

        return segments
    }

    private enum TextSegment {
        case plain(String)
        case citation(index: Int, label: String)
    }
}

/// Clickable citation badge
struct CitationBadgeView: View {
    let index: Int
    let label: String
    let source: RAGSource?
    var onTap: () -> Void

    @State private var isHovered = false

    var body: some View {
        Button(action: onTap) {
            Text("[\(label)]")
                .font(.callout.weight(.medium))
                .foregroundStyle(badgeColor)
                .padding(.horizontal, 2)
                .background(
                    isHovered ? badgeColor.opacity(0.15) : Color.clear
                )
                .clipShape(RoundedRectangle(cornerRadius: 3))
        }
        .buttonStyle(.plain)
        .onHover { hovering in
            withAnimation(.easeInOut(duration: 0.1)) {
                isHovered = hovering
            }
        }
        .help(source?.title ?? "Source \(index + 1)")
    }

    private var badgeColor: Color {
        source?.color ?? .accentColor
    }
}

/// Simple wrapping horizontal stack for text with inline elements
struct WrappingHStack: Layout {
    var alignment: VerticalAlignment = .center
    var spacing: CGFloat = 4

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let result = arrangeSubviews(proposal: proposal, subviews: subviews)
        return result.size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let result = arrangeSubviews(proposal: proposal, subviews: subviews)

        for (index, position) in result.positions.enumerated() {
            let size = subviews[index].sizeThatFits(.unspecified)
            let y: CGFloat

            switch alignment {
            case .top:
                y = bounds.minY + position.y
            case .bottom:
                y = bounds.minY + position.y + result.lineHeights[position.line] - size.height
            default:
                y = bounds.minY + position.y + (result.lineHeights[position.line] - size.height) / 2
            }

            subviews[index].place(
                at: CGPoint(x: bounds.minX + position.x, y: y),
                proposal: .unspecified
            )
        }
    }

    private struct Position {
        var x: CGFloat
        var y: CGFloat
        var line: Int
    }

    private func arrangeSubviews(proposal: ProposedViewSize, subviews: Subviews) -> (size: CGSize, positions: [Position], lineHeights: [CGFloat]) {
        let maxWidth = proposal.width ?? .infinity
        var positions: [Position] = []
        var lineHeights: [CGFloat] = []
        var currentX: CGFloat = 0
        var currentY: CGFloat = 0
        var currentLine = 0
        var currentLineHeight: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)

            if currentX + size.width > maxWidth && currentX > 0 {
                lineHeights.append(currentLineHeight)
                currentX = 0
                currentY += currentLineHeight + spacing
                currentLine += 1
                currentLineHeight = 0
            }

            positions.append(Position(x: currentX, y: currentY, line: currentLine))
            currentX += size.width
            currentLineHeight = max(currentLineHeight, size.height)
        }

        lineHeights.append(currentLineHeight)
        let totalHeight = currentY + currentLineHeight

        return (CGSize(width: maxWidth, height: totalHeight), positions, lineHeights)
    }
}

// MARK: - Preview

#Preview {
    let sources = [
        RAGSource(
            contentId: "doc-1",
            sourceType: "document",
            title: "Requirements Doc",
            excerpt: "...",
            score: 0.9,
            mailFrom: nil,
            mailDate: nil,
            mailSubject: nil
        ),
        RAGSource(
            contentId: "mail-1",
            sourceType: "email",
            title: "Project Email",
            excerpt: "...",
            score: 0.8,
            mailFrom: "test@example.com",
            mailDate: "2024-01-15",
            mailSubject: "Subject"
        )
    ]

    return VStack(alignment: .leading, spacing: 20) {
        Text("Static:")
            .font(.headline)
        CitationTextView(
            text: "Based on the requirements [Document 1], the deployment should proceed as discussed in the email [Email 2].",
            sources: sources
        )

        Divider()

        Text("Interactive:")
            .font(.headline)
        InteractiveCitationTextView(
            text: "The project [1] requires features mentioned in [Document 1] and confirmed via [Email 2].",
            sources: sources
        ) { index in
            print("Tapped citation \(index)")
        }
    }
    .padding()
    .frame(width: 400)
}
