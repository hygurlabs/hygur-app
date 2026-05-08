import SwiftUI

/// Sheet shown when the user clicks the learning-progress capsule in the
/// status bar. Exposes the per-pillar breakdown so the gauge isn't an opaque
/// number — the user can see *why* the percentage is what it is and what to
/// do next to push it forward.
///
/// Phase 1 (pair mode): pillars are read from the same
/// `LearningProgressViewModel` that drives the bar. The view doesn't poll on
/// its own — it reuses the parent's snapshot so opening the sheet is instant.
struct LearningInsightsView: View {
    @Environment(\.dismiss) private var dismiss
    let viewModel: LearningProgressViewModel

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: HygurSpacing.lg) {
                    summaryCard
                    pillarsSection
                    nextStepCard
                }
                .padding(HygurSpacing.lg)
            }
        }
        .frame(minWidth: 460, idealWidth: 520, minHeight: 460, idealHeight: 540)
        .task {
            // Force a fresh snapshot when the user opens the sheet so the
            // numbers reflect the latest state — polling cadence is 60 s, so
            // the bar may be slightly stale.
            await viewModel.refresh()
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack(alignment: .center, spacing: HygurSpacing.sm) {
            Image(systemName: "brain.head.profile")
                .font(.title2)
                .foregroundStyle(HygurColors.brandBlue)
            VStack(alignment: .leading, spacing: 2) {
                Text("Hygur learning progress")
                    .font(.headline)
                Text("How much Hygur has learned about you yet")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button("Done") { dismiss() }
                .keyboardShortcut(.defaultAction)
        }
        .padding(.horizontal, HygurSpacing.lg)
        .padding(.vertical, HygurSpacing.md)
    }

    // MARK: - Summary

    private var summaryCard: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            HStack(alignment: .firstTextBaseline) {
                Text("\(percent(viewModel.coverage))%")
                    .font(.system(size: 44, weight: .semibold, design: .rounded))
                    .foregroundStyle(HygurColors.brandBlue)
                    .monospacedDigit()
                Text("of baseline")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                Spacer()
                if viewModel.isLoading {
                    ProgressView().controlSize(.small)
                }
            }

            Capsule()
                .fill(HygurColors.textSecondary.opacity(0.18))
                .frame(height: 6)
                .overlay(alignment: .leading) {
                    GeometryReader { geo in
                        Capsule()
                            .fill(HygurColors.brandBlue)
                            .frame(width: geo.size.width * CGFloat(viewModel.coverage))
                            .animation(.easeInOut(duration: 0.4), value: viewModel.coverage)
                    }
                }
                .frame(height: 6)

            if let err = viewModel.lastFetchError {
                Text("Couldn't refresh: \(err)")
                    .font(.caption2)
                    .foregroundStyle(HygurColors.warning)
            }
        }
        .padding(HygurSpacing.md)
        .background(
            RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                .fill(HygurColors.brandBlue.opacity(0.06))
        )
    }

    // MARK: - Pillars

    private var pillarsSection: some View {
        VStack(alignment: .leading, spacing: HygurSpacing.sm) {
            Text("Pillars")
                .font(.subheadline)
                .fontWeight(.semibold)
                .foregroundStyle(.secondary)

            if let pillars = viewModel.progress?.pillars, !pillars.isEmpty {
                VStack(spacing: HygurSpacing.xs) {
                    ForEach(pillars) { pillar in
                        pillarRow(pillar)
                    }
                }
            } else {
                Text("Pillars load when the sidecar reports a snapshot.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, HygurSpacing.sm)
            }
        }
    }

    private func pillarRow(_ pillar: LearningPillar) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(pillar.label)
                    .font(.body)
                Spacer()
                Text("\(pillar.current) / \(pillar.target)")
                    .font(HygurTypography.captionMono)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(HygurColors.textSecondary.opacity(0.18))
                    .frame(height: 4)
                GeometryReader { geo in
                    Capsule()
                        .fill(HygurColors.brandBlue.opacity(0.85))
                        .frame(width: geo.size.width * CGFloat(min(1, max(0, pillar.progress))))
                }
                .frame(height: 4)
            }
            .frame(height: 4)
            HStack(spacing: 4) {
                Text("Weight \(Int((pillar.weight * 100).rounded()))%")
                Text("·")
                Text("\(percent(pillar.progress))% complete")
            }
            .font(.caption2)
            .foregroundStyle(.tertiary)
        }
        .padding(.vertical, HygurSpacing.xs)
    }

    // MARK: - Next step

    @ViewBuilder
    private var nextStepCard: some View {
        if let progress = viewModel.progress, !progress.nextStepHint.isEmpty {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: HygurSpacing.xs) {
                    Image(systemName: "sparkles")
                        .foregroundStyle(HygurColors.brandBlue)
                    Text("Next milestone")
                        .font(.subheadline)
                        .fontWeight(.semibold)
                }
                Text(progress.nextStepHint)
                    .font(.body)
                    .foregroundStyle(HygurColors.textPrimary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(HygurSpacing.md)
            .background(
                RoundedRectangle(cornerRadius: HygurRadius.lg, style: .continuous)
                    .strokeBorder(HygurColors.brandBlue.opacity(0.25))
            )
        }
    }

    // MARK: - Helpers

    private func percent(_ value: Double) -> Int {
        Int((max(0, min(1, value)) * 100).rounded())
    }
}
