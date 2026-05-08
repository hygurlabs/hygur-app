import SwiftUI

/// Phase 1 (pair mode) — minimal capsule gauge for the status bar showing
/// how much "Hygur knows about you yet". The bar itself is dumb: it gets a
/// `progress` value in [0, 1] and a tooltip; the parent owns the polling.
///
/// Centring discipline: the parent must wrap this in `Spacer + bar + Spacer`
/// so the bar sits in the geometric centre of the status bar regardless of
/// what's pinned to either side. The bar itself only cares about its own
/// 120 × 4 footprint.
struct LearningProgressBar: View {
    let coverage: Double
    let tooltip: String
    let onTap: () -> Void

    private let trackWidth: CGFloat = 120
    private let trackHeight: CGFloat = 4

    var body: some View {
        Button(action: onTap) {
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(HygurColors.textSecondary.opacity(0.25))
                    .frame(width: trackWidth, height: trackHeight)
                Capsule()
                    .fill(HygurColors.brandBlue)
                    .frame(width: max(0, min(trackWidth, trackWidth * CGFloat(clampedCoverage))), height: trackHeight)
                    .animation(.easeInOut(duration: 0.4), value: clampedCoverage)
            }
            .frame(width: trackWidth, height: 14)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(tooltip)
        .accessibilityLabel("Hygur learning progress")
        .accessibilityValue("\(Int((clampedCoverage * 100).rounded())) percent")
    }

    private var clampedCoverage: Double {
        min(1, max(0, coverage))
    }
}
