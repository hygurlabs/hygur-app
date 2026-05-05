import SwiftUI

struct EmptyStateView: View {
    let icon: String
    let title: String
    var subtitle: String? = nil
    var action: (label: String, callback: () -> Void)? = nil

    @State private var appeared = false

    var body: some View {
        VStack(spacing: HygurSpacing.lg) {
            ZStack {
                Circle()
                    .fill(HygurColors.accent.opacity(0.08))
                    .frame(width: 80, height: 80)

                Image(systemName: icon)
                    .font(.system(size: 36))
                    .foregroundStyle(HygurColors.accent.opacity(0.7))
            }
            .scaleEffect(appeared ? 1 : 0.7)
            .opacity(appeared ? 1 : 0)

            VStack(spacing: HygurSpacing.xs) {
                Text(title)
                    .font(.title3)
                    .fontWeight(.semibold)
                    .foregroundStyle(HygurColors.textPrimary)

                if let subtitle = subtitle {
                    Text(subtitle)
                        .font(HygurTypography.subheadline)
                        .foregroundStyle(HygurColors.textSecondary)
                        .multilineTextAlignment(.center)
                        .lineLimit(3)
                }
            }
            .opacity(appeared ? 1 : 0)
            .offset(y: appeared ? 0 : 8)

            if let action = action {
                Button(action.label, action: action.callback)
                    .buttonStyle(.borderedProminent)
                    .opacity(appeared ? 1 : 0)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(HygurSpacing.xxxl)
        .onAppear {
            withAnimation(.spring(response: 0.45, dampingFraction: 0.7).delay(0.05)) {
                appeared = true
            }
        }
    }
}

#if DEBUG
#Preview {
    VStack(spacing: 24) {
        EmptyStateView(
            icon: "doc.text.magnifyingglass",
            title: "No results found",
            subtitle: "Try adjusting your search or filters to find what you're looking for."
        )
        EmptyStateView(
            icon: "folder.badge.plus",
            title: "No knowledge bases",
            subtitle: "Create your first knowledge base to get started.",
            action: ("Create Knowledge Base", {})
        )
    }
    .frame(width: 400, height: 500)
}
#endif
