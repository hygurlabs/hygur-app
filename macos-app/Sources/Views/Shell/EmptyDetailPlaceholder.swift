import SwiftUI

/// Right-most pane placeholder shown when no inspector / editor is in focus.
/// Kept intentionally quiet so the eye lands on the active list column.
struct EmptyDetailPlaceholder: View {
    var systemImage: String = "sidebar.right"
    var title: String = "Nothing selected"
    var subtitle: String? = "Pick an item in the list to see its details here."

    var body: some View {
        VStack(spacing: HygurSpacing.md) {
            Image(systemName: systemImage)
                .font(.system(size: 28, weight: .light))
                .foregroundStyle(HygurColors.textTertiary)
            Text(title)
                .font(HygurTypography.headline)
                .foregroundStyle(HygurColors.textSecondary)
            if let subtitle {
                Text(subtitle)
                    .font(HygurTypography.caption)
                    .foregroundStyle(HygurColors.textTertiary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 240)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(.regularMaterial)
    }
}

#Preview {
    EmptyDetailPlaceholder()
        .frame(width: 360, height: 480)
}
