import SwiftUI

struct HygurShadow {
    let color: Color
    let radius: CGFloat
    let x: CGFloat
    let y: CGFloat
}

enum HygurShadows {
    static let listRow = HygurShadow(color: .black.opacity(0.04), radius: 1, x: 0, y: 1)
    static let card    = HygurShadow(color: .black.opacity(0.08), radius: 4, x: 0, y: 2)
    static let overlay = HygurShadow(color: .black.opacity(0.16), radius: 12, x: 0, y: 4)
    static let float   = HygurShadow(color: .black.opacity(0.14), radius: 12, x: 0, y: 6)
    static let panel   = HygurShadow(color: .black.opacity(0.20), radius: 24, x: 0, y: 8)
}

extension View {
    func hygurCardShadow() -> some View {
        let s = HygurShadows.card
        return self.shadow(color: s.color, radius: s.radius, x: s.x, y: s.y)
    }

    func hygurOverlayShadow() -> some View {
        let s = HygurShadows.overlay
        return self.shadow(color: s.color, radius: s.radius, x: s.x, y: s.y)
    }
}
