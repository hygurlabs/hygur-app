import SwiftUI

/// Compact step indicator (one dot per step). The current step's dot grows
/// into a pill so users can see *where* they are at a glance, not just *how
/// far*. Past steps are filled in the accent color, future steps stay muted.
struct OnboardingProgressDots: View {
    let total: Int
    let currentIndex: Int

    var body: some View {
        HStack(spacing: HygurSpacing.sm) {
            ForEach(0..<total, id: \.self) { index in
                Capsule()
                    .fill(fill(for: index))
                    .frame(
                        width: index == currentIndex ? 22 : 8,
                        height: 8
                    )
                    .animation(.easeInOut(duration: 0.18), value: currentIndex)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Step \(currentIndex + 1) of \(total)")
    }

    private func fill(for index: Int) -> Color {
        if index <= currentIndex {
            return HygurColors.accent
        }
        return HygurColors.border
    }
}
