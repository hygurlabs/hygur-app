import SwiftUI

/// A 32x32 minimum hitbox icon button with mandatory accessibility label and tooltip.
///
/// Replaces bare `Button { Image(systemName:) }` patterns with undersized 24x24 frames
/// scattered across the codebase. Enforces a consistent tap target, `.help()` tooltip,
/// and accessibility label in a single declaration.
///
/// Usage:
/// ```swift
/// IconButton(systemImage: "plus", label: "Add note") {
///     addNote()
/// }
///
/// IconButton(
///     systemImage: "trash",
///     label: "Delete item",
///     foregroundColor: HygurColors.danger
/// ) {
///     deleteItem()
/// }
/// ```
struct IconButton: View {
    let systemImage: String
    let label: String
    let action: () -> Void
    var isDisabled: Bool = false
    var foregroundColor: Color = HygurColors.textSecondary

    var body: some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .foregroundStyle(isDisabled ? HygurColors.textTertiary : foregroundColor)
                .frame(width: 32, height: 32)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(isDisabled)
        .help(label)
        .accessibilityLabel(label)
    }
}

#if DEBUG
#Preview {
    HStack(spacing: 4) {
        IconButton(systemImage: "plus", label: "Add item") {}
        IconButton(systemImage: "pencil", label: "Edit item") {}
        IconButton(systemImage: "trash", label: "Delete item", action: {}, foregroundColor: HygurColors.danger)
        IconButton(systemImage: "arrow.clockwise", label: "Refresh", action: {}, isDisabled: true)
        IconButton(systemImage: "line.3.horizontal.decrease.circle", label: "Filter") {}
    }
    .padding()
}
#endif
