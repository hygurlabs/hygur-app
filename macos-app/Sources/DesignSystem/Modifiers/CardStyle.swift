import SwiftUI

struct HygurCardModifier: ViewModifier {
    func body(content: Content) -> some View {
        content
            .background(HygurColors.surface)
            .clipShape(RoundedRectangle(cornerRadius: HygurRadius.md))
            .hygurCardShadow()
    }
}

extension View {
    func hygurCard() -> some View {
        modifier(HygurCardModifier())
    }
}
