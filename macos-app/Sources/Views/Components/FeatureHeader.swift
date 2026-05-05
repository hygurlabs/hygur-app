import SwiftUI

/// A standardized header row for top-level feature views.
///
/// Replaces the `HStack { Text.headline; Spacer; Button }` pattern repeated across
/// Notes, Projects, Tags, KnowledgeBase, and EmailThreads.
///
/// Usage with actions:
/// ```swift
/// FeatureHeader(title: "Notes", count: notes.count) {
///     IconButton(systemImage: "plus", label: "Add note") { addNote() }
/// }
/// ```
///
/// Usage without actions:
/// ```swift
/// FeatureHeader(title: "Tags", count: tags.count)
/// ```
struct FeatureHeader<Actions: View>: View {
    let title: String
    var count: Int? = nil
    @ViewBuilder let actions: () -> Actions

    var body: some View {
        HStack(spacing: HygurSpacing.sm) {
            Text(title)
                .font(.title3)
                .fontWeight(.semibold)

            if let count = count, count > 0 {
                Text("\(count)")
                    .font(HygurTypography.captionMono)
                    .foregroundStyle(HygurColors.accent)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(HygurColors.accent.opacity(0.10))
                    .clipShape(Capsule())
            }

            Spacer()

            actions()
        }
        .padding(.horizontal, HygurSpacing.lg)
        .padding(.vertical, HygurSpacing.md)
        .background(.ultraThinMaterial)
        .shadow(color: .black.opacity(0.06), radius: 0.5, x: 0, y: 0.5)
    }
}

// MARK: - Convenience Init (no actions)

extension FeatureHeader where Actions == EmptyView {
    init(title: String, count: Int? = nil) {
        self.title = title
        self.count = count
        self.actions = { EmptyView() }
    }
}

#if DEBUG
#Preview {
    VStack(spacing: 0) {
        Divider()
        FeatureHeader(title: "Notes", count: 42) {
            IconButton(systemImage: "plus", label: "Add note") {}
            IconButton(systemImage: "line.3.horizontal.decrease.circle", label: "Filter") {}
        }
        Divider()
        FeatureHeader(title: "Tags", count: 0)
        Divider()
        FeatureHeader(title: "Knowledge Bases") {
            IconButton(systemImage: "plus", label: "Add knowledge base") {}
        }
        Divider()
    }
    .frame(width: 400)
}
#endif
