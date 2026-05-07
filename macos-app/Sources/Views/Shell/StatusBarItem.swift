import SwiftUI

/// A reusable item for the bottom status bar — icon (optional) + label.
/// Tap target is provided when an `action` is supplied; otherwise the item
/// renders as a static informational pill.
struct StatusBarItem: View {
    let systemImage: String?
    let label: String
    var tint: Color = HygurColors.textSecondary
    var action: (() -> Void)? = nil
    var help: String? = nil

    var body: some View {
        if let action {
            Button(action: action) {
                content
            }
            .buttonStyle(.plain)
            .help(help ?? label)
        } else {
            content
                .help(help ?? label)
        }
    }

    private var content: some View {
        HStack(spacing: 4) {
            if let systemImage {
                Image(systemName: systemImage)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(tint)
            }
            Text(label)
                .font(HygurTypography.statusCaption)
                .foregroundStyle(tint)
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .contentShape(Rectangle())
    }
}

/// A small colored dot used as a status indicator (e.g. sidecar connected).
struct StatusDot: View {
    let color: Color

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: 7, height: 7)
    }
}
