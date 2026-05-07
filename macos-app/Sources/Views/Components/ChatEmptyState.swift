import SwiftUI

/// First-run empty state for the chat surface. Replaces the generic
/// "Welcome to Hygur" placeholder with a focused set of prompt suggestions
/// the user can tap to seed the input — the goal is to remove the "blank
/// page paralysis" that bites every new user when faced with an empty
/// conversation.
///
/// Suggestions are intentionally short and verb-led so they read as actions,
/// not advertising copy. The list is small (4-6 items) because more would
/// just be noise; if we need to surface deeper capabilities we should do
/// it from the Help menu, not here.
struct ChatEmptyState: View {
    let onPick: (String) -> Void

    private struct Suggestion: Identifiable {
        let id: String
        let icon: String
        let label: String
        let prompt: String
    }

    private let suggestions: [Suggestion] = [
        .init(id: "summary",
              icon: "tray.full",
              label: "Catch me up on today",
              prompt: "Catch me up on today — what's in my inbox, calendar, and notes that I should know about?"),
        .init(id: "search",
              icon: "magnifyingglass",
              label: "Search my knowledge base",
              prompt: "Search my knowledge base for "),
        .init(id: "summarize_note",
              icon: "doc.text",
              label: "Summarize a recent note",
              prompt: "Summarize my most recent note in 3 bullet points."),
        .init(id: "draft_email",
              icon: "envelope.badge",
              label: "Draft a short reply to a recent email",
              prompt: "Draft a short, polite reply to the most recent unread email in my inbox."),
    ]

    var body: some View {
        VStack(spacing: HygurSpacing.xl) {
            VStack(spacing: HygurSpacing.sm) {
                ZStack {
                    Circle()
                        .fill(HygurColors.accent.opacity(0.08))
                        .frame(width: 72, height: 72)
                    Image(systemName: "sparkles")
                        .font(.system(size: 30))
                        .foregroundStyle(HygurColors.accent.opacity(0.8))
                }

                Text("How can Hygur help?")
                    .font(.title3).fontWeight(.semibold)
                    .foregroundStyle(HygurColors.textPrimary)

                Text("Pick a prompt to start, or just type your own question.")
                    .font(HygurTypography.subheadline)
                    .foregroundStyle(HygurColors.textSecondary)
            }

            VStack(spacing: HygurSpacing.sm) {
                ForEach(suggestions) { s in
                    Button {
                        onPick(s.prompt)
                    } label: {
                        HStack(spacing: HygurSpacing.md) {
                            Image(systemName: s.icon)
                                .font(.system(size: 14, weight: .medium))
                                .foregroundStyle(HygurColors.accent)
                                .frame(width: 22)
                            Text(s.label)
                                .font(HygurTypography.subheadline)
                                .foregroundStyle(HygurColors.textPrimary)
                            Spacer()
                            Image(systemName: "arrow.up.right")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(HygurColors.textTertiary)
                        }
                        .padding(.horizontal, HygurSpacing.lg)
                        .padding(.vertical, HygurSpacing.md)
                        .background(HygurColors.surface)
                        .overlay(
                            RoundedRectangle(cornerRadius: HygurRadius.md)
                                .stroke(HygurColors.divider, lineWidth: 1)
                        )
                        .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
                    }
                    .buttonStyle(.plain)
                }
            }
            .frame(maxWidth: 480)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(HygurSpacing.xxxl)
    }
}

#if DEBUG
#Preview {
    ChatEmptyState(onPick: { print("pick: \($0)") })
        .frame(width: 720, height: 560)
}
#endif
