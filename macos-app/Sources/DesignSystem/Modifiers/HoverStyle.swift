import SwiftUI

// MARK: - Hoverable Row Modifier

/// Applies a subtle background highlight on hover — used on list rows, tiles, and
/// any interactive surface that should respond visually to mouse-over.
struct HoverableRow: ViewModifier {
    @State private var isHovered = false

    func body(content: Content) -> some View {
        content
            .background(
                HygurColors.hoveredBackground(isHovered),
                in: RoundedRectangle(cornerRadius: HygurRadius.sm)
            )
            .onHover { isHovered = $0 }
            .animation(.easeOut(duration: 0.12), value: isHovered)
    }
}

extension View {
    /// Adds a hover highlight suitable for list rows and interactive tiles.
    func hoverableRow() -> some View {
        modifier(HoverableRow())
    }
}

// MARK: - Float Shadow Helpers

extension View {
    func hygurFloatShadow() -> some View {
        let s = HygurShadows.float
        return self.shadow(color: s.color, radius: s.radius, x: s.x, y: s.y)
    }

    func hygurPanelShadow() -> some View {
        let s = HygurShadows.panel
        return self.shadow(color: s.color, radius: s.radius, x: s.x, y: s.y)
    }
}
