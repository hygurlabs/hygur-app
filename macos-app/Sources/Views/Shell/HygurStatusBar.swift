import SwiftUI

/// Bottom-of-window status bar (VSCode-style). Surfaces sidecar + LM Studio
/// reachability, sync activity, and quick access to Settings via a gear icon.
/// Observes `EventStreamService` so updates flow live without extra wiring.
struct HygurStatusBar: View {
    @Environment(EventStreamService.self) private var events
    @Environment(\.openSettings) private var openSettings

    /// Phase 1 (pair mode): polls `/insights/learning-progress` every 60s
    /// and feeds the central capsule. Owned by the status bar so the polling
    /// task starts/stops with the bar's lifecycle (the bar is always mounted
    /// while the main window is up).
    @State private var learningVM = LearningProgressViewModel()
    @State private var showLearningSheet = false

    var body: some View {
        HStack(spacing: HygurSpacing.sm) {
            // Workspace label
            HStack(spacing: 5) {
                Image(systemName: "circle.hexagongrid.fill")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(HygurColors.brandBlue)
                Text("hygur")
                    .font(HygurTypography.statusCaption.weight(.medium))
                    .foregroundStyle(HygurColors.textPrimary)
            }

            divider

            // Version
            StatusBarItem(systemImage: nil, label: "v\(appVersion)")

            divider

            // Sidecar connection status
            HStack(spacing: 5) {
                StatusDot(color: events.sidecarConnected ? HygurColors.success : HygurColors.danger)
                Text(events.sidecarConnected ? "Connected" : "Disconnected")
                    .font(HygurTypography.statusCaption)
                    .foregroundStyle(HygurColors.textSecondary)
            }
            .help(events.sidecarConnected ? "Sidecar event stream connected" : "Sidecar event stream disconnected")

            // LM Studio status (only show when not unknown)
            if events.lmStudioStatus != .unknown {
                divider
                HStack(spacing: 5) {
                    StatusDot(color: events.lmStudioStatus == .up ? HygurColors.success : HygurColors.warning)
                    Text("LM Studio")
                        .font(HygurTypography.statusCaption)
                        .foregroundStyle(HygurColors.textSecondary)
                }
                .help("LM Studio is \(events.lmStudioStatus.rawValue)")
            }

            // Sync activity (last ingest_progress / sync event in flight)
            if let activity = currentActivity {
                divider
                HStack(spacing: 5) {
                    Image(systemName: "arrow.triangle.2.circlepath")
                        .font(.system(size: 10, weight: .medium))
                        .foregroundStyle(HygurColors.brandBlue)
                    Text(activity)
                        .font(HygurTypography.statusCaption)
                        .foregroundStyle(HygurColors.textSecondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
                .help(activity)
            }

            Spacer()

            // Learning progress capsule — centered between left status block
            // and right action block. Wrapping in `Spacer + bar + Spacer`
            // guarantees the bar sits in the geometric centre regardless of
            // what's pinned to either side (status pills can be variable width).
            LearningProgressBar(
                coverage: learningVM.coverage,
                tooltip: learningVM.tooltip
            ) {
                showLearningSheet = true
            }

            Spacer()

            // Notifications bell — badge if there are unread events
            StatusBarItem(
                systemImage: "bell",
                label: "",
                tint: HygurColors.textSecondary,
                action: { /* future: open notifications panel */ },
                help: "Notifications"
            )

            divider

            // Settings gear
            StatusBarItem(
                systemImage: "gearshape",
                label: "",
                tint: HygurColors.textSecondary,
                action: { openSettings() },
                help: "Settings"
            )
        }
        .padding(.horizontal, HygurSpacing.md)
        .frame(height: 26)
        .background(.thinMaterial)
        .overlay(alignment: .top) {
            Rectangle()
                .fill(HygurColors.divider)
                .frame(height: 0.5)
        }
        .onAppear { learningVM.startPolling() }
        .onDisappear { learningVM.stopPolling() }
        .sheet(isPresented: $showLearningSheet) {
            LearningInsightsView(viewModel: learningVM)
        }
    }

    // MARK: - Helpers

    private var divider: some View {
        Rectangle()
            .fill(HygurColors.divider)
            .frame(width: 1, height: 12)
    }

    private var appVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "—"
    }

    /// Returns a short label for the most recent in-flight activity event,
    /// or `nil` if the stream is idle. We only inspect the freshest event so
    /// the bar reads "Syncing…" once and clears as soon as the stream goes idle.
    private var currentActivity: String? {
        guard let latest = events.recentEvents.first else { return nil }
        // Show only fresh events (< 8s old) so the label clears naturally.
        guard Date().timeIntervalSince(latest.receivedAt) < 8 else { return nil }
        switch latest.type {
        case "ingest_start":    return "Indexing…"
        case "ingest_progress": return "Indexing…"
        case "ingest_complete": return "Indexed"
        case "sync":            return latest.status == "running" ? "Syncing…" : "Synced"
        default:                return nil
        }
    }
}
